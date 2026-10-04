package step_based_workflow

import (
	"fmt"
	"strings"
)

// ValidateRelayPlanStructure applies the Relay contract to the existing
// workflow plan graph. The ordinary plan validator remains authoritative for
// shared fields and references; this only narrows the allowed execution path.
func ValidateRelayPlanStructure(plan *PlanningResponse, outputStepID string) error {
	if err := ValidatePlanStructure(plan); err != nil {
		return err
	}
	if plan == nil || len(plan.Steps) == 0 {
		return fmt.Errorf("Relay needs at least one step")
	}
	if len(plan.OrphanSteps) != 0 {
		return fmt.Errorf("Relay does not support orphan steps")
	}
	outputStepID = strings.TrimSpace(outputStepID)
	if outputStepID == "" {
		return fmt.Errorf("Relay needs relay_output_step_id")
	}

	steps := make(map[string]PlanStepInterface, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.GetID()] = step
		switch s := step.(type) {
		case *MessageSequencePlanStep:
			if !s.AuthoredPrompt || strings.TrimSpace(s.SystemPrompt) == "" {
				return fmt.Errorf("Relay agent %q needs an authored system prompt", s.ID)
			}
			// A Relay agent may own saved Python scripts it calls as named tools
			// (scripted routes, PLAT-441). Sub-agents are not supported, and a
			// script is never rewritten, like a Relay script node.
			for _, route := range s.PredefinedRoutes {
				script, ok := route.SubAgentStep.(*RegularPlanStep)
				if !ok || !script.ScriptOnly {
					return fmt.Errorf("Relay agent %q route %q must be a saved script (type regular, script_only: true); Relays do not support sub-agents", s.ID, route.RouteID)
				}
			}
			for _, item := range s.Items {
				if item.Type != "" && item.Type != "user_message" {
					return fmt.Errorf("Relay agent %q supports only authored user messages", s.ID)
				}
			}
		case *RegularPlanStep:
			if !s.ScriptOnly {
				return fmt.Errorf("Relay script %q must set script_only to disable agent repair", s.ID)
			}
		case *BranchPlanStep:
			if strings.TrimSpace(s.ValuePath) == "" || len(s.ValueCases) == 0 {
				return fmt.Errorf("Relay decision %q needs value_path and value_cases", s.ID)
			}
			path := strings.TrimSpace(s.ValuePath)
			if authoredPromptVariable.FindString(path) != path || !(strings.HasPrefix(path, "{{input.") || strings.HasPrefix(path, "{{steps.")) {
				return fmt.Errorf("Relay decision %q value_path must be one input or step-output reference", s.ID)
			}
			routeIDs := map[string]bool{}
			for _, route := range s.Routes {
				routeIDs[route.RouteID] = true
			}
			for value, routeID := range s.ValueCases {
				if !routeIDs[routeID] {
					return fmt.Errorf("Relay decision %q value %q selects unknown route %q", s.ID, value, routeID)
				}
			}
		default:
			return fmt.Errorf("Relay step %q has unsupported type %q", step.GetID(), step.StepType())
		}
	}
	output, ok := steps[outputStepID].(*MessageSequencePlanStep)
	if !ok || !output.AuthoredPrompt {
		return fmt.Errorf("Relay output step %q must be an authored agent", outputStepID)
	}
	if output.NextStepID != nextStepIDSentinelEnd {
		return fmt.Errorf("Relay output step %q must set next_step_id to end", outputStepID)
	}

	// Every route must terminate at the designated output. Requiring explicit
	// successors removes the workflow engine's sequential fallthrough, which
	// could otherwise execute a sibling branch after its own path completed.
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if id == nextStepIDSentinelEnd || id == "" {
			return fmt.Errorf("Relay path ends before output step %q", outputStepID)
		}
		step, exists := steps[id]
		if !exists {
			return fmt.Errorf("Relay path references unknown step %q", id)
		}
		if state[id] == 1 {
			return fmt.Errorf("Relay graph contains a cycle at %q", id)
		}
		if state[id] == 2 || id == outputStepID {
			state[id] = 2
			return nil
		}
		state[id] = 1
		var next []string
		switch s := step.(type) {
		case *MessageSequencePlanStep:
			next = []string{s.NextStepID}
		case *RegularPlanStep:
			next = []string{s.NextStepID}
		case *BranchPlanStep:
			for _, route := range s.Routes {
				next = append(next, route.NextStepID)
			}
		}
		for _, successor := range next {
			if err := visit(strings.TrimSpace(successor)); err != nil {
				return fmt.Errorf("after Relay step %q: %w", id, err)
			}
		}
		state[id] = 2
		return nil
	}
	if err := visit(plan.Steps[0].GetID()); err != nil {
		return err
	}
	for id := range steps {
		if state[id] == 0 {
			return fmt.Errorf("Relay step %q is unreachable from Start", id)
		}
	}
	return nil
}
