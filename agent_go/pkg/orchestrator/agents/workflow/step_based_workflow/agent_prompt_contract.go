package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

const AgentDescriptionFirstContractVersion = "1.0.48"
const AgentDescriptionFirstPromptContract = "description-first-v1"
const AgentDescriptionTurnID = "__step_description__"

func agentDescriptionFirst(step *AgentPlanStep) bool {
	return step != nil && !step.AuthoredPrompt && step.PromptContract == AgentDescriptionFirstPromptContract
}

// New authoring opts in. Reading or partially editing saved agents never does:
// their first items may perform external actions and require semantic migration.
func initializeNewAgentPromptContracts(step PlanStepInterface) {
	agent, ok := step.(*AgentPlanStep)
	if !ok || agent == nil {
		return
	}
	if !agent.AuthoredPrompt && agent.PromptContract == "" {
		agent.PromptContract = AgentDescriptionFirstPromptContract
	}
	for _, route := range agent.PredefinedRoutes {
		initializeNewAgentPromptContracts(route.SubAgentStep)
	}
}

func agentDescriptionItem(step *AgentPlanStep) AgentItem {
	item := AgentItem{ID: AgentDescriptionTurnID, Type: "user_message", Kind: "execution", Message: step.Description, Synthetic: true}
	if step.InitialWriteAccess != nil {
		item.WriteAccess = *step.InitialWriteAccess
	}
	return item
}

// Apply the same authority layout to ordinary, delegating and preview renderers.
// The executable description is in history, not replayed as system instructions.
func applyAgentPromptContract(vars map[string]string, step *AgentPlanStep, values map[string]string) {
	if !agentDescriptionFirst(step) {
		return
	}
	vars["DescriptionFirst"] = "true"
	vars["StepDescription"] = ""
	vars["StepSystemRules"] = ResolveVariables(step.SystemRules, values)
}

// A stamp proves all live agent definitions were reviewed, including routes and
// orphans. It does not infer policy or delete execution items from their text.
func ValidateAgentDescriptionFirstContractStamp(content string) error {
	if err := ValidateAgentStepContractStamp(content); err != nil {
		return err
	}
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return err
	}
	if err := resolvePlanOrphanStepRefs(&plan); err != nil {
		return err
	}
	var check func(PlanStepInterface) error
	check = func(step PlanStepInterface) error {
		agent, ok := step.(*AgentPlanStep)
		if !ok {
			return nil
		}
		if !agent.AuthoredPrompt && !agentDescriptionFirst(agent) {
			return fmt.Errorf("agent %q requires reviewed description-first-v1 migration before stamping %s", agent.ID, AgentDescriptionFirstContractVersion)
		}
		if agentDescriptionFirst(agent) && len(agent.Items) > 0 {
			first := agent.Items[0]
			if strings.TrimSpace(first.RetainedReason) == "" {
				return fmt.Errorf("agent %q: first retained item %q needs retained_reason explaining its reviewed subsequent phase and protection against repeating initial work before stamping", agent.ID, first.ID)
			}
			if _, narrowed := requestedAgentItemWriteAccess(first); narrowed && agent.InitialWriteAccess == nil {
				return fmt.Errorf("agent %q: explicitly review initial_write_access before stamping when its first follow-up narrows stores", agent.ID)
			}
			if first.Type == "scripted" && strings.TrimSpace(first.Message) == "" {
				return fmt.Errorf("agent %q: script-first migration needs a message explaining its reviewed subsequent-phase ordering; prerequisites must move upstream before stamping", agent.ID)
			}
		}
		if err := validateAgentStepFieldsTyped(agent); err != nil {
			return err
		}
		if agentDescriptionFirst(agent) && strings.TrimSpace(agent.Description) == "" {
			return fmt.Errorf("agent %q requires an executable description", agent.ID)
		}
		for _, route := range agent.PredefinedRoutes {
			if err := check(route.SubAgentStep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, steps := range [][]PlanStepInterface{plan.Steps, plan.OrphanSteps} {
		for _, step := range steps {
			if err := check(step); err != nil {
				return err
			}
		}
	}
	return nil
}
