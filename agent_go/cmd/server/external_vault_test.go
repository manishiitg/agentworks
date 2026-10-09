package server

import (
	"context"
	"encoding/json"
	"io"
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
	w := request("manage_vault_secret_access", map[string]any{"operation": "list"})
	if w.Code != 200 {
		t.Fatalf("global operation tried workflow resolution: %d %s", w.Code, w.Body)
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	w = request("manage_vault_secret_access", map[string]any{"operation": "list"})
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
	w := call("manage_vault_groups", map[string]any{"operation": "add_member", "group_id": "team", "user_id": "active"})
	if w.Code != 200 || len(paths) != 2 || paths[0] != "POST /api/admin/users/sync" || paths[1] != "POST /api/admin/groups/team/members" {
		t.Fatalf("binding: %d %s %v", w.Code, w.Body, paths)
	}
	for _, user := range []string{"disabled", "unknown", "../users"} {
		w = call("manage_vault_groups", map[string]any{"operation": "add_member", "group_id": "team", "user_id": user})
		if w.Code != 400 || len(paths) != 2 {
			t.Fatalf("invalid member reached service: %s %d %v", user, w.Code, paths)
		}
	}
	w = call("manage_vault_groups", map[string]any{"operation": "list_members", "group_id": "../users"})
	if w.Code != 400 || len(paths) != 2 {
		t.Fatal("path traversal")
	}
	w = call("manage_vault_secret_access", map[string]any{"operation": "set", "group_id": "team", "name": "KEY"})
	if w.Code != 400 || len(paths) != 2 {
		t.Fatal("missing allowed should not change grants")
	}
	w = call("manage_vault_secret_access", map[string]any{"operation": "list", "value": "secret"})
	if w.Code != 400 || len(paths) != 2 {
		t.Fatal("secret values accepted")
	}
	if w = call("manage_vault_secret_access", map[string]any{"operation": "delete", "name": "KEY", "confirm": "OTHER"}); w.Code != 400 {
		t.Fatalf("delete without a matching confirm: %d %s", w.Code, w.Body)
	}
	// Re-approving a changed tool approves exactly the reviewed definition.
	if w = call("manage_vault_tools", map[string]any{"operation": "approve", "public_name": "crm_search"}); w.Code != 400 || len(paths) != 2 {
		t.Fatal("approve without the reviewed fingerprint reached the service")
	}
	if w = call("manage_vault_tools", map[string]any{"operation": "approve", "public_name": "crm_search", "fingerprint": "abc", "version": 3}); w.Code != 200 || paths[2] != "POST /api/admin/tools/crm_search/approve" {
		t.Fatalf("approve: %d %s %v", w.Code, w.Body, paths)
	}
	if w = call("read_vault_audit", map[string]any{"operation": "events", "tool": "crm_search", "limit": 5}); w.Code != 200 || paths[3] != "GET /api/admin/audit?limit=5&tool=crm_search" {
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
	globalSecrets = []globalSecretEntry{{Name: "TEST_KEY", Value: "dummy-value-must-not-appear"}}
	managedGlobals = map[string]string{}
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
	if !strings.Contains(initialized.Instructions, "Vault management is authorized") || strings.Contains(initialized.Instructions, "Every tool reads;") {
		t.Fatalf("incorrect management instructions: %s", initialized.Instructions)
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"manage_vault_access", "manage_vault_groups", "manage_vault_secret_access"}})
	requireRemoteSuccess(t, spec, "Vault schemas")
	raw := marshalContent(t, spec)
	for _, name := range []string{"manage_vault_access", "manage_vault_groups", "manage_vault_secret_access"} {
		if !strings.Contains(raw, name) {
			t.Fatalf("missing %s", name)
		}
	}
	result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "manage_vault_secret_access", "arguments": map[string]any{"operation": "list"}})
	requireRemoteSuccess(t, result, "secret metadata")
	raw = marshalContent(t, result)
	if !strings.Contains(raw, "TEST_KEY") || strings.Contains(raw, "dummy-value-must-not-appear") {
		t.Fatalf("secret projection: %s", raw)
	}
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	cli = reconnectAfterCatalogChange(t, ctx, cli, srv.URL)
	result = callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "manage_vault_secret_access", "arguments": map[string]any{"operation": "list"}})
	requireRemoteError(t, result, "revoked management", "insufficient_scope")
}
