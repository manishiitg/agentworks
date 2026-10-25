package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExplicitConversationScriptConversationPlanValidates(t *testing.T) {
	var plan PlanningResponse
	err := json.Unmarshal([]byte(`{
		"steps":[
			{
				"id":"prepare-input",
				"type":"agent",
				"title":"Prepare input",
				"description":"Prepare durable input for the deterministic transform.",
				"context_output":"db/prepared-input.json",
				"items":[{"id":"verify-input","type":"user_message","message":"Verify the durable input and correct it if needed."}],
				"validation_schema":{"files":[{"file_name":"db/prepared-input.json","must_exist":true}]}
			},
			{
				"id":"transform-input",
				"type":"scripted",
				"title":"Transform input",
				"description":"Run the standalone deterministic transform from learnings/transform-input/main.py.",
				"context_dependencies":["db/prepared-input.json"],
				"context_output":"db/transformed-output.json",
				"validation_schema":{"files":[{"file_name":"db/transformed-output.json","must_exist":true}]}
			},
			{
				"id":"review-output",
				"type":"agent",
				"title":"Review output",
				"description":"Review the durable transform output and decide the next action.",
				"context_dependencies":["db/transformed-output.json"],
				"items":[{"id":"critique-output","type":"user_message","message":"Critique the transformed output against the workflow goal."}],
				"validation_schema":{"files":[{"file_name":"db/transformed-output.json","must_exist":true}]}
			}
		]
	}`), &plan)
	if err != nil {
		t.Fatalf("decode explicit context-flow plan: %v", err)
	}
	if err := ValidatePlanStructure(&plan); err != nil {
		t.Fatalf("agent -> scripted step -> agent plan should validate: %v", err)
	}
}

func TestAgentValidatorRejectsLegacyCodeItem(t *testing.T) {
	step := &AgentPlanStep{
		Type: StepTypeAgent,
		CommonStepFields: CommonStepFields{
			ID:          "legacy",
			Title:       "Legacy",
			Description: "Old code sequence",
		},
		Items: []AgentItem{{ID: "run", Type: "code"}},
	}
	err := validateAgentStepFieldsTyped(step)
	if err == nil || !strings.Contains(err.Error(), `"code"`) {
		t.Fatalf("validation error = %v, want removed code-item refusal", err)
	}
}

// PLAT-825-111: removing the converter also removes its special loader.
func TestPlanMutationRefusesRemovedInlineCodeAndLoadsCurrentItems(t *testing.T) {
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "task", Title: "Task", Description: "Review the result."}, Items: []AgentItem{{ID: "run", Type: "code"}}}
	plan := &PlanningResponse{Steps: []PlanStepInterface{step}}
	read := func(context.Context, string) (string, error) { raw, err := json.Marshal(plan); return string(raw), err }
	if _, err := readPlanForMutation(context.Background(), "Workflow/example", read); err == nil || !strings.Contains(err.Error(), `"code"`) {
		t.Fatalf("removed code item was not refused by the actual loader: %v", err)
	}
	step.Items = []AgentItem{{ID: "review", Type: "user_message", Message: "Review the saved evidence."}}
	if _, err := readPlanForMutation(context.Background(), "Workflow/example", read); err != nil {
		t.Fatalf("current conversational item was refused: %v", err)
	}
}
