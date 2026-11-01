package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type configurationOnlyCrewRunner struct{ configuration CrewAgentConfiguration }

func (*configurationOnlyCrewRunner) RunCrewStep(context.Context, CrewStepRequest) (CrewStepResult, error) {
	return CrewStepResult{}, fmt.Errorf("configuration reuse must not call a Crew chat")
}
func (r *configurationOnlyCrewRunner) ResolveCrewAgentConfiguration(context.Context, string, string) (CrewAgentConfiguration, error) {
	return r.configuration, nil
}

// PLAT-746-265: configuration is a per-run copy; resolving the next run must
// not mutate the saved plan, reuse a previous prompt, or drop an explicit pin.
func TestCrewAgentDefaultsArePerRunAndPreserveExplicitStepOverrides(t *testing.T) {
	var saved AgentPlanStep
	if err := json.Unmarshal([]byte(`{"id":"report","type":"agent","crew_agent":"crew:chat:reporter","description":"Write a report","prompt_contract":"description-first-v1"}`), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.CrewAgent == "" {
		t.Fatal("agent reference lost during plan decoding")
	}
	controller := newAgentClosingTestOrchestrator(t)
	runner := &configurationOnlyCrewRunner{configuration: CrewAgentConfiguration{Model: &AgentLLMConfig{Provider: "codex-cli", ModelID: "first"}, Instructions: "First saved role", Servers: []string{"github"}, Skills: []string{"research"}}}
	controller.SetExecutionOptions(&ExecutionOptions{CrewRunner: runner})
	first, err := controller.resolveCrewAgentStep(t.Context(), &saved)
	if err != nil {
		t.Fatal(err)
	}
	runner.configuration.Instructions = "Second saved role"
	runner.configuration.Model = &AgentLLMConfig{Provider: "codex-cli", ModelID: "second"}
	retained, err := controller.resolveCrewAgentStep(t.Context(), first)
	if err != nil || retained.SystemRules != first.SystemRules || retained.AgentConfigs.ExecutionLLM.ModelID != "first" {
		t.Fatal("existing run snapshot picked up another run's configuration", err)
	}
	second, err := controller.resolveCrewAgentStep(t.Context(), &saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.SystemRules, "Second") || !strings.Contains(second.SystemRules, "Second") || first.AgentConfigs.ExecutionLLM.ModelID != "first" || second.AgentConfigs.ExecutionLLM.ModelID != "second" || saved.SystemRules != "" || saved.AgentConfigs != nil {
		t.Fatal("one run changed another run's configuration or the saved plan")
	}
	saved.AgentConfigs = &AgentConfigs{ExecutionLLM: &AgentLLMConfig{Provider: "pi-cli", ModelID: "service/pinned"}, EnabledSkills: []string{"explicit-skill"}}
	pinned, err := controller.resolveCrewAgentStep(t.Context(), &saved)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.AgentConfigs.ExecutionLLM.ModelID != "service/pinned" || strings.Join(pinned.AgentConfigs.EnabledSkills, ",") != "explicit-skill" {
		t.Fatal("saved Crew defaults replaced an explicit step override")
	}
}
