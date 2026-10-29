package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const projectMCPAlphaRoot = "Crew/alpha"
const projectMCPBetaRoot = "Crew/beta"

// Canonical registered Crews, including an ID that differs from its folder.
// OAuth and workspace HTTP use the same isolated fixtures and owner registry.
func projectMCPCrewFixture(t *testing.T) (triggerLinkEnv, string) {
	t.Helper()
	owners := withProjectOwnerRegistry(t)
	env := newTriggerLinkEnv(t)
	registry := agentprofiles.NewRegistry()
	profile := workproduct.BuiltinAgentProfile()
	profile.Product = "work"
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	env.api.agentProfiles = registry
	env.mock.mu.Lock()
	defer env.mock.mu.Unlock()
	for path := range env.mock.files {
		if strings.HasPrefix(path, "_users/") {
			delete(env.mock.files, path)
		}
	}
	for _, row := range []struct{ id, folder, owner string }{{"alpha", "alpha", "owner"}, {"beta", "beta", "owner"}, {"checks", "checks-folder", "owner"}, {"gamma", "gamma", "other"}} {
		root := "Crew/" + row.folder
		if err := owners.Register(projectOwnerRecord{Product: "work", Folder: row.folder, ProjectID: row.id, OwnerID: row.owner, Shared: true, CanonicalRoot: root, Private: true, AudienceConfigured: true}); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "product": "work", "id": row.id, "title": row.id, "session_id": "sess-" + row.id, "owner_id": row.owner, "created_at": "2026-10-01T00:00:00Z"})
		env.mock.files[root+"/product.json"] = string(raw)
	}
	return env, "Crew/checks-folder"
}

func TestProjectMCPResourceCannotWiden(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	for _, kind := range []string{"work", "workflow", "relay"} {
		for _, access := range []string{"view", "use"} {
			resource := "https://agentworks.example.com" + projectMCPPrefix + kind + "/selected/" + access
			if !validMCPResource(resource) {
				t.Fatal(resource)
			}
			grant := mcpOAuthGrant{Resource: resource, Scopes: slices.Clone(mcpOAuthScopes), UserID: "owner"}
			token := mcpOAuthTokenForGrant(grant)
			if token.AllWorkflows || token.AllCrews || token.ProjectKind != kind || token.BuilderAccess() || token.Allows("code:run") || token.Allows("crews:write") || token.Allows("users:manage") || token.Allows("knowledgebase:read") {
				t.Fatalf("widened %s/%s: %+v", kind, access, token)
			}
			if token.AllowsCrew("other") || token.AllowsWorkflow("other") {
				t.Fatal("escaped bounds")
			}
			if (kind == "work") != token.AllowsCrew("selected") {
				t.Fatal("wrong Crew bound")
			}
			if (kind == "workflow" || kind == "relay") != token.AllowsWorkflow("selected") {
				t.Fatal("wrong workflow bound")
			}
			if token.Allows("crews:run") || token.Allows("runs:execute") {
				if access != "use" {
					t.Fatal("View gained execution")
				}
			}
			limited := grant
			limited.Scopes = []string{"dashboards:read"}
			if len(mcpOAuthTokenForGrant(limited).Scopes) != 1 {
				t.Fatal("saved consent widened")
			}
		}
	}
	for _, resource := range []string{"https://agentworks.example.com" + projectMCPPrefix + "code/selected/view", "https://agentworks.example.com" + projectMCPPrefix + "code/selected/use", "https://evil.example" + projectMCPPrefix + "work/a/use", "https://agentworks.example.com" + projectMCPPrefix + "work/a/build", "https://agentworks.example.com" + projectMCPPrefix + "work/a/use?project=b", "https://agentworks.example.com" + projectMCPPrefix + "work/a/use#x", "https://agentworks.example.com" + projectMCPPrefix + "work/../use", "https://agentworks.example.com" + projectMCPPrefix + "work/%61/use"} {
		if validMCPResource(resource) {
			t.Fatal("accepted", resource)
		}
	}
}

