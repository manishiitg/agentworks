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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Real MCP -> Crew dispatcher -> actor-bound Vault proxy -> upstream MCP.
// Cached connections cannot bypass a changed live grant or argument rule.
func TestExternalCrewConnectedMCP(t *testing.T) {
	t.Setenv("AUTH_SECRET", "crew-connected-mcp-test-signing-secret")
	withMCPConnectionsRoot(t)
	f, _ := newExternalCrewCommandFixture(t)
	f.write(t, externalCommandCrewRoot+"/workflow.json", `{"id":"team","created_by":"owner","capabilities":{"selected_servers":["vault_one"]}}`)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	credential := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", credential)
	var calls atomic.Int32
	var revoked atomic.Bool
	upstream := mcpserver.NewMCPServer("crew-connection-test", "1")
	upstream.AddTool(mcp.NewTool("fetch", mcp.WithString("id", mcp.Required())), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("saved id=" + req.GetString("id", "")), nil
	})
	transport := mcpserver.NewStreamableHTTPServer(upstream, mcpserver.WithStateLess(true))
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential || r.Header.Get("X-CapLayer-Actor") != "owner" {
			t.Error("caller identity was not preserved")
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/api/admin/runtime/servers" {
			json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": "one", "label": "Crew test", "provider": "example", "tools": []any{map[string]any{"name": "fetch", "input_schema": map[string]any{"type": "object"}}}}}})
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
			if rpc.Method == "tools/call" && (revoked.Load() || rpc.Params.Arguments["id"] != "allowed") {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32000, "message": "permission denied by live grant"}})
				return
			}
		}
		transport.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	f.api.logger = loggerv2.NewNoop()
	f.api.mcpConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(f.api.mcpConfigPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(f.api.handleVaultRuntimeMCP))
	defer proxy.Close()
	address, _ := url.Parse(proxy.URL)
	host, port, _ := net.SplitHostPort(address.Host)
	f.api.config.Host = host
	f.api.config.Port, _ = strconv.Atoi(port)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "mcp", "arguments": map[string]any{"crew_id": "team", "action": "call", "server": "vault_one", "tool": "fetch", "args": map[string]any{"id": "allowed"}}})
	requireRemoteSuccess(t, result, "live Crew MCP dispatch")
	raw, _ := json.Marshal(result)
	if calls.Load() != 1 || !strings.Contains(string(raw), "saved id=allowed") || strings.Contains(string(raw), credential) {
		t.Fatalf("wrong upstream result %s calls=%d", raw, calls.Load())
	}
	for _, args := range []map[string]any{{"server": "vault_one", "tool": "fetch", "args": map[string]any{"id": "denied"}}, {"server": "unattached-personal", "tool": "fetch", "args": map[string]any{"id": "allowed"}}} {
		crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "call", "server": args["server"], "tool": args["tool"], "args": args["args"]}, 400)
	}
	revoked.Store(true)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "mcp", map[string]any{"action": "call", "server": "vault_one", "tool": "fetch", "args": map[string]any{"id": "allowed"}}, 400)
	if calls.Load() != 1 {
		t.Fatal("denied or revoked call reached upstream")
	}
	crewCapabilityHTTP(t, f, crewWriter("other"), "mcp", map[string]any{"action": "call", "server": "vault_one", "tool": "fetch", "args": map[string]any{"id": "allowed"}}, 403)
}
