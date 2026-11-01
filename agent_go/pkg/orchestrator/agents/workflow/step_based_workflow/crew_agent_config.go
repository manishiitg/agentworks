package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
)

// CrewAgentConfiguration deliberately carries no session/native handle or
// conversation history. The workflow executor owns its run-scoped memory.
type CrewAgentConfiguration struct {
	Name         string
	Instructions string
	Model        *AgentLLMConfig
	Servers      []string
	Tools        []string
	Skills       []string
}

// The server's existing Crew service resolves configuration through the trusted
// executable-to-Crew binding. Tool input cannot nominate another Crew or owner.
type CrewAgentConfigurationResolver interface {
	ResolveCrewAgentConfiguration(context.Context, string, string) (CrewAgentConfiguration, error)
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveCrewAgentStep(ctx context.Context, step *AgentPlanStep) (*AgentPlanStep, error) {
	if strings.TrimSpace(step.CrewAgent) == "" || step.crewAgentResolved {
		return step, nil
	}
	opts := hcpo.GetExecutionOptions()
	if opts == nil {
		return nil, fmt.Errorf("step %q: Crew agent configuration is unavailable", step.ID)
	}
	resolver, ok := opts.CrewRunner.(CrewAgentConfigurationResolver)
	if !ok {
		return nil, fmt.Errorf("step %q: Crew agent configuration is unavailable", step.ID)
	}
	resolved, err := resolver.ResolveCrewAgentConfiguration(ctx, hcpo.GetWorkspacePath(), step.CrewAgent)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.ID, err)
	}
	copy := *step
	copy.crewAgentResolved = true
	cfg := AgentConfigs{}
	if step.AgentConfigs != nil {
		cfg = *step.AgentConfigs
	}
	// Explicit step overrides stay intentional. The saved Crew configuration
	// supplies defaults; the workflow's existing tool/folder limits still apply.
	if cfg.ExecutionLLM == nil && cfg.ExecutionTier == "" {
		cfg.ExecutionLLM = resolved.Model
	}
	if len(cfg.SelectedServers) == 0 {
		cfg.SelectedServers = append([]string{}, resolved.Servers...)
	}
	if len(cfg.SelectedTools) == 0 {
		cfg.SelectedTools = append([]string{}, resolved.Tools...)
	}
	if cfg.EnabledSkills == nil {
		cfg.EnabledSkills = append([]string{}, resolved.Skills...)
	}
	copy.AgentConfigs = &cfg
	if copy.AuthoredPrompt {
		copy.SystemPrompt += "\n\n" + resolved.Instructions
	} else {
		copy.SystemRules += "\n\n" + resolved.Instructions
	}
	return &copy, nil
}