func TestProjectMCPCatalogExcludesGlobalAndBuilderTools(t *testing.T) {
	tokenTestSetup(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks,work,relays,knowledgebase,mcp-gateway,bullpen")
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"work", "workflow", "relay"} {
		for _, mode := range []string{"view", "use"} {
			a := projectMCPAudience{kind, "selected", mode}
			token := a.token(accesstokens.Token{Scopes: a.scopes()})
			claims := &UserClaims{UserID: GetDefaultUserID(), AccessToken: &token}
			listed := map[string]externalTool{}
			for _, tool := range catalog {
				if !externalTokenAllows(claims, tool) {
					continue
				}
				if tool.actions != nil {
					var ok bool
					tool, ok = externalMergedForClaims(claims, tool)
					if !ok {
						t.Fatal("empty merged catalog")
					}
				}
				listed[tool.Name] = tool
			}
			for _, name := range []string{"chat", "settings", "models", "get_settings", "manage_project", "users", "account", "costs", "messages", "code", "code_review", "builder", "plan", "vault", "brain_admin", "bullpen_admin", "brain_read", "brain_update", "server_product", "write_file", "write_crew_file"} {
				if _, exists := listed[name]; exists {
					t.Fatalf("%s/%s exposes %s", kind, mode, name)
				}
			}
			if _, exists := listed["dashboard"]; !exists {
				t.Fatalf("%s/%s has no dashboard reads", kind, mode)
			}
			definition, exists := listed["functions"]
			actions, _ := definition.InputSchema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]any)
			canCall := slices.Contains(actions, any("call"))
			if !exists || canCall != (mode == "use") {
				t.Fatalf("%s/%s canonical function capability incorrect", kind, mode)
			}
		}
	}
}

