package mcpserver

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProductMCPScopesIdentityConnectorAndRevocation(t *testing.T) {
	var calls atomic.Int32
	srv := server.NewMCPServer("upstream", "1")
	srv.AddTool(mcp.NewTool("read"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("ok"), nil
	})
	remote := httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithStateLess(true)))
	defer remote.Close()
	up, err := upstream.DialWithOptions(context.Background(), remote.URL+"/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	st.AddMember("g", "alice")
	for _, id := range []string{"one", "two"} {
		st.AddConnector(store.Connector{ID: id, WorkspaceID: "w", Provider: id, Status: store.StatusActive})
	}
	g := New(st, nil, map[string]*upstream.Client{"one": up, "two": up}, nil)
	if err := g.SyncTools(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one__read", "two__read"} {
		snap, _ := st.GetTool(name)
		st.ApproveTool("w", name, snap.Fingerprint, snap.Version)
	}
	st.AddGroupServerGrant("g", "one")
	st.AddGroupServerGrant("g", "two")
	handler := g.ProductHandler(func(r *http.Request) (auth.Identity, string, bool) {
		return auth.Identity{UserID: r.Header.Get("Actor"), WorkspaceID: "w", ClientID: "agentworks"}, r.Header.Get("Connector"), r.Header.Get("Authorization") == "Bearer service" && r.Header.Get("Origin") == ""
	})
	request := func(actor, connector, method, params string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/admin/runtime/mcp", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params)))
		r.Header.Set("Authorization", "Bearer service")
		r.Header.Set("Actor", actor)
		r.Header.Set("Connector", connector)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	list := request("alice", "one", "tools/list", `{}`)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "one__read") || strings.Contains(list.Body.String(), "two__read") {
		t.Fatalf("list did not restrict connector: %d %s", list.Code, list.Body.String())
	}
	if got := request("bob", "one", "tools/list", `{}`); strings.Contains(got.Body.String(), "one__read") {
		t.Fatal("Bob inherited Alice's grant")
	}
	if got := request("alice", "one", "tools/call", `{"name":"two__read","arguments":{}}`); calls.Load() != 0 || !strings.Contains(got.Body.String(), "denied") {
		t.Fatalf("cross connector call reached upstream: %s", got.Body.String())
	}
	got := request("alice", "one", "tools/call", `{"name":"one__read","arguments":{}}`)
	if calls.Load() != 1 || !strings.Contains(got.Body.String(), "ok") {
		t.Fatalf("authorized call failed: %s", got.Body.String())
	}
	st.RemoveGroupConnectorAccess("w", "g", "one", "admin")
	got = request("alice", "one", "tools/call", `{"name":"one__read","arguments":{}}`)
	if calls.Load() != 1 || !strings.Contains(got.Body.String(), "denied") {
		t.Fatal("warm connection survived revocation")
	}
	// Disabling audit collection must leave the runtime permission checks intact.
	st.AddGroupServerGrant("g", "one")
	before := st.AuditCount()
	off, err := store.OpenAuditBackend(store.AuditOptions{Provider: "off"})
	if err != nil {
		t.Fatal(err)
	}
	st.SetAuditBackend(off)
	got = request("alice", "one", "tools/call", `{"name":"one__read","arguments":{}}`)
	if calls.Load() != 2 || !strings.Contains(got.Body.String(), "ok") || st.AuditCount() != before {
		t.Fatal("off affected calls or collected events")
	}
	if got = request("bob", "one", "tools/call", `{"name":"one__read","arguments":{}}`); calls.Load() != 2 || !strings.Contains(got.Body.String(), "denied") {
		t.Fatal("off bypassed permission checks")
	}
	st.SetAuditBackend(brokenAudit{})
	got = request("alice", "one", "tools/call", `{"name":"one__read","arguments":{}}`)
	if calls.Load() != 3 || !strings.Contains(got.Body.String(), "audit persistence failed") || !strings.Contains(got.Body.String(), "do not retry automatically") {
		t.Fatal("completed upstream action hid audit failure")
	}
}
