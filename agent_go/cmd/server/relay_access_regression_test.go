package server

import (
	"context"
	"encoding/json"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestRelayPublishedSharedProviderAccount(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	setDirectoryAdmin(t, "alice", true) // workflow shares are admin-only (PLAT-715)
	account := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Shared Claude", "auth_method": "cli_login", "sharing": map[string]interface{}{"mode": "shared", "workflows": []string{"wf-w"}}})
	draft := "Workflow/w"
	release := relayReleaseWorkspace(draft, "v1")
	raw := `{"id":"wf-w","kind":"relay","label":"Shared Relay","created_by":"bob","access":{"owners":["bob"],"readers":["alice"]}}`
	env.mock.mu.Lock()
	env.mock.files[manifestPath(draft)] = raw
	env.mock.files[manifestPath(release)] = raw
	env.mock.mu.Unlock()
	for _, tc := range []struct {
		path    string
		allowed bool
	}{{draft, true}, {release, true}} {
		scope := providerAccountScope{Principal: "bob", WorkspacePath: tc.path}
		run := env.api.describeProviderAccountRun(context.Background(), scope)
		_, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", account.ID)
		if (err == nil) != tc.allowed {
			t.Fatalf("path=%s expected allowed=%t got err=%v", tc.path, tc.allowed, err)
		}
		t.Logf("path=%s workflowID=%q workflowPath=%q allowed=%t error=%v", tc.path, run.WorkflowID, run.WorkflowPath, err == nil, err)
	}
}

func TestRelayReleaseLogAliasesDenied(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator"},{"id":"bob","username":"bob","role":"editor","products":["agentworks"]}]}`)
	draft := "Workflow/review-private"
	release := relayReleaseWorkspace(draft, "v1")
	m := NewWorkflowManifest("Private Relay")
	m.Kind = "relay"
	m.Access = &WorkflowAccess{Owners: []string{"alice"}}
	raw, _ := json.Marshal(m)
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		manifestPath(draft): string(raw), manifestPath(release): string(raw),
		release + "/planning/plan.json":                                    `{"steps":[{"id":"answer","title":"Answer","context_output":"result.json"}]}`,
		release + "/runs/iteration-0/default/execution/answer/result.json": `{"private":"alice-private-output"}`,
	}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	for _, tc := range []struct {
		user   string
		path   string
		status int
	}{{"bob", release, 403}, {"bob", "./" + release, 403}, {"bob", "Workflow//" + strings.TrimPrefix(release, "Workflow/"), 403}, {"bob", "/" + release, 403}, {"alice", release, 200}, {"alice", "./" + release, 403}} {
		req := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+tc.path+"&run_folder=iteration-0/default", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: tc.user, Username: tc.user}))
		out := httptest.NewRecorder()
		(&StreamingAPI{}).handleGetExecutionLogs(out, req)
		if out.Code != tc.status {
			t.Fatalf("path %q got %d: %s", tc.path, out.Code, out.Body.String())
		}
		if tc.status == 200 && !strings.Contains(out.Body.String(), "alice-private-output") {
			t.Fatalf("owner cannot read private output: %s", out.Body.String())
		}
		// Exercise the middleware used by all shared workflow data readers too.
		if got := workspaceReadAllowed(req.Context(), GetUserFromContext(req.Context()), tc.path, logicalPathIsDefaultUser, true); got != (tc.status == 200) {
			t.Fatalf("middleware user=%s path=%s allowed=%t", tc.user, tc.path, got)
		}
		t.Logf("path=%q status=%d private_output=%t", tc.path, out.Code, strings.Contains(out.Body.String(), "alice-private-output"))
	}
}

func TestRelayReaderCanListPublishedVersions(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator"},{"id":"reader","username":"reader","role":"viewer","products":["agentworks"]}]}`)
	m := NewWorkflowManifest("Shared Relay")
	m.Kind = "relay"
	m.Access = &WorkflowAccess{Owners: []string{"alice"}, Readers: []string{"reader"}}
	raw, _ := json.Marshal(m)
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/review-shared"): string(raw)}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	req := httptest.NewRequest("GET", "/api/relays/"+m.ID+"/releases", nil)
	req = mux.SetURLVars(req, map[string]string{"id": m.ID})
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "reader", Username: "reader"}))
	out := httptest.NewRecorder()
	(&StreamingAPI{}).handleListRelayReleases(out, req)
	if out.Code != 200 {
		t.Fatalf("reader status %d: %s", out.Code, out.Body.String())
	}
	for _, scopes := range [][]string{{"workflows:read"}, {"runs:execute"}} {
		tokenReq := req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "reader", Username: "reader", AccessToken: &accesstokens.Token{Scopes: scopes, WorkflowIDs: []string{m.ID}}}))
		out := httptest.NewRecorder()
		(&StreamingAPI{}).handleListRelayReleases(out, tokenReq)
		want := 404
		if scopes[0] == "workflows:read" {
			want = 200
		}
		if out.Code != want {
			t.Fatalf("scopes=%v status=%d body=%s", scopes, out.Code, out.Body.String())
		}
	}
	t.Log("live reader grant permits release listing")
}

