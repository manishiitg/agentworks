package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestScriptedToolNamesArePlanMutationsAndLegacyNamesAreNot(t *testing.T) {
	for _, name := range []string{"add_scripted_step", "update_scripted_step"} {
		if !IsPlanModificationTool(name) {
			t.Fatalf("expected %q to be recognized as a plan mutation", name)
		}
	}
	for _, name := range []string{"add_regular_step", "update_regular_step"} {
		if IsPlanModificationTool(name) {
			t.Fatalf("legacy authoring tool %q must not remain callable", name)
		}
	}
}

func TestScriptedToolSchemasExposeNextStepID(t *testing.T) {
	for name, schema := range map[string]string{
		"add_scripted_step":    getAddScriptedStepSchema(),
		"update_scripted_step": getUpdateScriptedStepSchema(),
	} {
		var decoded struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal([]byte(schema), &decoded); err != nil {
			t.Fatalf("%s schema is invalid JSON: %v", name, err)
		}
		if _, ok := decoded.Properties["next_step_id"]; !ok {
			t.Fatalf("%s schema does not expose next_step_id", name)
		}
	}
}

func TestScriptedStepNextStepIDRoundTripAndUpdate(t *testing.T) {
	original := &ScriptedPlanStep{
		Type:             StepTypeScripted,
		CommonStepFields: CommonStepFields{ID: "script-a"},
		NextStepID:       "script-b",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ScriptedPlanStep
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.NextStepID != "script-b" {
		t.Fatalf("round-trip next_step_id = %q", decoded.NextStepID)
	}

	updated := mergePartialStepUpdate(original, PartialPlanStep{NextStepID: "shared"}).(*ScriptedPlanStep)
	if updated.NextStepID != "shared" {
		t.Fatalf("updated next_step_id = %q, want shared", updated.NextStepID)
	}
}

func TestUpdateAgentExecutorAtomicallyPersistsLegacyUpgrade(t *testing.T) {
	legacyPlan := &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{
			Type: StepTypeScripted,
			CommonStepFields: CommonStepFields{
				ID:                  "voice-latency-collector",
				Title:               "Voice latency collector",
				Description:         "Collect voice latency.",
				ContextDependencies: []string{"input.json"},
				ContextOutput:       FlexibleContextOutput("latency.json"),
			},
			NextStepID: "end",
		},
	}}
	planJSON, err := json.Marshal(legacyPlan)
	if err != nil {
		t.Fatalf("marshal legacy plan: %v", err)
	}
	stepConfigJSON := `{"steps":[{"id":"voice-latency-collector","agent_configs":{"declared_execution_mode":"agentic"}}]}`
	var writtenPlan string
	readFile := func(_ context.Context, path string) (string, error) {
		switch {
		case strings.HasSuffix(path, "planning/plan.json"):
			return string(planJSON), nil
		case strings.HasSuffix(path, "planning/step_config.json"):
			return stepConfigJSON, nil
		default:
			return "", errors.New("not found")
		}
	}
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "planning/plan.json") {
			writtenPlan = content
		}
		return nil
	}
	executor := createUpdateAgentStepExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	result, err := executor(context.Background(), map[string]interface{}{
		"existing_step_id": "voice-latency-collector",
		"description":      "Collect voice latency and verify per-language sample coverage.",
		"items": []interface{}{
			map[string]interface{}{
				"id":      "verify-language-coverage",
				"type":    "user_message",
				"message": "Verify every supported language has non-zero samples and repair missing evidence.",
			},
		},
		"reason": "repair the recurring missing per-language measurement evidence",
	})
	if err != nil {
		t.Fatalf("legacy agent update failed: %v", err)
	}
	if !strings.Contains(result, "upgraded its saved legacy scripted type to agent") {
		t.Fatalf("update result did not disclose compatibility upgrade: %s", result)
	}
	if writtenPlan == "" {
		t.Fatal("updated plan was not persisted")
	}

	var persisted PlanningResponse
	if err := json.Unmarshal([]byte(writtenPlan), &persisted); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	sequence, ok := persisted.Steps[0].(*AgentPlanStep)
	if !ok {
		t.Fatalf("persisted step type = %T, want *AgentPlanStep", persisted.Steps[0])
	}
	if sequence.Description != "Collect voice latency and verify per-language sample coverage." {
		t.Fatalf("description was not updated: %q", sequence.Description)
	}
	if len(sequence.Items) != 1 || sequence.Items[0].ID != "verify-language-coverage" {
		t.Fatalf("items were not updated: %#v", sequence.Items)
	}
	if sequence.ContextOutput.String() != "latency.json" || sequence.NextStepID != "end" {
		t.Fatalf("legacy fields were not preserved: %#v", sequence)
	}
}

func TestCollectScriptedPlanStepsIncludesNestedTodoRoutes(t *testing.T) {
	regular := &ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "fetch-data", Title: "Fetch data"}}
	sequence := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "analyze-data"}}
	nested := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "nested"},
		PredefinedRoutes: []PlanOrchestrationRoute{
			{RouteID: "fetch-data", SubAgentStep: regular},
			{RouteID: "analyze-data", SubAgentStep: sequence},
		},
	}
	root := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "root"},
		PredefinedRoutes: []PlanOrchestrationRoute{{RouteID: "nested", SubAgentStep: nested}},
	}

	got := collectScriptedPlanSteps(root)
	if len(got) != 1 || got[0] != regular {
		t.Fatalf("expected only the nested scripted boundary, got %#v", got)
	}
}

func TestAgentRejectsIncompleteAgentRoute(t *testing.T) {
	step := &AgentPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "orchestrate",
			Title:       "Orchestrate",
			Description: "Delegate work.",
		},
		Items:      []AgentItem{{ID: "delegate", Type: "user_message", Message: "Delegate work."}},
		NextStepID: "end",
		PredefinedRoutes: []PlanOrchestrationRoute{{
			RouteID:   "analyze",
			RouteName: "Analyze",
			SubAgentStep: &AgentPlanStep{CommonStepFields: CommonStepFields{
				ID:          "analyze",
				Title:       "Analyze",
				Description: "Analyze the evidence.",
			}},
		}},
	}

	if err := validateAgentStepFieldsTyped(step); err == nil {
		t.Fatal("expected an empty agent route to fail validation")
	}
	step.PredefinedRoutes[0].SubAgentStep.(*AgentPlanStep).Items = []AgentItem{{
		ID:      "analyze",
		Type:    "user_message",
		Message: "Analyze the evidence and save the result.",
	}}
	if err := validateAgentStepFieldsTyped(step); err != nil {
		t.Fatalf("expected a complete agent route to pass validation: %v", err)
	}
}
