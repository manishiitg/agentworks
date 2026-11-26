package mcpclient

import (
	"sync"
	"testing"
)

func TestResolveConnectionSessionID(t *testing.T) {
	registry := &SessionConnectionRegistry{
		connLocks: make(map[string]*sync.Mutex),
	}

	if got := registry.ResolveConnectionSessionID("tool-session", "github"); got != "global" {
		t.Fatalf("MCP servers should use the global connection session, got %q", got)
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
