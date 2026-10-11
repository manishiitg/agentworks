package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Human-decided branch (route_source: "human") -- the successor to
// yesno/multiple_choice human_input. See controller_branch_human.go.

func humanBranchTestRoutes() []RoutingRoute {
	return []RoutingRoute{
		{RouteID: "route-hold", RouteName: "Hold", Condition: "do nothing", NextStepID: "end"},
		{RouteID: "route-publish", RouteName: "Publish", Condition: "go", NextStepID: "publish"},
	}
}

func humanBranchTestStep(defaultRouteID string) *BranchPlanStep {
	return &BranchPlanStep{
		Type:             StepTypeBranch,
		CommonStepFields: CommonStepFields{ID: "approval-gate", Title: "Approval gate"},
		BranchQuestion:   "Publish the draft?",
		Routes:           humanBranchTestRoutes(),
		DefaultRouteID:   defaultRouteID,
		RouteSource:      "human",
	}
}

// humanBranchTestOrchestrator builds the lightest orchestrator that can run
// resolveHumanBranchSelection (it needs a logger; nothing touches the
// workspace). Mirrors TestExecuteRoutingStepRunsRealBranchExecution's setup.
func humanBranchTestOrchestrator(t *testing.T, skipHuman bool) *StepBasedWorkflowOrchestrator {
	t.Helper()
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "Workflow/human-branch-demo",
		0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	return &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, skipHumanInput: skipHuman}
}

// swapHumanBranchChoice replaces the one blocking call for the test's
// lifetime and records what the person would have been shown.
func swapHumanBranchChoice(t *testing.T, answer string, fail error) (gotQuestion *string, gotOptions *[]string) {
	t.Helper()
	prev := requestHumanBranchChoice
	gotQuestion, gotOptions = new(string), new([]string)
	requestHumanBranchChoice = func(_ *StepBasedWorkflowOrchestrator, _ context.Context, _ string, question string, options []string) (string, error) {
		*gotQuestion = question
		*gotOptions = options
		return answer, fail
	}
	t.Cleanup(func() { requestHumanBranchChoice = prev })
	return gotQuestion, gotOptions
}

func TestResolveHumanBranchAnswerAcceptsEveryAnswerShape(t *testing.T) {
	routes := humanBranchTestRoutes()
	for answer, want := range map[string]string{
		"option0":        "route-hold",
		"option1":        "route-publish",
		"1":              "route-publish",
		"Publish":        "route-publish",
		"publish":        "route-publish", // route_name, case-insensitive... and also the next_step_id
		"route-hold":     "route-hold",
		" route-publish": "route-publish",
	} {
		got, err := resolveHumanBranchAnswer(routes, answer)
		if err != nil {
			t.Errorf("answer %q: unexpected error %v", answer, err)
			continue
		}
		if got != want {
			t.Errorf("answer %q = %q, want %q", answer, got, want)
		}
	}
	for _, bad := range []string{"", "option7", "-1", "maybe"} {
		if _, err := resolveHumanBranchAnswer(routes, bad); err == nil {
			t.Errorf("answer %q should not resolve to a route", bad)
		}
	}
}

func TestHumanBranchAsksThePersonWhenNothingWasPreseeded(t *testing.T) {
	hcpo := humanBranchTestOrchestrator(t, false)
	question, options := swapHumanBranchChoice(t, "option1", nil)
	step := humanBranchTestStep("route-hold")

	sel, err := hcpo.resolveHumanBranchSelection(context.Background(), step, 3, nil)
	if err != nil {
		t.Fatalf("resolveHumanBranchSelection: %v", err)
	}
	if sel.SelectedRouteID != "route-publish" || sel.SourceKind != "human" {
		t.Fatalf("selection = %+v, want route-publish from source human", sel)
	}
	if *question != "Publish the draft?" {
		t.Fatalf("person was asked %q, want the branch_question", *question)
	}
	if strings.Join(*options, "|") != "Hold|Publish" {
		t.Fatalf("person was shown options %v, want the route names in order", *options)
	}
	// default_route_id must NOT short-circuit an interactive run.
	if sel.SelectedRouteID == step.DefaultRouteID {
		t.Fatal("interactive run took default_route_id instead of the person's answer")
	}
}

func TestHumanBranchPropagatesPromptFailure(t *testing.T) {
	hcpo := humanBranchTestOrchestrator(t, false)
	swapHumanBranchChoice(t, "", context.DeadlineExceeded)
	_, err := hcpo.resolveHumanBranchSelection(context.Background(), humanBranchTestStep(""), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to get human decision") {
		t.Fatalf("expected the prompt failure to surface, got: %v", err)
	}
}

func TestHumanBranchUnattendedRunUsesDefaultRouteAndNeverAsks(t *testing.T) {
	hcpo := humanBranchTestOrchestrator(t, true)
	asked := false
	prev := requestHumanBranchChoice
	requestHumanBranchChoice = func(*StepBasedWorkflowOrchestrator, context.Context, string, string, []string) (string, error) {
		asked = true
		return "option1", nil
	}
	t.Cleanup(func() { requestHumanBranchChoice = prev })

	sel, err := hcpo.resolveHumanBranchSelection(context.Background(), humanBranchTestStep("route-hold"), 0, nil)
	if err != nil {
		t.Fatalf("unattended run with default_route_id must succeed: %v", err)
	}
	if asked {
		t.Fatal("unattended run must never block on a person")
	}
	if sel.SelectedRouteID != "route-hold" || sel.SourceKind != "default_route_id" {
		t.Fatalf("selection = %+v, want default route-hold", sel)
	}
}

