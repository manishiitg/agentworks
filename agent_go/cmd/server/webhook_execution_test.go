package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestDirectWebhookRequestPreservesBindingWithoutBuilder(t *testing.T) {
	sctx := &ScheduleContext{
		WorkspacePath: "Workflow/smoke",
		Schedule:      WorkflowSchedule{Name: "PR smoke", GroupNames: []string{"preprod"}, RouteSelections: map[string]string{"router": "auth_gate"}},
		WebhookInput:  &WorkflowWebhookDelivery{RunID: "delivery-1", Variables: map[string]string{"base_url": "https://preview.test"}},
	}
	req := map[string]interface{}{
		"agent_mode": "workflow_phase", "phase_id": "workflow_builder", "keep_native_session_alive": true,
		"selected_folder": sctx.WorkspacePath, "triggered_by": "webhook",
		"execution_options": map[string]interface{}{
			"selected_run_folder": "iteration-0", "execution_strategy": stepworkflow.ExecutionStrategyResumeFromStepNoHuman,
			"resume_from_step": 3, "capacity_account_key": "account-hash", "pace_threshold_percent": 80, "execution_mode": "close_only",
		},
	}
	opts, err := configureDirectWebhookRequest(req, sctx, "iteration-12-hook")
	if err != nil {
		t.Fatal(err)
	}
	if req["agent_mode"] != "workflow" || req["phase_id"] != nil || req["keep_native_session_alive"] != nil {
		t.Fatalf("builder path retained: %v", req)
	}
	if opts.SelectedRunFolder != "iteration-12-hook" || opts.RouteSelections["router"] != "auth_gate" || len(opts.EnabledGroupNames) != 1 || opts.EnabledGroupNames[0] != "preprod" {
		t.Fatalf("binding lost: %+v", opts)
	}
	if opts.ResumeFromStep != 3 || opts.CapacityAccountKey != "account-hash" || opts.PaceThresholdPercent != 80 {
		t.Fatalf("capacity resume lost: %+v", opts)
	}
	if opts.WebhookVariables["base_url"] != "https://preview.test" || opts.WebhookInputFile == "" {
		t.Fatal("delivery context lost")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "preview.test") || strings.Contains(string(encoded), opts.WebhookInputFile) {
		t.Fatal("internal binding leaked into public query options")
	}
	var decoded QueryRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ExecutionOptions.SelectedRunFolder != "iteration-12-hook" || decoded.ExecutionOptions.RouteSelections["router"] != "auth_gate" || decoded.ExecutionOptions.ExecutionMode != "close_only" {
		t.Fatalf("query setup binding lost: %+v", decoded.ExecutionOptions)
	}
	sctx.Schedule.RouteSelections = nil
	opts, err = configureDirectWebhookRequest(req, sctx, "iteration-13-hook")
	if err != nil || len(opts.RouteSelections) != 0 {
		t.Fatalf("full-plan binding: %+v %v", opts, err)
	}
}

func TestDirectWebhookRequiresPreparedWorkflow(t *testing.T) {
	for _, manifest := range []*WorkflowManifest{nil, {}, {Version: "1.0.1"}} {
		if err := directWebhookPreflight(manifest); !errors.Is(err, errWorkflowContractMigrationRequired) {
			t.Fatalf("unprepared workflow allowed: %v", err)
		}
	}
	for _, version := range []string{
		workflowContractExplicitSchedulePulseVersion,
		workflowContractRunScopedRoutesVersion,
		workflowContractEvalRetirementVersion,
	} {
		if err := directWebhookPreflight(&WorkflowManifest{Version: version}); !errors.Is(err, errWorkflowContractMigrationRequired) {
			t.Fatalf("older contract %s bypassed the nested-artifact migration: %v", version, err)
		}
	}
	if err := directWebhookPreflight(&WorkflowManifest{Version: WorkflowContractCurrentVersion, CodeLayoutVersion: 1}); err != nil {
		t.Fatalf("current prepared workflow rejected: %v", err)
	}
}

func TestDirectWebhookCapacityResumeUsesRecordedFolder(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/relay-resume-folder"
	if err := os.MkdirAll(filepath.Join(docs, workspace), 0700); err != nil {
		t.Fatal(err)
	}
	folder, err := allocateWebhookRunFolder(workspace, "original")
	if err != nil {
		t.Fatal(err)
	}
	sctx := &ScheduleContext{WorkspacePath: workspace, CapacityResumeRunID: "original", CapacityResumeRunFolder: folder}
	got, err := webhookExecutionRunFolder(sctx, "original")
	if err != nil || got != folder {
		t.Fatalf("resume folder = %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(docs, workspace, "runs", folder, ".webhook-run-id")); err != nil {
		t.Fatal(err)
	}
	if _, err := webhookExecutionRunFolder(sctx, "original"); err == nil {
		t.Fatal("missing resume binding allocated a replacement folder")
	}
	folders, err := os.ReadDir(filepath.Join(docs, workspace, "runs"))
	if err != nil || len(folders) != 1 {
		t.Fatalf("replacement folder created: %v %v", folders, err)
	}
}
