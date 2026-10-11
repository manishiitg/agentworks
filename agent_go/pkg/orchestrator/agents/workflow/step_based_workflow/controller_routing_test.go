package step_based_workflow

import "testing"

func TestRoutingNextStepTypesByID(t *testing.T) {
	steps := []PlanStepInterface{
		&RegularPlanStep{CommonStepFields: CommonStepFields{ID: "write"}},
		&RoutingPlanStep{CommonStepFields: CommonStepFields{ID: "route"}},
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "orchestrate"}},
	}

	stepTypes := routingNextStepTypesByID(steps)

	if got := stepTypes["write"]; got != "regular" {
		t.Fatalf("write step type = %q, want regular", got)
	}
	if got := stepTypes["route"]; got != "routing" {
		t.Fatalf("route step type = %q, want routing", got)
	}
	if got := stepTypes["orchestrate"]; got != "orchestrator" {
		t.Fatalf("orchestrate step type = %q, want orchestrator", got)
	}
}
