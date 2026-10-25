package costobserver

import "testing"

// TestScopeForScheduledLLMRoleNamesPulse pins the PLAT-088
// fix. A scheduled Pulse turn is indistinguishable from the workflow
// orchestration turns around it by agent mode or phase id alone — same
// session, same "workflow-builder" phase — so before this the entire Pulse
// stage of every scheduled run was charged to `chat`. The scheduler already
// stamps llm_config_source when it swaps in the Pulse LLM, so the
// intent is known at the source; this maps it to the cost scope.

func TestScopeForScheduledTurnUsesLifecycleFlagWithoutModelOverride(t *testing.T) {
	if got := ScopeForScheduledTurn(true, ""); got != ScopePulse {
		t.Fatalf("Autopilot lifecycle turn scope = %q, want %q", got, ScopePulse)
	}
	if got := ScopeForScheduledTurn(false, "scheduled_pulse"); got != ScopePulse {
		t.Fatalf("legacy scheduled_pulse scope = %q, want %q", got, ScopePulse)
	}
	if got := ScopeForScheduledTurn(false, ""); got != "" {
		t.Fatalf("normal schedule override scope = %q, want empty for normal inference", got)
	}
}

// TestInferScopeStillChargesWorkflowPhaseToBuilder documents the second half
// of PLAT-088: handleQuery rewrites req.AgentMode to "multi-agent" purely to
// route workflow_phase requests down the standard agent path, and the cost
// observer read that rewritten value. Passing the pre-rewrite mode is what
// keeps scheduled workflow-orchestration turns out of the chat bucket.
func TestInferScopeStillChargesWorkflowPhaseToBuilder(t *testing.T) {
	if got := InferScope("workflow_phase", "workflow-builder"); got != ScopeBuilder {
		t.Fatalf("InferScope(workflow_phase) = %q, want %q", got, ScopeBuilder)
	}
	// The rewritten value is what produced the wrong answer — pinned here so
	// the regression is visible rather than implied.
	if got := InferScope("multi-agent", "workflow-builder"); got != ScopeChat {
		t.Fatalf("InferScope(multi-agent) = %q, want %q (the pre-fix behavior this guards against)", got, ScopeChat)
	}
}
