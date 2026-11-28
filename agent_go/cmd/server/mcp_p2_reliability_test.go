package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestCapLayerTransportConfiguresConnectionPool(t *testing.T) {
	if capLayerTransport.MaxIdleConnsPerHost < 32 {
		t.Fatalf("capLayerTransport.MaxIdleConnsPerHost = %d, want >= 32 (Go default is 2)", capLayerTransport.MaxIdleConnsPerHost)
	}
	if capLayerTransport.MaxIdleConns < capLayerTransport.MaxIdleConnsPerHost {
		t.Fatalf("capLayerTransport.MaxIdleConns = %d, want >= MaxIdleConnsPerHost (%d)", capLayerTransport.MaxIdleConns, capLayerTransport.MaxIdleConnsPerHost)
	}
	if capLayerTransport.IdleConnTimeout <= 0 {
		t.Fatalf("capLayerTransport.IdleConnTimeout = %v, want > 0", capLayerTransport.IdleConnTimeout)
	}
	if capLayerTransport.ResponseHeaderTimeout != 90*time.Second {
		t.Fatalf("capLayerTransport.ResponseHeaderTimeout = %v, want 90s", capLayerTransport.ResponseHeaderTimeout)
	}
	if capLayerTransport.TLSHandshakeTimeout != 5*time.Second {
		t.Fatalf("capLayerTransport.TLSHandshakeTimeout = %v, want 5s", capLayerTransport.TLSHandshakeTimeout)
	}
}

func TestVaultDelegationExpiryGuaranteesMinTTLAcrossHourBoundary(t *testing.T) {
	nearEnd := time.Date(2026, 11, 28, 10, 59, 59, 900_000_000, time.UTC)
	startOfHour := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)

	expEnd := vaultDelegationExpiry(nearEnd, time.Hour)
	expStart := vaultDelegationExpiry(startOfHour, time.Hour)
	if expEnd != expStart {
		t.Fatalf("expiry changed within same hour bucket: start=%d end=%d", expStart, expEnd)
	}
	remaining := time.Unix(expEnd, 0).Sub(nearEnd)
	if remaining < time.Hour {
		t.Fatalf("remaining TTL near :59:59 = %v, want >= 1h", remaining)
	}
	if remaining > 2*time.Hour {
		t.Fatalf("remaining TTL = %v, want <= 2h", remaining)
	}

	// Verify resolveGovernedMCP uses the safe expiry for vault-builder delegations.
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","email":"admin@example.com","role":"admin","products":[]}]}`)
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"servers": []any{map[string]any{"id": "c-11111111", "label": "Notion", "provider": "notion", "tools": []any{}}},
		})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)

	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("vault-chat", "admin")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "admin", Provider: "local"})
	req := QueryRequest{AgentProfileID: caplayerproduct.ProfileID, AgentMode: "multi-agent", SelectedFolder: caplayerproduct.WorkspaceRoot}
	ctx, err := api.bindToolExecutionContext(requestCtx, "vault-chat", req, false)(context.Background(), "list_mcp_servers")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := api.resolveGovernedMCP(ctx, "admin", "vault_c-11111111")
	if err != nil {
		t.Fatal(err)
	}
	del, err := verifyVaultDelegation(secret, strings.TrimPrefix(resolved.Config.Headers["Authorization"], "Bearer "))
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(del.Expires, 0)); left < time.Hour-5*time.Second {
		t.Fatalf("vault-builder delegation expires too soon: %v left", left)
	}
}

