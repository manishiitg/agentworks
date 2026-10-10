package server

import (
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebhookRunOutputsAndDownloads(t *testing.T) {
	t.Setenv("AUTH_SECRET", "webhook-test-signing-secret-long-enough")
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/test"
	if err := os.MkdirAll(filepath.Join(docs, workspace, "runs", "iteration-4-sched"), 0700); err != nil {
		t.Fatal(err)
	}
	folder, err := allocateWebhookRunFolder(workspace, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if folder != "iteration-5-hook" {
		t.Fatalf("webhook folder = %q, want shared namespace allocation iteration-5-hook", folder)
	}
	again, err := allocateWebhookRunFolder(workspace, "run-1")
	if err != nil || again != folder {
		t.Fatalf("idempotent allocation: %s %v", again, err)
	}
	other, err := allocateWebhookRunFolder(workspace, "run-2")
	if err != nil || other == folder {
		t.Fatalf("distinct allocation: %s %v", other, err)
	}
	base := filepath.Join(docs, workspace, "runs", folder)
	files := map[string]string{"dev/execution/smoke/result.json": `{"passed":2,"failed":0}`, "dev/execution/smoke/video.webm": "video-bytes", "dev/execution/smoke/.env": "secret", "dev/execution/smoke/code/main.py": "private-code", "dev/execution/smoke/logs/internal.txt": "private-log"}
	files["dev/webhook_progress.json"] = `{"smoke:":{"step_id":"smoke","title":"Smoke","status":"completed","updated_at":"2026-09-12T12:00:00Z"}}`
	for p, b := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(base, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, p), []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(base, "dev/execution/smoke/.env"), filepath.Join(base, "dev/execution/smoke/link.txt")); err != nil {
		t.Fatal(err)
	}
	sched := webhookTestSchedule(t, "bearer")
	manifest := NewWorkflowManifest("test")
	manifest.ID = "wf_test"
	manifest.Schedules = []WorkflowSchedule{sched}
	raw, _ := json.Marshal(manifest)
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath(workspace): string(raw)}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope, id, key := scheduleStateScope(buildScheduleContext(workspace, manifest, sched))
	run := schedulerstate.Run{RunID: "run-1", ScheduleID: sched.ID, ScopeType: scope, ScopeID: id, LockKey: key, TriggerSource: "webhook"}
	ctx := context.Background()
	if err := store.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignRunFolder(ctx, run.RunID, folder); err != nil {
		t.Fatal(err)
	}
	svc := &SchedulerService{stateStore: store}
	router := mux.NewRouter()
	WorkflowWebhookRoutes(router, svc)
	get := func(url, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", url, nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	statusURL := webhookStatusPath(sched.ID, run.RunID)
	for _, auth := range []string{"", "Bearer wrong", "test-trigger-secret"} {
		if w := get(statusURL, auth); w.Code != 401 {
			t.Fatalf("invalid auth: %d %s", w.Code, w.Body.String())
		}
	}
	for _, state := range []schedulerstate.State{schedulerstate.StateWorkflowRunning, schedulerstate.StateWorkflowFinished, schedulerstate.StateCompleted} {
		if err := store.Transition(ctx, schedulerstate.Transition{RunID: run.RunID, To: state}); err != nil {
			t.Fatal(err)
		}
	}
	w := get(statusURL, "Bearer test-trigger-secret")
	if w.Code != 200 {
		t.Fatalf("poll: %d %s", w.Code, w.Body.String())
	}
	var result webhookRunResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Terminal || result.Status != "completed" || result.RunFolder != folder || len(result.Steps) != 1 || len(result.Steps[0].Artifacts) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Progress) != 1 || result.Progress[0].Status != "completed" {
		t.Fatalf("missing step progress: %+v", result.Progress)
	}
	output := result.Steps[0].Outputs["result.json"].(map[string]interface{})
	if output["passed"] != float64(2) {
		t.Fatalf("generic output: %v", output)
	}
	for _, a := range result.Steps[0].Artifacts {
		w := get(a.DownloadURL, "")
		if w.Code != 200 || w.Body.String() != files[a.Path] {
			t.Fatalf("signed download: %d %s", w.Code, w.Body.String())
		}
		if w := get(strings.Replace(a.DownloadURL, "run-1", "run-2", 1), ""); w.Code != 401 {
			t.Fatalf("cross-run token: %d", w.Code)
		}
	}
	for _, p := range []string{"../outside", "dev/execution/smoke/.env", "dev/execution/smoke/link.txt", "dev/execution/smoke/code/main.py"} {
		if w := get(statusURL+"/artifact?path="+p, "Bearer test-trigger-secret"); w.Code != 404 {
			t.Fatalf("private path %s: %d", p, w.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "dev/execution/smoke/result.json"), []byte(`{"changed":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	w = get(statusURL, "Bearer test-trigger-secret")
	if strings.Contains(w.Body.String(), "changed") {
		t.Fatal("terminal inline result changed")
	}
	if w := get(webhookStatusPath(sched.ID, "unknown"), "Bearer test-trigger-secret"); w.Code != 404 {
		t.Fatalf("unknown run: %d", w.Code)
	}
}

func TestWebhookFileTokenScopeAndExpiry(t *testing.T) {
	t.Setenv("AUTH_SECRET", "webhook-test-signing-secret-long-enough")
	token, err := webhookAccessToken("trigger", "run", "dev/execution/step/file.txt", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !validWebhookAccess(token, "trigger", "run", "dev/execution/step/file.txt") {
		t.Fatal("valid token rejected")
	}
	for _, v := range [][3]string{{"other", "run", "dev/execution/step/file.txt"}, {"trigger", "other", "dev/execution/step/file.txt"}, {"trigger", "run", "other"}, {"trigger", "run", ""}} {
		if validWebhookAccess(token, v[0], v[1], v[2]) {
			t.Fatal("token escaped scope")
		}
	}
	expired, _ := webhookAccessToken("trigger", "run", "file", -time.Minute)
	if validWebhookAccess(expired, "trigger", "run", "file") {
		t.Fatal("expired accepted")
	}
}

func TestWebhookNeverRunsPulse(t *testing.T) {
	svc := &SchedulerService{}
	for _, mode := range []string{"basic", "full", "off"} {
		sctx := &ScheduleContext{Schedule: WorkflowSchedule{ScheduleType: "webhook", PulseMode: mode}, ForcePulseReview: true}
		if effectiveSchedulePulseMode(sctx, NewWorkflowManifest("test")) != schedulePulseModeOff {
			t.Fatal("webhook enabled Pulse")
		}
		if got := svc.runPulseLifecycle(context.Background(), sctx, mode, "completed", "iteration-1-hook", "", "", ""); got != pulseLifecycleNotRun {
			t.Fatalf("Pulse ran: %s", got)
		}
	}
}

func TestWebhookCompletionHasNoBackupDirective(t *testing.T) {
	snap := BackgroundAgentSnapshot{Status: BGAgentCompleted, Kind: "workflow_run_tool", Metadata: map[string]string{"trigger_source": "webhook"}}
	if workflowRunCompletionDirective(snap) != "" {
		t.Fatal("webhook completion requested backup")
	}
	delete(snap.Metadata, "trigger_source")
	if workflowRunCompletionDirective(snap) == "" {
		t.Fatal("ordinary run lost backup")
	}
}

// PLAT-812: a step's private scratch folder (Playwright's temp artifacts, written by the slot user) must not make the
// whole run's result unavailable: a workflow function call would otherwise report "failed" while the run is healthy.
func TestWebhookRunOutputsSkipUnreadableScratchFolders(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission denial cannot be shown as root")
	}
	dir := t.TempDir()
	step := filepath.Join(dir, "dev", "execution", "smoke")
	if err := os.MkdirAll(filepath.Join(step, "pwtmp", "playwright-artifacts-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(step, "result.json"), []byte(`{"verdict":"PASS"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(step, "pwtmp")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	steps, _, err := collectWebhookOutputs(root)
	if err != nil {
		t.Fatalf("an unreadable scratch folder failed the result: %v", err)
	}
	if len(steps) != 1 || steps[0].Outputs["result.json"] == nil {
		t.Fatalf("outputs = %+v, want the step's result.json", steps)
	}
}
