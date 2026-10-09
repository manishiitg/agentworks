package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapLayerUsesProductAdminRoleAndHidesServiceCredential(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":[]},{"id":"member","username":"member","role":"creator","products":["mcp-gateway"]}]}`)
	calls := 0
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/admin/setup/status" || r.URL.Query().Get("token") != "" {
			t.Errorf("unexpected upstream URL %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || r.Header.Get("Cookie") != "" || r.Header.Get("X-User-ID") != "" || r.Header.Get("X-CapLayer-Actor") != "admin" {
			t.Error("browser credentials forwarded or actor not replaced")
		}
		w.Header().Set("Set-Cookie", "gw_admin=secret")
		w.Write([]byte(`{"configured":false}`))
	}))
	defer service.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", service.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	for _, tc := range []struct {
		user string
		want int
	}{{"", 401}, {"member", 403}, {"unknown", 403}, {"admin", 200}} {
		var claims *UserClaims
		if tc.user != "" {
			claims = &UserClaims{UserID: tc.user, Username: tc.user}
		}
		req := adminRequest("GET", "/api/caplayer/api/admin/setup/status?token=browser-jwt", "", claims, nil)
		req.Header.Set("Authorization", "Bearer browser-jwt")
		req.Header.Set("Cookie", "auth_token=browser-jwt")
		req.Header.Set("X-User-ID", "spoofed")
		req.Header.Set("X-CapLayer-Actor", "spoofed")
		rec := httptest.NewRecorder()
		api.handleCapLayerAdmin(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s: got %d %s want %d", tc.user, rec.Code, rec.Body, tc.want)
		}
		if rec.Header().Get("Set-Cookie") != "" || strings.Contains(rec.Body.String(), strings.Repeat("s", 32)) {
			t.Fatal("gateway credential exposed")
		}
	}
	if calls != 1 {
		t.Fatalf("unauthorized request reached gateway, calls=%d", calls)
	}
	*directory = `{"users":[{"id":"admin","username":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	rec := httptest.NewRecorder()
	api.handleCapLayerAdmin(rec, adminRequest("GET", "/api/caplayer/api/admin/setup/status", "", &UserClaims{UserID: "admin", Username: "admin"}, nil))
	if rec.Code != 403 || calls != 1 {
		t.Fatal("revoked admin retained CapLayer management access")
	}
}

func TestCapLayerDirectoryBindsOnlyActiveProductIdentities(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":[]},{"id":"active","username":"a","email":"a@example.com","role":"viewer","products":[]},{"id":"disabled","username":"d","disabled":true}]}`)
	calls := []string{}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, r.URL.Path+":"+string(body))
		if r.URL.Path == "/api/admin/users/sync" {
			w.WriteHeader(204)
		} else {
			w.Write([]byte(`{"status":"added"}`))
		}
	}))
	defer service.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", service.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "admin", Username: "admin"}
	rec := httptest.NewRecorder()
	api.handleCapLayerAdmin(rec, adminRequest("GET", "/api/caplayer/api/admin/users", "", claims, nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "disabled") || strings.Contains(rec.Body.String(), "password") {
		t.Fatal("wrong user directory response", rec.Body.String())
	}
	for _, tc := range []struct {
		id   string
		want int
	}{{"unknown", 400}, {"disabled", 400}, {"active", 200}} {
		rec := httptest.NewRecorder()
		api.handleCapLayerAdmin(rec, adminRequest("POST", "/api/caplayer/api/admin/groups/g1/members", `{"user_id":"`+tc.id+`"}`, claims, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s got %d %s", tc.id, rec.Code, rec.Body)
		}
	}
	if len(calls) != 2 || !strings.Contains(calls[0], `"ID":"active"`) || !strings.Contains(calls[1], `"user_id":"active"`) {
		t.Fatal("identity binding did not precede membership", calls)
	}
	rec = httptest.NewRecorder()
	api.handleCapLayerAdmin(rec, adminRequest("POST", "/api/caplayer/api/admin/users", `{"ID":"other"}`, claims, nil))
	if rec.Code != 405 {
		t.Fatal("separate account creation allowed")
	}
}

func TestCapLayerServiceConfigFailsClosed(t *testing.T) {
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	for _, value := range []string{"", "http://example.com", "https://user:pass@example.com", "https://example.com?token=s", "file:///tmp/service"} {
		t.Setenv("CAPLAYER_SERVICE_URL", value)
		if _, _, err := capLayerServiceConfig(); err == nil {
			t.Fatalf("accepted unsafe config %s", value)
		}
	}
}

func TestCapLayerServiceCredentialFailureDoesNotTriggerProductLogin(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin","products":[]}]}`)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer service.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", service.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	rec := httptest.NewRecorder()
	api.handleCapLayerAdmin(rec, adminRequest("GET", "/api/caplayer/api/admin/setup/status", "", &UserClaims{UserID: "admin", Username: "admin"}, nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "service authentication") {
		t.Fatalf("service failure became a product login failure: %d %s", rec.Code, rec.Body)
	}
}

func TestCapLayerAgentUsesAllowedSetupOperationsAndRechecksRoles(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin","products":[]},{"id":"member","role":"creator","products":["mcp-gateway"]}]}`)
	calls := 0
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/admin/setup/tool" || r.Header.Get("X-CapLayer-Actor") != "admin" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			t.Error("incorrect trusted service call")
		}
		var input struct {
			Operation string          `json:"operation"`
			Arguments json.RawMessage `json:"arguments"`
		}
		allowed := map[string]bool{"inspect_environment": true, "connect_server": true, "inspect_group": true, "remove_group_mcp": true}
		if json.NewDecoder(r.Body).Decode(&input) != nil || !allowed[input.Operation] || string(input.Arguments) != "{}" {
			t.Error("incorrect tool arguments")
		}
		w.Write([]byte(`{"tools":[]}`))
	}))
	defer service.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", service.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	for _, id := range []string{"", "member", "unknown"} {
		if _, err := capLayerAgentAccess(context.Background(), id, "inspect_environment", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("%s gained agent access", id)
		}
	}
	for _, operation := range []string{"publish", "revoke", "add_member", "execute_tool"} {
		if _, err := capLayerAgentAccess(context.Background(), "admin", operation, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("agent accepted %s", operation)
		}
	}
	if calls != 0 {
		t.Fatal("denied operation reached gateway")
	}
	for _, operation := range []string{"inspect_environment", "connect_server", "inspect_group", "remove_group_mcp"} {
		if _, err := capLayerAgentAccess(context.Background(), "admin", operation, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":[]}]}`
	invalidateUserDirectoryCache()
	if _, err := capLayerAgentAccess(context.Background(), "admin", "inspect_environment", json.RawMessage(`{}`)); err == nil {
		t.Fatal("revoked administrator retained tool access")
	}
	if calls != 4 {
		t.Fatalf("expected four authorized calls, got %d", calls)
	}
}

func TestCapLayerProfileUsesSharedNativeToolsAndGovernanceBridge(t *testing.T) {
	profile := caplayerproduct.BuiltinAgentProfile()
	if profile.Runtime.AgentTools.Mode != "full" || profile.Runtime.APITransport.Mode != "" || profile.Runtime.Capabilities.Secrets != agentprofiles.CapabilityDisabled {
		t.Fatal("Vault must request full native tools without enabling secret injection")
	}
	registry := agentprofiles.NewRegistry()
	if err := caplayerproduct.RegisterRuntime(registry, func(context.Context, string, string, json.RawMessage) (string, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "caplayer.access"}, agentprofiles.ToolRuntimeContext{UserID: "admin"})
	if err != nil || tool.Name != "manage_vault_access" || tool.Category != "vault" {
		t.Fatalf("management tool still exposes legacy branding: %+v, %v", tool, err)
	}
	parameters, err := json.Marshal(tool.Parameters)
	if err != nil || !strings.Contains(string(parameters), `"list_users"`) || !strings.Contains(string(parameters), `"inspect_group"`) || !strings.Contains(string(parameters), `"remove_group_mcp"`) {
		t.Fatal("Vault tool schema does not expose account lookup", err)
	}
	if len(profile.Skills) != 1 || profile.Skills[0] != "vault-access" {
		t.Fatal("Vault skill still exposes legacy branding")
	}
	profile.Product = "mcp-gateway"
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Tools) != 9 || profile.Tools[0].ID != "caplayer.access" {
		t.Fatal("unexpected governance tool surface")
	}
	if len(profile.Runtime.BridgeTools) != 9 || profile.Runtime.BridgeTools[0] != "manage_vault_access" {
		t.Fatal("tool is not directly reachable without shell")
	}
	if profile.Runtime.Workspace.Root != "Chats/CapLayer" || profile.Runtime.Conversation.Mode != "singleton" {
		t.Fatal("not a durable isolated product chat")
	}
	for _, name := range profile.ToolPolicy.Enabled {
		if !isExternalVaultTool(name) {
			t.Fatalf("unexpected tool %s", name)
		}
	}
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin","products":[]},{"id":"member","role":"creator","products":["mcp-gateway"]}]}`)
	for _, tc := range []struct {
		id      string
		allowed bool
	}{{"admin", true}, {"member", false}, {"unknown", false}, {"", false}} {
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: tc.id})
		if got := canUseCapLayerProfile(ctx, profile.ID); got != tc.allowed {
			t.Fatalf("%s profile access %v", tc.id, got)
		}
	}
}

