package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/mcpagent/executor"
)

func TestVaultBuilderAuthorityIsBoundToAdminProfileAndRevokedLive(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","email":"admin@example.com","role":"admin","products":[]},{"id":"member","email":"member@example.com","role":"creator","products":["mcp-gateway"]},{"id":"disabled","email":"disabled@example.com","disabled":true}]}`)
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	var builderCalls int
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("X-CapLayer-Actor") != "admin" {
			t.Error("unverified service actor")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/admin/runtime/servers":
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{}, "secrets": []any{}})
		case "/api/admin/runtime/builder/servers":
			if r.Header.Get("X-Vault-Builder") != "1" {
				t.Error("no setup assertion")
			}
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": "notion", "label": "Notion", "tools": []any{}}}, "secrets": []any{map[string]any{"name": "TEST2"}}})
		case "/api/admin/runtime/builder/mcp":
			if r.Header.Get("X-Vault-Builder") != "1" {
				t.Error("no builder assertion")
			}
			builderCalls++
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("vault-chat", "admin")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "admin", Provider: "local"})
	req := QueryRequest{AgentProfileID: caplayerproduct.ProfileID, AgentMode: "multi-agent", SelectedFolder: caplayerproduct.WorkspaceRoot}
	bind := api.bindToolExecutionContext(requestCtx, "vault-chat", req, false)
	ctx, err := bind(context.Background(), "list_mcp_servers")
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := vaultAccessFor(ctx, "admin")
	if err != nil || len(inventory.Servers) != 1 || len(inventory.Secrets) != 1 {
		t.Fatalf("builder inventory %v %v", inventory, err)
	}
	if len(inventory.Users) != 2 || inventory.Users[1].ID != "member" || inventory.Users[1].Email != "member@example.com" {
		t.Fatal("builder lacks the active platform directory", inventory.Users)
	}
	listing, err := api.mcpConnectionTool(ctx, "admin", "list_mcp_servers", nil)
	if err != nil || !strings.Contains(listing, `"vault_users"`) || !strings.Contains(listing, "member@example.com") || strings.Contains(listing, "disabled@example.com") {
		t.Fatal("shared builder tool lacks active emails", listing, err)
	}
	// Automatic defaults retain the existing raw admin builder authority;
	// ordinary product sessions below still use group-scoped inventory.
	names, overrides, _, err := api.scopeAgentMCP(ctx, "vault-chat", nil, nil)
	if err != nil || len(names) != 1 {
		t.Fatalf("builder defaults: %v %v", names, err)
	}
	defaultToken := strings.TrimPrefix(overrides[names[0]].Server.Headers["Authorization"], "Bearer ")
	defaultGrant, err := verifyVaultDelegation(secret, defaultToken)
	if err != nil || defaultGrant.Purpose != "vault-builder" || defaultGrant.Session != "vault-chat" {
		t.Fatal("automatic builder lost raw authority", err)
	}
	ordinary, err := vaultAccessFor(executor.WithSessionID(requestCtx, "vault-chat"), "admin")
	if err != nil || ordinary.Users != nil {
		t.Fatal("ordinary product inventory exposed directory", ordinary.Users, err)
	}
	if _, err := api.resolveMCPServer(ctx, "another-chat", "vault_notion", "fetch"); err == nil {
		t.Fatal("builder scope crossed chat sessions")
	}
	resolved, err := api.resolveMCPServer(ctx, "vault-chat", "vault_notion", "fetch")
	if err != nil {
		t.Fatal(err)
	}
	delegation, err := verifyVaultDelegation(secret, strings.TrimPrefix(resolved.Config.Headers["Authorization"], "Bearer "))
	if err != nil || delegation.Purpose != "vault-builder" || delegation.Session != "vault-chat" {
		t.Fatal("unbound builder delegation", err)
	}
	proxy := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/internal/vault/mcp", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Vault-Builder", "1") // Browser-supplied headers cannot elevate a normal token.
		w := httptest.NewRecorder()
		api.handleVaultRuntimeMCP(w, r)
		return w
	}
	token := strings.TrimPrefix(resolved.Config.Headers["Authorization"], "Bearer ")
	if w := proxy(token); w.Code != 200 || builderCalls != 1 {
		t.Fatal("builder proxy failed", w.Code)
	}
	// A different product, read-only turn, child, PAT or bot must not inherit authority.
	for _, tc := range []struct {
		claims   *UserClaims
		query    QueryRequest
		child    string
		readOnly bool
	}{
		{&UserClaims{UserID: "admin", Provider: "local"}, QueryRequest{AgentMode: "multi-agent"}, "vault-chat", false},
		{&UserClaims{UserID: "admin", Provider: "local"}, req, "vault-chat", true},
		{&UserClaims{UserID: "admin", Provider: "local"}, req, "child", false},
		{&UserClaims{UserID: "admin", AccessToken: &accesstokens.Token{ID: "pat"}}, req, "vault-chat", false},
		{&UserClaims{UserID: "admin", Provider: "bot_owner"}, req, "vault-chat", false},
	} {
		input := context.WithValue(context.Background(), UserContextKey, tc.claims)
		output, err := api.bindToolExecutionContextForSession(input, "vault-chat", tc.child, tc.query, tc.readOnly)(context.WithValue(context.Background(), vaultBuilderKey{}, vaultBuilderAuthority{"admin", "vault-chat"}), "list_mcp_servers")
		if err == nil {
			if _, present := output.Value(vaultBuilderKey{}).(vaultBuilderAuthority); present {
				t.Fatal("setup authority leaked")
			}
		}
	}
	normal := executor.WithSessionID(requestCtx, "vault-chat")
	if _, err := api.resolveMCPServer(normal, "vault-chat", "vault_notion", "fetch"); err == nil {
		t.Fatal("ordinary admin chat inherited setup access")
	}
	api.eventStore.SetSessionOwner("vault-chat", "member")
	if w := proxy(token); w.Code != 403 {
		t.Fatal("session revocation ignored", w.Code)
	}
	api.eventStore.SetSessionOwner("vault-chat", "admin")
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	if w := proxy(token); w.Code != 403 {
		t.Fatal("administrator revocation ignored", w.Code)
	}
	if _, err := bind(context.Background(), "list_mcp_servers"); err == nil {
		t.Fatal("tool binding retained revoked admin")
	}
	if _, err := vaultAccessFor(ctx, "admin"); err == nil {
		t.Fatal("old context retained inventory access")
	}
	if builderCalls != 1 {
		t.Fatal("revoked requests reached gateway")
	}
	forged := signVaultDelegation(secret, vaultDelegation{Person: "admin", Connector: "notion", Purpose: "vault-builder", Expires: time.Now().Add(time.Hour).Unix()})
	if w := proxy(forged); w.Code != 403 {
		t.Fatal("sessionless setup delegation admitted")
	}
}
