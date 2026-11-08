package step_based_workflow

import "context"

// Optional on the existing Crew resource adapter. It appends progress to an
// authorized chat, never starts an agent or sends to an external channel.
type ScriptNotificationTarget interface {
	NotifyScript(context.Context, string, string, string, string) (string, error)
}

// CrewResources supplies saved agent configuration and scripts to workflow nodes.
type CrewResources struct {
	Agents  CrewAgentConfigurationResolver
	Scripts CrewScriptResolver
}

func bindCrewResources(controller *StepBasedWorkflowOrchestrator, resources *CrewResources) {
	if controller == nil || resources == nil {
		return
	}
	if opts := controller.GetExecutionOptions(); opts != nil {
		opts.CrewResources = resources
		return
	}
	controller.SetExecutionOptions(&ExecutionOptions{CrewResources: resources})
}