// Real OAuth storage, HTTP auth/consent and tool dispatch, isolated from all
// real user data. No model call, provider request or business run is started.
func TestProjectMCPOAuthDiscoveryPermissionsAndRevocation(t *testing.T) {
	env, checksRoot := projectMCPCrewFixture(t)
	invalidateWorkflowManifestIndex()
	t.Cleanup(invalidateWorkflowManifestIndex)
	api := env.api
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
	t.Setenv("AUTH_SECRET", "project-mcp-isolated-signing-secret")
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	t.Setenv("AGENT_PRODUCTS", "agentworks,work,relays")
	env.mock.mu.Lock()
	var goal WorkflowManifest
	if err := json.Unmarshal([]byte(env.mock.files[manifestPath("Workflow/reports")]), &goal); err != nil {
		t.Fatal(err)
	}
	relay := goal
	relay.ID = "wf_project_relay"
	relay.Label = "Project Flow"
	relay.Kind = "relay"
	raw, _ := json.Marshal(relay)
	env.mock.files[manifestPath("Workflow/project-flow")] = string(raw)
	env.mock.mu.Unlock()

	router := mux.NewRouter()
	router.HandleFunc(mcpOAuthRegisterPath, api.handleMCPOAuthRegister).Methods("POST")
	router.HandleFunc(mcpOAuthAuthorizePath, api.handleMCPOAuthAuthorize).Methods("GET")
	router.HandleFunc(mcpOAuthConsentPath, api.handleMCPOAuthConsent).Methods("GET", "POST")
	router.HandleFunc(mcpOAuthTokenPath, api.handleMCPOAuthToken).Methods("POST")
	router.HandleFunc(mcpOAuthConnectionsPath, api.handleMCPOAuthConnections).Methods("GET")
	router.HandleFunc(mcpOAuthConnectionsPath+"/{id}", api.handleMCPOAuthConnections).Methods("DELETE")
	router.HandleFunc("/api/oauth/mcp/project", api.handleProjectMCPSetup).Methods("GET")
	router.HandleFunc(projectMCPPrefix+"{kind}/{project}/{access}", api.handleProjectMCP).Methods("GET", "POST", "DELETE")
	router.HandleFunc(externalMCPPath, api.handleExternalMCP).Methods("GET", "POST", "DELETE")
	router.HandleFunc(mcpOAuthProtectedResourcePath+"/project/{kind}/{project}/{access}", api.handleMCPOAuthProtectedResource).Methods("GET")
	handler := AuthMiddleware(router)
	request := func(method, path, body, token, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	jwt, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	registered := request("POST", mcpOAuthRegisterPath, `{"client_name":"Project fixture","redirect_uris":["https://client.example/callback"]}`, "", "application/json")
	var client mcpOAuthClient
	if registered.Code != 201 || json.Unmarshal(registered.Body.Bytes(), &client) != nil {
		t.Fatal(registered.Code, registered.Body)
	}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	for _, fixture := range []struct{ kind, id, root string }{{"work", "beta", projectMCPBetaRoot}, {"work", "checks", checksRoot}, {"workflow", goal.ID, "Workflow/reports"}, {"relay", relay.ID, "Workflow/project-flow"}} {
		for _, mode := range []string{"view", "use"} {
			t.Run(fixture.kind+"/"+fixture.id+"/"+mode, func(t *testing.T) {
				path := projectMCPPrefix + fixture.kind + "/" + fixture.id + "/" + mode
				resource := "https://agentworks.example.com" + path
				info := request("GET", "/api/oauth/mcp/project?workspace="+url.QueryEscape(fixture.root), "", jwt, "")
				if info.Code != 200 || !strings.Contains(info.Body.String(), fixture.id) {
					t.Fatal("setup", info.Code, info.Body)
				}
				unauth := request("GET", path, "", "", "")
				if unauth.Code != 401 || !strings.Contains(unauth.Header().Get("WWW-Authenticate"), path) {
					t.Fatal("challenge", unauth.Code, unauth.Header())
				}
				metadata := request("GET", "/.well-known/oauth-protected-resource"+path, "", "", "")
				if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), resource) {
					t.Fatal("metadata", metadata.Code, metadata.Body)
				}
				params := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {"https://client.example/callback"}, "state": {"project-test"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {resource}}
				auth := request("GET", mcpOAuthAuthorizePath+"?"+params.Encode(), "", "", "")
				if auth.Code != 303 {
					t.Fatal("authorize", auth.Code, auth.Body)
				}
				next, _ := url.Parse(auth.Header().Get("Location"))
				consentPath := mcpOAuthConsentPath + "?request=" + next.Query().Get("request")
				consent := request("GET", consentPath, "", jwt, "")
				if consent.Code != 200 || !strings.Contains(consent.Body.String(), `"project":`) {
					t.Fatal("consent", consent.Code, consent.Body)
				}
				var consentDTO struct {
					Project map[string]any `json:"project"`
				}
				if json.Unmarshal(consent.Body.Bytes(), &consentDTO) != nil || len(consentDTO.Project) != 4 || consentDTO.Project["kind"] != fixture.kind || consentDTO.Project["access"] != mode || consentDTO.Project["id"] != fixture.id {
					t.Fatal("project consent leaked or mislabelled metadata", consent.Body)
				}
				approved := request("POST", consentPath, `{"decision":"approve","workflow_ids":[]}`, jwt, "application/json")
				var redirect struct {
					URL string `json:"redirect_url"`
				}
				if approved.Code != 200 || json.Unmarshal(approved.Body.Bytes(), &redirect) != nil {
					t.Fatal("approve", approved.Code, approved.Body)
				}
				callback, _ := url.Parse(redirect.URL)
				form := url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ID}, "redirect_uri": {"https://client.example/callback"}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}, "resource": {resource}}
				exchange := request("POST", mcpOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded")
				var pair struct {
					Access  string `json:"access_token"`
					Refresh string `json:"refresh_token"`
				}
				if exchange.Code != 200 || json.Unmarshal(exchange.Body.Bytes(), &pair) != nil || pair.Access == "" {
					t.Fatal("exchange", exchange.Code)
				}
				store, err := openMCPOAuthStore()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				grant, err := store.Authenticate(t.Context(), pair.Access)
				if err != nil {
					t.Fatal(err)
				}
				token := mcpOAuthTokenForGrant(grant)
				claims := &UserClaims{UserID: "owner", AccessToken: &token}
				// The issued OAuth audience cannot be swapped for the global connection
				// or another project, even though this same account owns both projects.
				if w := request("GET", externalMCPPath, "", pair.Access, ""); w.Code != 401 {
					t.Fatal("global audience accepted", w.Code)
				}
				if w := request("GET", projectMCPPrefix+"work/alpha/use", "", pair.Access, ""); w.Code != 401 {
					t.Fatal("wrong audience accepted", w.Code)
				}

				if mode == "view" {
					if w := request("GET", projectMCPPrefix+fixture.kind+"/"+fixture.id+"/use", "", pair.Access, ""); w.Code != 401 {
						t.Fatal("view token accepted use audience", w.Code)
					}
				}
				init := request("POST", path, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"project-fixture","version":"1"}}}`, pair.Access, "application/json")
				if init.Code != 200 {
					t.Fatal("authenticated project MCP initialize", init.Code, init.Body)
				}
				projects, err := api.externalCrewsVisible(t.Context(), claims, "")
				if err != nil {
					t.Fatal(err)
				}
				if fixture.kind == "work" {
					if len(projects) != 1 || projects[0]["id"] != fixture.id {
						t.Fatal("crew discovery", projects)
					}
				} else if len(projects) != 0 {
					t.Fatal("other Crews leaked", projects)
				}
				callAs := func(caller *UserClaims, name string, args map[string]any) (int, map[string]any) {
					body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
					rec := httptest.NewRecorder()
					api.handleExternalCall(rec, adminRequest("POST", path, string(body), caller, nil))
					out := map[string]any{}
					_ = json.Unmarshal(rec.Body.Bytes(), &out)
					return rec.Code, out
				}
				call := func(name string, args map[string]any) (int, map[string]any) { return callAs(claims, name, args) }
				status, listed := call("workspace", map[string]any{"action": "list"})
				if fixture.kind == "work" {
					rows, _ := listed["workspaces"].([]any)
					if status != 200 || len(rows) != 1 || rows[0].(map[string]any)["id"] != fixture.id {
						t.Fatal("workspace discovery", status, listed)
					}
				} else if status != 403 {
					t.Fatal("workflow connection reached workspace discovery", status, listed)
				}
				for _, selector := range []string{"alpha", "work:alpha", "code:alpha", "../alpha", projectMCPAlphaRoot} {
					if status, out := call("workspace", map[string]any{"action": "get", "workspace_id": selector}); status < 400 {
						t.Fatal("tampered selector", selector, status, out)
					}
				}
				status, listed = call("list_workflows", map[string]any{})
				if fixture.kind == "workflow" || fixture.kind == "relay" {
					rows, _ := listed["workflows"].([]any)
					if status != 200 || len(rows) != 1 {
						t.Fatal("workflow discovery", status, listed)
					}
				} else if status != 403 {
					t.Fatal("Crew connection reached workflows", status, listed)
				}
				if status, _ := call("messages", map[string]any{"action": "list_agents"}); status != 403 {
					t.Fatal("global agents exposed", status)
				}
				if status, _ := call("update_crew", map[string]any{"crew_id": fixture.id, "name": "must-not-write"}); status != 404 {
					t.Fatal("retired Builder entry point must be refused, 404", status)
				}
				if status, _ := call("workspace", map[string]any{"action": "update", "workspace_id": "work:" + fixture.id, "changes": map[string]any{"name": "must-not-write"}}); status != 403 {
					t.Fatal("canonical Builder permission exposed", status)
				}
				if fixture.kind == "work" {
					if status, _ := callAs(crewWriter("other"), "workspace", map[string]any{"action": "update", "workspace_id": "work:" + fixture.id, "changes": map[string]any{"name": "must-not-write"}}); status != 404 {
						t.Fatal("private Crew existence must be hidden from non-member, refused404", status)
					}
				}
				if status, _ := call("workspace", map[string]any{"action": "ask", "workspace_id": fixture.kind + ":" + fixture.id, "message": "must-not-run"}); status != 403 {
					t.Fatal("Builder conversation exposed", status)
				}
				if mode == "view" {
					for name, action := range map[string]string{"functions": "call", "relay": "run"} {
						definition, exists := externalCatalogTool(name)
						if !exists {
							t.Fatal("missing canonical tool", name)
						}
						filtered, allowed := externalMergedForClaims(claims, definition)
						var actions []any
						if allowed {
							actions, _ = filtered.InputSchema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]any)
						}
						if slices.Contains(actions, any(action)) {
							t.Fatal("View execution", name, action)
						}
						if status, _ := call(name, map[string]any{"action": action, "crew_id": fixture.id, "workflow_id": fixture.id, "function": "must-not-run"}); status != 403 {
							t.Fatal("View execution must be refused", name, action, status)
						}
					}
				}
				// Refresh preserves the exact resource, scopes and project cap.
				refreshed, _, nextRefresh, err := store.Refresh(t.Context(), pair.Refresh, client.ID, resource)
				if err != nil {
					t.Fatal(err)
				}
				after := mcpOAuthTokenForGrant(refreshed)
				if after.ProjectIdentity == "" || after.ProjectIdentity != token.ProjectIdentity || after.ProjectKind != fixture.kind || after.AllCrews || after.AllWorkflows || !slices.Equal(after.Scopes, token.Scopes) {
					t.Fatal("refresh widened")
				}
				connections, err := store.Connections(t.Context(), "owner")
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, c := range connections {
					if c.ID == grant.FamilyID {
						found = true
						if c.AllWorkflows || c.Resource != resource {
							t.Fatal("mislabelled connection", c)
						}
					}
				}
				if !found {
					t.Fatal("connection not listed")
				}
				// Bypass the auth middleware deliberately: the handler also refuses
				// same-kind tokens on another ID or access mode.
				for _, wrong := range []projectMCPAudience{{fixture.kind, "other", mode}, {fixture.kind, fixture.id, map[string]string{"view": "use", "use": "view"}[mode]}} {
					rec := httptest.NewRecorder()
					api.handleProjectMCP(rec, adminRequest("POST", projectMCPPrefix+wrong.Kind+"/"+wrong.ID+"/"+wrong.Access, "{}", claims, nil))
					if rec.Code != 403 {
						t.Fatal("handler audience bypass", rec.Code)
					}
				}
				manifestFile := fixture.root + "/product.json"
				if fixture.kind == "workflow" || fixture.kind == "relay" {
					manifestFile = manifestPath(fixture.root)
				}
				env.mock.mu.Lock()
				original := env.mock.files[manifestFile]
				var replacement map[string]any
				_ = json.Unmarshal([]byte(original), &replacement)
				replacement["created_at"] = "2026-10-18T00:00:00Z"
				changed, _ := json.Marshal(replacement)
				env.mock.files[manifestFile] = string(changed)
				env.mock.mu.Unlock()
				if rec := request("POST", path, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, pair.Access, "application/json"); rec.Code != 403 {
					t.Fatal("ID reuse retargeted connection", rec.Code, rec.Body)
				}
				form = url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ID}, "resource": {resource}, "refresh_token": {nextRefresh}}
				if rec := request("POST", mcpOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded"); rec.Code != 400 {
					t.Fatal("ID reuse refreshed connection", rec.Code)
				}
				env.mock.mu.Lock()
				env.mock.files[manifestFile] = original
				env.mock.mu.Unlock()
				if _, err := store.ActiveFamily(t.Context(), grant.FamilyID); err == nil {
					t.Fatal("revoked family active")
				}
			})
		}
	}
}

func TestProjectMCPSharingRemovalCheckedNextRequest(t *testing.T) {
	env, checksRoot := projectMCPCrewFixture(t)
	invalidateWorkflowManifestIndex()
	t.Cleanup(invalidateWorkflowManifestIndex)
	t.Setenv("AGENT_PRODUCTS", "agentworks,work,relays")
	env.mock.mu.Lock()
	var goal WorkflowManifest
	_ = json.Unmarshal([]byte(env.mock.files[manifestPath("Workflow/reports")]), &goal)
	goal.Access.Readers = []string{"other"}
	raw, _ := json.Marshal(goal)
	env.mock.files[manifestPath("Workflow/reports")] = string(raw)
	relay := goal
	relay.ID = "wf_share_relay"
	relay.Kind = "relay"
	relayRaw, _ := json.Marshal(relay)
	env.mock.files[manifestPath("Workflow/share-flow")] = string(relayRaw)
	env.mock.mu.Unlock()
	for _, fixture := range []struct{ kind, id, root string }{{"work", "beta", projectMCPBetaRoot}, {"work", "checks", checksRoot}, {"workflow", goal.ID, "Workflow/reports"}, {"relay", relay.ID, "Workflow/share-flow"}} {
		t.Run(fixture.kind+"/"+fixture.id, func(t *testing.T) {
			if fixture.kind == "work" {
				if err := defaultProjectOwners().SetAudience(fixture.kind, strings.TrimPrefix(fixture.root, "Crew/"), true, []string{"other"}); err != nil {
					t.Fatal(err)
				}
			}
			a := projectMCPAudience{fixture.kind, fixture.id, "use"}
			token := a.token(accesstokens.Token{Scopes: a.scopes()})
			claims := &UserClaims{UserID: "other", AccessToken: &token}
			if _, err := env.api.projectMCPTarget(t.Context(), claims, a); err != nil {
				t.Fatal("shared access refused", err)
			}
			if fixture.kind == "work" {
				if err := defaultProjectOwners().SetAudience(fixture.kind, strings.TrimPrefix(fixture.root, "Crew/"), true, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				env.mock.mu.Lock()
				var current WorkflowManifest
				_ = json.Unmarshal([]byte(env.mock.files[manifestPath(fixture.root)]), &current)
				current.Access.Readers = nil
				raw, _ := json.Marshal(current)
				env.mock.files[manifestPath(fixture.root)] = string(raw)
				env.mock.mu.Unlock()
			}
			if _, err := env.api.projectMCPTarget(t.Context(), claims, a); err == nil {
				t.Fatal("sharing removal did not end access")
			}
		})
	}
	// CLI tokens and PATs cannot reach the new app-only configuration route.
	if cliOAuthAllowedPath("GET", "/api/oauth/mcp/project") {
		t.Fatal("CLI allowed app-only connection management")
	}
}

func TestProjectMCPDashboardDiscoveryFiltersOtherAccessibleProjects(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/secret/workflow.json", `{"id":"secret","label":"Another accessible project","access":{"owners":["owner"]}}`)
	f.write(t, "Workflow/invoices/db/reports/index.html", "<html><title>Invoices</title><body>Selected project</body></html>")
	f.write(t, "Workflow/secret/db/reports/index.html", "<html><title>Other</title><body>Other accessible project</body></html>")
	a := projectMCPAudience{"workflow", "invoices", "view"}
	token := a.token(accesstokens.Token{Scopes: a.scopes()})
	claims := &UserClaims{UserID: "owner", AccessToken: &token}
	rec := httptest.NewRecorder()
	f.api.listAccessibleDashboards(rec, adminRequest("GET", "/api/external/v1/call", "", claims, nil), 100, 0, false, false)
	var result struct {
		Dashboards []struct {
			ProjectID string `json:"project_id"`
		} `json:"dashboards"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Dashboards) != 1 || result.Dashboards[0].ProjectID != "invoices" {
		t.Fatalf("dashboard discovery: %d %s", rec.Code, rec.Body)
	}
}

