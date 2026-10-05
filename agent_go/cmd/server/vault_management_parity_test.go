package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestVaultManifestSharesBuilderAndExternalContracts(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	api := &StreamingAPI{}
	if err := registerVaultManagementTools(registry, api); err != nil {
		t.Fatal(err)
	}
	if err := caplayerproduct.RegisterRuntime(registry, api.capLayerConnectionAccess); err != nil {
		t.Fatal(err)
	}
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	names := caplayerproduct.ExternalTools()
	profile := caplayerproduct.BuiltinAgentProfile()
	if !reflect.DeepEqual(names, profile.Runtime.BridgeTools) || !reflect.DeepEqual(names, profile.ToolPolicy.Enabled) {
		t.Fatal("manifest tool drift")
	}
	for i, binding := range profile.Tools {
		spec, err := registry.BuildTool(binding, agentprofiles.ToolRuntimeContext{UserID: "admin", WorkspacePath: caplayerproduct.WorkspaceRoot})
		if err != nil {
			t.Fatal(err)
		}
		if spec.Name != names[i] {
			t.Fatalf("binding %s produced %s instead of %s", binding.ID, spec.Name, names[i])
		}
		found := false
		for _, external := range catalog {
			if external.Name != spec.Name {
				continue
			}
			found = true
			builderSchema, _ := json.Marshal(spec.Parameters)
			externalSchema, _ := json.Marshal(external.InputSchema)
			if string(builderSchema) != string(externalSchema) || external.Description != spec.Description {
				t.Fatalf("different contracts for %s\nbuilder: %s\nexternal: %s", spec.Name, builderSchema, externalSchema)
			}
		}
		if !found {
			t.Fatalf("builder tool missing from platform MCP: %s", spec.Name)
		}
	}
}

func TestExternalVaultSetupUsesSharedLiveMCPAndSQL(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","email":"admin@example.com","role":"admin"}]}`)
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	calls := 0
	upstream := server.NewMCPServer("vault-setup-test", "1", server.WithToolCapabilities(false))
	upstream.AddTool(mcp.NewTool("notion-fetch", mcp.WithString("page_id", mcp.Required())), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls++
		args := req.Params.Arguments.(map[string]any)
		return mcp.NewToolResultText("resolved " + args["page_id"].(string)), nil
	})
	rpc := server.NewStreamableHTTPServer(upstream, server.WithEndpointPath("/api/admin/runtime/builder/mcp"), server.WithStateLess(true))
	sqlCalls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("X-CapLayer-Actor") != "admin" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect verified identity")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/admin/runtime/builder/servers":
			if r.Header.Get("X-Vault-Builder") != "1" {
				t.Error("missing setup assertion")
			}
			w.Write([]byte(`{"servers":[{"id":"notion","label":"Notion","tools":[{"name":"notion-fetch","input_schema":{"type":"object"}}]}],"groups":[],"secrets":[{"name":"TEST_KEY"}]}`))
		case "/api/admin/runtime/builder/mcp":
			if r.Header.Get("X-Vault-Builder") != "1" || r.Header.Get("X-Vault-Connector") != "notion" {
				t.Error("missing scoped setup authority")
			}
			rpc.ServeHTTP(w, r)
		case "/api/admin/database/query", "/api/admin/database/mutate":
			sqlCalls++
			w.Write([]byte(`{"rows":[],"affected_rows":1}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("vault-chat", "admin")
	claims := &UserClaims{UserID: "admin", AccessToken: &accesstokens.Token{Scopes: []string{"vault:manage"}}}
	host := serveExternalMCP(t, api, claims)
	ctx := context.Background()
	cli := dialExternalMCP(t, ctx, host.URL)
	initializeExternalMCP(t, ctx, cli)
	invoke := func(name string, args map[string]any) *mcp.CallToolResult {
		return callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": name, "arguments": args})
	}
	listing := invoke("list_vault_mcp_servers", map[string]any{})
	requireRemoteSuccess(t, listing, "administrator inventory")
	if raw := marshalContent(t, listing); !strings.Contains(raw, "vault_notion") || !strings.Contains(raw, "admin@example.com") {
		t.Fatal(raw)
	}
	for name, args := range map[string]map[string]any{
		"query_vault_db":  {"sql": "SELECT id FROM groups"},
		"mutate_vault_db": {"sql": "UPDATE groups SET description=? WHERE id=?", "params": []any{"test", "team"}},
	} {
		requireRemoteSuccess(t, invoke(name, args), name)
	}
	result := invoke("call_vault_mcp_tool", map[string]any{"server": "vault_notion", "tool": "notion-fetch", "arguments": map[string]any{"page_id": "canonical-id"}})
	requireRemoteSuccess(t, result, "setup execution")
	if calls != 1 || sqlCalls != 2 || !strings.Contains(marshalContent(t, result), "canonical-id") {
		t.Fatalf("calls=%d sql=%d result=%s", calls, sqlCalls, marshalContent(t, result))
	}
	registry := agentprofiles.NewRegistry()
	if err := registerVaultManagementTools(registry, api); err != nil {
		t.Fatal(err)
	}
	builderTool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "caplayer.mcp.call"}, agentprofiles.ToolRuntimeContext{UserID: "admin", WorkspacePath: caplayerproduct.WorkspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	builderCtx := executor.WithSessionID(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "admin"}), "vault-chat")
	arguments := map[string]any{"server": "vault_notion", "tool": "notion-fetch", "arguments": map[string]any{"page_id": "builder-id"}}
	if _, err := builderTool.Execute(builderCtx, arguments); err == nil {
		t.Fatal("ordinary chat acquired setup authority")
	}
	builderCtx = context.WithValue(builderCtx, vaultBuilderKey{}, vaultBuilderAuthority{Person: "admin", Session: "vault-chat"})
	output, err := builderTool.Execute(builderCtx, arguments)
	if err != nil || !strings.Contains(output, "builder-id") || calls != 2 {
		t.Fatalf("builder shared execution: %s %v", output, err)
	}
	requireRemoteError(t, invoke("call_vault_mcp_tool", map[string]any{"server": "http://arbitrary-host", "tool": "notion-fetch", "arguments": map[string]any{}}), "foreign server", "exact active Vault")
	*directory = `{"users":[{"id":"admin","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	requireRemoteError(t, invoke("call_vault_mcp_tool", map[string]any{"server": "vault_notion", "tool": "notion-fetch", "arguments": map[string]any{}}), "revoked admin", "insufficient_scope")
	if _, err := builderTool.Execute(builderCtx, arguments); err == nil {
		t.Fatal("builder retained revoked administrator")
	}
	if calls != 2 {
		t.Fatal("revoked administrator executed upstream")
	}
}
