// Package e2e proves the M0 gateway path end to end: a real MCP client talks
// to the gateway over Streamable HTTP after signing in through the real
// OAuth flow; the gateway enforces grants and proxies to a (fake) upstream
// MCP server.
package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcpoauth"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

const testHumanToken = "e2e-human-token"

// fakeUpstream serves two tools: allowed_tool echoes, secret_tool must never run.
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := server.NewMCPServer("fake-upstream", "0.1.0")
	echo := mcp.Tool{Name: "allowed_tool", Description: "echoes input"}
	srv.AddTool(echo, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: fmt.Sprintf("echo:%v", req.Params.Arguments)}},
		}, nil
	})
	secret := mcp.Tool{Name: "secret_tool", Description: "must stay hidden"}
	srv.AddTool(secret, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("secret_tool ran through the gateway")
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "leaked"}},
		}, nil
	})
	h := server.NewStreamableHTTPServer(srv, server.WithEndpointPath("/mcp"), server.WithStateLess(true))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func dialGateway(t *testing.T, url, token string) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, err := client.NewStreamableHttpClient(url,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}),
	)
	if err != nil {
		t.Fatalf("gateway client: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("gateway start: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "e2e", Version: "0.1.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("gateway initialize: %v", err)
	}
	return c
}

func TestM0GovernedCallPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	upstreamSrv := fakeUpstream(t)
	up, err := upstream.Dial(ctx, upstreamSrv.URL+"/mcp")
	if err != nil {
		t.Fatalf("dial upstream: %v", err)
	}
	defer up.Close()

	human := mcpoauth.User{ID: "u1", Username: "e2e", Email: "e2e@example.com", Provider: "e2e"}
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "e2e"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "e2e@example.com"})
	st.AddConnector(store.Connector{
		ID: "c1", WorkspaceID: "w1", Provider: "fake",
		Label: "fake", UpstreamURL: upstreamSrv.URL + "/mcp", Status: store.StatusActive,
	})

	// The OAuth deployment origin must equal the served URL: listen first,
	// then build the gateway against it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	publicURL := "http://" + l.Addr().String()
	oauthSrv := mcpoauth.NewServer(mcpserver.OAuthConfig(publicURL,
		filepath.Join(t.TempDir(), "mcp-oauth.sqlite"), testHumanToken, human))
	gw := mcpserver.New(st, auth.OAuth{Server: oauthSrv, WorkspaceID: "w1"},
		map[string]*upstream.Client{"c1": up}, oauthSrv)
	if err := gw.SyncTools(ctx, "w1"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	st.AddGrant(store.Grant{UserID: "u1", PublicName: "fake__allowed_tool"})
	go http.Serve(l, gw.Handler()) //nolint:errcheck

	// Unauthenticated callers get 401 plus the OAuth challenge before any
	// MCP handling.
	resp, err := http.Get(publicURL + "/mcp")
	if err != nil {
		t.Fatalf("unauth probe: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth probe status = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "oauth-protected-resource") {
		t.Fatalf("unauth probe missing OAuth challenge: %v", resp.Header)
	}

	access := fetchAccessToken(t, publicURL, testHumanToken)
	c := dialGateway(t, publicURL+"/mcp", access)

	// tools/list shows only the granted tool.
	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != "fake__allowed_tool" {
		names := []string{}
		for _, tl := range list.Tools {
			names = append(names, tl.Name)
		}
		t.Fatalf("tools/list = %v, want [fake__allowed_tool]", names)
	}

	// Granted call succeeds through the upstream.
	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "fake__allowed_tool"
	callReq.Params.Arguments = map[string]any{"ping": "pong"}
	res, err := c.CallTool(ctx, callReq)
	if err != nil {
		t.Fatalf("granted tools/call: %v", err)
	}
	if res.IsError {
		t.Fatalf("granted tools/call returned error result: %+v", res.Content)
	}

	// Ungranted call is denied even though the tool exists upstream.
	denyReq := mcp.CallToolRequest{}
	denyReq.Params.Name = "fake__secret_tool"
	if _, err := c.CallTool(ctx, denyReq); err == nil {
		t.Fatalf("ungranted tools/call succeeded, want denial")
	}

	// Revocation takes effect on the very next call.
	st.RevokeGrant("u1", "fake__allowed_tool")
	if _, err := c.CallTool(ctx, callReq); err == nil {
		t.Fatalf("post-revocation tools/call succeeded, want denial")
	}

	// Audit trail records every attempt with decisions and outcomes.
	events := st.ListAudit()
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want 3", len(events))
	}
	want := []struct{ decision, outcome string }{
		{store.DecisionAllow, store.OutcomeOK},
		{store.DecisionDeny, store.OutcomeDenied},
		{store.DecisionDeny, store.OutcomeDenied},
	}
	for i, w := range want {
		if events[i].Decision != w.decision || events[i].Outcome != w.outcome {
			t.Fatalf("audit[%d] = %s/%s, want %s/%s",
				i, events[i].Decision, events[i].Outcome, w.decision, w.outcome)
		}
		if events[i].CallID == "" || events[i].UserID != "u1" || events[i].WorkspaceID != "w1" {
			t.Fatalf("audit[%d] missing correlation fields: %+v", i, events[i])
		}
	}
}
