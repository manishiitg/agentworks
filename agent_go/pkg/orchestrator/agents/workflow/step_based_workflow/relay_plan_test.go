package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func relayTestPlan() *PlanningResponse {
	agent := func(id, next string) *MessageSequencePlanStep {
		return &MessageSequencePlanStep{
			CommonStepFields: CommonStepFields{ID: id, Title: id, Description: "Produce JSON"},
			AuthoredPrompt:   true, SystemPrompt: "Return JSON", NextStepID: next,
			Items: []MessageSequenceItem{{ID: "turn", Type: "user_message", Message: "{{input.kind}}"}},
		}
	}
	return &PlanningResponse{Steps: []PlanStepInterface{
		agent("classify", "choose"),
		&BranchPlanStep{
			CommonStepFields: CommonStepFields{ID: "choose", Title: "Choose"},
			BranchQuestion:   "Which path?", ValuePath: "{{input.kind}}",
			ValueCases: map[string]string{"quick": "quick", "deep": "deep"},
			Routes:     []RoutingRoute{{RouteID: "quick", RouteName: "Quick", Condition: "quick", NextStepID: "quick-script"}, {RouteID: "deep", RouteName: "Deep", Condition: "deep", NextStepID: "deep-agent"}},
		},
		&RegularPlanStep{CommonStepFields: CommonStepFields{ID: "quick-script", Title: "Quick script", Description: "Produce JSON"}, ScriptOnly: true, NextStepID: "answer"},
		agent("deep-agent", "answer"),
		agent("answer", "end"),
	}}
}

func TestValidateRelayPlanStructure(t *testing.T) {
	plan := relayTestPlan()
	if err := ValidateRelayPlanStructure(plan, "answer"); err != nil {
		t.Fatalf("valid Relay graph rejected: %v", err)
	}
	plan.Steps[2].(*RegularPlanStep).ScriptOnly = false
	if err := ValidateRelayPlanStructure(plan, "answer"); err == nil || !strings.Contains(err.Error(), "script_only") {
		t.Fatalf("script with agent fallback accepted: %v", err)
	}
	plan = relayTestPlan()
	plan.Steps[3].(*MessageSequencePlanStep).NextStepID = "choose"
	if err := ValidateRelayPlanStructure(plan, "answer"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle accepted: %v", err)
	}
	plan = relayTestPlan()
	plan.Steps[3].(*MessageSequencePlanStep).NextStepID = "end"
	if err := ValidateRelayPlanStructure(plan, "answer"); err == nil || !strings.Contains(err.Error(), "before output") {
		t.Fatalf("path bypassing output accepted: %v", err)
	}
	plan = relayTestPlan()
	plan.Steps[4].(*MessageSequencePlanStep).NextStepID = ""
	if err := ValidateRelayPlanStructure(plan, "answer"); err == nil || !strings.Contains(err.Error(), "next_step_id to end") {
		t.Fatalf("output with sequential fallthrough accepted: %v", err)
	}
}

func TestRelayDecisionReadsInputValue(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewDefault(), nil, orchestrator.OrchestratorTypeWorkflow, "Workflow/demo", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, variableValues: map[string]string{"INPUT": `{"kind":"deep"}`}}
	branch := relayTestPlan().Steps[1].(*BranchPlanStep)
	selection, err := controller.resolveDeterministicRoutingSelection(context.Background(), branch, 1, "step-2", nil, nil)
	if err != nil || selection.SelectedRouteID != "deep" || selection.SourceKind != "value_path" {
		t.Fatalf("decision = %+v, %v", selection, err)
	}
}
