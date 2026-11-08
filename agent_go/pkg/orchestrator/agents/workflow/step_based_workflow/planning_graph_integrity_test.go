package step_based_workflow

import (
	"errors"
	"strings"
	"testing"
)

func routingStepWithTargets(targets ...string) *RoutingPlanStep {
	routes := make([]RoutingRoute, 0, len(targets))
	for index, target := range targets {
		routes = append(routes, RoutingRoute{
			RouteID:    "route-" + string(rune('a'+index)),
			RouteName:  "Route " + string(rune('A'+index)),
			Condition:  "condition",
			NextStepID: target,
		})
	}
	return &RoutingPlanStep{
		Type: StepTypeRouting,
		CommonStepFields: CommonStepFields{
			ID:    "router",
			Title: "Router",
		},
		RoutingQuestion: "Which route?",
		Routes:          routes,
		DefaultRouteID:  "route-a",
	}
}

func TestValidatePlanStructureReportsEveryDanglingReference(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		routingStepWithTargets("missing-a", "missing-b"),
		&BranchPlanStep{Type: StepTypeBranch, CommonStepFields: CommonStepFields{ID: "approval", Title: "Approval"},
			BranchQuestion: "Approve?", RouteSource: "human", Routes: []RoutingRoute{
				{RouteID: "yes", RouteName: "Approve", NextStepID: "missing-c"},
				{RouteID: "no", RouteName: "Reject", NextStepID: "end"},
			},
		},
		&ScriptedPlanStep{
			Type:             StepTypeScripted,
			CommonStepFields: CommonStepFields{ID: "scripted"},
			NextStepID:       "missing-d",
		},
	}}

	err := ValidatePlanStructure(plan)
	if err == nil {
		t.Fatal("expected graph validation error")
	}
	var validationErr *PlanValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *PlanValidationError", err)
	}
	for _, want := range []string{
		"PLAN_GRAPH_INVALID: 4 next-step reference(s)",
		`route "route-a".next_step_id in step "router" points to missing step "missing-a"`,
		`route "route-b".next_step_id in step "router" points to missing step "missing-b"`,
		`route "yes".next_step_id in step "approval" points to missing step "missing-c"`,
		`next_step_id in step "scripted" points to missing step "missing-d"`,
		"No changes were saved",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q:\n%s", want, err)
		}
	}
}
