package mcpclient

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/mcpagent/oauth"
)

type stubPooledClient struct {
	ClientInterface
	closed atomic.Int32
}

func (s *stubPooledClient) Close() error {
	s.closed.Add(1)
	return nil
}

func (s *stubPooledClient) Ping(context.Context) error { return nil }

func TestResolveConnectionSessionID(t *testing.T) {
	registry := &SessionConnectionRegistry{
		connLocks: make(map[string]*sync.Mutex),
	}

	if got := registry.ResolveConnectionSessionID("tool-session", "github"); got != "global" {
		t.Fatalf("MCP servers should use the global connection session, got %q", got)
	}
}

func TestServerConfigPoolKeyIsolatesUsersAndOverrides(t *testing.T) {
	aliceOAuth := MCPServerConfig{URL: "https://mcp.example.com/mcp", Protocol: ProtocolHTTP, OAuth: &oauth.OAuthConfig{TokenFile: "/tokens/alice/linear.json"}}
	bobOAuth := MCPServerConfig{URL: "https://mcp.example.com/mcp", Protocol: ProtocolHTTP, OAuth: &oauth.OAuthConfig{TokenFile: "/tokens/bob/linear.json"}}
	if serverConfigPoolKey("linear", aliceOAuth) == serverConfigPoolKey("linear", bobOAuth) {
		t.Fatal("different users' OAuth TokenFile produced the same pool key")
	}

	wfA := MCPServerConfig{Command: "npx", Args: []string{"server"}, WorkingDir: "/work/a", Env: map[string]string{"KEY": "1"}}
	wfB := MCPServerConfig{Command: "npx", Args: []string{"server"}, WorkingDir: "/work/b", Env: map[string]string{"KEY": "1"}}
	wfC := MCPServerConfig{Command: "npx", Args: []string{"server"}, WorkingDir: "/work/a", Env: map[string]string{"KEY": "2"}}
	if serverConfigPoolKey("stdio", wfA) == serverConfigPoolKey("stdio", wfB) {
		t.Fatal("different WorkingDir produced the same pool key")
	}
	if serverConfigPoolKey("stdio", wfA) == serverConfigPoolKey("stdio", wfC) {
		t.Fatal("different Env produced the same pool key")
	}

	// Vault __scope_ names already encode their authorization hash and stay verbatim.
	scoped := "vault_c1__scope_0123456789abcdef0123456789abcdef"
	if got := serverConfigPoolKey(scoped, aliceOAuth); got != scoped {
		t.Fatalf("scoped server key changed: got %q, want %q", got, scoped)
	}

	// CloseSessionServer must close every config-fingerprinted client for that server.
	registry := &SessionConnectionRegistry{connLocks: make(map[string]*sync.Mutex)}
	c1, c2 := &stubPooledClient{}, &stubPooledClient{}
	registry.StoreConnection("global", serverConfigPoolKey("linear", aliceOAuth), c1)
	registry.StoreConnection("global", serverConfigPoolKey("linear", bobOAuth), c2)
	registry.CloseSessionServer("global", "linear")
	if c1.closed.Load() != 1 || c2.closed.Load() != 1 {
		t.Fatalf("CloseSessionServer did not close all config variants: c1=%d c2=%d", c1.closed.Load(), c2.closed.Load())
	}
}

func TestHTTPSessionForMCPSessionTracksOnlyLiveRelationship(t *testing.T) {
	registry := GetSessionRegistry()
	httpSessionID := "reverse-http-session-test"
	mcpSessionID := "reverse-mcp-session-test"
	registry.RegisterHTTPSession(httpSessionID, mcpSessionID)
	if got := registry.HTTPSessionForMCPSession(mcpSessionID); got != httpSessionID {
		t.Fatalf("HTTPSessionForMCPSession() = %q, want %q", got, httpSessionID)
	}
	registry.CloseHTTPSession(httpSessionID)
	if got := registry.HTTPSessionForMCPSession(mcpSessionID); got != "" {
		t.Fatalf("closed relationship still resolves to %q", got)
	}
}
