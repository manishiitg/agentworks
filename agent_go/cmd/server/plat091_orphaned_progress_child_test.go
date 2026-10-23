package server

import (
	"testing"
	"time"
)

// plat091Parent and its children reproduce the exact ids captured live on
// social-media 2026-08-11: the parent evaluation execution finalized at
// 18:25:33 while four "-step-0-" progress children never received any end
// event, pinning the session busy and stalling Pulse for the full three-hour
// ceiling.
const plat091Parent = "eval-full-iteration-0/default-1786450956765003000"

var plat091Orphans = []string{
	plat091Parent + "-step-0-msomrwct0g",
	plat091Parent + "-step-0-mson9usj0i",
	plat091Parent + "-step-0-msonowed0j",
	plat091Parent + "-step-0-msonug9q0k",
}

func plat091Registry(t *testing.T) *BackgroundAgentRegistry {
	t.Helper()
	registry := NewBackgroundAgentRegistry()
	registry.Register("sess", &BackgroundAgent{
		ID: plat091Parent, SessionID: "sess", Status: BGAgentRunning, CreatedAt: time.Now(),
	})
	for _, id := range plat091Orphans {
		registry.Register("sess", &BackgroundAgent{
			ID: id, SessionID: "sess", Status: BGAgentRunning, CreatedAt: time.Now(),
		})
	}
	return registry
}

// TestReconcileOrphanedProgressChildrenReleasesTheSession is the PLAT-091
// regression: with the parent finished, the session must stop reporting live
// child work, because that is the exact signal the scheduler's Pulse
// drain-wait blocks on.
func TestReconcileOrphanedProgressChildrenReleasesTheSession(t *testing.T) {
	registry := plat091Registry(t)
	if !registry.HasRunningAgents("sess") {
		t.Fatal("precondition: the session should look busy before reconciliation")
	}

	// The parent settles first, exactly as OnExecutionComplete does.
	registry.Get("sess", plat091Parent).SetResult("done")
	settled := registry.ReconcileOrphanedProgressChildren("sess", plat091Parent, "parent finished")
	if len(settled) != len(plat091Orphans) {
		t.Fatalf("settled %d orphan(s) (%v), want %d", len(settled), settled, len(plat091Orphans))
	}
	for _, id := range plat091Orphans {
		if status := registry.Get("sess", id).GetStatus(); status == BGAgentRunning {
			t.Fatalf("orphan %s is still running after reconciliation", id)
		}
	}

	// HasRunningAgents keeps recently-settled agents "live" for a short grace
	// window, so assert the thing that actually matters once it lapses.
	for _, id := range plat091Orphans {
		agent := registry.Get("sess", id)
		stale := time.Now().Add(-2 * hasRunningAgentsGracePeriod)
		agent.mu.Lock()
		agent.CompletedAt = &stale
		agent.mu.Unlock()
	}
	parent := registry.Get("sess", plat091Parent)
	stale := time.Now().Add(-2 * hasRunningAgentsGracePeriod)
	parent.mu.Lock()
	parent.CompletedAt = &stale
	parent.mu.Unlock()

	if registry.HasRunningAgents("sess") {
		t.Fatal("session still reports live child work; Autopilot would stall until the 3h ceiling")
	}
}

// TestReconcileOrphanedProgressChildrenOnlyTouchesItsOwnDescendants guards the
// blast radius. Reconciliation must never settle a genuinely running child of
// a different parent — a legitimately long-quiet child (a browser step pacing
// itself, a long model call) is exactly what the drain-wait exists to protect.
func TestReconcileOrphanedProgressChildrenOnlyTouchesItsOwnDescendants(t *testing.T) {
	registry := plat091Registry(t)
	const otherParent = "workflow-full-msogmzrd08"
	const otherChild = otherParent + "-step-2-abc123"
	registry.Register("sess", &BackgroundAgent{
		ID: otherChild, SessionID: "sess", Status: BGAgentRunning, CreatedAt: time.Now(),
	})
	// A sibling that merely shares a name prefix but is not a progress child.
	const notAChild = plat091Parent + "-summary"
	registry.Register("sess", &BackgroundAgent{
		ID: notAChild, SessionID: "sess", Status: BGAgentRunning, CreatedAt: time.Now(),
	})

	registry.ReconcileOrphanedProgressChildren("sess", plat091Parent, "parent finished")

	if got := registry.Get("sess", otherChild).GetStatus(); got != BGAgentRunning {
		t.Fatalf("another parent's running child was settled (status=%s)", got)
	}
	if got := registry.Get("sess", notAChild).GetStatus(); got != BGAgentRunning {
		t.Fatalf("a non-progress sibling was settled (status=%s); only \"-step-\" children are owned", got)
	}
	if !registry.HasRunningAgents("sess") {
		t.Fatal("the session must stay busy while another parent's child is genuinely running")
	}
}

// TestReconcileOrphanedProgressChildrenPreservesRealOutcomes proves the sweep
// is a backstop, not an overwrite: a child that already reported its own
// result keeps it, so a real failure is never relabelled as this bookkeeping
// repair.
func TestReconcileOrphanedProgressChildrenPreservesRealOutcomes(t *testing.T) {
	registry := plat091Registry(t)
	completed := registry.Get("sess", plat091Orphans[0])
	completed.SetResult("step finished normally")
	failed := registry.Get("sess", plat091Orphans[1])
	failed.SetError("step failed for its own reasons")

	settled := registry.ReconcileOrphanedProgressChildren("sess", plat091Parent, "parent finished")
	if len(settled) != 2 {
		t.Fatalf("settled %d (%v), want only the 2 still-running orphans", len(settled), settled)
	}
	if got := completed.GetSnapshot().Result; got != "step finished normally" {
		t.Fatalf("an already-completed child's result was overwritten: %q", got)
	}
	if got := failed.GetSnapshot().Error; got != "step failed for its own reasons" {
		t.Fatalf("an already-failed child's error was overwritten: %q", got)
	}
}

// TestReconcileOrphanedProgressChildrenIgnoresEmptyParent pins the no-op guard
// so a missing execution id can never sweep a whole session.
func TestReconcileOrphanedProgressChildrenIgnoresEmptyParent(t *testing.T) {
	registry := plat091Registry(t)
	if settled := registry.ReconcileOrphanedProgressChildren("sess", "  ", "parent finished"); len(settled) != 0 {
		t.Fatalf("an empty parent id settled %d agent(s): %v", len(settled), settled)
	}
	if !registry.HasRunningAgents("sess") {
		t.Fatal("an empty parent id must not settle anything")
	}
}