func TestCapLayerRuntimeEnablesNativeToolsWithoutInheritedSecrets(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_CLI_FULL", "on")
	t.Setenv("AGENTWORKS_CLI_LANDLOCK", "on")
	t.Setenv("AGENTWORKS_CLI_FULL_UNCONFINED", "on")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin","products":[]}]}`)
	registry := agentprofiles.NewRegistry()
	profile := caplayerproduct.BuiltinAgentProfile()
	profile.Product = "mcp-gateway"
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{agentProfiles: registry}
	selectedGlobals := []string{"EXAMPLE_SECRET"}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: profile.ID, SelectedFolder: caplayerproduct.WorkspaceRoot,
		AgentProfileContext:   agentprofiles.PromptContext{ProjectTitle: "CapLayer"},
		SelectedGlobalSecrets: &selectedGlobals}
	req.DecryptedSecrets = append(req.DecryptedSecrets, struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}{Name: "EXAMPLE_SECRET", Value: "test-only"})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "admin"})
	resolved, err := api.resolveAgentProfileForQuery(ctx, &req, "admin", "caplayer-test")
	if err != nil {
		t.Fatal(err)
	}
	if agentProfileToolsMode(resolved) != "full" {
		t.Fatal("Vault did not request the shared full native CLI runtime")
	}
	if len(req.DecryptedSecrets) != 0 || req.SelectedGlobalSecrets == nil || len(*req.SelectedGlobalSecrets) != 0 || len(resolved.ChatSecrets) != 0 {
		t.Fatal("inherited secrets reached the CLI runtime")
	}
	// Full native tools are controlled by the provider runtime, not this
	// product bridge allowlist; bridge authority and secret injection stay narrow.
	gate := newProductToolGate(resolved)
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "get_secret", "run_in_background", "publish", "agent_browser", "manage_caplayer_access"} {
		if gate.Admit(name) {
			t.Fatalf("CapLayer admitted %s", name)
		}
	}
	if !gate.Admit("manage_vault_access") || !gate.Admit("list_vault_mcp_servers") || !gate.Admit("call_vault_mcp_tool") {
		t.Fatal("governance tool disappeared")
	}
	registrar := &gateRecordingRegistrar{gate: gate}
	policy := resolveWorkflowChatPolicy("caplayer-test", req, nil, false)
	if err := api.registerMCPToolsForChat(registrar, policy, func(name string) bool {
		return profileDisablesVirtualTool(resolved, name)
	}); err != nil {
		t.Fatal(err)
	}
	if len(registrar.admitted) != 0 {
		t.Fatalf("Vault builder MCP registration = %v", registrar.admitted)
	}
	for _, tc := range []struct {
		name     string
		profile  string
		readOnly bool
		disabled bool
	}{{"ordinary product", "", false, false}, {"read-only Vault", profile.ID, true, false}, {"disabled by profile", profile.ID, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			narrowed := req
			narrowed.AgentProfileID = tc.profile
			registered := &gateRecordingRegistrar{gate: gate}
			if err := api.registerMCPToolsForChat(registered, resolveWorkflowChatPolicy("caplayer-test", narrowed, nil, tc.readOnly), func(name string) bool {
				return tc.disabled && name == "call_mcp_tool" || profileDisablesVirtualTool(resolved, name)
			}); err != nil {
				t.Fatal(err)
			}
			for _, name := range registered.admitted {
				if name == "call_mcp_tool" {
					t.Fatal("Vault-only call admission widened another policy")
				}
			}
		})
	}
}

func TestCapLayerPromptDoesNotPromiseWorkspaceMemory(t *testing.T) {
	ctx := promptContext{HasProfile: true, ProfileID: "caplayer", WorkspaceFilesDisabled: true}
	for _, name := range []string{"project-memory", "workspace-map"} {
		if sectionByName(t, name).Applies(ctx) {
			t.Fatalf("narrow tool profile got %s", name)
		}
	}
	ctx.WorkspaceFilesDisabled = false
	if !sectionByName(t, "project-memory").Applies(ctx) {
		t.Fatal("regular product memory changed")
	}
}
