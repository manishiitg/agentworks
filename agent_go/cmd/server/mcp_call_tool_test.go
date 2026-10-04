package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Exercise the same executor, delegated proxy and MCP transport as production.
// The fake gateway enforces changing grants and argument restrictions; upstream
// must never see a denied request, even when the client connection is cached.
func TestCallMCPToolUsesSharedGovernedExecutor(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	credential := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", credential)
	var calls atomic.Int32
	var revoked atomic.Bool
	mcpServer := mcpserver.NewMCPServer("resource-lookup-test", "1")
	mcpServer.AddTool(mcp.NewTool("notion__fetch", mcp.WithString("id", mcp.Required())), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("Task list: canonical-id=" + req.GetString("id", "")), nil
	})
	transport := mcpserver.NewStreamableHTTPServer(mcpServer, mcpserver.WithStateLess(true))
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential || r.Header.Get("X-CapLayer-Actor") != "alice" {
			t.Error("gateway did not receive verified caller identity")
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/api/admin/runtime/servers" {
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": "one", "label": "Notion test", "provider": "notion", "tools": []any{map[string]any{"name": "notion__fetch", "input_schema": map[string]any{"type": "object"}}}}}})
			return
		}
		if r.URL.Path != "/api/admin/runtime/mcp" || r.Header.Get("X-Vault-Connector") != "one" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			var rpc struct {
				ID     any    `json:"id"`
				Method string `json:"method"`
				Params struct {
					Arguments map[string]any `json:"arguments"`
				} `json:"params"`
			}
			json.Unmarshal(body, &rpc)
			if rpc.Method == "tools/call" && (revoked.Load() || rpc.Params.Arguments["id"] != "allowed-task") {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "permission denied by live grant or argument restriction"}})
				return
			}
		}
		transport.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	api := &StreamingAPI{logger: loggerv2.NewNoop(), eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("vault-chat", "alice")
	product := httptest.NewServer(http.HandlerFunc(api.handleVaultRuntimeMCP))
	defer product.Close()
	address, _ := url.Parse(product.URL)
	host, port, _ := net.SplitHostPort(address.Host)
	api.config.Host = host
	api.config.Port, _ = strconv.Atoi(port)
	ctx := executor.WithSessionID(personContext("alice"), "vault-chat")
	resolved, err := api.resolveMCPServer(ctx, "vault-chat", "vault_one", "notion__fetch")
	if err != nil {
		t.Fatal(err)
	}
	defer mcpclient.GetSessionRegistry().CloseSession(resolved.ConnectionSessionID)
	args := func(id string) map[string]interface{} {
		return map[string]interface{}{"server": "vault_one", "tool": "notion__fetch", "arguments": map[string]interface{}{"id": id}}
	}
	result, err := api.callMCPTool(ctx, args("allowed-task"))
	if err != nil || !strings.Contains(result, "canonical-id=allowed-task") || calls.Load() != 1 {
		t.Fatalf("authorized lookup failed: %q %v calls=%d", result, err, calls.Load())
	}
	if strings.Contains(result, credential) {
		t.Fatal("credential reached tool response")
	}
	if _, err := api.callMCPTool(ctx, args("different-task")); err == nil || calls.Load() != 1 {
		t.Fatal("argument restriction was bypassed")
	}
	revoked.Store(true)
	if _, err := api.callMCPTool(ctx, args("allowed-task")); err == nil || calls.Load() != 1 {
		t.Fatal("cached connection bypassed revoked grant")
	}
	for _, badCtx := range []context.Context{context.Background(), personContext("alice"), executor.WithSessionID(personContext("bob"), "vault-chat")} {
		if _, err := api.callMCPTool(badCtx, args("allowed-task")); err == nil {
			t.Fatal("unverified session was admitted")
		}
	}
	foreign := args("allowed-task")
	foreign["server"] = "vault_other"
	if _, err := api.callMCPTool(ctx, foreign); err == nil {
		t.Fatal("ungranted connection was admitted")
	}
	if calls.Load() != 1 {
		t.Fatal("denied request reached upstream")
	}
}
