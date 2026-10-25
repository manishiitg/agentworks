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

func goalScriptedRoutes(routes []PlanOrchestrationRoute) []PlanOrchestrationRoute {
	out := make([]PlanOrchestrationRoute, 0, len(routes))
	for _, route := range routes {
		if _, ok := route.SubAgentStep.(*ScriptedPlanStep); ok {
			out = append(out, route)
		}
	}
	return out
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
			return fmt.Errorf("route %q needs a saved scripted Python helper; put LLM specialists in an explicit Crew step", route.RouteID)
		}
	}
	return nil
}

// ValidateGoalAgentAuthoring keeps specialist work in an explicit Crew step.
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
