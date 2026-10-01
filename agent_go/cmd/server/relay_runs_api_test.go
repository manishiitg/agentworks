package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestRelayCallPayloadMatches(t *testing.T) {
	saved, _ := json.Marshal(WorkflowWebhookDelivery{Payload: json.RawMessage(`{"function":"process","args":{"INPUT":{"count":2,"ok":true}},"relay_caller":"owner"}`)})
	for _, tc := range []struct {
		function, user string
		input          map[string]interface{}
		want           bool
	}{
		{"process", "owner", map[string]interface{}{"ok": true, "count": float64(2)}, true},
		{"process", "other", map[string]interface{}{"ok": true, "count": float64(2)}, false},
		{"process", "owner", map[string]interface{}{"ok": false, "count": float64(2)}, false},
		{"other", "owner", map[string]interface{}{"ok": true, "count": float64(2)}, false},
	} {
		if got := relayCallPayloadMatches(string(saved), tc.function, tc.input, tc.user); got != tc.want {
			t.Fatalf("match(%q, %q, %v) = %v", tc.function, tc.user, tc.input, got)
		}
	}
}

func TestRelayRunPollsDurableStoreAfterServiceRestart(t *testing.T) {
	t.Setenv("AUTH_SECRET", "relay-test-signing-secret-long-enough")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspace := "Workflow/relay-poll"
	if err := os.MkdirAll(filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Relay")
	manifest.Kind = "relay"
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	manifest.RelayOutputStepID = "answer"
	sched := WorkflowSchedule{ID: "function-trigger", Name: "Process", ScheduleType: "webhook", Kind: triggerKindFunction, Enabled: true, WorkshopMode: "run", Function: &WorkflowFunctionSpec{Name: "process", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}
	manifest.Schedules = []WorkflowSchedule{sched}
	manifestRaw, _ := json.Marshal(manifest)
	runID := webhookDeliveryRunID(manifest.ID, sched.ID, "owner\x00request-1")
	payloadRaw, _ := json.Marshal(WorkflowWebhookDelivery{RunID: runID, Payload: json.RawMessage(`{"function":"process","args":{"INPUT":{"value":1}},"relay_caller":"owner","relay_output_step_id":"answer"}`)})
	apiFiles := &mockWorkspaceAPI{files: map[string]string{manifestPath(workspace): string(manifestRaw), webhookInputPath(workspace, runID): string(payloadRaw)}}
	ws := httptest.NewServer(apiFiles)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	storePath := filepath.Join(t.TempDir(), "runs.sqlite")
	store, err := schedulerstate.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	scope, scopeID, lockKey := scheduleStateScope(buildScheduleContext(workspace, manifest, sched))
	if err := store.BeginRun(context.Background(), schedulerstate.Run{RunID: runID, ScheduleID: sched.ID, ScopeType: scope, ScopeID: scopeID, LockKey: lockKey, TriggerSource: "webhook"}); err != nil {
		t.Fatal(err)
	}
	folder, err := allocateWebhookRunFolder(workspace, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignRunFolder(context.Background(), runID, folder); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "runs", folder, "default", "execution", "answer")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "result.json"), []byte(`{"value":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, state := range []schedulerstate.State{schedulerstate.StateWorkflowRunning, schedulerstate.StateWorkflowFinished, schedulerstate.StateCompleted} {
		if err := store.Transition(context.Background(), schedulerstate.Transition{RunID: runID, To: state, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	manifest.RelayOutputStepID = "changed-after-call"
	manifestRaw, _ = json.Marshal(manifest)
	apiFiles.mu.Lock()
	apiFiles.files[manifestPath(workspace)] = string(manifestRaw)
	apiFiles.mu.Unlock()
	reopened, err := schedulerstate.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	api := &StreamingAPI{scheduler: &SchedulerService{stateStore: reopened}}
	poll := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/relays/"+manifest.ID+"/runs/"+runID, nil)
		r = mux.SetURLVars(r, map[string]string{"id": manifest.ID, "run": runID})
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.handleGetRelayRun(w, r)
		return w
	}
	if w := poll("other"); w.Code != 404 {
		t.Fatalf("other user could poll Relay: %d %s", w.Code, w.Body.String())
	}
	w := poll("owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"result":{"value":1}`) {
		t.Fatalf("durable poll: %d %s", w.Code, w.Body.String())
	}
	start := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/relays/"+manifest.ID+"/runs", strings.NewReader(body))
		r = mux.SetURLVars(r, map[string]string{"id": manifest.ID})
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
		w := httptest.NewRecorder()
		api.handleStartRelayRun(w, r)
		return w
	}
	if w := start(`{"function":"process","input":{"value":1},"idempotency_key":"request-1"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatalf("duplicate call: %d %s", w.Code, w.Body.String())
	}
	if w := start(`{"function":"process","input":{"value":2},"idempotency_key":"request-1"}`); w.Code != 409 {
		t.Fatalf("changed input reused key: %d %s", w.Code, w.Body.String())
	}
}
