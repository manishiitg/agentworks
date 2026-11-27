package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// TestMCPFullChainE2E_BridgeToVaultUpstreamKeepaliveAndIsolation exercises the
// real production MCP stack end to end without external network or LLM calls:
//
//	real mcpbridge stdio binary
//	  -> real agent_go bridgeAuthMiddleware + bridgeActivityKeepaliveMiddleware
//	  -> real /tools/virtual/* & /tools/mcp/* routing (vaultBridgeToolName)
//	  -> real mcpagent executor.HandlePerToolMCPRequest + resolveMCPServer
//	  -> real handleVaultRuntimeMCP (signed vaultDelegation)
//	  -> simulated Vault gateway enforcing live actor/grant revocation
//	  -> real Streamable HTTP upstream MCP server
//
// Verifies: readiness marker creation on tools/list, virtual tool routing both
// directly and through call_tool, governed upstream MCP tool call, session
// activity keepalive on /tools/*, live grant revocation on a warm pooled
// connection, and refusals for #secret-read tokens, cross-session headers,
// cross-session virtual scopes, and ungranted servers.
func TestMCPFullChainE2E_BridgeToVaultUpstreamKeepaliveAndIsolation(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	serviceToken := strings.Repeat("v", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", serviceToken)

	common.SetBridgeTokenSecret("e2e-bridge-secret")
	t.Cleanup(func() { common.SetBridgeTokenSecret("") })

	const (
		sessionID     = "mcp-e2e-chat-alice"
		otherSession  = "mcp-e2e-chat-bob"
		virtualScope  = sessionID + ":vt:e2e-1"
		connectorID   = "c1"
		vaultServer   = "vault_" + connectorID
		upstreamTool  = "linear__fetch"
		allowedItemID = "ENG-42"
	)

	var upstreamCalls atomic.Int32
	var grantRevoked atomic.Bool

	upstreamMCP := mcpserver.NewMCPServer("e2e-linear-upstream", "1.0.0")
	upstreamMCP.AddTool(
		mcp.NewTool(upstreamTool, mcp.WithString("id", mcp.Required())),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			upstreamCalls.Add(1)
			return mcp.NewToolResultText("issue:" + req.GetString("id", "")), nil
		},
	)
	upstreamHTTP := mcpserver.NewStreamableHTTPServer(upstreamMCP, mcpserver.WithStateLess(true))

	vaultGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+serviceToken || r.Header.Get("X-CapLayer-Actor") != "alice" {
			http.Error(w, "unauthorized gateway caller", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/admin/runtime/servers" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"servers": []any{
					map[string]any{
						"id":       connectorID,
						"label":    "Linear E2E",
						"provider": "linear",
						"tools": []any{
							map[string]any{
								"name":         upstreamTool,
								"input_schema": map[string]any{"type": "object"},
							},
						},
					},
				},
			})
			return
		}
		if r.URL.Path != "/api/admin/runtime/mcp" || r.Header.Get("X-Vault-Connector") != connectorID {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			var rpc struct {
				ID     any    `json:"id"`
				Method string `json:"method"`
			}
			_ = json.Unmarshal(body, &rpc)
			if rpc.Method == "tools/call" && grantRevoked.Load() {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc.ID,
					"error":   map[string]any{"code": -32000, "message": "permission denied: live grant revoked"},
				})
				return
			}
		}
		upstreamHTTP.ServeHTTP(w, r)
	}))
	defer vaultGateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", vaultGateway.URL)

	staleActivity := time.Now().Add(-20 * time.Minute)
	logger := loggerv2.NewNoop()
	eventStore := events.NewEventStore(20)
	eventStore.SetSessionOwner(sessionID, "alice")
	eventStore.SetSessionOwner(otherSession, "bob")

	api := &StreamingAPI{
		logger:     logger,
		eventStore: eventStore,
		activeSessions: map[string]*ActiveSessionInfo{
			sessionID: {SessionID: sessionID, Status: "running", LastActivity: staleActivity},
		},
	}

	// Register session-scoped virtual tools (search_tools, get_api_spec).
	codeexec.InitRegistry(nil, nil, nil, logger)
	codeexec.InitRegistryVirtualToolsForSession(virtualScope, map[string]func(context.Context, map[string]interface{}) (string, error){
		"search_tools": func(_ context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			return fmt.Sprintf(`{"tools":[{"name":%q,"server":%q}],"query":%q}`, upstreamTool, vaultServer, query), nil
		},
		"get_api_spec": func(_ context.Context, args map[string]interface{}) (string, error) {
			return fmt.Sprintf(`{"tool":%q,"route":"POST /tools/mcp/%s/%s"}`, args["tool_name"], vaultServer, upstreamTool), nil
		},
	}, logger)
	t.Cleanup(func() {
		codeexec.CleanupSession(virtualScope)
		mcpagent.CloseSession(sessionID)
	})

	executorHandlers := executor.NewExecutorHandlers("", logger)
	executorHandlers.SetMCPServerResolver(api.resolveMCPServer)

	router := mux.NewRouter()
	router.HandleFunc("/internal/vault/mcp", api.handleVaultRuntimeMCP).Methods(http.MethodPost, http.MethodGet, http.MethodDelete)
	toolsRouter := router.PathPrefix("/tools").Subrouter()
	toolsRouter.Use(bridgeAuthMiddleware("unused-global-token"))
	toolsRouter.Use(api.bridgeActivityKeepaliveMiddleware())
	toolsRouter.HandleFunc("/mcp/{server}/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		sid := strings.TrimSpace(r.Header.Get("X-Session-ID"))
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, sid)
		ctx, realServer, realTool, err := api.vaultBridgeToolName(ctx, sid, vars["server"], vars["tool"])
		if err != nil {
			writeUsersJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": err.Error()})
			return
		}
		executorHandlers.HandlePerToolMCPRequest(w, r.WithContext(ctx), realServer, realTool)
	}).Methods(http.MethodPost, http.MethodOptions)
	toolsRouter.HandleFunc("/virtual/{tool}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		executorHandlers.HandlePerToolVirtualRequest(w, r, vars["tool"])
	}).Methods(http.MethodPost, http.MethodOptions)
	toolsRouter.HandleFunc("/custom/{tool}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": "custom-ok"})
	}).Methods(http.MethodPost, http.MethodOptions)

	productServer := httptest.NewServer(router)
	defer productServer.Close()

	parsedURL, err := url.Parse(productServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(parsedURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	api.config.Host = host
	api.config.Port, _ = strconv.Atoi(portStr)

	// Ensure scoped Vault registration cleans up pooled connections after test.
	t.Cleanup(func() {
		mcpclient.GetSessionRegistry().CloseSession(sessionID)
	})

	sessionToken := common.BridgeTokenForSession(sessionID)
	readyFile := filepath.Join(t.TempDir(), ".mcpbridge-ready-e2e.marker")
	toolsSpec := []map[string]any{
		{
			"name":        "search_tools",
			"description": "Discover tools for this session.",
			"type":        "virtual",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
			},
		},
		{
			"name":        "get_api_spec",
			"description": "Inspect tool schema.",
			"type":        "virtual",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"tool_name": map[string]any{"type": "string"}},
			},
		},
		{
			"name":        "call_tool",
			"description": "Invoke a discovered tool.",
			"type":        "call",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"server_name": map[string]any{"type": "string"},
					"arguments":   map[string]any{"type": "object"},
				},
				"required": []string{"tool_name"},
			},
		},
	}
	toolsJSON, err := json.Marshal(toolsSpec)
	if err != nil {
		t.Fatal(err)
	}

	bridge, err := client.NewStdioMCPClient(buildTestMCPBridge(t), append(os.Environ(),
		"MCP_API_URL="+productServer.URL,
		"MCP_API_TOKEN="+sessionToken,
		"MCP_SESSION_ID="+sessionID,
		"MCP_VIRTUAL_SCOPE_ID="+virtualScope,
		"MCP_READY_FILE="+readyFile,
		"MCP_TOOLS_FILE="+writeMCPToolsFileForTest(t, toolsJSON),
	))
	if err != nil {
		t.Fatalf("start real mcpbridge: %v", err)
	}
	defer bridge.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "mcp-full-chain-e2e", Version: "1.0"},
	}}); err != nil {
		t.Fatalf("bridge.Initialize: %v", err)
	}

	// 1. tools/list succeeds and writes the 0600 MCP_READY_FILE marker.
	listed, err := bridge.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("bridge.ListTools: %v", err)
	}
	if len(listed.Tools) != 3 {
		t.Fatalf("bridge listed %d tools, want 3", len(listed.Tools))
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if info, statErr := os.Stat(readyFile); statErr == nil {
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Fatalf("MCP_READY_FILE mode = %o, want 0600", perm)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mcpbridge did not create MCP_READY_FILE %q after tools/list", readyFile)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// 2. Virtual tool discovery works both directly and through call_tool.
	searchReq := mcp.CallToolRequest{}
	searchReq.Params.Name = "search_tools"
	searchReq.Params.Arguments = map[string]any{"query": "linear"}
	searchRes, err := bridge.CallTool(ctx, searchReq)
	if err != nil || !strings.Contains(fmt.Sprint(searchRes.Content), upstreamTool) {
		t.Fatalf("direct search_tools failed: res=%+v err=%v", searchRes, err)
	}

	callVirtualReq := mcp.CallToolRequest{}
	callVirtualReq.Params.Name = "call_tool"
	callVirtualReq.Params.Arguments = map[string]any{
		"tool_name": "search_tools",
		"arguments": map[string]any{"query": "via-call-tool"},
	}
	callVirtualRes, err := bridge.CallTool(ctx, callVirtualReq)
	if err != nil || !strings.Contains(fmt.Sprint(callVirtualRes.Content), "via-call-tool") {
		t.Fatalf("call_tool -> virtual search_tools failed: res=%+v err=%v", callVirtualRes, err)
	}

	// 3. Governed Vault MCP call through call_tool reaches the real Streamable HTTP
	// upstream, updates session LastActivity, and never leaks serviceToken.
	callMCPReq := mcp.CallToolRequest{}
	callMCPReq.Params.Name = "call_tool"
	callMCPReq.Params.Arguments = map[string]any{
		"tool_name":   upstreamTool,
		"server_name": vaultServer,
		"arguments":   map[string]any{"id": allowedItemID},
	}
	mcpRes, err := bridge.CallTool(ctx, callMCPReq)
	if err != nil {
		t.Fatalf("call_tool -> Vault MCP failed: %v", err)
	}
	resText := fmt.Sprint(mcpRes.Content)
	if !strings.Contains(resText, "issue:"+allowedItemID) {
		t.Fatalf("unexpected upstream tool result: %s", resText)
	}
	if strings.Contains(resText, serviceToken) {
		t.Fatal("Vault service token leaked into tool result")
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", upstreamCalls.Load())
	}
	api.activeSessionsMux.RLock()
	updatedActivity := api.activeSessions[sessionID].LastActivity
	api.activeSessionsMux.RUnlock()
	if !updatedActivity.After(staleActivity) {
		t.Fatalf("bridge /tools/* call did not update session LastActivity (still %s)", updatedActivity)
	}

	// 4. Live grant revocation takes effect immediately on the warm pooled client.
	grantRevoked.Store(true)
	revokedRes, err := bridge.CallTool(ctx, callMCPReq)
	if err != nil {
		t.Fatalf("revoked bridge call transport error: %v", err)
	}
	revokedText := fmt.Sprint(revokedRes.Content)
	if !strings.Contains(strings.ToLower(revokedText), "revoked") && !strings.Contains(strings.ToLower(revokedText), "denied") {
		t.Fatalf("expected revocation denial in tool output, got: %s", revokedText)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("revoked call reached upstream tool handler: calls=%d", upstreamCalls.Load())
	}

	// 5. Ungranted connector is refused before upstream.
	foreignMCPReq := mcp.CallToolRequest{}
	foreignMCPReq.Params.Name = "call_tool"
	foreignMCPReq.Params.Arguments = map[string]any{
		"tool_name":   upstreamTool,
		"server_name": "vault_ungranted",
		"arguments":   map[string]any{"id": allowedItemID},
	}
	foreignRes, err := bridge.CallTool(ctx, foreignMCPReq)
	if err != nil {
		t.Fatalf("ungranted bridge call transport error: %v", err)
	}
	if !strings.Contains(strings.ToLower(fmt.Sprint(foreignRes.Content)), "error") {
		t.Fatalf("expected error for ungranted vault server, got: %+v", foreignRes.Content)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("ungranted server reached upstream: calls=%d", upstreamCalls.Load())
	}

	// 6. Security boundary checks against the live HTTP bridge server:
	// #secret-read token is rejected (401), and cross-session headers/scopes are rejected (403).
	secretReadToken := common.BridgeTokenForSession(sessionID + mcpagent.SecretReadTokenSuffix)
	httpPost := func(path, token, hdrSession, hdrScope string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, productServer.URL+path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if hdrSession != "" {
			req.Header.Set("X-Session-ID", hdrSession)
		}
		if hdrScope != "" {
			req.Header.Set("X-Virtual-Scope-ID", hdrScope)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for _, path := range []string{"/tools/mcp/" + vaultServer + "/" + upstreamTool, "/tools/virtual/search_tools", "/tools/custom/execute_shell_command"} {
		if code := httpPost(path, secretReadToken, sessionID, ""); code != http.StatusUnauthorized {
			t.Fatalf("#secret-read token on %s returned %d, want 401", path, code)
		}
	}
	if code := httpPost("/tools/custom/execute_shell_command", sessionToken, otherSession, ""); code != http.StatusForbidden {
		t.Fatalf("cross-session X-Session-ID returned %d, want 403", code)
	}
	if code := httpPost("/tools/virtual/search_tools", sessionToken, sessionID, otherSession+":vt:1"); code != http.StatusForbidden {
		t.Fatalf("cross-session X-Virtual-Scope-ID returned %d, want 403", code)
	}
}
