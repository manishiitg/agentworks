package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/toolerr"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestExternalVaultCatalogScopeAndLiveRole(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin"},{"id":"member","role":"creator","products":["mcp-gateway"]}]}`)
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	claims := func(user string, scopes ...string) *UserClaims {
		return &UserClaims{UserID: user, AccessToken: &accesstokens.Token{Scopes: scopes}}
	}
	for _, tool := range catalog {
		if !isExternalVaultTool(tool.Name) {
			continue
		}
		for _, tc := range []struct {
			user   string
			scopes []string
			want   bool
		}{
			{"admin", []string{"vault:manage"}, true},
			{"admin", []string{"workflows:read", "runs:execute", "builder:chat"}, false},
			{"member", []string{"vault:manage"}, false},
			{"unknown", []string{"vault:manage"}, false},
		} {
			if got := externalTokenAllows(claims(tc.user, tc.scopes...), tool); got != tc.want {
				t.Fatalf("%s %s: %v", tc.user, tool.Name, got)
			}
		}
		if _, ok := tool.InputSchema["properties"].(map[string]any)["workflow_id"]; ok {
			t.Fatal("Vault tool requires a workflow")
		}
	}
	api := &StreamingAPI{}
	admin := claims("admin", "vault:manage")
	request := func(name string, args map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		r := adminRequest("POST", "/api/external/v1/call", string(raw), admin, nil)
		w := httptest.NewRecorder()
		api.handleExternalCall(w, r)
		return w
	}
	w := request("vault", map[string]any{"action": "shared_secrets"})
	if w.Code != 200 {
		t.Fatalf("global operation tried workflow resolution: %d %s", w.Code, w.Body)
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	w = request("vault", map[string]any{"action": "shared_secrets"})
	if w.Code != 403 {
		t.Fatalf("revoked administrator: %d %s", w.Code, w.Body)
	}
}

func TestExternalVaultGroupsReuseIdentityBindingAndRejectPaths(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin"},{"id":"active","email":"a@example.com"},{"id":"disabled","disabled":true}]}`)
	paths := []string{}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, strings.TrimSuffix(r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery, "?"))
		if (r.URL.Path != "/api/admin/users/sync" && r.Header.Get("X-CapLayer-Actor") != "admin") || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || r.Header.Get("Cookie") != "" {
			t.Error("identity/credential forwarding")
		}
		if r.URL.Path == "/api/admin/users/sync" {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "a@example.com") {
				t.Error("directory email missing")
			}
			w.WriteHeader(204)
			return
		}
		w.Write([]byte(`{"status":"saved"}`))
	}))
	defer service.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", service.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	claims := &UserClaims{UserID: "admin", AccessToken: &accesstokens.Token{Scopes: []string{"vault:manage"}}}
	api := &StreamingAPI{}
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		req := adminRequest("POST", "/api/external/v1/call", string(raw), claims, nil)
		req.Header.Set("Cookie", "client-cookie")
		req.Header.Set("X-CapLayer-Actor", "spoofed")
		w := httptest.NewRecorder()
		api.handleExternalCall(w, req)
		return w
	}
	w := call("vault", map[string]any{"action": "groups_add_member", "group_id": "team", "user_id": "active"})
	if w.Code != 200 || len(paths) != 2 || paths[0] != "POST /api/admin/users/sync" || paths[1] != "POST /api/admin/groups/team/members" {
		t.Fatalf("binding: %d %s %v", w.Code, w.Body, paths)
	}
	for _, user := range []string{"disabled", "unknown", "../users"} {
		w = call("vault", map[string]any{"action": "groups_add_member", "group_id": "team", "user_id": user})
		if w.Code != 400 || len(paths) != 2 {
			t.Fatalf("invalid member reached service: %s %d %v", user, w.Code, paths)
		}
	}
	w = call("vault", map[string]any{"action": "groups_list_members", "group_id": "../users"})
	if w.Code != 400 || len(paths) != 2 {
		t.Fatal("path traversal")
	}
	w = call("vault", map[string]any{"action": "grant_secret", "group_id": "team", "name": "KEY"})
	if w.Code != 400 || len(paths) != 2 {
		t.Fatal("missing allowed should not change grants")
	}
	// "value" belongs to vault action=mine_set_secret, so the merged tool drops it from other actions; a field no
	// action takes is still refused before anything reaches Vault.
	w = call("vault", map[string]any{"action": "shared_secrets", "secret_value": "secret"})
	if w.Code != 400 || len(paths) != 2 {
		t.Fatal("secret values accepted")
	}
	if w = call("vault", map[string]any{"action": "delete_shared_secret", "name": "KEY", "confirm": "OTHER"}); w.Code != 400 {
		t.Fatalf("delete without a matching confirm: %d %s", w.Code, w.Body)
	}
	if w = call("vault", map[string]any{"action": "audit_events", "tool": "crm_search", "limit": 5}); w.Code != 200 || paths[2] != "GET /api/admin/audit?limit=5&tool=crm_search" {
		t.Fatalf("audit: %d %s %v", w.Code, w.Body, paths)
	}
}

