package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func personContext(person string) context.Context {
	return context.WithValue(context.Background(), common.UserIDKey, person)
}
func TestVaultDelegationCannotChangeActorOrConnector(t *testing.T) {
	secret := strings.Repeat("s", 32)
	valid := vaultDelegation{Person: "alice", Connector: "c1", Expires: time.Now().Add(time.Hour).Unix()}
	token := signVaultDelegation(secret, valid)
	if got, err := verifyVaultDelegation(secret, token); err != nil || got != valid {
		t.Fatal("valid delegation denied")
	}
	for _, bad := range []string{token + "x", signVaultDelegation("wrong", valid), signVaultDelegation(secret, vaultDelegation{Person: "alice", Connector: "c1", Expires: 1})} {
		if _, err := verifyVaultDelegation(secret, bad); err == nil {
			t.Fatal("tampered or expired delegation accepted")
		}
	}
}

func TestVaultRuntimeConnectsFromHostRatherThanWorkspaceContainer(t *testing.T) {
	t.Setenv("CAPLAYER_SERVICE_URL", "http://127.0.0.1:18163")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("MCP_API_URL", "http://host.docker.internal:18161")
	api := &StreamingAPI{config: ServerConfig{Host: "0.0.0.0", Port: 18161}}
	cfg, err := api.vaultServerConfig("alice", "c1")
	if err != nil || cfg.URL != "http://127.0.0.1:18161/internal/vault/mcp" {
		t.Fatalf("host MCP connection used workspace address: %s %v", cfg.URL, err)
	}
}
func TestVaultInventoryUsesHostIdentityAndNoCredentials(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	secret := strings.Repeat("s", 32)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("X-CapLayer-Actor") != "alice" || r.Header.Get("X-Vault-Platform-User") != "1" || r.Header.Get("Cookie") != "" {
			t.Error("host identity was not applied")
		}
		json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": "c1", "label": "Shared Linear", "provider": "linear", "tools": []any{map[string]any{"name": "linear__search", "input_schema": map[string]any{"type": "object"}}}}}})
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	r := httptest.NewRequest("GET", "/api/me/mcp/vault", nil).WithContext(personContext("alice"))
	r.Header.Set("X-CapLayer-Actor", "bob")
	r.Header.Set("Cookie", "session=untrusted")
	w := httptest.NewRecorder()
	api.handleMyVaultServers(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Shared Linear") || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("inventory: %d %s", w.Code, w.Body.String())
	}
	names, overrides, _, err := api.scopeAgentMCP(personContext("alice"), "", []string{"vault_c1", "UnknownLegacy"}, nil)
	if err != nil || len(names) != 1 || vaultSelectionName(names[0]) != "vault_c1" || overrides[names[0]].Server == nil {
		t.Fatal("runtime admitted a legacy shared connection")
	}
	cfg := overrides[names[0]].Server
	if strings.Contains(cfg.Headers["Authorization"], secret) || strings.Contains(cfg.URL, "example.com") {
		t.Fatal("service or upstream credential leaked to agent")
	}
}

func TestVaultRuntimeNamesSeparateUsersSessionsAndCredentialLifetimes(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	secret := strings.Repeat("s", 32)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": "c1", "label": "Company", "provider": "linear", "tools": []any{map[string]any{"name": "linear__read"}}}}})
	}))
	defer up.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", up.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	namesSeen := map[string]bool{}
	for _, item := range []struct{ person, session string }{{"alice", "chat1"}, {"bob", "chat1"}, {"alice", "chat2"}} {
		names, overrides, aliases, err := api.scopeAgentMCP(personContext(item.person), item.session, []string{"vault_c1", "workspace"}, nil)
		if err != nil || len(names) != 2 || names[1] != "workspace" {
			t.Fatalf("scoped servers: %v %v", names, err)
		}
		if namesSeen[names[0]] {
			t.Fatal("global pool key was reused across actors or sessions")
		}
		namesSeen[names[0]] = true
		cfg := *overrides[names[0]].Server
		delegate, err := verifyVaultDelegation(secret, strings.TrimPrefix(cfg.Headers["Authorization"], "Bearer "))
		if err != nil || delegate.Person != item.person || delegate.Session != item.session || delegate.Connector != "c1" {
			t.Fatal("delegation lost its actor/session binding")
		}
		selected := common.RemapMCPToolSelection([]string{"vault_c1:linear__read", "workspace:read_file"}, aliases)
		if selected[0] != names[0]+":linear__read" || selected[1] != "workspace:read_file" {
			t.Fatalf("tool restrictions lost: %v", selected)
		}
		newer := delegate
		newer.Expires++
		cfg.Headers["Authorization"] = "Bearer " + signVaultDelegation(secret, newer)
		if vaultRuntimeName("vault_c1", cfg) == names[0] {
			t.Fatal("new token lifetime would reuse an expired pooled client")
		}
	}
}

