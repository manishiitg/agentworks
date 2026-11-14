package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

type retainedTurnIdentity struct {
	ID string
	// Captured from the accepted Session, independently of any terminal UI.
	Provider, Transport, NativeSessionID string
	// Captured before provider delivery, not when its acknowledgement returns.
	// A replay may identify Run's handoff but must never answer a newer input.
	AcceptedAt time.Time
	// Run completion promises a subsequent SDK lifecycle; a replay of Run's
	// completion must not claim that follow-up merely because its ID is new.
	FollowupFrom string
}

// nativeDescriptor is lifecycle data only. It is never inserted in the terminal
// store or presented as a terminal. TUI transports retain their existing path.
func (identity retainedTurnIdentity) nativeDescriptor(sessionID string) (terminals.Snapshot, bool) {
	switch identity.Transport {
	case "msp", "sdk", "app-server", "acp", "rpc":
		if identity.Provider != "" {
			return terminals.Snapshot{SessionID: sessionID, ExecutionKind: "main_agent", Status: terminals.Status{ProviderLabel: identity.Provider}}, true
		}
	}
	return terminals.Snapshot{}, false
}

func (identity retainedTurnIdentity) matchesCompletion(event events.Event) bool {
	return identity.matches(retainedCompletionTurnID(event)) &&
		(identity.AcceptedAt.IsZero() || event.Timestamp.IsZero() || !event.Timestamp.Before(identity.AcceptedAt))
}

// A normal Session.Run completion has the canonical lifecycle marker but no
// retained-session source. Only its exact accepted ID may hand off ownership;
// it cannot itself settle or persist a retained input.
func retainedRunHandoff(event events.Event, identity retainedTurnIdentity) bool {
	return identity.ID != "" && identity.ID == retainedCompletionTurnID(event) &&
		completionMetadataFlag(event, "canonical_turn_completion") &&
		completionMetadataFlag(event, "live_input_followup")
}

func (identity retainedTurnIdentity) matches(nativeID string) bool {
	return nativeID != "" && (identity.ID == nativeID || (identity.ID == "" && nativeID != identity.FollowupFrom))
}

func retainedCompletionPayload(event events.Event) (*unifiedevents.UnifiedCompletionEvent, bool) {
	if event.Data == nil {
		return nil, false
	}
	completion, ok := event.Data.Data.(*unifiedevents.UnifiedCompletionEvent)
	return completion, ok && completion != nil
}

func retainedSessionCompletion(event events.Event) bool {
	return mcpAgentSessionCompletion(event) || completionMetadataString(event, "source") == "retained_session_watchdog"
}

// A very short response can finish between Session.Send returning and the
// host recording its acknowledgement. Reconcile only this accepted native ID
// (and its explicit SDK follow-up), never an earlier answer selected by time.
func (api *StreamingAPI) reconcileAcceptedNativeCompletions(sessionID, nativeID string) {
	if api.eventStore == nil || strings.TrimSpace(nativeID) == "" {
		return
	}
	found := false
	for _, event := range api.eventStore.GetAllEventsRaw(sessionID) {
		if !retainedMainTurnCompletionEvent(event.Type) ||
			(!mcpAgentSessionCompletion(event) && !completionMetadataFlag(event, "canonical_turn_completion")) {
			continue
		}
		if retainedCompletionTurnID(event) == strings.TrimSpace(nativeID) {
			found = true
		}
		if found {
			api.observeRetainedMainTurnEvent(sessionID, event)
		}
	}
}

// retainedSessionIdleChecksToRelease is how many consecutive watchdog checks
// must find the native Session without an active turn before the retained
// mark is released.
const retainedSessionIdleChecksToRelease = 2

// lookupRetainedSession is the native Session lookup; tests replace it.
var lookupRetainedSession = func(sessionID string) (interface{ ActiveTurnID() string }, bool) {
	session, ok := mcpagent.LookupSession(sessionID)
	if !ok || session == nil {
		return nil, false
	}
	return session, true
}

