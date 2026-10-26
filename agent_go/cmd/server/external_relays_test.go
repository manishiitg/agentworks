package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

var relayConsentScopes = []string{"workflows:read", "files:read", "runs:execute", "relays:write"}

type externalRelayFixture struct {
	*builderOperationFixture
	mock *mockWorkspaceAPI
}

func newExternalRelayFixture(t *testing.T) *externalRelayFixture {
	t.Helper()
	f := newBuilderOperationFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator","products":["agentworks","relays"]},{"id":"outsider","username":"outsider","role":"editor","products":["agentworks","relays"]},{"id":"reader","username":"reader","role":"viewer","products":["agentworks","relays"]}]}`)
	m := NewWorkflowManifest("Relay")
	m.ID = "invoices"
	m.Kind = "relay"
	m.CreatedBy = "owner"
	m.RelayOutputStepID = "answer"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Editors: []string{"outsider"}, Readers: []string{"reader"}}
	m.Schedules = []WorkflowSchedule{{ID: "process", Name: "Process", Enabled: true, ScheduleType: "webhook", Kind: triggerKindFunction, WorkshopMode: "run", Function: &WorkflowFunctionSpec{Name: "process", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}}
	raw, _ := json.Marshal(m)
	mock := &mockWorkspaceAPI{files: map[string]string{
		"Workflow/invoices/workflow.json":            string(raw),
		"Workflow/invoices/planning/plan.json":       `{"steps":[{"type":"agent","id":"answer","title":"Answer","description":"Reply","authored_prompt":true,"system_prompt":"Return JSON v1","next_step_id":"end","items":[{"id":"turn","type":"user_message","message":"{{input.name}}"}]}]}`,
		"Workflow/invoices/variables/variables.json": `{"variables":[{"name":"INPUT","type":"object","value":"{}"}]}`,
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	return &externalRelayFixture{f, mock}
}
func (f *externalRelayFixture) token(t *testing.T, user string, scopes []string, all bool) (accesstokens.Token, string) {
	t.Helper()
	s, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := accesstokens.Token{Name: "Relay MCP " + uuid.NewString(), UserID: user, Username: user, Scopes: scopes, AllWorkflows: all, ExpiresAt: time.Now().Add(time.Hour)}
	if !all {
		token.WorkflowIDs = []string{"invoices"}
	}
	token, raw, err := s.Issue(t.Context(), token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token, raw
}
func (f *externalRelayFixture) relayCall(t *testing.T, raw, name string, args map[string]any, want int) map[string]any {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	req := httptest.NewRequest("POST", "/api/external/v1/call", strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	AuthMiddleware(http.HandlerFunc(f.api.handleExternalCall)).ServeHTTP(w, req)
	return externalTestBody(t, w, want)
}
func TestExternalRelayCreationRetryAndConsent(t *testing.T) {
	f := newExternalRelayFixture(t)
	_, writer := f.token(t, "owner", relayConsentScopes, true)
	args := map[string]any{"label": "Hello Relay", "submission_id": "create-hello"}
	first := f.createRelay(t, writer, args, 200)
	id := first["workflow_id"].(string)
	second := f.createRelay(t, writer, args, 200)
	if second["workflow_id"] != id || second["duplicate"] != true {
		t.Fatalf("retry: %+v", second)
	}
	_, savedManifest, manifestErr := findWorkflowManifestByID(t.Context(), id)
	if manifestErr != nil {
		t.Fatal(manifestErr)
	}
	encodedManifest, _ := json.Marshal(savedManifest)
	manifest := map[string]any{}
	_ = json.Unmarshal(encodedManifest, &manifest)
	if manifest["kind"] != "relay" || manifest["relay_runtime"] != "python" || manifest["relay_output_step_id"] != nil {
		t.Fatalf("manifest: %+v", manifest)
	}
	trigger, err := findWorkflowFunctionTriggerFromTest(t.Context(), id)
	if err != nil || len(trigger.GroupNames) != 0 {
		t.Fatalf("Relay creation retained groups: %+v %v", trigger, err)
	}
	f.createRelay(t, writer, map[string]any{"label": "Different", "submission_id": "create-hello"}, 409)
	_, bounded := f.token(t, "owner", relayConsentScopes, false)
	f.createRelay(t, bounded, map[string]any{"label": "Bounded", "submission_id": "bounded"}, 200)
	_, reader := f.token(t, "reader", relayConsentScopes, true)
	f.createRelay(t, reader, map[string]any{"label": "Reader", "submission_id": "reader"}, 403)
	_, runner := f.token(t, "owner", []string{"runs:execute"}, true)
	f.createRelay(t, runner, args, 200)
}
func findWorkflowFunctionTriggerFromTest(ctx context.Context, id string) (*WorkflowSchedule, error) {
	_, m, err := findWorkflowManifestByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return findWorkflowFunctionTrigger(m, "process")
}

func TestExternalRelayBuilderAndPublishingRespectRoles(t *testing.T) {
	f := newExternalRelayFixture(t)
	token, writer := f.token(t, "owner", relayConsentScopes, true)
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateBuilderGrant(t.Context(), claims, "invoices", "Workflow/invoices"); err != nil {
		t.Fatal(err)
	}
	// Ordinary person MCP follows current account roles (20 October decision),
	// not the scopes originally selected when this token was issued.
	f.mock.mu.Lock()
	saved := f.mock.files["Workflow/invoices/workflow.json"]
	f.mock.files["Workflow/invoices/workflow.json"] = strings.Replace(saved, `"kind":"relay"`, `"kind":""`, 1)
	f.mock.mu.Unlock()
	if _, err := validateBuilderGrant(t.Context(), claims, "invoices", "Workflow/invoices"); err != nil {
		t.Fatal("ordinary MCP must follow the owner's current product access", err)
	}
	f.mock.mu.Lock()
	f.mock.files["Workflow/invoices/workflow.json"] = saved
	f.mock.mu.Unlock()
	op := f.submit(t, writer, "relay-edit", "Change the authored system prompt")
	_, ctx, _ := f.claim(t, "owner")
	if GetUserFromContext(ctx).ExternalBuilderOperationID != op.ID {
		t.Fatal("lost operation provenance")
	}
	p := resolveWorkflowChatPolicy(op.SessionID, QueryRequest{ExternalBuilderOperationID: op.ID}, nil, false)
	if p.Mode != "builder" || p.Origin != "external_builder" {
		t.Fatalf("wrong mode %+v", p)
	}
	// Drive the actual shared plan mutation behind the MCP operation, with
	// its normal input validation and edit audit; no model fixture is needed.
	reg := &productSurfaceDraft{}
	read := func(ctx context.Context, p string) (string, error) {
		raw, found, err := readFileFromWorkspace(ctx, p)
		if err != nil {
			return "", err
		}
		if !found {
			return "", errors.New("missing file")
		}
		return raw, nil
	}
	if err := stepworkflow.RegisterPlanModificationTools(externalBuilderRegistrar(reg, GetUserFromContext(ctx)), "Workflow/invoices", loggerv2.NewNoop(), read, writeFileToWorkspace, nil, "Relay MCP builder"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["update_step"].exec(ctx, map[string]interface{}{"step_id": "answer", "changes": map[string]interface{}{"system_prompt": "Return JSON after MCP edit", "reason": "Relay MCP edit test"}}); err != nil {
		t.Fatal(err)
	}
	raw, _, err := readFileFromWorkspace(ctx, "Workflow/invoices/planning/plan.json")
	if err != nil || !strings.Contains(raw, "Return JSON after MCP edit") {
		t.Fatalf("plan edit not persisted %s %v", raw, err)
	}
	f.relayCall(t, writer, "publish_relay", map[string]any{"workflow_id": "invoices"}, 200)
	versions := f.relayCall(t, writer, "get_relay_releases", map[string]any{"workflow_id": "invoices"}, 200)
	if versions["active_version"] != "v1" {
		t.Fatalf("versions: %+v", versions)
	}
	_, reader := f.token(t, "reader", relayConsentScopes, true)
	for _, name := range []string{"publish_relay", "update_relay"} {
		f.relayCall(t, reader, name, map[string]any{"workflow_id": "invoices"}, 403)
	}
	f.call(t, reader, "builder_chat", map[string]any{"message": "Edit", "submission_id": "read-edit"}, 403)
	_, editor := f.token(t, "outsider", relayConsentScopes, false)
	// A former editor runs published versions; only owners can publish.
	f.relayCall(t, editor, "publish_relay", map[string]any{"workflow_id": "invoices"}, 403)
	f.relayCall(t, writer, "chat", map[string]any{"workflow_id": "invoices", "message": "Edit"}, 400)
	f.relayCall(t, writer, "execute_step", map[string]any{"workflow_id": "invoices", "step_id": "answer"}, 400)
	f.relayCall(t, writer, "update_relay", map[string]any{"workflow_id": "invoices", "label": "Updated"}, 200)
}

func TestExternalRelayMCPTransportAndSchema(t *testing.T) {
	// This transport fixture never needs to probe locally installed providers.
	t.Setenv("AGENTWORKS_PRODUCT_DEFAULTS", `{"agentworks":{"provider":"codex-cli","model":"gpt-5"}}`)
	f := newExternalRelayFixture(t)
	token, _ := f.token(t, "owner", relayConsentScopes, true)
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	relaySpec := marshalStructured(t, callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"workspace", "relay", "builder"}}))
	for _, want := range []string{`"create"`, `"publish"`, `"test"`, `"run"`, `"get_run"`, `"chat"`} {
		if !strings.Contains(relaySpec, want) {
			t.Fatalf("relay/builder spec missing action %s: %s", want, relaySpec)
		}
	}
	created := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "workspace", "arguments": testWorkspaceCreate("relay", map[string]any{"label": "MCP hello", "submission_id": "transport"})})
	requireRemoteSuccess(t, created, "create Relay through MCP")
	bad := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "run_relay", "arguments": map[string]any{"workflow_id": "invoices", "function": "process", "input": "not JSON object", "idempotency_key": "bad"}})
	if !bad.IsError {
		t.Fatal("invalid input reached run ingress")
	}
	// The existing polling action also exposes the dashboard's recorded reader.
	// Drive it through MCP, including the gap before any trace has been saved.
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.api.scheduler = &SchedulerService{stateStore: store}
	runID := uuid.NewString()
	workspace := "Workflow/invoices"
	if err := store.BeginRun(t.Context(), schedulerstate.Run{RunID: runID, ScopeType: "workflow", ScopeID: workspace, LockKey: workspace, ScheduleID: "process", TriggerSource: "webhook"}); err != nil {
		t.Fatal(err)
	}
	payload := `{"run_id":"` + runID + `","payload":{"function":"process","relay_caller":"owner","args":{"INPUT":{"invoice":"INV-7"}}}}`
	f.mock.mu.Lock()
	f.mock.files[webhookInputPath(workspace, runID)] = payload
	f.mock.mu.Unlock()
	f.write(t, webhookInputPath(workspace, runID), payload)
	inspect := func() string {
		t.Helper()
		response := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "relay", "arguments": map[string]any{"action": "get_run", "workflow_id": "invoices", "run_id": runID}})
		requireRemoteSuccess(t, response, "read execution and logs through MCP")
		return marshalStructured(t, response)
	}
	if body := inspect(); !strings.Contains(body, `"available":false`) || !strings.Contains(body, `"terminal":false`) {
		t.Fatalf("unrecorded execution must remain pollable: %s", body)
	}
	folder, err := allocateWebhookRunFolder(workspace, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignRunFolder(t.Context(), runID, folder); err != nil {
		t.Fatal(err)
	}
	f.write(t, workspace+"/runs/"+folder+"/relay_trace.json", `{"workflow_id":"`+runID+`","status":"running","calls":[{"name":"extract","status":"completed","started_at":10,"completed_at":12,"output":{"total":42}}]}`)
	f.write(t, workspace+"/runs/"+folder+"/dbos_events.jsonl", `{"timestamp":1,"level":"INFO","message":"old-log-marker"}`+"\n"+strings.Repeat(`{"timestamp":12,"level":"INFO","message":"checked total","step_id":1}`+"\n", 201))
	body := inspect()
	for _, want := range []string{`"diagnostics"`, `"available":true`, `"name":"extract"`, `"total":42`, `"invoice":"INV-7"`, `"message":"checked total"`, `"logs_truncated":true`, "In the builder, open Files", workspace + "/runs/" + folder + "/dbos_events.jsonl", `"terminal":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("execution diagnostics missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "old-log-marker") {
		t.Fatal("MCP execution logs were not bounded")
	}
}

func TestExternalRelayDraftAndPublishedIngress(t *testing.T) {
	f := newExternalRelayFixture(t)
	_, writer := f.token(t, "owner", relayConsentScopes, true)
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	// This is an ingress/dispatch test: deliberately fail the executor's
	// contract preflight so no model or credential service is contacted.
	f.mock.mu.Lock()
	var m WorkflowManifest
	if err := json.Unmarshal([]byte(f.mock.files["Workflow/invoices/workflow.json"]), &m); err != nil {
		t.Fatal(err)
	}
	m.CodeLayoutVersion = 0
	raw, _ := json.Marshal(m)
	f.mock.files["Workflow/invoices/workflow.json"] = string(raw)
	f.mock.mu.Unlock()
	f.api = &StreamingAPI{}
	f.api.scheduler = NewSchedulerService(f.api)
	f.api.scheduler.stateStore = store
	runArgs := map[string]any{"workflow_id": "invoices", "function": "process", "input": map[string]any{"name": "Asha"}, "idempotency_key": "same-key"}
	wait := func(runID string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			run, err := store.GetRun(t.Context(), runID)
			if err != nil {
				t.Fatal(err)
			}
			f.api.scheduler.runtimeStatesMu.RLock()
			active := false
			for _, state := range f.api.scheduler.runtimeStates {
				active = active || state.ActiveRunID == runID
			}
			f.api.scheduler.runtimeStatesMu.RUnlock()
			if schedulerstate.IsTerminal(run.State) && !active {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("ingress fixture did not settle")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// An unpublished draft is testable and cannot be run as a published API.
	draft := f.relayCall(t, writer, "test_relay", runArgs, 202)
	draftID := draft["run_id"].(string)
	wait(draftID)
	draftRun, err := store.GetRun(t.Context(), draftID)
	if err != nil || draftRun.ScopeID != "Workflow/invoices" {
		t.Fatalf("draft scope: %+v %v", draftRun, err)
	}
	f.relayCall(t, writer, "run_relay", runArgs, 409)
	f.relayCall(t, writer, "publish_relay", map[string]any{"workflow_id": "invoices"}, 200)
	f.mock.mu.Lock()
	f.mock.files["Workflow/invoices/planning/plan.json"] = strings.Replace(f.mock.files["Workflow/invoices/planning/plan.json"], "JSON v1", "JSON v2", 1)
	f.mock.mu.Unlock()
	f.relayCall(t, writer, "publish_relay", map[string]any{"workflow_id": "invoices"}, 200)
	_, runner := f.token(t, "reader", []string{"runs:execute"}, false)
	runArgs["version"] = "v1"
	published := f.relayCall(t, runner, "run_relay", runArgs, 202)
	runID := published["run_id"].(string)
	wait(runID)
	run, err := store.GetRun(t.Context(), runID)
	if err != nil || run.ScopeID != relayReleaseWorkspace("Workflow/invoices", "v1") {
		t.Fatalf("published scope %+v %v", run, err)
	}
	if runID == draftID || published["version"] != "v1" {
		t.Fatalf("draft/version mixed %+v", published)
	}
	duplicate := f.relayCall(t, runner, "run_relay", runArgs, 200)
	if duplicate["run_id"] != runID {
		t.Fatal("retry changed run")
	}
	f.relayCall(t, runner, "get_relay_run", map[string]any{"workflow_id": "invoices", "run_id": runID}, 200)
	f.relayCall(t, writer, "get_relay_run", map[string]any{"workflow_id": "invoices", "run_id": runID}, 200)
	// Recorded runs: everyone with access sees every caller's (one company per server).
	if all := f.relayCall(t, runner, "list_relay_runs", map[string]any{"workflow_id": "invoices"}, 200); all["scope"] != "all" {
		t.Fatalf("a runner must see every run: %v", all)
	}
	if all := f.relayCall(t, writer, "list_relay_runs", map[string]any{"workflow_id": "invoices", "version": "v1"}, 200); all["scope"] != "all" {
		t.Fatalf("editor must see every run: %v", all)
	}
	f.relayCall(t, runner, "list_relay_runs", map[string]any{"workflow_id": "invoices", "version": "v9"}, 404)
	f.relayCall(t, runner, "test_relay", map[string]any{"workflow_id": "invoices", "function": "process", "input": map[string]any{}, "idempotency_key": "denied"}, 403)
	runArgs["input"] = map[string]any{"name": "changed"}
	f.relayCall(t, runner, "run_relay", runArgs, 409)
}

func TestRelayOAuthAuthoringIsExplicit(t *testing.T) {
	f := newExternalRelayFixture(t)
	scopes, ok := validMCPOAuthScopes("")
	if !ok {
		t.Fatal("default scopes invalid")
	}
	for _, scope := range scopes {
		if scope == "relays:write" {
			t.Fatal("implicit Relay authoring")
		}
	}
	if _, ok := validMCPOAuthScopes("relays:write"); ok {
		t.Fatal("missing companion permissions accepted")
	}
	if _, ok := validMCPOAuthScopes(strings.Join(relayConsentScopes, " ")); !ok {
		t.Fatal("explicit Relay scopes rejected")
	}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	if err := validateMCPOAuthBuilderSelection(t.Context(), claims, relayConsentScopes, nil); err != nil {
		t.Fatal(err)
	}
	claims.UserID = "reader"
	claims.Username = "reader"
	if err := validateMCPOAuthBuilderSelection(t.Context(), claims, relayConsentScopes, nil); err == nil {
		t.Fatal("read-only account granted authoring")
	}
	token, _ := f.token(t, "owner", relayConsentScopes, true)
	grant := mcpOAuthGrant{FamilyID: "relay-test", UserID: token.UserID, Scopes: relayConsentScopes, Expires: time.Now().Add(time.Hour)}
	oauth := mcpOAuthTokenForGrant(grant)
	if !oauth.RelayBuilderAccess() || !oauth.BuilderAccess() || !oauth.AllWorkflows {
		t.Fatalf("wrong OAuth authority: %+v", oauth)
	}
}

// Runs are visible to everyone with access, but a secret value never is.
func TestMaskSecretValuesHidesCanary(t *testing.T) {
	doc := map[string]any{"result": "token=canary-secret-value-1", "steps": []any{map[string]any{"out": "x canary-secret-value-1 y"}}}
	masked, _ := json.Marshal(maskSecretValues(doc, []string{"canary-secret-value-1"}))
	if strings.Contains(string(masked), "canary-secret-value-1") || !strings.Contains(string(masked), "REDACTED") {
		t.Fatalf("secret value leaked: %s", masked)
	}
}
