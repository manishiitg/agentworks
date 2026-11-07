package step_based_workflow

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
