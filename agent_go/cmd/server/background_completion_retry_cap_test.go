package server

import (
	"testing"
	"time"
)

// A synthetic turn that keeps failing (the session's CLI is gone) backs off
// and gives up after maxFailedCompletionRetries instead of retrying every
// five seconds forever.
func TestFailedCompletionRetriesAreCapped(t *testing.T) {
	api := &StreamingAPI{bgAgentRegistry: NewBackgroundAgentRegistry()}
	const session = "session-cap-test"
	var delays []time.Duration
	previous := completionRetryAfterFunc
	completionRetryAfterFunc = func(delay time.Duration, _ func()) {
		delays = append(delays, delay)
		// The retry "ran" and failed again: clear the scheduled flag as the
		// real callback does, so the next failure schedules.
		api.pendingMu.Lock()
		delete(api.completionRetryScheduled, session)
		api.pendingMu.Unlock()
	}
	t.Cleanup(func() { completionRetryAfterFunc = previous; resetFailedCompletionRetries(session) })
	api.queuePendingCompletion(session, "agent-1")
	for i := 0; i < maxFailedCompletionRetries; i++ {
		api.scheduleFailedCompletionRetry(session)
	}
	if got, _ := failedCompletionRetries.Load(session); got != maxFailedCompletionRetries {
		t.Fatalf("attempts = %v, want %d", got, maxFailedCompletionRetries)
	}
	api.scheduleFailedCompletionRetry(session)
	if _, ok := failedCompletionRetries.Load(session); ok {
		t.Fatal("the retry counter must be cleared after giving up")
	}
	if pending := api.drainPendingCompletions(session); len(pending) != 0 {
		t.Fatalf("pending completions = %v, want none after giving up", pending)
	}
	if len(delays) != maxFailedCompletionRetries || delays[0] != 5*time.Second || delays[len(delays)-1] != maxFailedCompletionBackoff {
		t.Fatalf("retry delays = %v, want 5s doubling up to %s", delays, maxFailedCompletionBackoff)
	}
}
