package server

import (
	"bytes"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// The dashboard function routes resolve the project from the server-owned
// dashboard record and reuse the typed-call path, so a viewer lists only what
// they can use, a stranger gets nothing, and the page cannot name a project.
func TestDashboardFunctionRoutesFollowTheDashboardProject(t *testing.T) {
	f := newExternalToolsFixture(t)
	withProjectOwnerRegistry(t)
	t.Setenv("AUTH_SECRET", "dashboard-function-test-secret")
	t.Setenv("PUBLIC_URL", "https://example.com")
	router := mux.NewRouter()
	routes := router.PathPrefix("/api").Subrouter()
	routes.HandleFunc("/dashboards", f.api.handleDashboards).Methods(http.MethodPost)
	f.api.registerDashboardQuestionRoutes(routes)
	f.api.registerDashboardFunctionRoutes(routes)
	server := httptest.NewServer(AuthMiddleware(router))
	t.Cleanup(server.Close)
	tokens := map[string]string{}
	for _, user := range []string{"owner", "reader", "outsider"} {
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user] = token
	}
	do := func(user, method, path string, body any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokens[user])
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return response.StatusCode, out
	}
	op := func(action, revision string) map[string]any {
		t.Helper()
		status, out := do("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": action, "workspace": "Workflow/invoices", "dashboard_id": "overview", "expected_revision": revision, "title": "Overview", "files": map[string]string{"index.html": "<p>Total</p>"}})
		if status != 200 {
			t.Fatalf("%s: %d %v", action, status, out)
		}
		return out
	}
	created := op("create", "")
	op("publish", created["dashboard"].(map[string]any)["revision"].(string))
	resource := op("get", "")["resource_id"].(string)
	base := "/api/dashboards/" + resource

	if status, out := do("reader", http.MethodGet, base+"/functions", nil); status != 200 || out["functions"] == nil {
		t.Fatalf("a viewer lists the project's functions: %d %v", status, out)
	}
	if status, _ := do("outsider", http.MethodGet, base+"/functions", nil); status != 404 {
		t.Fatalf("a person without access to the project must not list functions: %d", status)
	}
	if status, _ := do("outsider", http.MethodPost, base+"/functions/call", map[string]any{"function": "anything", "submission_id": "s1"}); status != 404 {
		t.Fatalf("a person without access must not call: %d", status)
	}
	if status, _ := do("reader", http.MethodPost, base+"/functions/call", map[string]any{"function": "not_published", "submission_id": "s2"}); status != 404 {
		t.Fatalf("an unpublished function must be refused: %d", status)
	}
	// The page cannot choose another project or account through the body.
	if status, _ := do("reader", http.MethodPost, base+"/functions/call", map[string]any{"function": "x", "submission_id": "s3", "workflow_id": "wf_other", "crew_id": "other"}); status != 400 {
		t.Fatalf("a body naming a project must be refused: %d", status)
	}
	if status, _ := do("reader", http.MethodGet, base+"/function-calls/does-not-exist", nil); status != 404 {
		t.Fatalf("an unknown call must be refused: %d", status)
	}
	// PLAT-750-22: Share includes colleagues' results, but never lets a member answer their pending input.
	call := &crewFunctionCall{ID: "fn-dashboard-shared-result-75022", UserID: "owner", CallerKind: triggerCallerUser,
		TargetKind: triggerCallerWorkflow, TargetID: "invoices", TargetPath: "Workflow/invoices", Status: "completed",
		Result: map[string]any{"total": 42}, done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	t.Cleanup(func() { crewFunctionCalls.Lock(); delete(crewFunctionCalls.m, call.ID); crewFunctionCalls.Unlock() })
	for _, person := range []string{"owner", "reader"} {
		status, out := do(person, http.MethodGet, base+"/function-calls/"+call.ID, nil)
		result, _ := out["result"].(map[string]any)
		if status != 200 || result["total"] != float64(42) || out["can_reply"] != (person == "owner") {
			t.Fatalf("%s must see the shared result with caller-owned reply rights: %d %v", person, status, out)
		}
	}
	if status, _ := do("outsider", http.MethodGet, base+"/function-calls/"+call.ID, nil); status != 404 {
		t.Fatalf("a person without current project access read its function result: %d", status)
	}
}

