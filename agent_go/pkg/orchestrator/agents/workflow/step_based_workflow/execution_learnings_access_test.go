package step_based_workflow

import "testing"

func TestResolveExecutionLearningsAccessMatchesPromptGate(t *testing.T) {
	config := &AgentConfigs{LearningsAccess: LearningsAccessReadWrite, LearningObjective: "Capture reusable execution mechanics."}

	tests := []struct {
		name string
		step PlanStepInterface
		want string
	}{
		{name: "regular", step: &RegularPlanStep{}, want: LearningsAccessReadWrite},
		{name: "agent", step: &AgentPlanStep{}, want: LearningsAccessReadWrite},
		{name: "todo orchestrator", step: &AgentPlanStep{}, want: LearningsAccessReadWrite},
		{name: "deterministic routing", step: &RoutingPlanStep{}, want: LearningsAccessNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveExecutionLearningsAccess(config, tt.step); got != tt.want {
				t.Fatalf("access = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveExecutionLearningsAccessHonorsExplicitNone(t *testing.T) {
	config := &AgentConfigs{LearningsAccess: LearningsAccessNone}
	if got := resolveExecutionLearningsAccess(config, &RegularPlanStep{}); got != LearningsAccessNone {
		t.Fatalf("explicit none resolved to %q", got)
	}
}
