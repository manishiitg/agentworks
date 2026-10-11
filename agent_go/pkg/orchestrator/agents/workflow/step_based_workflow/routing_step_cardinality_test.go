package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-294: a plan has at most one routing step. The rule is enforced only on
// the mutations that would introduce a second routing step; plans that already
// carry two are left as they are.

func cardinalityTestRoutingStep(id string) *RoutingPlanStep {
	return &RoutingPlanStep{
		Type:             StepTypeRouting,
		CommonStepFields: CommonStepFields{ID: id, Title: "Mode selector " + id},
		RoutingQuestion:  "Which mode?",
		Routes:           convertRoutingBranchTestRoutes(),
		DefaultRouteID:   "route-a",
	}
}

func cardinalityTestScriptedStep(id string) *ScriptedPlanStep {
	return &ScriptedPlanStep{
		Type:             StepTypeScripted,
		CommonStepFields: CommonStepFields{ID: id, Title: "Setup " + id, Description: "deterministic setup"},
	}
}

// Routes that start real sub-workflows. The plan write validates the graph,
// so fixtures that add these routes must also contain the two target steps
// (cardinalityTestRouteTargets).
func cardinalityTestRoutesArg() []interface{} {
	return []interface{}{
		map[string]interface{}{"route_id": "route-a", "route_name": "A", "condition": "c", "next_step_id": "audit-chain"},
		map[string]interface{}{"route_id": "route-b", "route_name": "B", "condition": "c", "next_step_id": "apply-chain"},
	}
}

func cardinalityTestRouteTargets() []PlanStepInterface {
	return []PlanStepInterface{
		cardinalityTestScriptedStep("audit-chain"),
		cardinalityTestScriptedStep("apply-chain"),
	}
}

func cardinalityTestNoopMove(_ context.Context, _, _ string) error { return nil }

// A routing route straight to end is the tell for a simple if-condition
// (skip/continue, probe ok/failed): that belongs in a branch step.
func TestAddRoutingStepRejectsRouteToEnd(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{cardinalityTestScriptedStep("audit")}}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	add := createSingleStepAdder("workflow", loggerv2.NewNoop(), readFile, writeFile, cardinalityTestNoopMove, "routing")

	_, err := add(context.Background(), map[string]interface{}{
		"id":               "run-remediation-route",
		"title":            "Remediate?",
		"routing_question": "Remediate?",
		"routes": []interface{}{
			map[string]interface{}{"route_id": "skip_remediation", "route_name": "Skip", "condition": "default", "next_step_id": "end"},
			map[string]interface{}{"route_id": "remediate_now", "route_name": "Remediate", "condition": "operator asked", "next_step_id": "prepare-remediation-proposal"},
		},
		"insert_after_step_id": "audit",
		"reason":               "skip/continue gate typed as routing",
	})
	if err == nil {
		t.Fatal("expected add_routing_step to reject a route that points at end")
	}
	for _, want := range []string{`"skip_remediation"`, "points at end", "if-condition", "add_branch_step"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got: %v", want, err)
		}
	}
	if *writtenPlan != "" {
		t.Fatal("plan.json must not be written when the mutation is rejected")
	}
}

// The same route-to-end rule applies when promoting a branch to routing; the
// branch itself may of course keep its end route.
func TestConvertBranchWithEndRouteToRoutingRejected(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&BranchPlanStep{
			Type:             StepTypeBranch,
			CommonStepFields: CommonStepFields{ID: "browser-gate", Title: "Browser gate"},
			BranchQuestion:   "Browser ok?",
			Routes: []RoutingRoute{
				{RouteID: "browser_ok", RouteName: "OK", Condition: "c", NextStepID: "run-mode"},
				{RouteID: "browser_failed", RouteName: "Failed", Condition: "c", NextStepID: "end"},
			},
			DefaultRouteID: "browser_failed",
		},
	}}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	convert := createConvertRoutingBranchStepTypeExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	_, err := convert(context.Background(), map[string]interface{}{
		"existing_step_id": "browser-gate",
		"target_type":      "routing",
		"reason":           "promote",
	})
	if err == nil || !strings.Contains(err.Error(), "points at end") {
		t.Fatalf("expected rejection for the end route, got: %v", err)
	}
	if *writtenPlan != "" {
		t.Fatal("plan.json must not be written when the conversion is rejected")
	}
}

func TestAddRoutingStepRejectsSecondRoutingStep(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		cardinalityTestRoutingStep("workflow-entry-route"),
		cardinalityTestScriptedStep("audit"),
	}}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	add := createSingleStepAdder("workflow", loggerv2.NewNoop(), readFile, writeFile, cardinalityTestNoopMove, "routing")

	_, err := add(context.Background(), map[string]interface{}{
		"id":                   "run-remediation-route",
		"title":                "Run remediation route",
		"routing_question":     "Remediate?",
		"routes":               cardinalityTestRoutesArg(),
		"insert_after_step_id": "audit",
		"reason":               "second fork",
	})
	if err == nil {
		t.Fatal("expected add_routing_step to reject a second routing step")
	}
	for _, want := range []string{"at most one routing step", `"workflow-entry-route"`, "add_branch_step", "convert_routing_branch_step_type"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q so the builder knows what to do; got: %v", want, err)
		}
	}
	if *writtenPlan != "" {
		t.Fatal("plan.json must not be written when the mutation is rejected")
	}
}

