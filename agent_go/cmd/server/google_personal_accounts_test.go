package server

import (
	"context"
	"encoding/json"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Drive the actual website handlers and Builder/MCP executor against the
// persisted registry, then resolve the account as a different Run caller.
func TestPersonalGoogleConnectionsAcrossSurfaces(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("LOCAL_MODE", "false")
	t.Setenv("GOG_HOME", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"builder","username":"builder","can_create":true},{"id":"editor","username":"editor","can_create":true},{"id":"runner","username":"runner"},{"id":"boss","username":"boss","admin":true}]}`)
	wf := WorkflowManifest{ID: "orders", Label: "Orders", Access: &WorkflowAccess{Owners: []string{"builder"}, Editors: []string{"editor"}, Readers: []string{"runner"}}}
	raw, _ := json.Marshal(wf)
	flow, _ := json.Marshal(WorkflowManifest{ID: "flow", Kind: "relay", Label: "Flow", Access: &WorkflowAccess{Owners: []string{"builder"}, Editors: []string{"editor"}}})
	gog := filepath.Join(t.TempDir(), "gog")
	script := `#!/bin/sh
case "$*" in
 *"auth list"*) printf '%s\n' '{"accounts":[{"email":"editor@example.com","client":"personal-app","valid":true,"scopes":["https://www.googleapis.com/auth/gmail.readonly"]},{"email":"original@example.com","client":"personal-app","valid":true,"scopes":["https://www.googleapis.com/auth/gmail.readonly"]}]}' ;;
 *) printf '%s\n' "$@" ;;
