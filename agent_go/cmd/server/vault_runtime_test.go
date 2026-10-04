package server

import (
	"context"
	"encoding/json"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func personContext(person string) context.Context {
	return context.WithValue(context.Background(), common.UserIDKey, person)
}
func TestPrivateMCPRuntimeDoesNotShareOwnerCredentials(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", strings.Repeat("a", 32))
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	for _, person := range []string{"alice", "bob"} {
		if err := setPersonalSecret(person, "KEY", person+"-secret"); err != nil {
			t.Fatal(err)
		}
		_, err := addPlaceMCPServer(person, placeMCPServer{Name: "linear", Catalog: "Linear", URL: "https://example.com/mcp", Transport: "http", Headers: map[string]placeMCPHeader{"Authorization": {Secret: "KEY", Format: "Bearer {}"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	a, err := api.resolveGovernedMCP(personContext("alice"), "alice", "Linear")
	if err != nil {
		t.Fatal(err)
	}
	b, err := api.resolveGovernedMCP(personContext("bob"), "bob", "Linear")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name == b.Name || a.ConnectionSessionID == b.ConnectionSessionID || a.Config.Headers["Authorization"] != "Bearer alice-secret" || b.Config.Headers["Authorization"] != "Bearer bob-secret" {
		t.Fatal("private credentials or connection pools crossed users")
	}
	if _, err := api.resolveGovernedMCP(personContext("bob"), "bob", a.Name); err == nil {
		t.Fatal("another user's internal server resolved")
	}
	names, overrides, _, err := api.scopeAgentMCP(personContext("bob"), "", []string{a.Name}, mcpclient.RuntimeOverrides{a.Name: {Server: &a.Config}})
	if err != nil || len(overrides) != 0 || len(names) != 1 || names[0] != mcpclient.NoServers {
		t.Fatal("forged private override admitted")
	}
	if err := recordPrivateMCP("alice", "linear", "Workflow/w"); err != nil {
		t.Fatal(err)
	}
	if names, _ := attachedMCPServersForRoot(personContext("bob"), "Workflow/w"); len(names) != 0 {
		t.Fatal("shared project exposed owner's private account")
	}
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
	for _, person := range []string{"alice", "bob"} {
		_, err := addPlaceMCPServer(person, placeMCPServer{Name: "linear", Catalog: "Linear", URL: "https://example.com/mcp", Transport: "http"})
		if err != nil {
			t.Fatal(err)
		}
		names, overrides, aliases, err := api.scopeAgentMCP(personContext(person), "", []string{"Linear", placeMCPInternalName(person, "linear")}, nil)
		want := placeMCPInternalName(person, "linear")
		if err != nil || len(names) != 1 || names[0] != want || overrides[want].Server == nil || aliases["linear"] != want {
			t.Fatal("private SDK pool retained a shared catalog name")
		}
	}
}

func TestVaultRuntimeProxyStripsSpoofedServiceIdentity(t *testing.T) {
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
	svc := &ProductScheduleService{api: &StreamingAPI{mcpConfig: cfg, mcpConfigPath: file, logger: loggerv2.NewNoop()}}
	names, pending, err := svc.validateCrewCreationServers(personContext("alice"), "alice", []string{"vault_c1", "LegacyShared"})
	if err != nil || len(names) != 2 || names[0] != "vault_c1" || len(pending) != 1 || pending[0].Name != "LegacyShared" {
		t.Fatalf("creator selection: %v %v %v", names, pending, err)
	}
	if _, _, err := svc.validateCrewCreationServers(personContext("bob"), "bob", []string{"vault_c1"}); err == nil {
		t.Fatal("Bob inherited Alice's Vault grant")
	}
}

func TestPrivateMCPHTTPDefaultsToCallerAndCannotDisconnectAnotherUser(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("MCP_CONFIG_LOCKED", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","is_admin":true},{"id":"bob","username":"bob"}]}`)
	file := filepath.Join(t.TempDir(), "mcp.json")
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Test": {URL: "https://example.com/mcp", Protocol: mcpclient.ProtocolHTTP}}}
	if err := mcpclient.SaveConfig(file, cfg); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfig: cfg, mcpConfigPath: file, logger: loggerv2.NewNoop()}
	request := func(person, body string, disconnect bool) *httptest.ResponseRecorder {
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: person, Username: person})
		r := httptest.NewRequest("POST", "/api/mcp/connect", strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("X-CapLayer-Actor", "another-user")
		w := httptest.NewRecorder()
		if disconnect {
			api.handleDisconnectServer(w, r)
		} else {
			api.handleConnectServer(w, r)
		}
		return w
	}
	if w := request("alice", `{"server_name":"Test","api_key":"alice-test-key"}`, false); w.Code != 200 {
		t.Fatalf("private connect: %d %s", w.Code, w.Body.String())
	}
	alice, err := api.resolveGovernedMCP(personContext("alice"), "alice", "Test")
	if err != nil || alice.Config.Headers["Authorization"] != "Bearer alice-test-key" {
		t.Fatalf("private credential was not saved: %v", err)
	}
	if own, _ := listPlaceMCPServers("bob"); len(own) != 0 {
		t.Fatal("new account was shared with another user")
	}
	if w := request("bob", `{"server_name":"Test"}`, true); w.Code != 200 {
		t.Fatalf("own disconnect: %d", w.Code)
	}
	if _, err := api.resolveGovernedMCP(personContext("alice"), "alice", "Test"); err != nil {
		t.Fatal("Bob's disconnect removed Alice's login")
	}
	if w := request("bob", `{"server_name":"Test","scope":"vault"}`, false); w.Code != 403 {
		t.Fatal("non-admin could modify Vault connections")
	}
}

func TestPrivateMCPSecretsHTTPStoresOnlyCallerAndNeverReturnsValues(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", strings.Repeat("k", 32))
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice"},{"id":"bob","username":"bob"}]}`)
	request := func(person, method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/me/secrets", strings.NewReader(body)).WithContext(personContext(person))
		r.Header.Set("X-User-ID", "alice")
		w := httptest.NewRecorder()
		handlePersonalMCPSecrets(w, r)
		return w
	}
	saved := request("alice", "POST", `{"name":"MCP_TEST_KEY","value":"test-private-value"}`)
	if saved.Code != 200 || strings.Contains(saved.Body.String(), "test-private-value") || saved.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe save response: %d %s", saved.Code, saved.Body.String())
	}
	own := request("alice", "GET", "")
	if own.Code != 200 || !strings.Contains(own.Body.String(), "MCP_TEST_KEY") || strings.Contains(own.Body.String(), "test-private-value") {
		t.Fatal("private listing did not return names only")
	}
	other := request("bob", "GET", "")
	if other.Code != 200 || strings.Contains(other.Body.String(), "MCP_TEST_KEY") {
		t.Fatal("another person's secrets leaked through listing")
	}
	if value, err := personalSecretValue("alice", "MCP_TEST_KEY"); err != nil || value != "test-private-value" {
		t.Fatal("owner could not resolve sealed key")
	}
	if _, err := personalSecretValue("bob", "MCP_TEST_KEY"); err == nil {
		t.Fatal("another person could resolve the key")
	}
	if w := request("", "POST", `{"name":"MCP_TEST_KEY","value":"v"}`); w.Code != 401 {
		t.Fatal("identity was accepted from caller-controlled header")
	}
}

func TestPrivateMCPInventoryAndToolTesterUseCallerWithoutGlobalFallback(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	t.Setenv("AUTH_SECRET", strings.Repeat("a", 32))
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Catalog": {URL: "https://example.com/catalog"}}}
	file := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(file, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := addPlaceMCPServer("alice", placeMCPServer{Name: "custom", URL: "https://example.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfig: cfg, mcpConfigPath: file, logger: loggerv2.NewNoop()}
	inventory := func(person string) []ToolStatus {
		w := httptest.NewRecorder()
		api.handleGetTools(w, httptest.NewRequest("GET", "/api/tools", nil).WithContext(personContext(person)))
		var rows []ToolStatus
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil {
			t.Fatal("could not list caller inventory")
		}
		return rows
	}
	own := inventory("alice")
	if len(own) != 2 {
		t.Fatal("custom private connection missing from inventory")
	}
	for _, status := range own {
		if status.Name == "Catalog" && status.Connection != connectionAvailable {
			t.Fatal("catalog template advertised as connected")
		}
		if status.Name == "custom" && status.Connection != connectionConnected {
			t.Fatal("own private connection not connected")
		}
	}
	if other := inventory("bob"); len(other) != 1 || other[0].Name != "Catalog" {
		t.Fatal("private custom metadata leaked to Bob")
	}
	resolved, err := api.resolveMCPServer(personContext("alice"), "", "custom", "read")
	if err != nil || resolved == nil || resolved.Name != placeMCPInternalName("alice", "custom") {
		t.Fatal("tool tester could not resolve the authenticated owner's connection")
	}
	if _, err := api.resolveMCPServer(personContext("bob"), "", "custom", "read"); err == nil {
		t.Fatal("tool tester accepted someone else's connection")
	}
	if _, err := api.resolveMCPServer(personContext("alice"), "unknown-session", "custom", "read"); err == nil {
		t.Fatal("unknown session fell back to browser identity")
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

func TestPrivateMCPBuiltinsAndForeignNamesNeverContactVault(t *testing.T) {
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
	if called {
		t.Fatal("unnecessary Vault request delays private/builtin-only agents")
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
	builder, err := api.privateMCPTool(ctx, "alice", "list_mcp_servers", nil)
	if err != nil {
		t.Fatal(err)
	}
	code, err := api.placeMCPToolList(ctx, "alice", "Chats/Code/projects/test")
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{w.Body.String(), builder, code} {
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