func TestVaultRuntimeProxyStripsSpoofedServiceIdentity(t *testing.T) {
	// PLAT-856-217: runtime delegates retain their identity checks without the Vault screen.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks")
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	t.Setenv("MULTI_USER_MODE", "false")
	secret := strings.Repeat("s", 32)
	var called int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/api/admin/runtime/mcp" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("X-CapLayer-Actor") != "alice" || r.Header.Get("X-Vault-Platform-User") != "1" || r.Header.Get("X-Vault-Connector") != "one" || r.Header.Get("Cookie") != "" {
			t.Error("untrusted identity or credentials reached Vault")
		}
		w.Header().Set("Set-Cookie", "should-not-leave=1")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", up.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	token := signVaultDelegation(secret, vaultDelegation{Person: "alice", Connector: "one", Expires: time.Now().Add(time.Hour).Unix()})
	for _, item := range []struct {
		token, origin string
		status        int
	}{{token, "", 200}, {token, "https://browser.example", 401}, {token + "bad", "", 401}} {
		r := httptest.NewRequest("POST", "/internal/vault/mcp?spoof=1", strings.NewReader(`{"method":"tools/list"}`))
		r.Header.Set("Authorization", "Bearer "+item.token)
		r.Header.Set("Origin", item.origin)
		r.Header.Set("X-CapLayer-Actor", "bob")
		r.Header.Set("X-Vault-Connector", "two")
		r.Header.Set("Cookie", "session=bad")
		w := httptest.NewRecorder()
		api.handleVaultRuntimeMCP(w, r)
		if w.Code != item.status || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("proxy response: %d", w.Code)
		}
	}
	if called != 1 {
		t.Fatal("unauthorized request reached Vault")
	}
}

func TestCreateCrewMCPAvailabilityUsesCreatorScope(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	secret := strings.Repeat("s", 32)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		servers := []any{}
		if r.Header.Get("X-CapLayer-Actor") == "alice" {
			servers = append(servers, map[string]any{"id": "c1", "label": "Company Linear", "provider": "linear", "tools": []any{map[string]any{"name": "linear__read"}}})
		}
		json.NewEncoder(w).Encode(map[string]any{"servers": servers})
	}))
	defer up.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", up.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"LegacyShared": {URL: "https://example.com/mcp"}}}
	file := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(file, cfg); err != nil {
		t.Fatal(err)
	}
	if err := mcpclient.SaveConfig(strings.Replace(file, ".json", "_user.json", 1), cfg); err != nil {
		t.Fatal(err)
	}
	svc := &ProjectActionDispatcher{api: &StreamingAPI{mcpConfig: cfg, mcpConfigPath: file, logger: loggerv2.NewNoop()}}
	names, pending, err := svc.validateCrewCreationServers(personContext("alice"), "alice", []string{"vault_c1", "LegacyShared"})
	if err != nil || len(names) != 2 || names[0] != "vault_c1" || len(pending) != 1 || pending[0].Name != "LegacyShared" {
		t.Fatalf("creator selection: %v %v %v", names, pending, err)
	}
	if _, _, err := svc.validateCrewCreationServers(personContext("bob"), "bob", []string{"vault_c1"}); err == nil {
		t.Fatal("Bob inherited Alice's Vault grant")
	}
}

