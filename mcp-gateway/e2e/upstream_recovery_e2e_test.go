package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/manishiitg/coding-agent-loop/mcpoauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestMCPGatewayE2EUpstreamSessionRecovery proves end to end through OAuth +
// gateway MCP server + upstream dialer that when a stateful upstream rejects a
// tools/call with HTTP 404 (session not found), the gateway evicts the dead
// upstream client and automatically re-initializes on the next call so tools
// recover without restarting the gateway or re-adding the connector.
func TestMCPGatewayE2EUpstreamSessionRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		failNextCall atomic.Bool
		listCount    atomic.Int32
		toolCalls    atomic.Int32
	)

	mcpSrv := server.NewMCPServer("stateful-upstream", "1.0.0")
	mcpSrv.AddTool(mcp.NewTool("ping", mcp.WithString("msg")), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		n := toolCalls.Add(1)
		return mcp.NewToolResultText(fmt.Sprintf("pong:%d:%s", n, req.GetString("msg", ""))), nil
	})
	streamable := server.NewStreamableHTTPServer(mcpSrv, server.WithEndpointPath("/mcp"))

	upstreamTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
			var rpc struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(bodyBytes, &rpc)
			if rpc.Method == "tools/list" {
				listCount.Add(1)
			} else if rpc.Method == "tools/call" && failNextCall.CompareAndSwap(true, false) {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
		}
		streamable.ServeHTTP(w, r)
	}))
	defer upstreamTS.Close()

	human := mcpoauth.User{ID: "u1", Username: "e2e", Email: "e2e@example.com", Provider: "e2e"}
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "e2e"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "e2e@example.com"})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	publicURL := "http://" + l.Addr().String()
	oauthSrv := mcpoauth.NewServer(mcpserver.OAuthConfig(publicURL,
		filepath.Join(t.TempDir(), "mcp-oauth.sqlite"), testHumanToken, human))
	gw := mcpserver.New(st, auth.OAuth{Server: oauthSrv, WorkspaceID: "w1"},
		map[string]*upstream.Client{}, oauthSrv, upstream.DialOptions{AllowPrivate: true})
	t.Cleanup(func() { gw.Close() })

	adm := &admin.Admin{Store: st, Gateway: gw, WorkspaceID: "w1"}
	connector, err := adm.AddConnectorCustom(ctx, "stateful", "Stateful Upstream", "", upstreamTS.URL+"/mcp")
	if err != nil {
		t.Fatalf("add connector: %v", err)
	}
	defer gw.RemoveConnector(connector.ID)
	st.AddGrant(store.Grant{UserID: "u1", PublicName: "stateful__ping"})

	go http.Serve(l, gw.Handler()) //nolint:errcheck

	access := fetchAccessToken(t, publicURL, testHumanToken)
	c := dialGateway(t, publicURL+"/mcp", access)

	callPing := func(msg string) (*mcp.CallToolResult, error) {
		req := mcp.CallToolRequest{}
		req.Params.Name = "stateful__ping"
		req.Params.Arguments = map[string]any{"msg": msg}
		return c.CallTool(ctx, req)
	}

	// 1. First call succeeds on the initial upstream session.
	res1, err := callPing("first")
	if err != nil || res1.IsError || !strings.Contains(fmt.Sprint(res1.Content), "pong:1:first") {
		t.Fatalf("first call failed: res=%+v err=%v", res1, err)
	}
	beforeEvictLists := listCount.Load()

	// 2. Upstream rejects tools/call with HTTP 404 (expired session): call fails
	// and gateway evicts the dead upstream client.
	failNextCall.Store(true)
	if res2, err := callPing("hits-expired"); err == nil && !res2.IsError {
		t.Fatalf("expected error on expired upstream session, got %+v", res2)
	}

	// 3. Subsequent call automatically re-dials/re-discovers upstream and succeeds cleanly.
	res3, err := callPing("recovered")
	if err != nil || res3.IsError || !strings.Contains(fmt.Sprint(res3.Content), "pong:2:recovered") {
		t.Fatalf("call after upstream session eviction failed: res=%+v err=%v", res3, err)
	}
	if got := listCount.Load(); got <= beforeEvictLists {
		t.Fatalf("expected gateway to reconnect and re-list upstream tools after 404 eviction, listCount=%d (before=%d)", got, beforeEvictLists)
	}
}
