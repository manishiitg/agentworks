package step_based_workflow

import (
	"testing"
)

func TestLegacyLearningLockClearFieldsAreHonestNoOps(t *testing.T) {
	for _, field := range []string{"lock_learnings", "lock_learnings_reason"} {
		if _, ok := isRetiredStepConfigClearField(field); !ok {
			t.Fatalf("%q is not registered as retired", field)
		}
		if clearStepConfigField(&StepConfig{ID: "step", AgentConfigs: &AgentConfigs{}}, field) {
			t.Fatalf("%q was reported as an active clearable field", field)
		}
	}
}