// Exercise the real typed dispatcher and scheduler ingress. Executor contract
// preflight deliberately fails before a model starts; the stored delivery still
// proves publication and caller provenance, without an external side effect.
func TestDashboardRelayFunctionsUsePublishedVersionAndRealCaller(t *testing.T) {
	f := newExternalRelayFixture(t)
	t.Setenv("AUTH_SECRET", "dashboard-relay-test-secret")
	f.mock.mu.Lock()
	var live WorkflowManifest
	_ = json.Unmarshal([]byte(f.mock.files["Workflow/invoices/workflow.json"]), &live)
	live.CodeLayoutVersion = 0
	encoded, _ := json.Marshal(live)
	f.mock.files["Workflow/invoices/workflow.json"] = string(encoded)
	f.mock.mu.Unlock()
	release, err := publishRelayRelease(t.Context(), "Workflow/invoices")
	if err != nil {
		t.Fatal(err)
	}
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.api.scheduler = NewSchedulerService(f.api)
	f.api.scheduler.stateStore = store
	// An unpublished new draft function and description must not change the form.
	f.mock.mu.Lock()
	live.Schedules[0].Function.Description = "Unpublished draft description"
	live.Schedules = append(live.Schedules, WorkflowSchedule{ID: "draft-only", Name: "Draft only", Enabled: true, ScheduleType: "webhook", Kind: triggerKindFunction, Function: &WorkflowFunctionSpec{Name: "draft_only", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}})
	encoded, _ = json.Marshal(live)
	f.mock.files["Workflow/invoices/workflow.json"] = string(encoded)
	f.mock.mu.Unlock()
	resolved := resolvedDashboardQuestion{Target: dashboardTarget{Kind: "relay", ID: "invoices", Root: "Workflow/invoices"}}
	router := mux.NewRouter()
	router.HandleFunc("/api/functions/{action}", func(w http.ResponseWriter, r *http.Request) {
		tool := "list_workflow_functions"
		args := map[string]any{}
		if mux.Vars(r)["action"] == "call" {
			tool = "call_workflow_function"
			args = map[string]any{"function": "process", "args": map[string]any{"INPUT": map[string]any{"name": "Sample"}}, "submission_id": "relay-dashboard-sample", "wait_seconds": 0}
		}
		f.api.dashboardFunctionDispatch(w, r, resolved, "", tool, args)
	})
	server := httptest.NewServer(AuthMiddleware(router))
	defer server.Close()
	call := func(action string) (int, map[string]any) {
		t.Helper()
		token, _ := GenerateJWT("reader", "reader", "")
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/functions/"+action, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, out
	}
	status, listed := call("list")
	if status != 200 {
		t.Fatalf("list: %d %v", status, listed)
	}
	functions := listed["functions"].([]any)
	if len(functions) != 1 || functions[0].(map[string]any)["name"] != "process" || functions[0].(map[string]any)["description"] == "Unpublished draft description" {
		t.Fatalf("draft reached published form: %v", listed)
	}
	status, started := call("call")
	if status != 200 {
		t.Fatalf("call: %d %v", status, started)
	}
	functionCall := lookupCrewFunctionCall(started["call_id"].(string))
	if functionCall == nil {
		t.Fatal("missing typed call")
	}
	functionCall.mu.Lock()
	runID, callerID := functionCall.RunID, functionCall.CallerID
	functionCall.mu.Unlock()
	if callerID != "reader" || runID == "" {
		t.Fatalf("lost actual caller or dispatch: caller=%s run=%s result=%v", callerID, runID, started)
	}
	run, err := store.GetRun(t.Context(), runID)
	if err != nil || run.ScopeID != relayReleaseWorkspace("Workflow/invoices", release.Version) {
		t.Fatalf("dashboard ran draft rather than published version: %+v %v", run, err)
	}
	raw, found, err := readFileFromWorkspace(t.Context(), webhookInputPath(run.ScopeID, runID))
	if err != nil || !found {
		t.Fatalf("delivery missing: %v", err)
	}
	var delivery WorkflowWebhookDelivery
	_ = json.Unmarshal([]byte(raw), &delivery)
	var payload struct {
		Version string        `json:"relay_version"`
		Caller  triggerCaller `json:"relay_trigger_caller"`
	}
	_ = json.Unmarshal(delivery.Payload, &payload)
	if payload.Version != release.Version || payload.Caller.Type != triggerCallerUser || payload.Caller.ID != "reader" {
		t.Fatalf("wrong delivery provenance: %+v", payload)
	}
	// Remove product sharing: neither listing nor another call is available.
	f.mock.mu.Lock()
	live.Access.Readers = nil
	encoded, _ = json.Marshal(live)
	f.mock.files["Workflow/invoices/workflow.json"] = string(encoded)
	f.mock.mu.Unlock()
	if status, _ := call("list"); status != 404 {
		t.Fatalf("revoked share still lists: %d", status)
	}
	if status, _ := call("call"); status != 404 {
		t.Fatalf("revoked share still calls: %d", status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !schedulerstate.IsTerminal(run.State) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		run, _ = store.GetRun(t.Context(), runID)
	}
	if !schedulerstate.IsTerminal(run.State) {
		t.Fatal("fixture executor did not settle")
	}
}