func TestVaultRuntimeRejectsSessionOwnershipMismatchOrMissingOwner(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	secret := strings.Repeat("s", 32)
	var called bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) }))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("chat", "bob")
	if _, _, _, err := api.scopeAgentMCP(personContext("alice"), "chat", []string{"vault_one"}, nil); err == nil {
		t.Fatal("constructor silently substituted another session's owner")
	}
	for _, session := range []string{"chat", "unknown"} {
		token := signVaultDelegation(secret, vaultDelegation{Person: "alice", Connector: "one", Session: session, Expires: time.Now().Add(time.Hour).Unix()})
		req := httptest.NewRequest("POST", "/internal/vault/mcp", strings.NewReader(`{"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		api.handleVaultRuntimeMCP(w, req)
		if w.Code != 403 {
			t.Fatalf("session %s passed: %d", session, w.Code)
		}
	}
	if called {
		t.Fatal("session mismatch reached Vault")
	}
}

func TestPrivateMCPBuiltinsPreservedAndForeignNamesDenied(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	var called bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.Write([]byte(`{"servers":[]}`)) }))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	names, _, _, err := api.scopeAgentMCP(personContext("alice"), "", []string{"workspace"}, nil)
	if err != nil || len(names) != 1 || names[0] != "workspace" {
		t.Fatalf("builtin category lost: %v %v", names, err)
	}
	if _, err := api.resolveGovernedMCP(personContext("alice"), "alice", placeMCPInternalName("bob", "linear")); err == nil {
		t.Fatal("foreign private name accepted")
	}
	if !called {
		t.Fatal("default Vault inventory was not discovered")
	}
}

func TestVaultInventoryAndBuilderShareCallerGroupsAndSecretNames(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CapLayer-Actor") != "alice" {
			t.Error("caller identity changed")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"servers": []any{map[string]any{"id": "c", "label": "Notion", "tools": []any{map[string]any{"name": "notion__read"}}}},
			"groups":  []any{map[string]any{"id": "eng", "name": "Engineering", "servers": []any{map[string]any{"id": "c", "label": "Notion"}}, "secrets": []any{map[string]any{"name": "TEAM_KEY"}}}},
			"secrets": []any{map[string]any{"name": "TEAM_KEY", "value": "must-not-leak", "encrypted_value": "also-must-not-leak"}},
		})
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	ctx := personContext("alice")
	r := httptest.NewRequest("GET", "/api/me/mcp/vault?user=bob", nil).WithContext(ctx)
	r.Header.Set("X-CapLayer-Actor", "bob")
	w := httptest.NewRecorder()
	api.handleMyVaultServers(w, r)
	builder, err := api.mcpConnectionTool(ctx, "alice", "list_mcp_servers", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, out := range []string{builder} {
		for _, want := range []string{"Engineering", "TEAM_KEY", "vault_c"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %s: %s", want, out)
			}
		}
		for _, deny := range []string{"must-not-leak", "encrypted_value"} {
			if strings.Contains(out, deny) {
				t.Fatalf("leaked value metadata: %s", out)
			}
		}
	}
}

func TestVaultDefaultsWithoutProjectSelectionAndRevoke(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	allowed := true
	calls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		rows := []any{}
		if allowed && r.Header.Get("X-CapLayer-Actor") == "alice" {
			for _, id := range []string{"notion-work", "notion-personal"} {
				rows = append(rows, map[string]any{"id": id, "label": id, "provider": "notion", "tools": []any{map[string]any{"name": "notion__fetch"}}})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"servers": rows})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	api := &StreamingAPI{}
	for _, selected := range [][]string{nil, {mcpclient.NoServers}, {"workspace"}, {"vault_notion-work"}} {
		before := calls
		names, overrides, _, err := api.scopeAgentMCP(personContext("alice"), "", selected, nil)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, name := range names {
			if strings.HasPrefix(name, "vault_") {
				seen[vaultSelectionName(name)] = true
				if overrides[name].Server == nil {
					t.Fatal("missing runtime override")
				}
			}
		}
		// No Vault pick = every authorized connection; a pick = only that one (owner, 2026-10-20). NO_SERVERS is the
		// runtime sentinel for "chosen apps only" with nothing chosen (PLAT-856-394, runtimeAppSelection): no Vault app.
		want := map[string]bool{"vault_notion-work": true, "vault_notion-personal": true}
		if len(selected) == 1 && selected[0] == "vault_notion-work" {
			want = map[string]bool{"vault_notion-work": true}
		}
		if len(selected) == 1 && selected[0] == mcpclient.NoServers {
			want = map[string]bool{}
		}
		if len(seen) != len(want) || (want["vault_notion-work"] && !seen["vault_notion-work"]) || (want["vault_notion-personal"] && !seen["vault_notion-personal"]) || calls-before != 1 {
			t.Fatalf("named account inventory for %v: %v, requests %d", selected, names, calls-before)
		}
	}
	names, _, _, err := api.scopeAgentMCP(personContext("bob"), "", nil, nil)
	if err != nil || len(names) != 1 || names[0] != mcpclient.NoServers {
		t.Fatalf("Bob inherited access: %v %v", names, err)
	}
	before := calls
	names, _, _, err = api.scopeAgentMCP(context.Background(), "", nil, nil)
	if err != nil || len(names) != 1 || names[0] != mcpclient.NoServers || calls != before {
		t.Fatal("actorless run inherited Vault access")
	}
	allowed = false
	if _, err := api.resolveScopedGovernedMCP(personContext("alice"), nil, []string{"vault_notion-work"}, nil, "alice", "vault_notion-work", "notion__fetch"); err == nil {
		t.Fatal("retained project selection bypassed revocation")
	}
}
