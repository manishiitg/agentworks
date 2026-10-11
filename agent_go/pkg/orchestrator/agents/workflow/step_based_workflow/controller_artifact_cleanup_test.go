package step_based_workflow

import (
	"context"
	"testing"
)

func TestTodoSubAgentArtifactFolderNameUsesNestedParentLayout(t *testing.T) {
	got := todoSubAgentArtifactFolderName("parent", "step-2", "research/route", "call-001", false)
	want := "parent/agents/research-route/calls/call-001"
	if got != want {
		t.Fatalf("todoSubAgentArtifactFolderName() = %q, want %q", got, want)
	}
}

func TestScriptedRouteArtifactFolderNameUsesNestedParentLayout(t *testing.T) {
	got := todoSubAgentArtifactFolderName("parent", "step-2", "research/route", "call-001", true)
	want := "parent/scripts/routes/research-route/calls/call-001"
	if got != want {
		t.Fatalf("todoSubAgentArtifactFolderName() = %q, want %q", got, want)
	}
}

func TestNestedAgentUsesItsCallFolderAsParentRoot(t *testing.T) {
	parent := "parent/agents/research/calls/call-001"
	got := todoSubAgentArtifactFolderName("nested", parent, "summarize", "call-002", false)
	want := "parent/agents/research/calls/call-001/agents/summarize/calls/call-002"
	if got != want {
		t.Fatalf("todoSubAgentArtifactFolderName() = %q, want %q", got, want)
	}
}

func TestGenericAgentUsesNestedParentLayout(t *testing.T) {
	got := genericAgentArtifactFolderName("parent", "step-2", "call-001")
	want := "parent/agents/generic/calls/call-001"
	if got != want {
		t.Fatalf("genericAgentArtifactFolderName() = %q, want %q", got, want)
	}
}

func TestAgentRouteRoot(t *testing.T) {
	got := agentSequenceRouteRoot("parent/agents/research/calls/call-001")
	want := "parent/agents/research"
	if got != want {
		t.Fatalf("agentSequenceRouteRoot() = %q, want %q", got, want)
	}
	if got := agentSequenceRouteRoot("step-2"); got != "" {
		t.Fatalf("top-level route root = %q, want empty", got)
	}
}

func TestNestedArtifactParentRoot(t *testing.T) {
	got := nestedArtifactParentRoot("parent/agents/research/calls/call-001/agents/summarize/calls/call-002")
	want := "parent/agents/research/calls/call-001"
	if got != want {
		t.Fatalf("nestedArtifactParentRoot() = %q, want %q", got, want)
	}
}

func TestTopLevelOwnerStepNumberUsesStableParentIDForNestedArtifacts(t *testing.T) {
	ctx := withExecutionPlan(context.Background(), &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "first"}},
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "parent-agent"}},
	}})
	if got := topLevelOwnerStepNumber(ctx, "parent-agent/agents/research/calls/call-001"); got != 2 {
		t.Fatalf("owner step number = %d, want 2", got)
	}
}
