package step_based_workflow

func regularStep(id string) *ScriptedPlanStep {
	return &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: id, Title: id}}
}
