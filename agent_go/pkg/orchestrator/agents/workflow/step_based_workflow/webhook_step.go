package step_based_workflow

import "fmt"

// WebhookStepIndex resolves stable IDs at execution time, including after a
// plan reorder. Control-flow and human-input nodes are not standalone work.
func WebhookStepIndex(steps []PlanStepInterface, id string) (int, error) {
	for i, step := range steps {
		if step == nil || step.GetID() != id {
			continue
		}
		switch step.StepType() {
		case StepTypeRegular, StepTypeAgent, StepTypeCrew:
			return i, nil
		default:
			return 0, fmt.Errorf("step %q is not an executable webhook target; select a route instead", id)
		}
	}
	return 0, fmt.Errorf("webhook step %q is not an executable top-level plan step", id)
}

func bindWebhookStep(opts *ExecutionOptions, steps []PlanStepInterface) error {
	if opts == nil || opts.WebhookStepID == "" {
		return nil
	}
	if opts.WebhookInputFile == "" || len(opts.RouteSelections) != 0 {
		return fmt.Errorf("single-step webhook requires a delivery and no route selections")
	}
	index, err := WebhookStepIndex(steps, opts.WebhookStepID)
	if err != nil {
		return err
	}
	opts.ExecutionStrategy = ExecutionStrategyRunSingleStep
	opts.ResumeFromStep = index + 1
	return nil
}