func TestExternalVaultLocalAndOAuthScope(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	if !externalTokenAllows(&UserClaims{UserID: GetDefaultUserID()}, externalTool{Name: "manage_vault_access"}) {
		t.Fatal("local administrator denied")
	}
	token := accesstokens.Token{Name: "vault", UserID: "admin", Scopes: []string{"vault:manage"}, ExpiresAt: time.Now().Add(time.Hour)}
	if err := accesstokens.Validate(token, time.Now()); err != nil {
		t.Fatalf("Vault-only token requires workflow bounds: %v", err)
	}
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin"},{"id":"member","role":"viewer","products":["mcp-gateway"]}]}`)
	for _, tc := range []struct {
		user string
		want int
	}{{"admin", 1}, {"member", 0}, {"unknown", 0}} {
		scopes := mcpOAuthScopesFor(&UserClaims{UserID: tc.user}, []string{"vault:manage"})
		if len(scopes) != tc.want {
			t.Fatalf("%s scopes: %v", tc.user, scopes)
		}
	}
}

func TestExternalVaultThroughMainMCPDiscoveryAndInvocation(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin"}]}`)
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets = nil
	managedGlobals = map[string]string{"TEST_KEY": "dummy-value-must-not-appear"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	claims := &UserClaims{UserID: "admin", AccessToken: &accesstokens.Token{Scopes: []string{"vault:manage", "crews:write"}, AllCrews: true}}
	srv := serveExternalMCP(t, &StreamingAPI{}, claims)
	ctx := context.Background()
	cli := dialExternalMCP(t, ctx, srv.URL)
	initialized := initializeExternalMCP(t, ctx, cli)
	if !strings.Contains(initialized.Instructions, "Vault actions use current account") || strings.Contains(initialized.Instructions, "Every tool reads;") {
		t.Fatalf("incorrect management instructions: %s", initialized.Instructions)
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"vault"}})
	requireRemoteSuccess(t, spec, "Vault schemas")
	raw := marshalContent(t, spec)
	for _, name := range []string{"vault"} {
		if !strings.Contains(raw, name) {
			t.Fatalf("missing %s", name)
		}
	}
	result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "vault", "arguments": map[string]any{"action": "shared_secrets"}})
	requireRemoteSuccess(t, result, "secret metadata")
	raw = marshalContent(t, result)
	if !strings.Contains(raw, "TEST_KEY") || strings.Contains(raw, "dummy-value-must-not-appear") {
		t.Fatalf("secret projection: %s", raw)
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	cli = reconnectAfterCatalogChange(t, ctx, cli, srv.URL)
	result = callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "vault", "arguments": map[string]any{"action": "shared_secrets"}})
	requireRemoteError(t, result, "revoked management", "insufficient_scope")
}

// Two Vault levels (owner, 2026-10-09): a Vault reader, or a manager on a
// vault:read connection, may read Vault but every change is refused before
// it reaches the service; tools with no read operation are not offered.
func TestVaultReadLevelReadsButNeverChanges(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin"},{"id":"auditor","role":"viewer","products":["mcp-gateway"],"vault_reader":true},{"id":"member","role":"viewer","products":["mcp-gateway"]}]}`)
	api := &StreamingAPI{}
	call := func(claims *UserClaims, name string, args map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(raw), claims, nil))
		return w
	}
	token := func(user string, scopes ...string) *UserClaims {
		return &UserClaims{UserID: user, AccessToken: &accesstokens.Token{Scopes: scopes}}
	}
	for _, claims := range []*UserClaims{token("auditor", "vault:read"), token("auditor", "vault:manage"), token("admin", "vault:read")} {
		if w := call(claims, "vault", map[string]any{"action": "shared_secrets"}); w.Code != 200 {
			t.Fatalf("%s read: %d %s", claims.UserID, w.Code, w.Body)
		}
		if w := call(claims, "vault", map[string]any{"action": "groups_create", "group_id": "x", "name": "X"}); w.Code != 403 {
			t.Fatalf("%s changed Vault at read level: %d %s", claims.UserID, w.Code, w.Body)
		}
		for _, name := range []string{"mutate_vault_db", "call_vault_mcp_tool"} {
			if externalTokenAllows(claims, externalTool{Name: name}) {
				t.Fatalf("%s offered %s at read level", claims.UserID, name)
			}
		}
	}
	if externalTokenAllows(token("member", "vault:read"), externalTool{Name: "read_vault_audit"}) {
		t.Fatal("a person with no Vault role reads Vault")
	}
	if !externalTokenAllows(token("admin", "vault:manage"), externalTool{Name: "mutate_vault_db"}) {
		t.Fatal("manager lost manage tools")
	}
}