// The SDK owns success. This backstop can only report failure, never infer an
// answer from a quiet pane or a previous sidecar result.
func (api *StreamingAPI) watchMCPAgentSessionTurn(ctx context.Context, sessionID string, snapshot terminals.Snapshot, startedAt time.Time) {
	stopIdleWatch := api.watchRetainedTurnInactivity(ctx, sessionID, snapshot, startedAt)
	defer stopIdleWatch()
	interval := retainedMainTurnPaneCheckInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	misses, idleChecks := 0, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			api.retainedMainTurnsMu.Lock()
			current, tracked := api.retainedMainTurns[sessionID]
			_, owned := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]]
			api.retainedMainTurnsMu.Unlock()
			if !tracked || !owned || !current.Equal(startedAt) {
				return
			}
			// No terminal is expected for a native Session. Only its exact
			// completion or the existing inactivity failure may settle it.
			if snapshot.TerminalID == "" {
				continue
			}
			// The Session owns this turn's completion. When it reports no
			// active turn and no foreground turn runs, none will ever come
			// (a natively steered input answered inside its Run); release
			// after two checks instead of keeping the chat running.
			if native, ok := lookupRetainedSession(sessionID); ok && native.ActiveTurnID() == "" && !api.hasActiveTurnCancel(sessionID) {
				if idleChecks++; idleChecks >= retainedSessionIdleChecksToRelease {
					api.releaseStaleRetainedMainTurn(sessionID, "session_has_no_active_turn")
					return
				}
			} else {
				idleChecks = 0
			}
			// The main snapshot may be structured/tmux-less, or the provider may
			// have rebound its pane since this watcher started. Only a current,
			// named pane can supply evidence of a missing process.
			latest, exists := api.terminalStore.GetRaw(snapshot.TerminalID)
			if !exists || latest.SessionID != sessionID || strings.TrimSpace(latest.TmuxSession) == "" {
				misses = 0
				continue
			}
			snapshot = latest
			switch inspectRetainedMainTurnTmuxState(snapshot.TmuxSession) {
			case codingTmuxPaneMissing, codingTmuxPaneDead:
				misses++
			default:
				misses = 0
			}
			reason := ""
			if misses >= retainedMainTurnPaneMissesToFail {
				reason = "coding-agent terminal exited before its native turn completed"
			}
			if reason != "" {
				api.failMCPAgentSessionTurn(sessionID, snapshot, startedAt, reason, misses >= retainedMainTurnPaneMissesToFail)
				return
			}
		}
	}
}

func (api *StreamingAPI) failMCPAgentSessionTurn(sessionID string, snapshot terminals.Snapshot, startedAt time.Time, reason string, processExited bool) {
	api.retainedMainTurnsMu.Lock()
	current, tracked := api.retainedMainTurns[sessionID]
	executionID := api.retainedMainTurnExecutionIDs[sessionID]
	nativeID, owned := api.retainedMainTurnCanonicalIDs[executionID]
	api.retainedMainTurnsMu.Unlock()
	if !tracked || !owned || !current.Equal(startedAt) {
		return
	}
	now := time.Now()
	completion := unifiedevents.NewUnifiedCompletionEventWithError("coding_agent", "retained", "", strings.TrimSpace(reason), now.Sub(startedAt), 1)
	completion.Metadata["source"] = "retained_session_watchdog"
	completion.Metadata["turn_id"] = nativeID.ID
	completion.Metadata["process_exited"] = processExited
	event := events.Event{
		ID: fmt.Sprintf("retained-session-failure-%d", now.UnixNano()), Type: "unified_completion", Timestamp: now,
		SessionID: sessionID, ExecutionID: executionID, ExecutionKind: "main_agent",
		TerminalOwnerID: "main:" + sessionID, TerminalID: snapshot.TerminalID,
		Data: &unifiedevents.AgentEvent{Type: unifiedevents.EventTypeUnifiedCompletion, Timestamp: now, SessionID: sessionID, TurnID: nativeID.ID, Data: completion},
	}
	if api.eventStore != nil {
		api.eventStore.AddEvent(sessionID, event)
	} else {
		api.observeRetainedMainTurnEvent(sessionID, event)
	}
}
