package server

import "testing"

// A synthetic turn that keeps failing (the session's CLI is gone) backs off
// and gives up after maxFailedCompletionRetries instead of retrying every
// five seconds forever.
func TestFailedCompletionRetriesAreCapped(t *testing.T) {
	api := &StreamingAPI{}
	const session = "session-cap-test"
	t.Cleanup(func() { resetFailedCompletionRetries(session) })
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
}