func TestProjectMCPCrewStableIDUsesCanonicalFunctionSurface(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	a := projectMCPAudience{"work", "team", "use"}
	token := a.token(accesstokens.Token{Scopes: a.scopes()})
	claims := &UserClaims{UserID: "owner", AccessToken: &token}
	if token.Allows("code:run") {
		t.Fatal("retired Code execution granted")
	}
	target, err := api.projectMCPTarget(t.Context(), claims, a)
	if err != nil || target.ID != a.ID || target.Root != externalCommandCrewRoot {
		t.Fatalf("stable ID failed to resolve different storage folder: %+v %v", target, err)
	}
	for _, selector := range []string{a.ID, "work:" + a.ID} {
		_, manifest, ok := api.resolveExternalWorkspace(t.Context(), claims, selector)
		if !ok || manifest.ID != a.ID {
			t.Fatal("stable selector refused", selector)
		}
	}
	body, _ := json.Marshal(map[string]any{"name": "functions", "arguments": map[string]any{"action": "list", "project": a.ID}})
	rec := httptest.NewRecorder()
	api.handleExternalCall(rec, adminRequest("POST", "/api/external/call", string(body), claims, nil))
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out["functions"] == nil {
		t.Fatal("canonical Crew function surface refused", rec.Code, out)
	}
	for _, selector := range []string{"code:" + a.ID, "../" + a.ID, externalCommandCrewRoot} {
		if _, _, ok := api.resolveExternalWorkspace(t.Context(), claims, selector); ok {
			t.Fatal("wrong origin/path admitted", selector)
		}
	}
}

