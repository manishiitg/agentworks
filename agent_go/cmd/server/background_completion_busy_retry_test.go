package server

import (
	"fmt"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"reflect"
	"testing"
	"time"
)

func TestBusySyntheticCompletionRetriesBackOffWithoutLosingResults(t *testing.T) {
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	const session = "busy-synthetic-retry"
	api.setSessionBusy(session, true)
	api.setSyntheticTurn(session, true)
	api.queuePendingCompletion(session, "finished-function")
	old := completionRetryAfterFunc
	t.Cleanup(func() { completionRetryAfterFunc = old })
	var delays []time.Duration
	var callbacks []func()
	completionRetryAfterFunc = func(delay time.Duration, run func()) {
		delays = append(delays, delay)
		callbacks = append(callbacks, run)
	}
	api.schedulePendingCompletionRetry(session)
	// Every timer finds the same live synthetic turn. It must not repeatedly
	// attempt a steer or lose the queued function result.
	for i := 0; i < 6; i++ {
		callbacks[i]()
		api.pendingMu.RLock()
		queued := append([]string(nil), api.pendingCompletions[session]...)
		api.pendingMu.RUnlock()
		if len(queued) != 1 || queued[0] != "finished-function" {
			t.Fatalf("retry %d changed queued results: %v", i, queued)
		}
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute, time.Minute}
	for i, delay := range want {
		if delays[i] != delay {
			t.Fatalf("retry %d delay=%v, want %v", i, delays[i], delay)
		}
	}
	// Repeated completion arrivals cannot install a second retry timer.
	api.schedulePendingCompletionRetry(session)
	if len(delays) != len(want) {
		t.Fatalf("duplicate timer installed: %v", delays)
	}
}

func TestOccupiedSyntheticTurnDoesNotSpendFailureRetries(t *testing.T) {
	api := &StreamingAPI{}
	const session = "busy-is-not-failure"
	resetFailedCompletionRetries(session)
	old := completionRetryAfterFunc
	var scheduled int
	completionRetryAfterFunc = func(time.Duration, func()) { scheduled++ }
	t.Cleanup(func() { completionRetryAfterFunc = old; resetFailedCompletionRetries(session) })
	api.queuePendingCompletion(session, "result")
	api.scheduleCompletionOutcomeRetry(session, fmt.Errorf("provider refused: %w", mcpagent.ErrTurnAlreadyInFlight))
	if _, failed := failedCompletionRetries.Load(session); failed {
		t.Fatal("busy refusal spent a failure retry")
	}
	if scheduled != 1 {
		t.Fatalf("scheduled %d busy retries", scheduled)
	}
	if got := api.drainPendingCompletions(session); len(got) != 1 || got[0] != "result" {
		t.Fatalf("lost queued result: %v", got)
	}
}

func TestCompletionGroupsKeepInputOrderAndDifferentParentsSeparate(t *testing.T) {
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	const session = "ordered-completions"
	for _, item := range []struct{ id, parent string }{{"a1", "root-a"}, {"b1", "root-b"}, {"a2", "root-a"}, {"c1", "root-c"}} {
		api.bgAgentRegistry.Register(session, &BackgroundAgent{ID: item.id, SessionID: session, ParentExecutionID: item.parent, Status: BGAgentCompleted})
	}
	first, waiting := api.firstBackgroundCompletionGroup(session, []string{"a1", "b1", "a2", "c1"})
	if !reflect.DeepEqual(first, []string{"a1", "a2"}) || !reflect.DeepEqual(waiting, []string{"b1", "c1"}) {
		t.Fatalf("first=%v waiting=%v", first, waiting)
	}
}

func TestStoppedSessionRetainsDrainedBackgroundResults(t *testing.T) {
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	const session = "stopped-results"
	api.markSessionStopped(session)
	api.processBatchedBackgroundAgentCompletions(session, []string{"result-a", "result-b"})
	got := api.drainPendingCompletions(session)
	if !reflect.DeepEqual(got, []string{"result-a", "result-b"}) {
		t.Fatalf("stopped session lost results: %v", got)
	}
}