func TestHumanBranchUnattendedRunWithoutDefaultFailsActionably(t *testing.T) {
	hcpo := humanBranchTestOrchestrator(t, true)
	swapHumanBranchChoice(t, "option1", nil)
	_, err := hcpo.resolveHumanBranchSelection(context.Background(), humanBranchTestStep(""), 0, nil)
	if err == nil {
		t.Fatal("unattended run with no default and no preseed must fail, not guess")
	}
	for _, want := range []string{"unattended", `route_selections["approval-gate"]`, "default_route_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got: %v", want, err)
		}
	}
}

func TestHumanBranchWorkshopInputWinsOverAsking(t *testing.T) {
	hcpo := humanBranchTestOrchestrator(t, false)
	asked := false
	prev := requestHumanBranchChoice
	requestHumanBranchChoice = func(*StepBasedWorkflowOrchestrator, context.Context, string, string, []string) (string, error) {
		asked = true
		return "option0", nil
	}
	t.Cleanup(func() { requestHumanBranchChoice = prev })

	sel, err := hcpo.resolveHumanBranchSelection(context.Background(), humanBranchTestStep(""), 0, &ExecutionContext{WorkshopHumanInput: "Publish"})
	if err != nil {
		t.Fatalf("workshop-supplied answer must resolve: %v", err)
	}
	if asked {
		t.Fatal("workshop-supplied answer must not prompt")
	}
	if sel.SelectedRouteID != "route-publish" || sel.SourceKind != "workshop_human_input" {
		t.Fatalf("selection = %+v", sel)
	}
}

// A preseeded branch with route_source human never reaches the human path:
// the shared resolver takes default/route files first. Guard the ordering
// through the real resolver with a plain (non-human) fallback comparison.
func TestHumanBranchIsHumanRoutedOnlyForHumanSource(t *testing.T) {
	if isHumanRoutedBranch(humanBranchTestStep("")) != true {
		t.Fatal("route_source human must be recognised")
	}
	plain := humanBranchTestStep("")
	plain.RouteSource = ""
	if isHumanRoutedBranch(plain) {
		t.Fatal("a plain branch must not ask a person")
	}
	if isHumanRoutedBranch(cardinalityTestRoutingStep("mode")) {
		t.Fatal("routing steps never ask a person")
	}
}

func TestValidateBranchStepRejectsUnknownRouteSource(t *testing.T) {
	step := humanBranchTestStep("")
	step.RouteSource = "llm"
	err := validateBranchStepFieldsTyped(step)
	if err == nil || !strings.Contains(err.Error(), `unsupported route_source "llm"`) {
		t.Fatalf("expected unsupported route_source error, got: %v", err)
	}
	step.RouteSource = "human"
	if err := validateBranchStepFieldsTyped(step); err != nil {
		t.Fatalf("route_source human must validate: %v", err)
	}
}

func TestAddBranchStepPersistsRouteSourceHuman(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		cardinalityTestScriptedStep("draft"),
		cardinalityTestScriptedStep("publish"),
	}}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	add := createSingleStepAdder("workflow", loggerv2.NewNoop(), readFile, writeFile, cardinalityTestNoopMove, "branch")

	_, err := add(context.Background(), map[string]interface{}{
		"id":              "approval-gate",
		"title":           "Approval gate",
		"branch_question": "Publish the draft?",
		"route_source":    "human",
		"routes": []interface{}{
			map[string]interface{}{"route_id": "route-hold", "route_name": "Hold", "condition": "c", "next_step_id": "end"},
			map[string]interface{}{"route_id": "route-publish", "route_name": "Publish", "condition": "c", "next_step_id": "publish"},
		},
		"default_route_id":     "route-hold",
		"insert_after_step_id": "draft",
		"reason":               "person decides",
	})
	if err != nil {
		t.Fatalf("add_branch_step with route_source human: %v", err)
	}
	var written PlanningResponse
	if err := json.Unmarshal([]byte(*writtenPlan), &written); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	branch, ok := written.Steps[1].(*BranchPlanStep)
	if !ok {
		t.Fatalf("inserted step = %T, want *BranchPlanStep", written.Steps[1])
	}
	if branch.RouteSource != "human" {
		t.Fatalf("route_source not persisted: %+v", branch)
	}
}

func TestConvertHumanBranchToRoutingRejected(t *testing.T) {
	step := humanBranchTestStep("route-hold")
	// Give it non-end routes so the only objection is the human source.
	step.Routes = []RoutingRoute{
		{RouteID: "route-a", RouteName: "A", Condition: "c", NextStepID: "audit-chain"},
		{RouteID: "route-b", RouteName: "B", Condition: "c", NextStepID: "apply-chain"},
	}
	step.DefaultRouteID = "route-a"
	plan := &PlanningResponse{Steps: append([]PlanStepInterface{step}, cardinalityTestRouteTargets()...)}
	readFile, writeFile, _, writtenPlan := convertRoutingBranchTestPlanFileIO(t, plan)
	convert := createConvertRoutingBranchStepTypeExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	_, err := convert(context.Background(), map[string]interface{}{
		"existing_step_id": "approval-gate",
		"target_type":      "routing",
		"reason":           "promote",
	})
	if err == nil || !strings.Contains(err.Error(), `route_source "human"`) {
		t.Fatalf("expected rejection because routing cannot ask a person, got: %v", err)
	}
	if *writtenPlan != "" {
		t.Fatal("plan.json must not be written when the conversion is rejected")
	}
}
