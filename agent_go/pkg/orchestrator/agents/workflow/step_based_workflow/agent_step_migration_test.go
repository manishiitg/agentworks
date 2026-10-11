package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentStepMigrationPreservesNestedRoutesAndContracts(t *testing.T) {
	input := `{"unknown":{"type":"message_sequence"},"steps":[{"id":"owner","type":"orchestrator","description":"Keep the words todo_task and message_sequence in user text.","messages":[{"id":"work","type":"message","message":"Do the work","custom":true}],"next_step_id":"end","predefined_routes":[{"route_id":"specialist","sub_agent_step":{"id":"child","type":"todo_task","todo_task_step":{"type":"regular","description":"Specialist charter","context_output":"proof.json"},"messages":[{"id":"verify","type":"prevalidation","validation_schema":{"files":[]}}]}},{"route_id":"script","sub_agent_step":{"id":"fetch","type":"regular","description":"Fetch data"}}]}],"orphan_steps":[{"id":"reusable","type":"message_sequence","items":[{"id":"once","type":"user_message","message":"Analyze"}],"custom":{"kept":true}}]}`
	output, count, err := MigrateAgentStepContent(input)
	if err != nil || count != 3 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["unknown"]) != `{"type":"message_sequence"}` {
		t.Fatalf("unrelated JSON changed: %s", raw["unknown"])
	}
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		t.Fatal(err)
	}
	owner := plan.Steps[0].(*AgentPlanStep)
	if owner.Type != StepTypeAgent || owner.Items[0].Type != "user_message" || owner.NextStepID != "end" || !strings.Contains(owner.Description, "todo_task and message_sequence") {
		t.Fatalf("owner contract lost: %#v", owner)
	}
	child := owner.PredefinedRoutes[0].SubAgentStep.(*AgentPlanStep)
	if child.Description != "Specialist charter" || child.ContextOutput != "proof.json" || child.Items[0].Type != "prevalidation" {
		t.Fatalf("child contract lost: %#v", child)
	}
	if owner.PredefinedRoutes[1].SubAgentStep.StepType() != StepTypeRegular {
		t.Fatal("script became agent")
	}
	if !strings.Contains(output, `"custom"`) || strings.Contains(output, `"messages"`) || strings.Contains(output, `"todo_task_step"`) {
		t.Fatalf("migration lost unknown fields or retained retired shape: %s", output)
	}
	if again, count, err := MigrateAgentStepContent(output); err != nil || count != 0 || again != output {
		t.Fatalf("migration is not idempotent: count=%d err=%v", count, err)
	}
	// Reading an old plan directly yields the exact same canonical runtime shape.
	var old PlanningResponse
	if err := json.Unmarshal([]byte(input), &old); err != nil {
		t.Fatal(err)
	}
	if got := old.Steps[0].(*AgentPlanStep); got.Items[0].Type != "user_message" || got.StepType() != StepTypeAgent {
		t.Fatalf("legacy read did not normalize: %#v", got)
	}
}
