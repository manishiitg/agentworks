package step_based_workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestAgentHaltsBeforeStartingAnItemOnceCancelled is PLAT-130.
//
// The reported symptom: Stop on a running schedule interrupted the current
// message, the UI and run history both recorded "stopped" — and the next queued
// message_sequence item posted anyway.
//
// The loop already returned on any item error, so the assumption was that a
// canceled context would surface as an error and halt the queue. That makes
// halting conditional on some layer underneath converting cancellation into an
// error, and session teardown races that conversion: a coding-CLI turn whose
// pane is being killed can return a truncated-but-plausible result instead of
// failing. The queue reads that as success and starts the next item, whose side
// effect is real and outbound.
func TestAgentHaltsBeforeStartingAnItemOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := agentSequenceHaltedBeforeItem(ctx, "morning-sequence", "second-post")
	if err == nil {
		t.Fatal("a canceled run was allowed to start another queued item")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("halt error does not carry the cancellation cause: %v", err)
	}
	// The message lands in the run's session record and in the error the step
	// returns, so it has to name which item was refused, not merely that
	// something was canceled.
	for _, want := range []string{"morning-sequence", "second-post"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt error %q does not identify %q", err, want)
		}
	}
}

// TestAgentProceedsWhileTheRunIsLive is the other half: the guard must
// not become a reason for queues to stop on their own.
func TestAgentProceedsWhileTheRunIsLive(t *testing.T) {
	if err := agentSequenceHaltedBeforeItem(context.Background(), "s", "i"); err != nil {
		t.Errorf("a live run was halted: %v", err)
	}
	// A nil context is not evidence of cancellation. Several internal callers
	// pass one, and treating it as canceled would silently stop real queues.
	if err := agentSequenceHaltedBeforeItem(nil, "s", "i"); err != nil { //nolint:staticcheck // deliberate nil-ctx case
		t.Errorf("a nil context was treated as canceled: %v", err)
	}
}

// TestAgentHaltIsCheckedBeforeEveryItem pins that the gate sits at the
// top of the queue loop rather than only before the first item — the reported
// failure was specifically the SECOND item running.
func TestAgentHaltIsCheckedBeforeEveryItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // go vet: cancel on every path; the loop below cancels mid-run.

	items := []string{"first-post", "second-post", "third-post"}
	started := []string{}
	for i, id := range items {
		if err := agentSequenceHaltedBeforeItem(ctx, "seq", id); err != nil {
			break
		}
		started = append(started, id)
		if i == 0 {
			// Stop is clicked while the first item is in flight.
			cancel()
		}
	}

	if len(started) != 1 || started[0] != "first-post" {
		t.Errorf("items started = %v, want only [first-post]; the queue advanced past a canceled run", started)
	}
}
