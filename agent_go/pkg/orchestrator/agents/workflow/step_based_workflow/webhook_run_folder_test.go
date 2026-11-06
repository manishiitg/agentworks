package step_based_workflow

import (
	"encoding/json"
	"testing"
)

func TestWebhookRunFolderAndGroupIsolation(t *testing.T) {
	binding := &WebhookInvocation{RunFolder: "run-webhook-example"}
	if err := binding.ClaimGroup("dev"); err != nil {
		t.Fatal(err)
	}
	if err := binding.ClaimGroup("dev"); err == nil {
		t.Fatal("same group can overwrite outputs")
	}
	if err := binding.ClaimGroup("prod"); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookVariableOverridesDoNotMutateSavedGroups(t *testing.T) {
	saved := []VariableGroup{{Name: "dev", Values: map[string]string{"base_url": "saved", "suite": "smoke"}}}
	run := applyWebhookVariableOverrides(saved, map[string]string{"base_url": "temporary"})
	if run[0].Values["base_url"] != "temporary" || run[0].Values["suite"] != "smoke" || saved[0].Values["base_url"] != "saved" {
		t.Fatal("run variables not isolated")
	}
	run[0].Values["suite"] = "changed"
	if saved[0].Values["suite"] != "smoke" {
		t.Fatal("group map shared")
	}
}

func TestWebhookProgressPersistsWithoutLiveEventBridge(t *testing.T) {
	files := map[string]string{"Workflow/instagram/planning/plan.json": snapshotTestPlan(t, "smoke", "Smoke test")}
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: newFakeWorkspaceAPIWithContent(t, files), selectedRunFolder: "iteration-3-hook/dev", executionOptions: &ExecutionOptions{WebhookInputFile: "delivery.json"}}
	plan, err := controller.ReadCurrentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	controller.emitStepStartedEvent(t.Context(), plan.Steps[0], 0, "")
	const file = "Workflow/instagram/runs/iteration-3-hook/dev/webhook_progress.json"
	var progress map[string]WebhookProgressEntry
	if err := json.Unmarshal([]byte(files[file]), &progress); err != nil || progress["smoke:"].Status != "running" {
		t.Fatalf("running progress not persisted: %s %v", files[file], err)
	}
	controller.emitStepFinishedEvent(t.Context(), plan.Steps[0], 0, "")
	if err := json.Unmarshal([]byte(files[file]), &progress); err != nil || progress["smoke:"].Status != "completed" {
		t.Fatalf("completed progress not persisted: %s %v", files[file], err)
	}
}
