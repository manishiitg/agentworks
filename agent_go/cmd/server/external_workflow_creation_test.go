package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func workflowCreationArgs(folder string) map[string]any {
	return map[string]any{
		"folder_name":   folder,
		"workflow_json": map[string]any{"schema_version": 1, "version": "1.0.0", "code_layout_version": 0, "id": "wf_" + folder, "label": "KB reader", "created_by": "outsider", "access": map[string]any{"owners": []any{"outsider"}}},
		"plan_json": map[string]any{"steps": []any{map[string]any{
			"type": "agent", "id": "answer", "title": "Answer", "description": "Read attached KB", "next_step_id": "end",
			"items": []any{map[string]any{"id": "read", "type": "user_message", "message": "Read the attached knowledge."}},
		}}},
	}
}

func TestExternalWorkflowCreationThroughMCP(t *testing.T) {
	f := newExternalRelayFixture(t)
	token, raw := f.token(t, "owner", builderConsentScopes, true)
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	srv := serveExternalMCP(t, f.api, claims)
	ctx := context.Background()
	cli := dialExternalMCP(t, ctx, srv.URL)
	initializeExternalMCP(t, ctx, cli)
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"workspace"}})
	requireRemoteSuccess(t, spec, "creator schema")
	if !strings.Contains(marshalContent(t, spec)+marshalStructured(t, spec), "workspace") {
		t.Fatal("creator missing from MCP discovery")
	}
	created := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "workspace", "arguments": testWorkspaceCreate("goal", workflowCreationArgs("kb-reader"))})
	requireRemoteSuccess(t, created, "create workflow")
	if !strings.Contains(marshalContent(t, created)+marshalStructured(t, created), "wf_kb-reader") {
		t.Fatal("creation response missing workflow ID")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, "Workflow/kb-reader")
	if err != nil || !found || manifest.CreatedBy != "owner" || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner || len(manifest.Access.Owners) != 1 {
		t.Fatalf("ownership not assigned to caller: %+v %v", manifest, err)
	}
	if manifest.Version != WorkflowContractCurrentVersion || manifest.CodeLayoutVersion != 1 {
		t.Fatalf("fresh workflow has legacy contract/layout: %+v", manifest)
	}
	if err := requireCurrentWorkflowContractForManualRun(ctx, "Workflow/kb-reader"); err != nil {
		t.Fatalf("fresh workflow cannot execute: %v", err)
	}
	for _, path := range []string{"planning/plan.json", "soul/soul.md", "db/.gitkeep", "db/reports/.gitkeep"} {
		if _, found, err := readFileFromWorkspace(ctx, "Workflow/kb-reader/"+path); err != nil || !found {
			t.Fatalf("shared creator scaffold %s missing: %v", path, err)
		}
	}
	f.relayCall(t, raw, "get_workflow", map[string]any{"workflow_id": "wf_kb-reader"}, 200)
	f.createGoal(t, raw, workflowCreationArgs("kb-reader"), 200)
	duplicateID := workflowCreationArgs("another-folder")
	duplicateID["workflow_json"].(map[string]any)["id"] = "wf_kb-reader"
	f.createGoal(t, raw, duplicateID, 409)
	if _, found, _ := ReadWorkflowManifest(ctx, "Workflow/another-folder"); found {
		t.Fatal("duplicate ID created a folder")
	}
}

func TestExternalWorkflowCreationAuthorityAndValidation(t *testing.T) {
	f := newExternalRelayFixture(t)
	for _, test := range []struct {
		name, user string
		scopes     []string
		all        bool
	}{
		{"read only", "owner", []string{"workflows:read", "files:read"}, true},
		{"runner", "owner", []string{"workflows:read", "files:read", "runs:execute"}, true},
		{"bounded Builder", "owner", builderConsentScopes, false},
		{"viewer", "reader", builderConsentScopes, true},
		{"editor", "outsider", builderConsentScopes, true},
		{"Relay consent", "owner", relayConsentScopes, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			token, raw := f.token(t, test.user, test.scopes, test.all)
			claims, err := accessTokenClaims(token)
			if err != nil {
				t.Fatal(err)
			}
			allowed := test.user == "owner"
			if externalWorkflowCreationAllowed(claims) != allowed {
				t.Fatal("MCP creation differs from current person role")
			}
			want := 403
			if allowed {
				want = 200
			}
			f.createGoal(t, raw, workflowCreationArgs("role-"+strings.ToLower(strings.ReplaceAll(test.name, " ", "-"))), want)
		})
	}
	_, raw := f.token(t, "owner", builderConsentScopes, true)
	for _, field := range []string{"shared_knowledgebase", "folder_access", "Folder_Access", "knowledgebase_sources", "crew_attachments", "workflow_context_paths"} {
		args := workflowCreationArgs("invalid")
		args["workflow_json"].(map[string]any)[field] = []any{map[string]any{"path": "/private"}}
		f.createGoal(t, raw, args, 400)
	}
	badGraph := workflowCreationArgs("invalid")
	badGraph["plan_json"].(map[string]any)["steps"].([]any)[0].(map[string]any)["next_step_id"] = "missing-step"
	f.createGoal(t, raw, badGraph, 400)
	retiredRoute := workflowCreationArgs("invalid")
	retiredRoute["plan_json"].(map[string]any)["steps"].([]any)[0].(map[string]any)["predefined_routes"] = []any{map[string]any{"route_id": "legacy-worker", "route_name": "Legacy worker", "condition": "When needed", "sub_agent_step": map[string]any{"type": "agent", "id": "legacy-worker", "title": "Legacy worker", "description": "LLM specialist", "prompt_contract": "description-first-v1"}}}
	f.createGoal(t, raw, retiredRoute, 400)
	badPath := workflowCreationArgs("../outside")
	f.createGoal(t, raw, badPath, 400)
	if _, found, _ := ReadWorkflowManifest(t.Context(), "Workflow/invalid"); found {
		t.Fatal("invalid inputs wrote workflow")
	}
	// A stale in-flight identity must not survive revocation.
	token, _ := f.token(t, "owner", builderConsentScopes, true)
	claims, _ := accessTokenClaims(token)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Revoke(t.Context(), token.ID, "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]any{"name": "workspace", "arguments": testWorkspaceCreate("goal", workflowCreationArgs("revoked"))})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", strings.NewReader(string(encoded)))
	f.api.handleExternalCall(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)))
	externalTestBody(t, w, 403)
}

