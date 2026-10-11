package step_based_workflow

import "testing"

func TestPopulateRuntimeFields_PopulatesExecutionTierForScriptedStep(t *testing.T) {
	step := &ScriptedPlanStep{
		CommonStepFields: CommonStepFields{
			ID:    "step-1",
			Title: "Scripted Step",
		},
	}

	err := populateRuntimeFields(step, []StepConfig{
		{
			ID: "step-1",
			AgentConfigs: &AgentConfigs{
				ExecutionTier: "medium",
			},
		},
	})
	if err != nil {
		t.Fatalf("populateRuntimeFields returned error: %v", err)
	}
	if step.AgentConfigs == nil {
		t.Fatal("expected AgentConfigs to be populated for scripted step")
	}
	if step.AgentConfigs.ExecutionTier != "medium" {
		t.Fatalf("expected execution_tier to be populated on scripted step, got %q", step.AgentConfigs.ExecutionTier)
	}
}
