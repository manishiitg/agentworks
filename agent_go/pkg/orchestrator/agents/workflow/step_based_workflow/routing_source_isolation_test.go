package step_based_workflow

import (
	"strings"
	"testing"
)

func routeIsolationTestPlan(routeSource string, dependencies []string) *PlanningResponse {
	return &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{
			Type: StepTypeScripted,
			CommonStepFields: CommonStepFields{
				ID:                  "gate",
				Title:               "Gate",
				ContextDependencies: []string{},
				ContextOutput:       FlexibleContextOutput(routeSelectionFileName),
			},
		},
		&BranchPlanStep{
			Type: StepTypeBranch,
			CommonStepFields: CommonStepFields{
				ID:                  "branch",
				Title:               "Branch",
				ContextDependencies: dependencies,
			},
			BranchQuestion:  "Continue?",
			RouteSourceFile: routeSource,
			Routes: []RoutingRoute{
				{RouteID: "review", RouteName: "Review", NextStepID: "review"},
				{RouteID: "skip", RouteName: "Skip", NextStepID: "skip"},
			},
		},
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "review", Title: "Review", ContextDependencies: []string{}}, NextStepID: "end"},
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "skip", Title: "Skip", ContextDependencies: []string{}}, NextStepID: "end"},
	}}
}

func TestValidatePlanStructureRejectsSharedRouteMirrorWhenRunProducerExists(t *testing.T) {
	plan := routeIsolationTestPlan("db/assets/route_selection.json", nil)
	err := ValidatePlanStructure(plan)
	if err == nil {
		t.Fatal("expected shared route mirror to be rejected")
	}
	for _, want := range []string{"db/assets/route_selection.json", "gate", "context_dependencies", "parallel runs"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestValidatePlanStructureAllowsRunScopedRouteDependency(t *testing.T) {
	plan := routeIsolationTestPlan("", []string{routeSelectionFileName})
	if err := ValidatePlanStructure(plan); err != nil {
		t.Fatalf("expected run-scoped route dependency to validate: %v", err)
	}
}

func TestValidatePlanStructureAllowsIntentionalSharedRouteSource(t *testing.T) {
	plan := routeIsolationTestPlan("db/assets/manual_route_selection.json", nil)
	// This source is intentionally distinct from the producer's canonical
	// route_selection.json and therefore remains a valid shared input.
	if err := ValidatePlanStructure(plan); err != nil {
		t.Fatalf("expected intentional shared route source to validate: %v", err)
	}
}

func TestLegacyPlanLoadRemainsRepairable(t *testing.T) {
	plan := routeIsolationTestPlan("db/assets/route_selection.json", nil)
	if err := validateLoadedPlanStructure(plan); err != nil {
		t.Fatalf("legacy unsafe plan must remain loadable for repair: %v", err)
	}
}
