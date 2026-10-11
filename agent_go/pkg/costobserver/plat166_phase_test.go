package costobserver

import (
	"testing"

	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// TestObserverDefaultsToNoPhase pins the PLAT-166 scope-fix: a freshly
// constructed observer tags nothing by default. Defaulting to
// PhaseExecutionOnly (the original PLAT-166 shape) meant every execution —
// chat, builder, Pulse, evaluation, every plain workflow step, not just ones
// that ever run a reflection turn — grew a by_phase.execution_only entry
// that just duplicated its own top-level total in every Cost Analysis API
// response. An empty phase writes no ByPhase entry at all
// (addEntryToExecutionBucket), so the default costs nothing.
func TestObserverDefaultsToNoPhase(t *testing.T) {
	observer := New(nil, "sess-1", "user-1", "simple",
		WithAttribution(ScopeWorkflowExecution, "Workflow/demo", "", "sess-1:review-measure"),
	)
	if got := observer.Phase(); got != "" {
		t.Fatalf("Phase() = %q, want empty", got)
	}
	entry := observer.baseEntry(&unifiedevents.AgentEvent{})
	if entry.Phase != "" {
		t.Fatalf("baseEntry().Phase = %q, want empty", entry.Phase)
	}
}

// TestObserverSetPhaseTogglesEntryAttribution pins the reflection-turn /
// message_sequence-item bracket pattern (reflection_turn_run.go,
// controller_agent.go): SetPhase changes what every subsequent
// entry from this SAME observer instance carries, and a later SetPhase("")
// restores the untagged default — since both reuse the step's own
// agent/observer rather than getting a fresh one.
func TestObserverSetPhaseTogglesEntryAttribution(t *testing.T) {
	observer := New(nil, "sess-1", "user-1", "simple",
		WithAttribution(ScopeWorkflowExecution, "Workflow/demo", "", "sess-1:review-measure"),
	)

	observer.SetPhase(PhaseReflection)
	if got := observer.Phase(); got != PhaseReflection {
		t.Fatalf("Phase() after SetPhase(reflection) = %q, want %q", got, PhaseReflection)
	}
	reflectionEntry := observer.baseEntry(&unifiedevents.AgentEvent{})
	if reflectionEntry.Phase != PhaseReflection {
		t.Fatalf("entry during reflection: Phase = %q, want %q", reflectionEntry.Phase, PhaseReflection)
	}

	observer.SetPhase("")
	if got := observer.Phase(); got != "" {
		t.Fatalf("Phase() after restoring = %q, want empty", got)
	}
	restoredEntry := observer.baseEntry(&unifiedevents.AgentEvent{})
	if restoredEntry.Phase != "" {
		t.Fatalf("entry after restore: Phase = %q, want empty", restoredEntry.Phase)
	}
}

// TestObserverSetPhaseSupportsArbitraryAgentItemTags pins PLAT-167:
// SetPhase is not limited to the two PLAT-166 constants — a message_sequence
// item's own identity works exactly the same way.
func TestObserverSetPhaseSupportsArbitraryAgentItemTags(t *testing.T) {
	observer := New(nil, "sess-1", "user-1", "simple",
		WithAttribution(ScopeWorkflowExecution, "Workflow/demo", "", "sess-1:outreach-sequence"),
	)
	observer.SetPhase("item:draft-message")
	entry := observer.baseEntry(&unifiedevents.AgentEvent{})
	if entry.Phase != "item:draft-message" {
		t.Fatalf("entry.Phase = %q, want %q", entry.Phase, "item:draft-message")
	}
}
