package mcpagent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/mark3labs/mcp-go/mcp"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

type scopeCloseClient struct {
	mcpclient.ClientInterface
	closed  atomic.Int32
	started chan struct{}
	stopped chan struct{}
}

func (c *scopeCloseClient) Close() error {
	if c.closed.Add(1) == 1 && c.stopped != nil {
		close(c.stopped)
	}
	return nil
}
func (c *scopeCloseClient) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{mcp.NewTool("work")}, nil
}

func (c *scopeCloseClient) CallTool(ctx context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	close(c.started)
	select {
	case <-c.stopped:
		return nil, errors.New("scoped MCP call cancelled on close")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PLAT-746-876: two live agents may share a delegation key, but the last
// Agent/Session close must drop its client and pending lazy configs. Ordinary
// global clients and another delegation's authorization scope must survive.
func TestScopedMCPClientsReleasedAfterLastAgentOrSessionCloses(t *testing.T) {
	baseKeys, baseClients := codeexec.ScopedMCPCounts()
	key := "vault_example__scope_" + strings.Repeat("a", 32)
	otherKey := "vault_example__scope_" + strings.Repeat("b", 32)
	first, second, other := &Agent{}, &Agent{}, &Agent{}
	first.retainScopedMCPClient(key)
	second.retainScopedMCPClient(key)
	other.retainScopedMCPClient(otherKey)
	if keys, clients := codeexec.ScopedMCPCounts(); keys != baseKeys+2 || clients != baseClients {
		t.Fatalf("lazy configs counted as clients: keys=%d clients=%d", keys, clients)
	}
	defer first.Close()
	defer second.Close()
	defer other.Close()
	scoped, ordinary, otherClient := &scopeCloseClient{started: make(chan struct{}), stopped: make(chan struct{})}, &scopeCloseClient{}, &scopeCloseClient{}
	pool := mcpclient.GetSessionRegistry()
	pool.StoreConnection("global", key, scoped)
	pool.StoreConnection("global", otherKey, otherClient)
	pool.StoreConnection("global", "ordinary-scope-test", ordinary)
	defer pool.CloseSessionServer("global", "ordinary-scope-test")
	pool.StoreServerConfig("lazy-scope-test", key, mcpclient.MCPServerConfig{URL: "https://example.com"})
	codeexec.InitRegistry(map[string]mcpclient.ClientInterface{key: scoped, otherKey: otherClient, "ordinary-scope-test": ordinary}, nil, map[string]string{"example_tool": key}, loggerv2.NewNoop())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if scoped.closed.Load() != 0 || codeexec.MCPClientForServer(key) == nil {
		t.Fatal("first close removed another live agent's client")
	}
	// Exercise the real executor request while the last owner closes. It must
	// retain its local client reference and report an ordinary call error.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		h := executor.NewExecutorHandlers("missing-config", loggerv2.NewNoop())
		raw, _ := json.Marshal(map[string]any{"session_id": "scope-in-flight", "server": key, "tool": "work", "args": map[string]any{}})
		r := httptest.NewRequest("POST", "/api/mcp/execute", strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		h.HandleMCPExecute(w, r)
		done <- w
	}()
	select {
	case <-scoped.started:
	case <-time.After(5 * time.Second):
		t.Fatal("executor never entered scoped call")
	}
	session := &Session{agent: second}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		var result executor.MCPExecuteResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Success || result.Error == "" {
			t.Fatalf("in-flight close did not return an ordinary error: status=%d", w.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight call did not settle after close")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if scoped.closed.Load() != 1 || codeexec.MCPClientForServer(key) != nil || pool.GetSessionConnections("global")[key] != nil {
		t.Fatal("last close retained scoped client or closed twice")
	}
	if _, exists := pool.GetServerConfig("lazy-scope-test", key); exists {
		t.Fatal("lazy authorization config retained")
	}
	if otherClient.closed.Load() != 0 || ordinary.closed.Load() != 0 {
		t.Fatal("cleanup crossed delegation/global boundary")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if otherClient.closed.Load() != 1 {
		t.Fatal("second delegation retained after close")
	}
	// An old delayed close cannot remove a replacement for the same key.
	replacement := &Agent{}
	replacement.retainScopedMCPClient(key)
	defer replacement.Close()
	live := &scopeCloseClient{}
	pool.StoreConnection("global", key, live)
	codeexec.InitRegistry(map[string]mcpclient.ClientInterface{key: live}, nil, nil, loggerv2.NewNoop())
	_ = first.Close()
	_ = second.Close()
	if live.closed.Load() != 0 || codeexec.MCPClientForServer(key) == nil {
		t.Fatal("old close removed replacement")
	}
	_ = replacement.Close()
	if keys, clients := codeexec.ScopedMCPCounts(); keys != baseKeys || clients != baseClients {
		t.Fatalf("released scopes still counted: keys=%d clients=%d", keys, clients)
	}
	if live.closed.Load() != 1 {
		t.Fatal("replacement leaked")
	}
}