func TestProjectMCPCreationIdentityRequiredAndStable(t *testing.T) {
	env, checksRoot := projectMCPCrewFixture(t)
	for _, fixture := range []struct{ kind, id, root string }{{"work", "beta", projectMCPBetaRoot}, {"work", "checks", checksRoot}} {
		a := projectMCPAudience{fixture.kind, fixture.id, "use"}
		target := dashboardTarget{Root: fixture.root, ID: fixture.id}
		env.mock.mu.Lock()
		var doc map[string]any
		_ = json.Unmarshal([]byte(env.mock.files[fixture.root+"/product.json"]), &doc)
		delete(doc, "created_at")
		raw, _ := json.Marshal(doc)
		env.mock.files[fixture.root+"/product.json"] = string(raw)
		env.mock.mu.Unlock()
		if _, err := projectMCPCreationIdentity(t.Context(), a, target); err == nil {
			t.Fatal("missing creation identity admitted")
		}
		write := func(owner, created string) {
			env.mock.mu.Lock()
			defer env.mock.mu.Unlock()
			doc["owner_id"], doc["created_at"], doc["title"], doc["updated_at"] = owner, created, "Renamed", "2026-10-18"
			raw, _ := json.Marshal(doc)
			env.mock.files[fixture.root+"/product.json"] = string(raw)
		}
		write("owner", "2026-10-01T00:00:00Z")
		original, err := projectMCPCreationIdentity(t.Context(), a, target)
		if err != nil {
			t.Fatal(err)
		}
		write("owner", "2026-10-01T00:00:00Z")
		same, err := projectMCPCreationIdentity(t.Context(), a, target)
		if err != nil || same != original {
			t.Fatal("mutable presentation changed identity")
		}
		write("other-owner", "2026-10-01T00:00:00Z")
		other, err := projectMCPCreationIdentity(t.Context(), a, target)
		if err != nil || other != original {
			t.Fatal("current owner changed creation identity")
		}
	}
}
