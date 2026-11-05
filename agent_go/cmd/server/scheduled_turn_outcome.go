package server

import (
	"context"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// scheduledTurnFailureEventTypes are the events a turn emits when it could not
// produce a response at all.
//
// These are the same types the execution tree and the bot filter already treat
// as failures, so a turn the UI paints red is a turn the scheduler now counts
// as failed.
var scheduledTurnFailureEventTypes = map[string]bool{
	"conversation_error":       true,
	"agent_error":              true,
	"orchestrator_agent_error": true,
}

// scheduledTurnFailure returns the error text of a turn that ended without
// producing a response, or "" when the turn was fine.
//
// A scheduled turn fails in two very different ways and only one of them was
// visible to the scheduler. startSessionInternal returning an error means the
// turn could not be dispatched — that already fails the run. But a turn that
// dispatches fine and then has every LLM attempt fail records the failure as
// events, leaves the session status "completed", and returns no error at all.
//
// hetzner-ssh on 2026-08-18 20:06 is the case: both turns failed with
// "all LLMs failed (primary + 0 fallbacks): claudecode/ [quota_exhausted]",
// the run lasted 8.1 seconds, executed nothing, and was recorded success.
// The comment on the run-folder reconciliation right above this already names
// the same class of bug from 2026-07-29 — this is the LLM-level twin of it.
func scheduledTurnFailure(store *storeevents.EventStore, sessionID string, since time.Time) string {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	result := store.GetEvents(sessionID, storeevents.GetEventsOptions{})
	if !result.Exists {
		return ""
	}
	// Scan newest-first so the terminal outcome wins. Providers can emit a
	// transient conversation_error and then recover on a later attempt. Once a
	// main-level completion contains real assistant output, an older error from
	// the same turn no longer means that the turn "produced no response".
	for i := len(result.Events) - 1; i >= 0; i-- {
		event := result.Events[i]
		if !since.IsZero() && event.Timestamp.Before(since) {
			break
		}
		if scheduledTurnProducedResponse(event) {
			return ""
		}
		if !scheduledTurnFailureEventTypes[event.Type] {
			continue
		}
		if detail := scheduledEventErrorText(event); detail != "" {
			return detail
		}
		// The terminal failure can carry no text while the model's own error
		// (e.g. "codex-cli/... [network]: EOF") sits on an earlier event of the
		// same turn. Name that cause instead of only the event type.
		for j := i - 1; j >= 0; j-- {
			earlier := result.Events[j]
			if !since.IsZero() && earlier.Timestamp.Before(since) {
				break
			}
			if scheduledTurnFailureEventTypes[earlier.Type] || earlier.Type == "llm_generation_error" {
				if detail := scheduledEventErrorText(earlier); detail != "" {
					return "turn ended with " + event.Type + ": " + detail
				}
			}
		}
		return "turn ended with " + event.Type
	}
	return ""
}

// scheduledEventErrorText is an error event's text, from the stored field or
// its payload (a restored event keeps it only in the payload).
func scheduledEventErrorText(event storeevents.Event) string {
	if detail := strings.TrimSpace(event.Error); detail != "" {
		return detail
	}
	if event.Data == nil {
		return ""
	}
	switch payload := event.Data.Data.(type) {
	case *agentevents.ConversationErrorEvent:
		return strings.TrimSpace(payload.Error)
	case *agentevents.AgentErrorEvent:
		return strings.TrimSpace(payload.Error)
	case *agentevents.LLMGenerationErrorEvent:
		return strings.TrimSpace(payload.Error)
	case *storeevents.GenericEventData:
		text, _ := payload.Fields["error"].(string)
		return strings.TrimSpace(text)
	}
	return ""
}

// scheduledTurnNetworkRetryDelays are the waits before re-running a scheduled
// turn whose model call dropped on the network. Several schedules due the same
// minute can all lose the provider connection at once; one immediate retry
// failed the same way, so wait 30s, then 2 minutes.
var scheduledTurnNetworkRetryDelays = []time.Duration{30 * time.Second, 2 * time.Minute}

// isScheduledTurnNetworkError matches the provider's network kind. The error
// crossed the event store as text, so it is matched by the kind's "[network]"
// tag that llmerrors.Error prints.
func isScheduledTurnNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if llmerrors.KindOf(err) == llmerrors.KindNetwork {
		return true
	}
	return strings.Contains(err.Error(), "["+string(llmerrors.KindNetwork)+"]")
}

// runScheduledTurnWithNetworkRetry runs one scheduled turn and re-runs it after
// scheduledTurnNetworkRetryDelays while it fails with a network-kind error.
func runScheduledTurnWithNetworkRetry(ctx context.Context, label string, run func() (internalSessionTurnResult, error)) (internalSessionTurnResult, error) {
	result, err := run()
	for attempt, delay := range scheduledTurnNetworkRetryDelays {
		if !isScheduledTurnNetworkError(err) {
			break
		}
		scheduleLogf("[PRODUCT-SCHEDULE] %s: network error, retry %d/%d in %s: %v", label, attempt+1, len(scheduledTurnNetworkRetryDelays), delay, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, err
		case <-timer.C:
		}
		result, err = run()
	}
	return result, err
}

// scheduledTurnProducedResponse recognizes only a successful main-agent
// completion with non-empty assistant text. agent_end alone is deliberately
// insufficient: failure paths can emit lifecycle cleanup without a response.
// Nested workflow agents cannot rescue a failed workshop turn merely by
// completing their own work in the same session.
func scheduledTurnProducedResponse(event storeevents.Event) bool {
	if event.Data == nil || event.Data.HierarchyLevel != 0 || event.Data.Data == nil {
		return false
	}
	switch payload := event.Data.Data.(type) {
	case *agentevents.UnifiedCompletionEvent:
		return !strings.EqualFold(strings.TrimSpace(payload.Status), "error") &&
			strings.TrimSpace(payload.FinalResult) != ""
	case *agentevents.LLMGenerationEndEvent:
		return strings.TrimSpace(payload.Content) != ""
	default:
		return false
	}
}
