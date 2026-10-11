package step_based_workflow

import (
	"context"
	"testing"
)

func TestWebhookStepBindingAndBatchIsolation(t *testing.T) {
	steps := []PlanStepInterface{
		&ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "prepare"}},
		&ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "smoke"}},
		&ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: "publish"}},
	}
	opts := &ExecutionOptions{WebhookStepID: "smoke", WebhookInputFile: "delivery.json"}
	if err := bindWebhookStep(opts, steps); err != nil {
		t.Fatal(err)
	}
	if opts.ResumeFromStep != 2 || opts.ExecutionStrategy != ExecutionStrategyRunSingleStep {
		t.Fatalf("wrong selection: %+v", opts)
	}
	orch := &StepBasedWorkflowOrchestrator{BaseOrchestrator: newFakeWorkspaceAPIWithContent(t, nil)}
	orch.SetExecutionOptions(opts)
	em := &ExecutionManager{orchestrator: orch}
	setup, err := em.PrepareExecution(context.Background(), opts, nil, len(steps), "iteration-1-hook")
	if err != nil {
		t.Fatal(err)
	}
	for i, group := range []string{"dev", "prod"} {
		batch, err := em.PrepareForBatchGroup(context.Background(), group, "iteration-1-hook/"+group, len(steps), nil, true, setup.Context, i == 0)
		if err != nil {
			t.Fatal(err)
		}
		if !batch.Context.SkipHumanInput || batch.StartFromStep != 1 || !batch.Context.RunSingleStepOnly || batch.Context.SingleStepTarget != 1 {
			t.Fatalf("%s could run other steps: %+v", group, batch)
		}
	}
	steps[0], steps[1] = steps[1], steps[0]
	if err := bindWebhookStep(opts, steps); err != nil || opts.ResumeFromStep != 1 {
		t.Fatal("reorder changed target")
	}
	if err := bindWebhookStep(opts, steps[1:]); err == nil {
		t.Fatal("deleted step accepted")
	}
	opts.RouteSelections = map[string]string{"router": "route"}
	if err := bindWebhookStep(opts, steps); err == nil {
		t.Fatal("ambiguous target accepted")
	}
}