func TestAddRoutingStepRejectsWhenExistingRoutingStepIsOrphan(t *testing.T) {
	plan := &PlanningResponse{
		Steps:       []PlanStepInterface{cardinalityTestScriptedStep("audit")},
		OrphanSteps: []PlanStepInterface{cardinalityTestRoutingStep("parked-router")},
	}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	add := createSingleStepAdder("workflow", loggerv2.NewNoop(), readFile, writeFile, cardinalityTestNoopMove, "routing")

	_, err := add(context.Background(), map[string]interface{}{
		"id":                   "new-router",
		"title":                "New router",
		"routing_question":     "Which?",
		"routes":               cardinalityTestRoutesArg(),
		"insert_after_step_id": "audit",
		"reason":               "orphan already holds the routing slot",
	})
	if err == nil || !strings.Contains(err.Error(), `"parked-router"`) {
		t.Fatalf("expected rejection naming the orphan routing step, got: %v", err)
	}
	if *writtenPlan != "" {
		t.Fatal("plan.json must not be written when the mutation is rejected")
	}
}

func TestAddRoutingStepAllowedWhenPlanHasNone(t *testing.T) {
	plan := &PlanningResponse{Steps: append([]PlanStepInterface{cardinalityTestScriptedStep("login")}, cardinalityTestRouteTargets()...)}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	add := createSingleStepAdder("workflow", loggerv2.NewNoop(), readFile, writeFile, cardinalityTestNoopMove, "routing")

	result, err := add(context.Background(), map[string]interface{}{
		"id":                   "mode-router",
		"title":                "Mode router",
		"routing_question":     "Which mode?",
		"routes":               cardinalityTestRoutesArg(),
		"insert_after_step_id": "login",
		"reason":               "first and only routing step",
	})
	if err != nil {
		t.Fatalf("first routing step must be accepted: %v", err)
	}
	if !strings.Contains(result, "Successfully added routing step") {
		t.Fatalf("unexpected result: %s", result)
	}
	var written PlanningResponse
	if err := json.Unmarshal([]byte(*writtenPlan), &written); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	if len(written.Steps) != 4 {
		t.Fatalf("expected 4 steps after insert, got %d", len(written.Steps))
	}
	if _, ok := written.Steps[1].(*RoutingPlanStep); !ok {
		t.Fatalf("inserted step = %T, want *RoutingPlanStep", written.Steps[1])
	}
}

// Plans authored before the rule keep their extra routing steps, and every
// other mutation keeps working on them -- there is no migration.
func TestExistingPlanWithTwoRoutingStepsStillAcceptsBranchSteps(t *testing.T) {
	plan := &PlanningResponse{Steps: append([]PlanStepInterface{
		cardinalityTestRoutingStep("workflow-entry-route"),
		cardinalityTestScriptedStep("audit"),
		cardinalityTestRoutingStep("run-remediation-route"),
	}, cardinalityTestRouteTargets()...)}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	add := createSingleStepAdder("workflow", loggerv2.NewNoop(), readFile, writeFile, cardinalityTestNoopMove, "branch")

	_, err := add(context.Background(), map[string]interface{}{
		"id":                   "post-audit-gate",
		"title":                "Post-audit gate",
		"branch_question":      "Continue?",
		"routes":               cardinalityTestRoutesArg(),
		"insert_after_step_id": "audit",
		"reason":               "small in-flow decision",
	})
	if err != nil {
		t.Fatalf("branch steps must remain addable to a grandfathered plan: %v", err)
	}
	var written PlanningResponse
	if err := json.Unmarshal([]byte(*writtenPlan), &written); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	routingCount := 0
	for _, s := range written.Steps {
		if _, ok := s.(*RoutingPlanStep); ok {
			routingCount++
		}
	}
	if routingCount != 2 {
		t.Fatalf("existing routing steps must be untouched, got %d routing steps", routingCount)
	}
}

func TestConvertBranchToRoutingRejectedWhileAnotherRoutingStepExists(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		cardinalityTestRoutingStep("mode-router"),
		&BranchPlanStep{
			Type:             StepTypeBranch,
			CommonStepFields: CommonStepFields{ID: "approval-gate", Title: "Approval gate"},
			BranchQuestion:   "Publish?",
			Routes:           convertRoutingBranchTestRoutes(),
			DefaultRouteID:   "route-a",
		},
	}}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	convert := createConvertRoutingBranchStepTypeExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	_, err := convert(context.Background(), map[string]interface{}{
		"existing_step_id": "approval-gate",
		"target_type":      "routing",
		"reason":           "promote to routing",
	})
	if err == nil {
		t.Fatal("expected conversion to routing to be rejected while mode-router exists")
	}
	if !strings.Contains(err.Error(), `"mode-router"`) || !strings.Contains(err.Error(), "at most one routing step") {
		t.Fatalf("unexpected error: %v", err)
	}
	if *writtenPlan != "" {
		t.Fatal("plan.json must not be written when the conversion is rejected")
	}

	// The other direction is the intended fix and stays open.
	_, err = convert(context.Background(), map[string]interface{}{
		"existing_step_id": "mode-router",
		"target_type":      "branch",
		"reason":           "demote",
	})
	if err != nil {
		t.Fatalf("converting a routing step to branch must always be allowed: %v", err)
	}
}
