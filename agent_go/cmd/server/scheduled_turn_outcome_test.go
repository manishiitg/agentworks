package server

import (
	"context"
	"errors"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

func errEvent(kind, detail string, at time.Time) storeevents.Event {
	return storeevents.Event{Type: kind, Timestamp: at, Error: detail}
}

// TestScheduledTurnFailureCatchesATurnThatProducedNothing is the hetzner-ssh
// case from 2026-08-18 20:06: both turns failed with "all LLMs failed ...
// [quota_exhausted]", the run lasted 8.1 seconds, executed no step, and was
// recorded a successful security audit. startSessionInternal returned no error
// because dispatch worked — only the generation failed, and that is recorded as
// events, not as a return value.
func TestScheduledTurnFailureCatchesATurnThatProducedNothing(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session"
	turnStart := time.Now().UTC()

	store.AddEvent(sessionID, errEvent("conversation_error",
		"all LLMs failed (primary + 0 fallbacks): claudecode/ [quota_exhausted]: claude code usage limit reached", turnStart.Add(time.Second)))

	got := scheduledTurnFailure(store, sessionID, turnStart)
	if got == "" {
		t.Fatal("a turn whose every LLM attempt failed was reported as fine — the run would be recorded success")
	}
	if !contains(got, "quota_exhausted") {
		t.Errorf("failure text lost the cause: %q", got)
	}
}

// TestScheduledTurnFailureIgnoresAnEarlierTurnsFailure. Scheduled runs are many
// turns in one session; a failure from turn 1 must not condemn turn 2, or a
// recovered run is filed as failed.
func TestScheduledTurnFailureIgnoresAnEarlierTurnsFailure(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session-recovered"
	base := time.Now().UTC()

	store.AddEvent(sessionID, errEvent("conversation_error", "earlier turn blew up", base))
	laterTurnStart := base.Add(time.Minute)
	store.AddEvent(sessionID, storeevents.Event{Type: "agent_end", Timestamp: laterTurnStart.Add(time.Second)})

	if got := scheduledTurnFailure(store, sessionID, laterTurnStart); got != "" {
		t.Errorf("a previous turn's failure was attributed to this turn: %q", got)
	}
}

// TestScheduledTurnFailureStaysQuietForAHealthyTurn guards the direction that
// costs the most: inventing failures would fail runs that worked.
func TestScheduledTurnFailureStaysQuietForAHealthyTurn(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session-ok"
	turnStart := time.Now().UTC()
	store.AddEvent(sessionID, storeevents.Event{Type: "agent_end", Timestamp: turnStart.Add(time.Second)})

	if got := scheduledTurnFailure(store, sessionID, turnStart); got != "" {
		t.Errorf("healthy turn reported as failed: %q", got)
	}
	if got := scheduledTurnFailure(nil, sessionID, turnStart); got != "" {
		t.Errorf("nil store reported a failure: %q", got)
	}
	if got := scheduledTurnFailure(store, "unknown-session", turnStart); got != "" {
		t.Errorf("unknown session reported a failure: %q", got)
	}
}

func TestScheduledTurnFailureIgnoresTransientErrorWhenTurnLaterProducesResponse(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session-recovered-response"
	turnStart := time.Now().UTC()

	store.AddEvent(sessionID, errEvent("conversation_error", "", turnStart.Add(time.Second)))
	store.AddEvent(sessionID, storeevents.Event{
		Type:      "unified_completion",
		Timestamp: turnStart.Add(2 * time.Second),
		Data: &agentevents.AgentEvent{
			Type: "unified_completion",
			Data: &agentevents.UnifiedCompletionEvent{Status: "completed", FinalResult: "Workflow launched."},
		},
	})

	if got := scheduledTurnFailure(store, sessionID, turnStart); got != "" {
		t.Errorf("a recovered turn with assistant output was reported as failed: %q", got)
	}
}

func TestScheduledTurnFailureDoesNotLetEmptyCompletionHideFailure(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session-empty-completion"
	turnStart := time.Now().UTC()

	store.AddEvent(sessionID, errEvent("conversation_error", "provider failed", turnStart.Add(time.Second)))
	store.AddEvent(sessionID, storeevents.Event{
		Type:      "unified_completion",
		Timestamp: turnStart.Add(2 * time.Second),
		Data: &agentevents.AgentEvent{
			Type: "unified_completion",
			Data: &agentevents.UnifiedCompletionEvent{Status: "completed"},
		},
	})

	if got := scheduledTurnFailure(store, sessionID, turnStart); !contains(got, "provider failed") {
		t.Errorf("empty completion hid the real failure: %q", got)
	}
}

func TestScheduledTurnFailureUsesLaterTerminalFailure(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session-late-failure"
	turnStart := time.Now().UTC()

	store.AddEvent(sessionID, storeevents.Event{
		Type:      "unified_completion",
		Timestamp: turnStart.Add(time.Second),
		Data: &agentevents.AgentEvent{
			Type: "unified_completion",
			Data: &agentevents.UnifiedCompletionEvent{Status: "completed", FinalResult: "early response"},
		},
	})
	store.AddEvent(sessionID, errEvent("conversation_error", "terminal provider failure", turnStart.Add(2*time.Second)))

	if got := scheduledTurnFailure(store, sessionID, turnStart); !contains(got, "terminal provider failure") {
		t.Errorf("later terminal failure did not win: %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// TestScheduledTurnNetworkDropIsNamedAndRetried: six scheduled turns on a
// codex-cli model failed in the same minute with "[network]: EOF"; the schedule
// logged only "turn ended with conversation_error" and gave up after one try.
// The failure must carry the model's error, and a network drop is retried.
func TestScheduledTurnNetworkDropIsNamedAndRetried(t *testing.T) {
	store := storeevents.NewEventStore(200)
	const sessionID = "sched-session-network"
	turnStart := time.Now().UTC()
	store.AddEvent(sessionID, storeevents.Event{
		Type:      "llm_generation_error",
		Timestamp: turnStart.Add(time.Second),
		Data: &agentevents.AgentEvent{
			Type: "llm_generation_error",
			Data: &agentevents.LLMGenerationErrorEvent{Error: "codex-cli/gpt-5 [network]: EOF"},
		},
	})
	store.AddEvent(sessionID, errEvent("conversation_error", "", turnStart.Add(2*time.Second)))
	failure := scheduledTurnFailure(store, sessionID, turnStart)
	if !contains(failure, "[network]: EOF") {
		t.Fatalf("failure text lost the model error: %q", failure)
	}

	saved := scheduledTurnNetworkRetryDelays
	scheduledTurnNetworkRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { scheduledTurnNetworkRetryDelays = saved }()
	calls := 0
	_, err := runScheduledTurnWithNetworkRetry(context.Background(), "job", func() (internalSessionTurnResult, error) {
		calls++
		if calls < 3 {
			return internalSessionTurnResult{}, errors.New(failure)
		}
		return internalSessionTurnResult{FinalResponse: "done"}, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("network drop not retried twice: calls=%d err=%v", calls, err)
	}
	calls = 0
	_, err = runScheduledTurnWithNetworkRetry(context.Background(), "job", func() (internalSessionTurnResult, error) {
		calls++
		return internalSessionTurnResult{}, errors.New("claudecode/ [quota_exhausted]: limit reached")
	})
	if err == nil || calls != 1 {
		t.Fatalf("a non-network failure was retried: calls=%d err=%v", calls, err)
	}
}
