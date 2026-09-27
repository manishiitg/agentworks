package server

import (
	"encoding/json"
	"testing"
)

func TestApplyRelayResult(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.RelayOutputStepID = "answer"
	result := webhookRunResult{Status: "completed", Terminal: true, Steps: []webhookStepOutput{
		{StepID: "prepare", Outputs: map[string]interface{}{"result.json": map[string]interface{}{"ignored": true}}},
		{StepID: "answer", Outputs: map[string]interface{}{"result.json": map[string]interface{}{"ok": true}}},
	}}
	applyRelayResult(manifest, &result)
	var output map[string]interface{}
	if err := json.Unmarshal(result.Result, &output); err != nil || output["ok"] != true || result.Error != "" {
		t.Fatalf("Relay result = %s, error = %q, decode error = %v", result.Result, result.Error, err)
	}
	manifest.RelayOutputStepID = "missing"
	result.Result = nil
	applyRelayResult(manifest, &result)
	if result.Status != "failed" || result.Error == "" {
		t.Fatalf("missing Relay result was accepted: %+v", result)
	}
}

func TestOrdinaryWorkflowResultUnchanged(t *testing.T) {
	manifest := NewWorkflowManifest("Ordinary workflow")
	result := webhookRunResult{Status: "completed", Terminal: true, Steps: []webhookStepOutput{{StepID: "answer", Outputs: map[string]interface{}{"result.json": map[string]interface{}{"ok": true}}}}}
	applyRelayResult(manifest, &result)
	if result.Status != "completed" || result.Error != "" || len(result.Result) != 0 {
		t.Fatalf("ordinary workflow result changed: %+v", result)
	}
}

func TestRelayManifestKindValidation(t *testing.T) {
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.RelayOutputStepID = "answer"
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("relay manifest rejected: %v", err)
	}
	if manifest.PulseEnabled() || manifest.EffectivePulseMode(WorkflowSchedule{}) != schedulePulseModeOff {
		t.Fatal("Relay enabled Pulse")
	}
	manifest.Schedules = []WorkflowSchedule{{ID: "timer", ScheduleType: "cron", CronExpression: "0 * * * *", GroupNames: []string{"default"}}}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("Relay accepted a cron schedule")
	}
	manifest.Schedules = nil
	manifest.Kind = "other"
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("unknown workflow kind accepted")
	}
}
