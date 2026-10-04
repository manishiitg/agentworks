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

// PLAT-441: a Relay agent may call saved Python scripts as named tools
// (scripted routes); it may not have sub-agents or a script that is rewritten.
func TestRelayAgentMayOwnScriptToolsButNotSubAgents(t *testing.T) {
	route := func(step PlanStepInterface) PlanOrchestrationRoute {
		return PlanOrchestrationRoute{RouteID: step.GetID(), RouteName: step.GetID(), Condition: "When a customer id is known", SubAgentStep: step}
	}
	lookup := &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "lookup-customer", Title: "Lookup", Description: "Look up one customer"}, ScriptOnly: true}

	plan := relayTestPlan()
	plan.Steps[3].(*MessageSequencePlanStep).PredefinedRoutes = []PlanOrchestrationRoute{route(lookup)}
	if err := ValidateRelayPlanStructure(plan, "answer"); err != nil {
		t.Fatalf("a script tool on a Relay agent was rejected: %v", err)
	}

	rewritable := *lookup
	rewritable.ScriptOnly = false
	plan = relayTestPlan()
	plan.Steps[3].(*MessageSequencePlanStep).PredefinedRoutes = []PlanOrchestrationRoute{route(&rewritable)}
	if err := ValidateRelayPlanStructure(plan, "answer"); err == nil || !strings.Contains(err.Error(), "script_only") {
		t.Fatalf("a script tool that could be rewritten was accepted: %v", err)
	}

	subAgent := &MessageSequencePlanStep{CommonStepFields: CommonStepFields{ID: "helper", Title: "Helper", Description: "d"}, AuthoredPrompt: true, SystemPrompt: "x",
		Items: []MessageSequenceItem{{ID: "turn", Type: "user_message", Message: "go"}}}
	plan = relayTestPlan()
	plan.Steps[3].(*MessageSequencePlanStep).PredefinedRoutes = []PlanOrchestrationRoute{route(subAgent)}
	if err := ValidateRelayPlanStructure(plan, "answer"); err == nil || !strings.Contains(err.Error(), "sub-agents") {
		t.Fatalf("a sub-agent on a Relay agent was accepted: %v", err)
	}
}

func TestAuthoredAgentKeepsItsPromptThroughDelegation(t *testing.T) {
	lookup := &RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "lookup-customer", Title: "Lookup", Description: "Look up one customer"}, ScriptOnly: true}
	agent := &MessageSequencePlanStep{
		CommonStepFields: CommonStepFields{ID: "deep-agent", Title: "Deep", Description: "Produce JSON"},
		AuthoredPrompt:   true, SystemPrompt: "Return JSON about {{input.kind}}",
		Items:            []MessageSequenceItem{{ID: "turn", Type: "user_message", Message: "go"}},
		PredefinedRoutes: []PlanOrchestrationRoute{{RouteID: "lookup-customer", RouteName: "Lookup customer", Condition: "When a customer id is known", SubAgentStep: lookup}},
	}
	orchestratorStep := delegatingMessageSequenceAsOrchestrator(agent)
	if !orchestratorStep.AuthoredPrompt || orchestratorStep.SystemPrompt != agent.SystemPrompt {
		t.Fatalf("the delegation runtime lost the authored prompt: %+v", orchestratorStep)
	}
	block := authoredRoutesPromptBlock(agent.PredefinedRoutes)
	for _, want := range []string{"`lookup_customer`", "When a customer id is known", "final answer is still the JSON"} {
		if !strings.Contains(block, want) {
			t.Errorf("tools block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Sub-agents") {
		t.Errorf("a script-only agent must not be told about sub-agents:\n%s", block)
	}
	if authoredRoutesPromptBlock(nil) != "" {
		t.Error("an agent without routes gets no block")
	}
	if err := validateMessageSequenceStepFieldsTyped(agent); err != nil {
		t.Errorf("an authored agent with routes must pass plan validation: %v", err)
	}
}