esac
`
	if err := os.WriteFile(gog, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	config := services.GmailConfig{GogPath: gog, GwsPath: gog, Connections: []services.GmailConnection{
		{ID: "mine", DisplayName: "Personal account", Personal: true, OwnerID: "editor", Enabled: true, AuthBackend: "gog", ClientName: "personal-app", Email: "editor@example.com", AllowReadAccess: true},
		{ID: "other", DisplayName: "Other account", Personal: true, OwnerID: "builder", Enabled: true},
		{ID: "legacy", DisplayName: "Original Code account", Email: "original@example.com", AuthBackend: "gog", ClientName: "personal-app", AllowReadAccess: true, ScopeWorkspace: "_users/editor/Chats/Code/projects/original", OwnerID: "editor", Enabled: true},
		{ID: "company", DisplayName: "Company account", Enabled: true},
	}}
	cfg, _ := json.Marshal(config)
	mock := &mockWorkspaceAPI{files: map[string]string{"config/gmail-config.json": string(cfg), "Workflow/orders/workflow.json": string(raw), "Workflow/flow/workflow.json": string(flow)}}
	owners := withProjectOwnerRegistry(t)
	for _, folder := range []string{"second", "account-crew"} {
		if err := owners.Register(projectOwnerRecord{Product: "work", Folder: folder, ProjectID: folder, OwnerID: "editor", Shared: true}); err != nil {
			t.Fatal(err)
		}
		product, _ := json.Marshal(map[string]any{"schema_version": 1, "product": "work", "id": folder, "title": folder})
		mock.files["Crew/"+folder+"/product.json"] = string(product)
		mock.files["Crew/"+folder+"/workflow.json"] = string(product)
	}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	previous := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(previous) })
	svc, err := services.InitGmailService()
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	GmailConnectionRoutes(router, nil)
	selectAccount := func(user, id, path string, selected bool, want int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"workspace_path": path, "selected": selected})
		req := googleAppRequest(http.MethodPost, "/api/human-feedback/gmail/connections/"+id+"/selection", string(body), user)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s select %s=%t: %d %s", user, id, selected, rec.Code, rec.Body.String())
		}
	}
	selectAccount("editor", "mine", "Workflow/orders", true, 200)
	selectAccount("editor", "mine", "Workflow/flow", true, 403)
	selectAccount("builder", "other", "Workflow/flow", true, 200)
	selectAccount("builder", "mine", "Workflow/orders", true, 403)
	selectAccount("runner", "mine", "Workflow/orders", true, 403)
	selectAccount("boss", "mine", "Workflow/orders", true, 403)
	selectAccount("editor", "mine", "Crew/second", true, 200)
	for name, claims := range map[string]*UserClaims{
		"bot cannot revoke the connector's grant": {UserID: "editor", Username: "editor", Provider: "bot_route"},
		"token cannot manage another workflow":    {UserID: "editor", Username: "editor", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read", "builder:chat"}, WorkflowIDs: []string{"another"}}},
	} {
		body := `{"workspace_path":"Workflow/orders","selected":false}`
		if claims.AccessToken != nil {
			body = `{"workspace_path":"Workflow/orders","selected":true}`
		}
		req := httptest.NewRequest(http.MethodPost, "/api/human-feedback/gmail/connections/mine/selection", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// My Vault has no product selection and no shared-vault dependency.
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	restoreClientStore := services.SetGogClientStore(services.StoreGogClientForTest)
	t.Cleanup(restoreClientStore)
	if _, err := services.CreateOAuthClient(context.Background(), "personal-app", []byte(`{"web":{"client_id":"fixture.apps.googleusercontent.com","client_secret":"fixture-secret"}}`), false); err != nil {
		t.Fatal(err)
	}
	central := &StreamingAPI{}
	GoogleAppRoutes(router, central)
	GmailOAuthRoutes(router, central)
	centralCall := func(body, user string, want int) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, googleAppRequest(http.MethodPost, "http://localhost:8000/api/integrations/google/accounts", body, user))
		if rec.Code != want {
			t.Fatalf("My Vault %s: %d %s", user, rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	body := `{"action":"connect","client_name":"personal-app","allow_read_access":true,"allow_agent_write_access":false,"services":[{"service":"docs","write":true}],"owner_id":"builder","workspaces":["Workflow/orders"],"config_home":"/ignored"}`
	created := centralCall(body, "editor", 200)
	id, _ := created["connection_id"].(string)
	pending, found := svc.GetConnection(id)
	if !found || !pending.Personal || pending.OwnerID != "editor" || len(pending.Workspaces) != 0 || pending.ConfigHome != "" || !pending.AllowReadAccess || len(pending.Services) != 1 {
		t.Fatalf("central account identity/grants: %+v", pending)
	}
	if !strings.Contains(created["auth_url"].(string), "localhost%3A8000") {
		t.Fatal("local callback lost")
	}
	if again := centralCall(body, "editor", 200); again["connection_id"] != id {
		t.Fatal("repeated Connect duplicated pending account")
	}
	// A verified duplicate uses the existing ID, store and selections; another
	// owner's identical address is never reused.
	resolved, err := svc.ResolvePersonalGoogleIdentity(context.Background(), id, "EDITOR@example.com")
	if err != nil || resolved.ID != "mine" || len(resolved.Workspaces) != 2 || len(resolved.Services) != 0 {
		t.Fatalf("verified identity reuse: %+v %v", resolved, err)
	}
	if _, ok := svc.GetConnection(id); ok {
		t.Fatal("duplicate pending account retained")
	}
	list := centralCall(`{"action":"list_saved"}`, "editor", 200)
	listJSON, _ := json.Marshal(list)
	if strings.Contains(string(listJSON), `"id":"other"`) || strings.Contains(string(listJSON), `"id":"company"`) {
		t.Fatal("My Vault exposed another person's account")
	}
	centralCall(body, "runner", 403)
	centralCall(`{"action":"reconnect","connection_id":"mine"}`, "builder", 403)
	// Personal OAuth has one website entry; legacy routes are company-only.
	for _, legacy := range []struct{ path, body, user string }{
		{"/api/human-feedback/gmail/connections", `{"workspace_path":"Workflow/orders","display_name":"Account","client_name":"personal-app"}`, "boss"},
		{"/api/human-feedback/gmail/connections/mine/auth/start", `{}`, "editor"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, googleAppRequest(http.MethodPost, legacy.path, legacy.body, legacy.user))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("legacy personal OAuth available: %s %d", legacy.path, rec.Code)
		}
	}
	selected := services.GmailUseScope{Workspace: "Workflow/orders", UserID: "runner"}
	if c, err := svc.ConnectionForScope("mine", selected); err != nil || c.ID != "mine" {
		t.Fatalf("shared caller selected=%+v %v", c, err)
	}
	if _, err := svc.ConnectionForScope("mine", services.GmailUseScope{Workspace: "Workflow/another", UserID: "editor"}); err == nil {
		t.Fatal("ownership allowed implicit cross-item use")
	}
	// The shared implementation behind both Builder and direct MCP applies the same decision.
	api := &StreamingAPI{}
	reg := crewCapabilityRegistrar{}
	if err := api.registerPersonalGoogleAccountTool(reg, "Workflow/orders"); err != nil {
		t.Fatal(err)
	}
	ctx := googleAppRequest(http.MethodPost, "/", "", "editor").Context()
	out, err := reg["manage_google_account"].execute(ctx, map[string]any{"action": "list_saved"})
	if err != nil || !strings.Contains(out, "mine") || strings.Contains(out, "Other account") {
		t.Fatalf("saved accounts: %s %v", out, err)
	}
	workflowAccountMCPRequest(t, api, "editor", "google", map[string]any{"action": "attach", "connection_id": "mine"}, 200)
	workflowAccountMCPRequest(t, api, "runner", "google", map[string]any{"action": "list_saved"}, 403)
	workflowAccountMCPRequest(t, api, "builder", "google", map[string]any{"action": "attach", "connection_id": "mine"}, 403)
	workflowAccountMCPRequest(t, api, "editor", "google", map[string]any{"action": "detach", "connection_id": "mine"}, 200)
	if _, err := svc.ConnectionForScope("mine", selected); err == nil {
		t.Fatal("detached account still usable")
	}
	if _, err := svc.ConnectionForScope("mine", services.GmailUseScope{Workspace: "Crew/second", UserID: "runner"}); err != nil {
		t.Fatalf("detaching one disconnected another: %v", err)
	}
	if err := svc.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, ok := svc.GetConnection("mine")
	if !ok || len(c.Workspaces) != 1 {
		t.Fatalf("saved selection lost: %+v", c)
	}
	// An old private account can look personal in the UI while its stored
	// Personal bit is still unset. Keep the inbox ownership check strict;
	// the existing explicit Use here operation adopts the saved record.
	if _, err := reg["manage_google_account"].execute(ctx, map[string]any{"action": "set_inbox_account", "connection_id": "legacy"}); err == nil {
		t.Fatal("unadopted account bypassed the inbox ownership check")
	}
	selectAccount("editor", "legacy", "Workflow/orders", true, 200)
	c, _ = svc.GetConnection("legacy")
	if !c.Personal || len(c.Workspaces) != 2 || c.ScopeWorkspace == "" {
		t.Fatalf("legacy selection/store identity lost: %+v", c)
	}
	if _, err := reg["manage_google_account"].execute(ctx, map[string]any{"action": "set_inbox_account", "connection_id": "legacy"}); err != nil {
		t.Fatalf("adopted own account could not supply inboxes from Builder: %v", err)
	}
	// An administrator can disconnect, but cannot put their browser's Google
	// login under somebody else's saved personal connection.
	rec := httptest.NewRecorder()
	startGmailOAuthHandler(api)(rec, mux.SetURLVars(googleAppRequest("POST", "/", "", "boss"), map[string]string{"id": "mine"}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin personal reconnect: %d %s", rec.Code, rec.Body.String())
	}
	// Account owner and admin manage saved connections; another Builder cannot.
	if !canManageGoogleAccount(googleAppRequest("PATCH", "/", "", "editor"), c) || !canManageGoogleAccount(googleAppRequest("PATCH", "/", "", "boss"), c) || canManageGoogleAccount(googleAppRequest("PATCH", "/", "", "builder"), c) {
		t.Fatal("saved account ownership changed")
	}
	if canManageGoogleAccount(googleAppRequest("PATCH", "/", "", "editor"), services.GmailConnection{ID: "company"}) {
		t.Fatal("personal account authority reached company accounts")
	}
	// The execution target comes from server configuration, not attached folders.
	common.SetSessionWorkingDir("google-personal-test", "Workflow/orders/runs/run-1")
	common.SetSessionWorkflowPath("google-personal-test", "Workflow/orders")
	t.Cleanup(func() { common.ClearSessionShellConfig("google-personal-test") })
	runCtx := context.WithValue(context.Background(), common.ChatSessionIDKey, "google-personal-test")
	runCtx = context.WithValue(runCtx, common.UserIDKey, "runner")
	if root, err := common.GoogleWorkspaceFromContext(runCtx); err != nil || root != "Workflow/orders" {
		t.Fatalf("run target=%s %v", root, err)
	}
	// An agent in workflow X cannot name a personal account selected only in Y.
	execute := virtualtools.CreateHumanToolExecutors()["google_workspace_cli"]
	if _, err := execute(runCtx, map[string]any{"connection_id": "mine", "args": []interface{}{"gmail", "search", "subject:hello"}}); err == nil {
		t.Fatal("workflow X used an account attached only to Y")
	}
	// Shared Crew execution uses its selected account, independently of caller.
	crew := "Crew/account-crew"
	selectAccount("editor", "mine", crew, true, 200)
	// The connector is no longer a Builder. The owner's explicit decision is
	// that the existing selection still works until someone removes it.
	withMemoryUserDirectory(t, `{"users":[{"id":"builder","username":"builder","can_create":true},{"id":"editor","username":"editor"},{"id":"runner","username":"runner"},{"id":"boss","username":"boss","admin":true}]}`)
	common.SetSessionWorkingDir("google-personal-crew", crew)
	common.SetSessionWorkflowPath("google-personal-crew", crew)
	t.Cleanup(func() { common.ClearSessionShellConfig("google-personal-crew") })
	crewCtx := context.WithValue(runCtx, common.ChatSessionIDKey, "google-personal-crew")
	result, err := execute(crewCtx, map[string]any{"args": []interface{}{"gmail", "search", "subject:hello"}})
	if err != nil || !strings.Contains(result, "editor@example.com") {
		t.Fatalf("shared Crew did not use its selected account: %s %v", result, err)
	}
	// Lost item access still lets the connector revoke its explicit selection.
	selectAccount("editor", "mine", "Crew/account-crew", false, 200)
	selectAccount("editor", "mine", "Crew/account-crew", false, 403)

}