// A Run account cannot manage Vaults through the direct settings routes.
// The retired Code-only product is now Crew and is not a separate access role.
func TestRunOnlyAccountCannotManageVaults(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"coder","role":"viewer","products":["work"]}]}`)
	api := &StreamingAPI{}
	w := httptest.NewRecorder()
	api.handleMyVaultsOp(w, adminRequest("POST", "/api/my-vaults/op", `{"operation":"laptops"}`, &UserClaims{UserID: "coder"}, nil))
	if w.Code != 403 {
		t.Fatalf("Run-only direct Vault request: %d %s", w.Code, w.Body)
	}
}

// PLAT-825-113: grouping cannot turn the write-only secret payload into
// logged arguments, a model instruction or a plaintext history record.
func TestGroupedVaultSecretWritesKeepValuesOutOfRepliesLogsAndRecords(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := t.Context()
	const value = "grouped-vault-secret-canary-825113"
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	claims := &UserClaims{UserID: "admin", AccessToken: &accesstokens.Token{Scopes: []string{"vault:manage"}}}
	host := serveExternalMCP(t, api, claims)
	cli := dialExternalMCP(t, ctx, host.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	args := map[string]any{"action": "set_value", "scope": "shared", "values": map[string]any{"GROUPED_MCP_KEY": value}}
	result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "vault", "arguments": args})
	requireRemoteSuccess(t, result, "grouped secret write")
	if strings.Contains(marshalContent(t, result), value) {
		t.Fatal("secret value in MCP reply")
	}
	registry := agentprofiles.NewRegistry()
	if err := registerVaultManagementTools(registry, api); err != nil {
		t.Fatal(err)
	}
	var definitions []mcpagent.ToolDefinition
	for _, binding := range caplayerproduct.BuiltinAgentProfile().Tools {
		if binding.ID == "caplayer.access" {
			continue
		}
		spec, err := registry.BuildTool(binding, agentprofiles.ToolRuntimeContext{UserID: "admin", WorkspacePath: caplayerproduct.WorkspaceRoot})
		if err != nil {
			t.Fatal(err)
		}
		if spec.Name == "manage_vault_secret_access" {
			definitions = append(definitions, mcpagent.ToolDefinition{Name: spec.Name, InputSchema: spec.Parameters, Execute: spec.Execute})
		}
	}
	jobs, err := platformtools.Project(definitions)
	if err != nil {
		t.Fatal(err)
	}
	executed := false
	for _, job := range jobs {
		if job.Name != "vault" {
			continue
		}
		output, err := job.Execute(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "admin"}), map[string]any{"action": "set_value", "scope": "shared", "values": map[string]any{"GROUPED_NATIVE_KEY": value}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, value) {
			t.Fatal("secret value in native reply")
		}
		executed = true
	}
	if !executed {
		t.Fatal("native Vault action missing")
	}
	records, err := api.chatStore.ListUserSecrets(ctx, managedGlobalSecretsUserID)
	if err != nil || len(records) != 2 {
		t.Fatal("write did not reach encrypted secret storage")
	}
	for _, record := range records {
		if strings.Contains(record.EncryptedValue, value) {
			t.Fatal("secret was stored in plaintext")
		}
	}
	raw, _ := json.Marshal(args)
	// The SDK's actual argument formatter is key-based: the renamed family
	// must retain the existing values-field rule without a tool-name exception.
	if strings.Contains(toolerr.TruncateArgsForLog(string(raw)), value) {
		t.Fatal("native argument formatter exposed a grouped secret")
	}
	if strings.Contains(logs.String(), value) {
		t.Fatal("secret value in application/tool logs")
	}
}
