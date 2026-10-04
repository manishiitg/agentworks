package admin

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
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

func TestExternalPlatformMCPPermissionsAreLiveAndAuditedPerUser(t *testing.T) {
	var calls atomic.Int32
	origin := server.NewMCPServer("reference", "1")
	origin.AddTool(mcp.NewTool("read"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("ok"), nil
	})
	remote := httptest.NewServer(server.NewStreamableHTTPServer(origin, server.WithStateLess(true)))
	defer remote.Close()
	up, err := upstream.DialWithOptions(context.Background(), remote.URL+"/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	st.AddGroup(store.Group{ID: "readers", WorkspaceID: "w"})
	for _, id := range []string{"one", "two"} {
		st.AddConnector(store.Connector{ID: id, WorkspaceID: "w", Provider: id, Status: store.StatusActive})
	}
	gw := mcpserver.New(st, nil, map[string]*upstream.Client{"one": up, "two": up}, nil)
	if err := gw.SyncTools(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one__read", "two__read"} {
		snap, _ := st.GetTool(name)
		st.ApproveTool("w", name, snap.Fingerprint, snap.Version)
	}
	st.EnsurePlatformUser("w", "alice")
	st.AddMember("readers", "alice")
	st.AddGroupServerGrant("readers", "one")
	st.AddGroupServerGrant("readers", "two")
	a := &Admin{Store: st, Gateway: gw, WorkspaceID: "w", HumanToken: "service"}
	mux := http.NewServeMux()
	a.runtimeRoutes(mux)
	client := "mcp_client_" + strings.Repeat("a", 64)
	request := func(person, token, method, params string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/admin/runtime/external-mcp", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CapLayer-Actor", person)
		r.Header.Set("X-Vault-Platform-User", "1")
		r.Header.Set("X-Vault-OAuth-Client", client)
		r.Header.Set("X-Vault-Connector", "fake")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	list := request("alice", "service", "tools/list", `{}`)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "one__read") || !strings.Contains(list.Body.String(), "two__read") {
		t.Fatal("all permitted connectors were not listed", list.Code, list.Body.String())
	}
	list = request("bob", "service", "tools/list", `{}`)
	if list.Code != 200 || strings.Contains(list.Body.String(), "one__read") {
		t.Fatal("Bob inherited Alice's permissions")
	}
	if w := request("attacker", "wrong", "tools/list", `{}`); w.Code != 401 {
		t.Fatal("untrusted service accepted")
	}
	if _, found := st.GetUser("attacker"); found {
		t.Fatal("untrusted actor bound")
	}
	for _, name := range []string{"one__read", "two__read"} {
		if w := request("alice", "service", "tools/call", `{"name":"`+name+`","arguments":{}}`); !strings.Contains(w.Body.String(), "ok") {
			t.Fatal(w.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatal("authorized calls did not reach upstream")
	}
	st.RemoveGroupConnectorAccess("w", "readers", "one", "admin")
	if w := request("alice", "service", "tools/call", `{"name":"one__read","arguments":{}}`); !strings.Contains(w.Body.String(), "denied") || calls.Load() != 2 {
		t.Fatal("revoked grant still reached upstream")
	}
	if w := request("bob", "service", "tools/call", `{"name":"two__read","arguments":{}}`); !strings.Contains(w.Body.String(), "denied") || calls.Load() != 2 {
		t.Fatal("cross-user call reached upstream")
	}
	rows := st.ListAudit()
	if len(rows) != 4 {
		t.Fatal("missing audit events", len(rows))
	}
	for _, e := range rows {
		if e.ClientID != client || (e.UserID != "alice" && e.UserID != "bob") {
			t.Fatal("wrong audit identity")
		}
	}
}