func TestRelayRevokedUserCannotPollOrReplay(t *testing.T) {
	draft := "Workflow/review-revoked"
	release := relayReleaseWorkspace(draft, "v1")
	m := NewWorkflowManifest("Revoked caller")
	m.Kind = "relay"
	m.CreatedBy = "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}}
	sched := WorkflowSchedule{ID: "function", ScheduleType: "webhook", Kind: triggerKindFunction, Enabled: true, Function: &WorkflowFunctionSpec{Name: "process", AllowedCallers: []triggerCaller{{Type: triggerCallerUser, ID: "other"}}}}
	m.Schedules = []WorkflowSchedule{sched}
	raw, _ := json.Marshal(m)
	runID := webhookDeliveryRunID(m.ID, sched.ID, "owner\x00request-1")
	delivery, _ := json.Marshal(WorkflowWebhookDelivery{RunID: runID, Payload: json.RawMessage(`{"relay_version":"v1","relay_caller":"owner","function":"process","args":{"INPUT":{"value":1}}}`)})
	releaseRaw, _ := json.Marshal(relayRelease{Version: "v1", Hash: "test", FileCount: 1, Files: []string{"workflow.json"}})
	ws := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{manifestPath(draft): string(raw), manifestPath(release): string(raw), release + "/release.json": string(releaseRaw), webhookInputPath(release, runID): string(delivery)}})
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	scope, scopeID, lock := scheduleStateScope(buildScheduleContext(release, m, sched))
	if err := store.BeginRun(context.Background(), schedulerstate.Run{RunID: runID, ScheduleID: sched.ID, ScopeType: scope, ScopeID: scopeID, LockKey: lock, TriggerSource: "webhook", State: schedulerstate.StateStarting}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{scheduler: &SchedulerService{stateStore: store}}
	for _, method := range []string{"GET", "POST"} {
		url := "/api/relays/" + m.ID + "/runs"
		if method == "GET" {
			url += "/" + runID
		}
		req := httptest.NewRequest(method, url, strings.NewReader(`{"function":"process","input":{"value":1},"idempotency_key":"request-1"}`))
		req = mux.SetURLVars(req, map[string]string{"id": m.ID, "run": runID})
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
		out := httptest.NewRecorder()
		if method == "GET" {
			api.handleGetRelayRun(out, req)
		} else {
			api.handleStartRelayRun(out, req)
		}
		if out.Code != 404 {
			t.Fatalf("revoked %s got %d: %s", method, out.Code, out.Body.String())
		}
		t.Logf("revoked caller %s was denied", method)
	}
}

