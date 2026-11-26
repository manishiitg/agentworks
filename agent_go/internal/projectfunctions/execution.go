package projectfunctions

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"

// ExecutionTools is the shared admission and display group for published
// function calls from Crew and workflow execution agents. Each caller receives
// its own tool slice, so filtering cannot change another agent's admission.
func ExecutionTools() agentprofiles.ExecutionFunctionTools {
	return agentprofiles.ExecutionFunctionTools{
		Category: "crew_function_tools",
		Tools:    []string{"project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply"},
	}
}
