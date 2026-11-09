package server

import (
	"context"
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
		Schedule:      WorkflowSchedule{Name: "PR smoke", InputPreset: "preprod", Inputs: map[string]string{"COUNT": "4"}, RouteSelections: map[string]string{"router": "auth_gate"}},
		WebhookInput:  &WorkflowWebhookDelivery{RunID: "delivery-1", Variables: map[string]string{"base_url": "https://preview.test"}},
	}
	req := map[string]interface{}{
		"agent_mode": "workflow_phase", "phase_id": "workflow_builder", "keep_native_session_alive": true,
		"selected_folder": sctx.WorkspacePath, "triggered_by": "webhook",
		"execution_options": map[string]interface{}{
			"selected_run_folder": "iteration-0", "execution_strategy": stepworkflow.ExecutionStrategyResumeFromStepNoHuman,
			"resume_from_step": 3, "capacity_account_key": "account-hash", "pace_threshold_percent": 80, "execution_mode": "close_only",
			"input_preset": "preprod", "inputs": map[string]string{"COUNT": "4"},
		},
	}
	opts, err := configureDirectWebhookRequest(req, sctx, "iteration-12-hook")
	if err != nil {
		t.Fatal(err)
	}
	if req["agent_mode"] != "workflow" || req["phase_id"] != nil || req["keep_native_session_alive"] != nil {
		t.Fatalf("builder path retained: %v", req)
	}
	// PLAT-746: current callers bind one input preset plus overrides; retired
	// group arrays are no longer translated into execution options.
	if opts.SelectedRunFolder != "iteration-12-hook" || opts.RouteSelections["router"] != "auth_gate" || opts.InputPreset != "preprod" || opts.Inputs["COUNT"] != "4" {
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
	if decoded.ExecutionOptions.SelectedRunFolder != "iteration-12-hook" || decoded.ExecutionOptions.RouteSelections["router"] != "auth_gate" || decoded.ExecutionOptions.ExecutionMode != "close_only" || decoded.ExecutionOptions.InputPreset != "preprod" || decoded.ExecutionOptions.Inputs["COUNT"] != "4" {
		t.Fatalf("query setup binding lost: %+v", decoded.ExecutionOptions)
	}
	sctx.Schedule.RouteSelections = nil
	opts, err = configureDirectWebhookRequest(req, sctx, "iteration-13-hook")
	if err != nil || len(opts.RouteSelections) != 0 {
		t.Fatalf("full-plan binding: %+v %v", opts, err)
	}
}

// PLAT-746-500: an archived plan cannot become executable by carrying the
// latest version. A real registered script is accepted from its current source.
func TestDirectWebhookRequiresRegisteredSavedPython(t *testing.T) {
	newCrewWorkflowRunTestEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	archived, found, err := ReadWorkflowManifest(ctx, "Workflow/test")
	if err != nil || !found {
		t.Fatalf("archived descriptor fixture unreadable: %v", err)
	}
	for _, manifest := range []*WorkflowManifest{nil, {}, archived, {Version: WorkflowContractCurrentVersion, CodeLayoutVersion: 1}, {crewExecutable: &crewExecutableBinding{Kind: "workflow"}}} {
		err := directWebhookPreflight(manifest)
		if !errors.Is(err, errWorkflowScriptRequired) {
			t.Fatalf("archived or unregistered workflow admitted: %v", err)
		}
		// The trigger records this reason for a person: say why it cannot
		// start and who can convert it, rather than suggesting a contract stamp.
		if !strings.Contains(err.Error(), "cannot start") || !strings.Contains(err.Error(), "owner") || !strings.Contains(err.Error(), "convert") {
			t.Fatalf("unconverted workflow refusal has no actionable reason: %v", err)
		}
	}
	binding := crewExecutableBinding{ID: archived.ID, CrewID: "crew-project-a", CrewRoot: crewWorkflowTestCrewPath, Root: "Workflow/test", Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	if err := initializeCrewScriptSource(ctx, binding, "Saved Python"); err != nil {
		t.Fatal(err)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found {
		t.Fatalf("current script manifest unreadable: %v", err)
	}
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("registered saved Python refused: %v", err)
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

// A function or webhook run executes the workflow directly and has no registered child to stretch the idle window,
// so it must not get the chat window of 10 minutes (PLAT-839).
func TestDirectWorkflowRunsGetTheLongInactivityWindow(t *testing.T) {
	if got := turnInactivityLimit(context.Background()); got != schedulerWorkshopMaxInactivity {
		t.Fatalf("a chat turn keeps the workshop window, got %v", got)
	}
	direct := context.WithValue(context.Background(), directWebhookExecutionKey{}, struct{}{})
	if got := turnInactivityLimit(direct); got != schedulerWorkshopLiveChildCeiling || got <= schedulerWorkshopMaxInactivity {
		t.Fatalf("a direct workflow run needs the long window, got %v", got)
	}
}