// This exercises the real API/dispatcher/scheduler ingress without invoking an
// LLM. The absent physical release folder deliberately fails execution after
// acceptance; the failed run remains pollable by its reader caller.
func TestRelayReaderCanExecuteAndPollOwnVersion(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"reader","username":"reader"},{"id":"other","username":"other"},{"id":"stranger","username":"stranger"}]}`)
	draft := "Workflow/reader-api"
	m := NewWorkflowManifest("Reader API")
	m.Kind, m.CreatedBy, m.RelayOutputStepID = "relay", "owner", "answer"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader", "other"}}
	m.Schedules = []WorkflowSchedule{{ID: "call", Name: "Process", Enabled: true, WorkshopMode: "run", ScheduleType: "webhook", Kind: triggerKindFunction, GroupNames: []string{"default"}, Function: &WorkflowFunctionSpec{Name: "process", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}}
	raw, _ := json.Marshal(m)
	plan := &stepworkflow.PlanningResponse{Steps: []stepworkflow.PlanStepInterface{&stepworkflow.AgentPlanStep{CommonStepFields: stepworkflow.CommonStepFields{ID: "answer", Title: "Answer", Description: "Return JSON"}, AuthoredPrompt: true, SystemPrompt: "Return JSON", NextStepID: "end", Items: []stepworkflow.AgentItem{{ID: "turn", Type: "user_message", Message: "{{input}}"}}}}}
	planRaw, _ := json.Marshal(plan)
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath(draft): string(raw), draft + "/planning/plan.json": string(planRaw), draft + "/variables/variables.json": `{"variables":[{"name":"INPUT","type":"object"}],"groups":[{"name":"default","enabled":true}]}`}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	if _, err := publishRelayRelease(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	api := &StreamingAPI{}
	api.scheduler = NewSchedulerService(api)
	api.scheduler.stateStore = store
	request := func(method, user, body, runID string, token *accesstokens.Token) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/relays/"+m.ID+"/runs", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"id": m.ID, "run": runID})
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, AccessToken: token}))
		out := httptest.NewRecorder()
		if method == "POST" {
			api.handleStartRelayRun(out, req)
		} else {
			api.handleGetRelayRun(out, req)
		}
		return out
	}
	body := `{"function":"process","input":{"value":1},"idempotency_key":"reader-key"}`
	for _, tc := range []struct {
		user  string
		token *accesstokens.Token
	}{{"stranger", nil}, {"reader", &accesstokens.Token{Scopes: []string{"workflows:read"}, WorkflowIDs: []string{m.ID}}}, {"reader", &accesstokens.Token{Scopes: []string{"runs:execute"}, WorkflowIDs: []string{"another"}}}} {
		if out := request("POST", tc.user, body, "", tc.token); out.Code != 404 {
			t.Fatalf("unauthorized start: %d %s", out.Code, out.Body.String())
		}
	}
	missing := request("POST", "reader", `{"function":"process","version":"v99","input":{},"idempotency_key":"missing"}`, "", nil)
	if missing.Code != 404 {
		t.Fatalf("missing version: %d %s", missing.Code, missing.Body.String())
	}
	out := request("POST", "reader", body, "", &accesstokens.Token{Scopes: []string{"runs:execute"}, WorkflowIDs: []string{m.ID}})
	if out.Code != 202 {
		t.Fatalf("reader start: %d %s", out.Code, out.Body.String())
	}
	var response struct {
		RunID   string `json:"run_id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil || response.RunID == "" || response.Version != "v1" {
		t.Fatalf("accepted response: %s %v", out.Body.String(), err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := store.GetRun(context.Background(), response.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if schedulerstate.IsTerminal(run.State) {
			// Wait for final bookkeeping before environment cleanup.
			api.scheduler.runtimeStatesMu.RLock()
			active := false
			for _, state := range api.scheduler.runtimeStates {
				active = active || state.ActiveRunID == response.RunID
			}
			api.scheduler.runtimeStatesMu.RUnlock()
			if !active {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("test ingress run did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, tc := range []struct {
		user   string
		status int
	}{{"reader", 200}, {"other", 404}, {"stranger", 404}} {
		out := request("GET", tc.user, "", response.RunID, nil)
		if out.Code != tc.status {
			t.Fatalf("poll as %s: %d %s", tc.user, out.Code, out.Body.String())
		}
		if tc.status == 200 && !strings.Contains(out.Body.String(), `"version":"v1"`) {
			t.Fatalf("poll lost version: %s", out.Body.String())
		}
	}
}

func TestRelayReaderRunsUseOwnerCredentials(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	draft := "Workflow/w"
	release := relayReleaseWorkspace(draft, "v1")
	manifest := NewWorkflowManifest("Owner keys")
	manifest.Kind, manifest.ID, manifest.CreatedBy = "relay", "wf-w", "alice"
	manifest.Access = &WorkflowAccess{Owners: []string{"alice"}, Readers: []string{"bob"}}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath(draft)], env.mock.files[manifestPath(release)] = string(raw), string(raw)
	env.mock.mu.Unlock()
	sctx := buildScheduleContext(release, manifest, WorkflowSchedule{ID: "call"})
	sctx.WebhookInput = &WorkflowWebhookDelivery{Payload: json.RawMessage(`{"relay_caller":"bob"}`)}
	if sctx.OwnerUserID != "alice" {
		t.Fatalf("execution identity = %q", sctx.OwnerUserID)
	}
	owner := env.addAccount(t, "alice", map[string]interface{}{"provider": "claude-code", "display_name": "Owner", "auth_method": "cli_login"})
	reader := env.addAccount(t, "bob", map[string]interface{}{"provider": "claude-code", "display_name": "Reader", "auth_method": "cli_login"})
	scope := providerAccountScope{Principal: sctx.OwnerUserID, WorkspacePath: release}
	if _, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", owner.ID); err != nil {
		t.Fatalf("owner account: %v", err)
	}
	if _, err := env.api.admitProviderAccount(context.Background(), scope, "claude-code", reader.ID); err == nil {
		t.Fatal("reader private account attached to owner run")
	}
	for _, tc := range []struct{ user, value string }{{"alice", "owner-value"}, {"bob", "reader-value"}} {
		encrypted, err := encryptSecretValueWithAAD(tc.value, []byte(tc.user))
		if err != nil {
			t.Fatal(err)
		}
		if tc.user == "alice" {
			if err := env.api.chatStore.UpsertWorkflowSecret(context.Background(), tc.user, draft, "TOKEN", encrypted); err != nil {
				t.Fatal(err)
			}
		} else if err := env.api.chatStore.UpsertUserSecret(context.Background(), tc.user, "TOKEN", encrypted); err != nil {
			t.Fatal(err)
		}
	}
	secrets := env.api.loadSelectedSecrets(context.Background(), sctx.OwnerUserID, release, []string{"TOKEN"})
	if len(secrets) != 1 || secrets[0].Value != "owner-value" {
		t.Fatalf("owner secret resolution: %+v", secrets)
	}
}
