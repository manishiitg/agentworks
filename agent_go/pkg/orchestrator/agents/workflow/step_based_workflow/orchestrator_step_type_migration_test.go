package step_based_workflow

import (
	"strings"
	"testing"
)

func TestMigrateOrchestratorStepTypeContentRewritesOnlyTypeDiscriminators(t *testing.T) {
	plan := `{
  "steps": [
    {"type": "todo_task", "id": "a", "description": "prose: type is todo_task"},
    {"type":"todo_task","id":"b","predefined_routes":[{"route_id":"r","sub_agent_step":{"type": "todo_task","id":"n"}}]},
    {"type": "agent", "id": "c"}
  ]
}`
	got, count := migrateOrchestratorStepTypeContent(plan)
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if strings.Contains(got, `"type": "todo_task"`) || strings.Contains(got, `"type":"todo_task"`) {
		t.Fatalf("legacy discriminator survived: %s", got)
	}
	if !strings.Contains(got, `"type": "orchestrator"`) || !strings.Contains(got, `"type":"orchestrator"`) {
		t.Fatalf("new discriminator missing (both spacings must be preserved): %s", got)
	}
	if !strings.Contains(got, "type is todo_task") {
		t.Fatalf("prose mentioning todo_task must be left alone: %s", got)
	}
	again, count2 := migrateOrchestratorStepTypeContent(got)
	if count2 != 0 || again != got {
		t.Fatalf("migration is not idempotent: count=%d", count2)
	}
}

func TestIsOrchestratorStepTypeAcceptsLegacyAlias(t *testing.T) {
	for _, s := range []string{"orchestrator", "todo_task", " todo_task "} {
		if !IsOrchestratorStepType(s) {
			t.Fatalf("%q should be an orchestrator step type", s)
		}
	}
	for _, s := range []string{"agent", "routing", ""} {
		if IsOrchestratorStepType(s) {
			t.Fatalf("%q should not be an orchestrator step type", s)
		}
	}
}

func TestLegacyPlanTypeParsesAsOrchestrator(t *testing.T) {
	step, err := unmarshalStepFromJSON([]byte(`{"type":"todo_task","id":"legacy","title":"Legacy","description":"d"}`))
	if err != nil {
		t.Fatal(err)
	}
	if step.StepType() != StepTypeAgent {
		t.Fatalf("StepType() = %q, want %q", step.StepType(), StepTypeAgent)
	}
	if o, ok := step.(*AgentPlanStep); !ok || o.Type != StepTypeAgent {
		t.Fatalf("legacy type was not normalized on parse: %#v", step)
	}
}