func TestHandleMyVaultServersAvoidsNPlusOneCanManageCalls(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","email":"alice@example.com","role":"creator","products":["mcp-gateway"]}]}`)
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	for _, id := range []string{"c-aaaa1111", "c-aaaa2222", "c-cccc1111", "c-bbbb1111", "c-bbbb2222", "c-dddd1111", "c-eeee1111"} {
		vaultConnectionProviders.Delete(id)
	}
	t.Cleanup(func() {
		for _, id := range []string{"c-aaaa1111", "c-aaaa2222", "c-cccc1111", "c-bbbb1111", "c-bbbb2222", "c-dddd1111", "c-eeee1111"} {
			vaultConnectionProviders.Delete(id)
		}
	})

	var runtimeCalls, vaultsCalls, connectorCalls, vaultDetailCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/admin/runtime/servers":
			runtimeCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"servers": []map[string]any{
					{"id": "c-aaaa1111", "label": "My Notion", "provider": "Notion", "tools": []any{}},
					{"id": "c-aaaa2222", "label": "My Linear", "provider": "Linear", "tools": []any{}},
					{"id": "c-cccc1111", "label": "Crew Slack", "provider": "Slack", "tools": []any{}},
					{"id": "c-bbbb1111", "label": "Shared Jira", "provider": "Jira", "tools": []any{}},
					{"id": "c-bbbb2222", "label": "Shared GitHub", "provider": "GitHub", "tools": []any{}},
				},
			})
		case r.URL.Path == "/api/vaults":
			vaultsCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"vaults": []map[string]any{
					{
						"group": map[string]any{"ID": "v-personal", "Name": personalVaultName, "ProjectRoot": ""},
						"role":  "owner",
						"connections": []map[string]any{
							{"id": "c-aaaa1111", "label": "My Notion", "provider": "Notion", "status": "active"},
							{"id": "c-aaaa2222", "label": "My Linear", "provider": "Linear", "status": "active"},
							{"id": "c-dddd1111", "label": "Pending Figma", "provider": "Figma", "status": "authentication_required"},
						},
					},
					{
						"group": map[string]any{"ID": "v-crew", "Name": "Crew vault", "ProjectRoot": "crew:proj-1"},
						"role":  "owner",
						"connections": []map[string]any{
							{"id": "c-cccc1111", "label": "Crew Slack", "provider": "Slack", "status": "active"},
							{"id": "c-eeee1111", "label": "Crew Pending", "provider": "Sentry", "status": "authentication_required"},
						},
					},
					{
						"group": map[string]any{"ID": "v-shared", "Name": "Engineering", "ProjectRoot": ""},
						"role":  "member",
						"connections": []map[string]any{
							{"id": "c-bbbb1111", "label": "Shared Jira", "provider": "Jira", "status": "active"},
						},
					},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/api/admin/connectors/"):
			connectorCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/api/vaults/"):
			vaultDetailCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected gateway path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)

	api := &StreamingAPI{}
	r := httptest.NewRequest(http.MethodGet, "/api/vault/connections", nil).WithContext(personContext("alice"))
	w := httptest.NewRecorder()
	api.handleMyVaultServers(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if runtimeCalls.Load() != 1 || vaultsCalls.Load() != 1 {
		t.Fatalf("gateway calls: runtime=%d vaults=%d, want 1 and 1", runtimeCalls.Load(), vaultsCalls.Load())
	}
	if connectorCalls.Load() != 0 || vaultDetailCalls.Load() != 0 {
		t.Fatalf("N+1 calls still happened: connectors=%d vaultDetail=%d", connectorCalls.Load(), vaultDetailCalls.Load())
	}

	var resp struct {
		Servers []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			CanManage bool   `json:"can_manage"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]struct {
		status    string
		canManage bool
	}{}
	for _, s := range resp.Servers {
		byID[s.ID] = struct {
			status    string
			canManage bool
		}{s.Status, s.CanManage}
	}
	for _, owned := range []string{"c-aaaa1111", "c-aaaa2222", "c-cccc1111", "c-dddd1111"} {
		row, ok := byID[owned]
		if !ok || !row.canManage {
			t.Fatalf("expected owned connection %s with can_manage=true, got %+v (present=%v)", owned, row, ok)
		}
	}
	for _, shared := range []string{"c-bbbb1111", "c-bbbb2222"} {
		row, ok := byID[shared]
		if !ok || row.canManage {
			t.Fatalf("expected shared connection %s with can_manage=false, got %+v (present=%v)", shared, row, ok)
		}
	}
	if _, leaked := byID["c-eeee1111"]; leaked {
		t.Fatal("crew vault pending connection leaked into personal vaults list")
	}

	// Provider cache must be seeded by vaultAccessFor/allVaultsForPerson without hitting /api/admin/connectors/*.
	if got := vaultConnectionProvider(context.Background(), "c-aaaa1111"); got != "notion" {
		t.Fatalf("cached provider for c-aaaa1111 = %q, want notion", got)
	}
	if got := vaultConnectionProvider(context.Background(), "c-dddd1111"); got != "figma" {
		t.Fatalf("cached provider for c-dddd1111 = %q, want figma", got)
	}
	if connectorCalls.Load() != 0 {
		t.Fatalf("vaultConnectionProvider made %d gateway calls after seeding, want 0", connectorCalls.Load())
	}
}

func TestMCPToolStatusMatchesCatalogProviderAndLabelFromVault(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := []map[string]any{}
		if r.Header.Get("X-CapLayer-Actor") == "alice" {
			rows = append(rows,
				map[string]any{"id": "c-11111111", "label": "Work Notion", "provider": "Notion", "tools": []any{}},
				map[string]any{"id": "c-22222222", "label": "Team Drive", "provider": "google_drive", "tools": []any{}},
			)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": rows})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)

	connected := vaultServerSetFor(context.Background(), "alice")
	for _, wantConnected := range []string{
		"vault_c-11111111",
		"vault_c-11111111__scope_0123456789abcdef0123456789abcdef",
		"Notion",
		"notion",
		"Work Notion",
		"GoogleDrive",
		"Google Drive",
		"Team Drive",
	} {
		got := mcpToolStatusFromSet(wantConnected, connected)
		if got.Connection != connectionConnected || got.Status != "not_loaded" {
			t.Fatalf("mcpToolStatusFromSet(%q) = %+v, want connected/not_loaded", wantConnected, got)
		}
	}
	for _, wantAvailable := range []string{"Linear", "Slack", "vault_c-99999999"} {
		got := mcpToolStatusFromSet(wantAvailable, connected)
		if got.Connection != connectionAvailable || got.Status != "not_connected" {
			t.Fatalf("mcpToolStatusFromSet(%q) = %+v, want available/not_connected", wantAvailable, got)
		}
	}

	// Verify GET /api/tools end-to-end against a catalog config with catalog provider names.
	cfgPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	cfg := &mcpclient.MCPConfig{
		MCPServers: map[string]mcpclient.MCPServerConfig{
			"Notion":      {URL: "https://mcp.notion.com/mcp"},
			"GoogleDrive": {URL: "https://mcp.drive.example/mcp"},
			"Linear":      {URL: "https://mcp.linear.app/mcp"},
		},
	}
	if err := mcpclient.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: cfgPath, mcpConfig: cfg, logger: loggerv2.NewNoop()}
	req := httptest.NewRequest(http.MethodGet, "/api/tools", nil).WithContext(personContext("alice"))
	rec := httptest.NewRecorder()
	api.handleGetTools(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/tools status = %d body = %s", rec.Code, rec.Body.String())
	}
	var results []ToolStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	gotConn := map[string]string{}
	for _, item := range results {
		gotConn[item.Name] = item.Connection
	}
	if gotConn["Notion"] != connectionConnected || gotConn["GoogleDrive"] != connectionConnected || gotConn["Linear"] != connectionAvailable {
		t.Fatalf("/api/tools connections = %+v, want Notion=connected GoogleDrive=connected Linear=available", gotConn)
	}
}

func TestMCPConnectProblemMatchesVaultRuntimeNamesAndClearsAcrossScopes(t *testing.T) {
	t.Cleanup(func() {
		mcpConnectProblems.Range(func(k, _ any) bool {
			mcpConnectProblems.Delete(k)
			return true
		})
	})

	const (
		connID = "c-slack991"
		scopeA = "vault_c-slack991__scope_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		scopeB = "vault_c-slack991__scope_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	recordMCPConnectResult(scopeA, errors.New("connect failed: the server said: App is not enabled for Slack MCP server access: transport error"))
	if got := mcpConnectProblemFor(connID); got != "App is not enabled for Slack MCP server access" {
		t.Fatalf("mcpConnectProblemFor(%q) = %q, want Slack error", connID, got)
	}
	if got := mcpConnectProblemFor(""); got != "" {
		t.Fatalf("empty connectionID returned %q, want empty", got)
	}
	if got := mcpConnectProblemFor("c-other999"); got != "" {
		t.Fatalf("unrelated connectionID returned %q, want empty", got)
	}

	// A later successful connect under a new session/hour scope must clear the stored problem.
	recordMCPConnectResult(scopeB, nil)
	if got := mcpConnectProblemFor(connID); got != "" {
		t.Fatalf("problem was not cleared across scope rotation: %q", got)
	}

	// Legacy "__<id>" and server-vault "srv_<server>_<id>__scope_<hex>" names also match and clear.
	recordMCPConnectResult("alice__c-legacy11", errors.New("legacy connect error"))
	if got := mcpConnectProblemFor("c-legacy11"); got != "legacy connect error" {
		t.Fatalf("legacy problem = %q", got)
	}
	recordMCPConnectResult("alice__c-legacy11", nil)
	if got := mcpConnectProblemFor("c-legacy11"); got != "" {
		t.Fatalf("legacy problem not cleared: %q", got)
	}

	recordMCPConnectResult("srv_s1_c-remote11__scope_cccccccccccccccccccccccccccccccc", errors.New("remote connect error"))
	if got := mcpConnectProblemFor("c-remote11"); got != "remote connect error" {
		t.Fatalf("server-vault problem = %q", got)
	}
	recordMCPConnectResult("srv_s1_c-remote11__scope_dddddddddddddddddddddddddddddddd", nil)
	if got := mcpConnectProblemFor("c-remote11"); got != "" {
		t.Fatalf("server-vault problem not cleared: %q", got)
	}

	// Verify personalSlackAccounts surfaces active-account connect problems.
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"vaults": []map[string]any{{
				"group": map[string]any{"ID": "v1", "Name": personalVaultName},
				"role":  "owner",
				"connections": []map[string]any{{
					"id":       connID,
					"label":    "My Slack",
					"provider": "Slack",
					"status":   "active",
				}},
			}},
		})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)

	recordMCPConnectResult(scopeA, errors.New("connect failed: the server said: App is not enabled for Slack MCP server access: transport error"))
	api := &StreamingAPI{mcpConfigPath: filepath.Join("..", "..", "configs", "mcp_servers_clean.json"), logger: loggerv2.NewNoop()}
	r := httptest.NewRequest(http.MethodGet, "/api/integrations/slack/accounts", nil).WithContext(personContext("alice"))
	accounts, err := api.personalSlackAccounts(r, "")
	if err != nil {
		t.Fatalf("personalSlackAccounts failed: %v", err)
	}
	if len(accounts.Accounts) != 1 || accounts.Accounts[0].Problem != "App is not enabled for Slack MCP server access" {
		t.Fatalf("expected Slack connect problem on active account, got %+v", accounts.Accounts)
	}
}
