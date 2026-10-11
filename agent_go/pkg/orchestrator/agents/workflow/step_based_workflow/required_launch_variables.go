package step_based_workflow

import (
	"fmt"
	"strings"
)

func validateStepRequiredVariables(step PlanStep, values map[string]string) error {
	var missing []string
	for _, name := range step.GetCommonFields().RequiredVariables {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("step %q declares an empty required_variables name", step.GetID())
		}
		if strings.TrimSpace(values[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required launch variables for step %q: %s; supply declared variables at launch or in the selected variable group", step.GetID(), strings.Join(missing, ", "))
	}
	return nil
}

// Follow the known execution path, rather than requiring inputs for every mode.
// A decision produced during execution is checked again at its consumer boundary.
func validateRequiredLaunchVariables(steps []PlanStep, values map[string]string, selections map[string]string, start int, single bool) error {
	visited := map[int]bool{}
	for i := start; i >= 0 && i < len(steps) && !visited[i]; {
		visited[i] = true
		step := steps[i]
		if err := validateStepRequiredVariables(step, values); err != nil {
			return err
		}
		if single {
			return nil
		}
		next := ""
		if decision, ok := step.(routeSwitchStep); ok {
			// A branch may consume a decision that an earlier agent has not
			// produced yet, or ask a person. Its default does not preselect it.
			if step.StepType() == StepTypeBranch && strings.TrimSpace(selections[step.GetID()]) == "" {
				return nil
			}
			route := inferValidationRoute(decision, values, nil, selections)
			if route == nil {
				return nil
			}
			next = route.NextStepID
		} else {
			switch s := step.(type) {
			case *AgentPlanStep:
				next = s.NextStepID
			case *ScriptedPlanStep:
				next = s.NextStepID
			case *CrewPlanStep:
				next = s.NextStepID
			}
		}
		if strings.EqualFold(next, "end") {
			return nil
		}
		if next == "" {
			i++
			continue
		}
		i = planStepIndexByID(steps, next)
	}
	return nil
}

// This is the same immutable run selection used by seedRouteSelectionsForRun.
func (hcpo *StepBasedWorkflowOrchestrator) requiredLaunchRouteSelections() map[string]string {
	if hcpo.executionOptions == nil {
		return nil
	}
	return hcpo.executionOptions.RouteSelections
}
