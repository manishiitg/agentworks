package step_based_workflow

import "fmt"

const GoalAgentContractVersion = "1.0.50"

// Script helpers share the existing typed call/child lifecycle; LLM children no
// longer belong to a Goal step. This applies to legacy saved plans too.
func goalScriptHelperTool(name string) bool {
	switch name {
	case "call_scripted_sub_agent", "get_route_description", "query_sub_agent", "stop_sub_agent", "get_sub_agent_conversation":
		return true
	}
	return false
}

func retiredAgentHelperError(name string) error {
	return fmt.Errorf("This workflow still has an old agent helper (%s). Ask the Crew's main chat to convert its agent helpers into normal agent steps before running it. Saved script helpers are still supported.", name)
}

func validateGoalScriptedRouteAuthoring(routes []PlanOrchestrationRoute, plans ...*PlanningResponse) error {
	for _, route := range routes {
		if route.SubAgentStep == nil && route.OrphanStepRef != "" {
			if len(plans) == 0 {
				continue
			} // early inline check; resolved against the plan before save
			route.SubAgentStep, _, _ = findStepByID(plans[0].OrphanSteps, route.OrphanStepRef)
		}
		if _, ok := route.SubAgentStep.(*ScriptedPlanStep); !ok {
			return retiredAgentHelperError(route.RouteID)
		}
	}
	return nil
}

// ValidateGoalAgentAuthoring keeps specialist work in an agent step using its Crew configuration.
func ValidateGoalAgentAuthoring(plan *PlanningResponse) error {
	if plan == nil {
		return fmt.Errorf("a Goal plan is required")
	}
	for _, steps := range [][]PlanStepInterface{plan.Steps, plan.OrphanSteps} {
		for _, step := range steps {
			if agent, ok := step.(*AgentPlanStep); ok {
				if err := validateGoalScriptedRouteAuthoring(agent.PredefinedRoutes, plan); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