func TestLocalOwnerTokenCanCreateWorkflow(t *testing.T) {
	f := newExternalRelayFixture(t)
	f.api.stoppedSessions = map[string]bool{}
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "")
	t.Setenv("MULTI_USER_MODE", "false")
	owner := GetDefaultUserID()
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "agentworks,work")
	previousStore, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	previous, previousRaw, err := previousStore.Issue(t.Context(), accesstokens.Token{Name: "Existing reader", UserID: owner, Scopes: []string{"workflows:read"}, AllWorkflows: true, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	previousStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	f.api.accessTokenSessions.sessions = map[string]accessTokenSession{"prior-reader": {previous.ID, "prior-reader", "Workflow/invoices", WorkflowAccessRead}}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/access-tokens", strings.NewReader(`{"local_full_access":true}`))
	w := httptest.NewRecorder()
	f.api.handleAccessTokens(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: owner})))
	if w.Code != http.StatusCreated {
		t.Fatalf("local token: %d %s", w.Code, w.Body.String())
	}
	var issued struct {
		Token       string             `json:"token"`
		AccessToken accesstokens.Token `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.api.accessTokenSessions.sessions["prior-reader"]; !ok {
		t.Fatal("creating the local preset canceled another client session")
	}
	if issued.AccessToken.Name != "agentworks-local" || !issued.AccessToken.NonExpiring {
		t.Fatal("wrong local token policy")
	}
	raw := issued.Token
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stillReadOnly, err := store.Authenticate(t.Context(), previousRaw, time.Now())
	if err != nil || stillReadOnly.ID != previous.ID || stillReadOnly.Allows("builder:chat") {
		t.Fatalf("renewing full local access revoked or broadened another client: %v", err)
	}
	token, err := store.Authenticate(t.Context(), issued.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !token.LocalPreset {
		t.Fatal("local preset marker was not persisted")
	}
	f.api.accessTokenSessions.sessions["local-owner"] = accessTokenSession{token.ID, "local-owner", "Workflow/invoices", WorkflowAccessOwner}
	another := httptest.NewRequest(http.MethodPost, "/api/auth/access-tokens", strings.NewReader(`{"name":"Another client login","scopes":["workflows:read"],"all_workflows":true,"expires_in_days":7}`))
	otherResponse := httptest.NewRecorder()
	f.api.handleAccessTokens(otherResponse, another.WithContext(context.WithValue(another.Context(), UserContextKey, &UserClaims{UserID: owner})))
	if otherResponse.Code != http.StatusCreated {
		t.Fatalf("another local client: %d", otherResponse.Code)
	}
	if _, ok := f.api.accessTokenSessions.sessions["local-owner"]; !ok {
		t.Fatal("another client login canceled the independent local session")
	}
	if _, err = store.Authenticate(t.Context(), previousRaw, time.Now()); err == nil {
		t.Fatal("ordinary client replacement stopped revoking prior ordinary grants")
	}
	if _, err = store.Authenticate(t.Context(), issued.Token, time.Now()); err != nil {
		t.Fatalf("another client login revoked the full local grant: %v", err)
	}
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	if !externalWorkflowCreationAllowed(claims) {
		t.Fatal("local Owner token cannot create")
	}
	f.createGoal(t, raw, workflowCreationArgs("local-kb-reader"), 200)
	manifest, found, err := ReadWorkflowManifest(t.Context(), "Workflow/local-kb-reader")
	if err != nil || !found || manifest.CreatedBy != owner {
		t.Fatalf("local ownership: %+v %v", manifest, err)
	}
}
