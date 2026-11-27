package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
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

func TestVaultBuilderCallsUngroupedToolsButRetainsOperationalChecksAndAudit(t *testing.T) {
	var calls atomic.Int32
	srv := server.NewMCPServer("upstream", "1")
	srv.AddTool(mcp.NewTool("fetch", mcp.WithString("id", mcp.Required())), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("found"), nil
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
	st.AddUser(store.User{ID: "admin", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "empty", WorkspaceID: "w"})
	st.AddConnector(store.Connector{ID: "one", WorkspaceID: "w", Provider: "one", Status: store.StatusActive})
	g := New(st, nil, map[string]*upstream.Client{"one": up}, nil)
	if err := g.SyncTools(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	snap, _ := st.GetTool("one__fetch")
	draft, _ := st.SavePackageDraft(access.Package{ID: "restriction", WorkspaceID: "w", GroupID: "empty", Rules: []access.ToolRule{{PublicName: snap.PublicName, Fingerprint: snap.Fingerprint, Conditions: []access.Condition{{Path: "/id", Op: "equals", Value: "group-only-id"}}}}}, 0)
	if _, ok := st.PublishPackage("w", draft.ID, draft.Version); !ok {
		t.Fatal("publish failed")
	}
	identity := func(r *http.Request) (auth.Identity, string, bool) {
		return auth.Identity{UserID: "admin", WorkspaceID: "w", ClientID: "agentworks-vault-builder"}, "one", r.Header.Get("Authorization") == "Bearer service"
	}
	builder := g.BuilderProductHandler(identity)
	normal := g.ProductHandler(identity)
	request := func(handler http.Handler, path, method, params string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params)))
		req.Header.Set("Authorization", "Bearer service")
		req.Header.Set("X-Vault-Builder", "1")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	path := "/api/admin/runtime/builder/mcp"
	if w := request(builder, path, "tools/list", `{}`); !strings.Contains(w.Body.String(), snap.PublicName) {
		t.Fatal("builder still group scoped", w.Body.String())
	}
	params := `{"name":"one__fetch","arguments":{"id":"outside-group"}}`
	if w := request(normal, "/api/admin/runtime/mcp", "tools/call", params); !strings.Contains(w.Body.String(), "denied") || calls.Load() != 0 {
		t.Fatal("header/client name elevated normal call", w.Body.String())
	}
	if w := request(builder, path, "tools/call", params); !strings.Contains(w.Body.String(), "found") || calls.Load() != 1 {
		t.Fatal("builder could not query outside group restriction", w.Body.String())
	}
	logs := st.ListAudit()
	if len(logs) != 2 || logs[1].UserID != "admin" || logs[1].ClientID != "agentworks-vault-builder" || logs[1].Outcome != store.OutcomeOK {
		t.Fatal("builder audit missing", logs)
	}
	if w := request(builder, path, "tools/call", `{"name":"one__fetch","arguments":{"id":42}}`); !strings.Contains(w.Body.String(), "schema") || calls.Load() != 1 {
		t.Fatal("schema bypassed", w.Body.String())
	}
	// A changed definition is unavailable until explicitly approved again.
	st.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "one", PublicName: snap.PublicName, UpstreamName: "fetch", Fingerprint: "changed", InputSchema: snap.InputSchema})
	if w := request(builder, path, "tools/call", params); !strings.Contains(w.Body.String(), "denied") || calls.Load() != 1 {
		t.Fatal("unapproved fingerprint called", w.Body.String())
	}
	st.AddConnector(store.Connector{ID: "one", WorkspaceID: "w", Provider: "one", Status: store.StatusDisabled})
	if w := request(builder, path, "tools/call", params); !strings.Contains(w.Body.String(), "denied") || calls.Load() != 1 {
		t.Fatal("disabled connection called", w.Body.String())
	}
	if len(st.GroupsOf("admin")) != 0 {
		t.Fatal("builder added itself to a group")
	}

	// Builder setup authority must never list or call another user's personal vault connector.
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w"})
	bobVault, err := st.EnsurePersonalVault("w", "bob", "v-bob")
	if err != nil {
		t.Fatal(err)
	}
	st.AddConnector(store.Connector{ID: "bob-private", WorkspaceID: "w", VaultID: bobVault, Provider: "bobprivate", Status: store.StatusActive})
	g.upstreams["bob-private"] = up
	if err := g.SyncTools(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	bobBuilder := g.BuilderProductHandler(func(r *http.Request) (auth.Identity, string, bool) {
		return auth.Identity{UserID: "admin", WorkspaceID: "w", ClientID: "agentworks-vault-builder"}, "bob-private", r.Header.Get("Authorization") == "Bearer service"
	})
	if w := request(bobBuilder, path, "tools/list", `{}`); strings.Contains(w.Body.String(), "bobprivate__fetch") {
		t.Fatalf("builder listed another user's private vault tool: %s", w.Body.String())
	}
	if w := request(bobBuilder, path, "tools/call", `{"name":"bobprivate__fetch","arguments":{"id":"1"}}`); !strings.Contains(w.Body.String(), "denied") || calls.Load() != 1 {
		t.Fatalf("builder called another user's private vault tool: %s calls=%d", w.Body.String(), calls.Load())
	}
}

func TestRestoredToolsRecoverAfterOAuthStartupFailure(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			var calls, resolutions atomic.Int32
			var available atomic.Bool
			available.Store(true)
			srv := server.NewMCPServer("upstream", "1")
			install := func(description string) {
				srv.AddTool(mcp.NewTool("fetch", mcp.WithDescription(description), mcp.WithString("id", mcp.Required())), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					calls.Add(1)
					return mcp.NewToolResultText("canonical-page-id"), nil
				})
			}
			install("original")
			remote := httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithStateLess(true)))
			defer remote.Close()
			st := store.NewMemoryStore()
			st.AddWorkspace(store.Workspace{ID: "w"})
			st.AddUser(store.User{ID: "admin", WorkspaceID: "w"})
			st.AddUser(store.User{ID: "ungranted", WorkspaceID: "w"})
			c := store.Connector{ID: "one", WorkspaceID: "w", Provider: "one", Status: store.StatusActive, UpstreamURL: remote.URL + "/mcp", OAuthServer: "test", OAuthCredentialID: "one"}
			st.AddConnector(c)
			newGateway := func() *Gateway {
				g := New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
				g.SetSharedOAuth(func(context.Context, string, string, string) (string, error) {
					resolutions.Add(1)
					if !available.Load() {
						return "", fmt.Errorf("product is starting")
					}
					return "test-token", nil
				})
				return g
			}
			original := newGateway()
			if err := original.Resync(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			up, _ := original.upstreamFor(c.ID)
			up.Close()
			// New process, persisted tools, and a temporarily unavailable broker.
			available.Store(false)
			g := newGateway()
			g.RestoreTools("w")
			if err := g.Resync(context.Background(), c); err == nil {
				t.Fatal("expected startup OAuth failure")
			}
			identity := func(r *http.Request) (auth.Identity, string, bool) {
				return auth.Identity{UserID: r.Header.Get("Actor"), WorkspaceID: "w", ClientID: "test"}, "one", true
			}
			request := func(handler http.Handler, actor, path, method, params string) string {
				req := httptest.NewRequest("POST", path, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, method, params)))
				req.Header.Set("Actor", actor)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				return w.Body.String()
			}
			builder := g.BuilderProductHandler(identity)
			normal := g.ProductHandler(identity)
			path := "/api/admin/runtime/builder/mcp"
			if got := request(builder, "admin", path, "tools/list", `{}`); !strings.Contains(got, "one__fetch") {
				t.Fatal("persisted tool missing from transport", got)
			}
			params := `{"name":"one__fetch","arguments":{"id":"page"}}`
			before := resolutions.Load()
			if got := request(normal, "ungranted", "/api/admin/runtime/mcp", "tools/call", params); !strings.Contains(got, "denied") || resolutions.Load() != before {
				t.Fatal("denied caller attempted reconnect", got)
			}
			if got := request(builder, "admin", path, "tools/call", params); !strings.Contains(got, "upstream connection unavailable") || strings.Contains(got, "tool not found") || calls.Load() != 0 {
				t.Fatal("failed recovery hid cause or executed tool", got)
			}
			available.Store(true)
			if changed {
				install("changed definition")
			}
			got := request(builder, "admin", path, "tools/call", params)
			if changed {
				if !strings.Contains(got, "denied") || calls.Load() != 0 {
					t.Fatal("reconnect bypassed fingerprint review", got)
				}
			} else if !strings.Contains(got, "canonical-page-id") || calls.Load() != 1 {
				t.Fatal("did not recover on next call", got)
			}
			if connected, ok := g.upstreamFor(c.ID); ok {
				connected.Close()
			}
			if len(st.GroupsOf("admin")) != 0 || len(st.ListAudit()) != 3 {
				t.Fatal("recovery changed membership or skipped audit")
			}
		})
	}
}

func TestGatewayEvictsDeadUpstreamReconnectsAndSurfacesErrors(t *testing.T) {
	var failNextCall atomic.Bool
	var listCount atomic.Int32
	var calls atomic.Int32

	srv := server.NewMCPServer("stateful-upstream", "1")
	srv.AddTool(mcp.NewTool("fetch", mcp.WithString("id", mcp.Required())), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("ok"), nil
	})
	streamable := server.NewStreamableHTTPServer(srv, server.WithEndpointPath("/mcp"))
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		streamable.ServeHTTP(w, r)
	}))
	defer remote.Close()

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	st.AddMember("g", "alice")
	c := store.Connector{ID: "one", WorkspaceID: "w", Provider: "one", Status: store.StatusActive, UpstreamURL: remote.URL + "/mcp"}
	st.AddConnector(c)
	st.AddGroupServerGrant("g", "one")

	g := New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	defer g.Close()
	if err := g.Resync(context.Background(), c); err != nil {
		t.Fatalf("initial resync: %v", err)
	}
	if listCount.Load() != 1 {
		t.Fatalf("initial listCount = %d, want 1", listCount.Load())
	}

	handler := g.ProductHandler(func(r *http.Request) (auth.Identity, string, bool) {
		return auth.Identity{UserID: "alice", WorkspaceID: "w", ClientID: "test"}, "one", true
	})
	call := func(params string) string {
		req := httptest.NewRequest("POST", "/api/admin/runtime/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+`}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Body.String()
	}

	// 1. Schema validation failure surfaces actionable field detail.
	if got := call(`{"name":"one__fetch","arguments":{"id":42}}`); !strings.Contains(got, "arguments do not match approved tool schema:") || !strings.Contains(got, "id") {
		t.Fatalf("schema error hid field details: %s", got)
	}

	// 2. Normal call succeeds.
	if got := call(`{"name":"one__fetch","arguments":{"id":"a"}}`); !strings.Contains(got, "ok") || calls.Load() != 1 {
		t.Fatalf("initial call failed: %s", got)
	}

	// 3. Expire the upstream session (404): call fails with actionable detail and evicts the dead client,
	//    then the very next call transparently reconnects (listCount=2) and succeeds.
	failNextCall.Store(true)
	if got := call(`{"name":"one__fetch","arguments":{"id":"b"}}`); !strings.Contains(got, "upstream call failed:") || (!strings.Contains(got, "404") && !strings.Contains(got, "expired") && !strings.Contains(got, "session not found")) {
		t.Fatalf("dead-session error hid cause: %s", got)
	}
	if _, stillCached := g.upstreamFor("one"); stillCached {
		t.Fatal("dead upstream session was not evicted after 404")
	}
	if got := call(`{"name":"one__fetch","arguments":{"id":"b"}}`); !strings.Contains(got, "ok") || calls.Load() != 2 || listCount.Load() != 2 {
		t.Fatalf("next call did not reconnect after eviction: got=%s calls=%d lists=%d", got, calls.Load(), listCount.Load())
	}

	// 4. Non-OAuth Resync also evicts and redials when the cached session dies.
	upBefore, ok := g.upstreamFor("one")
	if !ok {
		t.Fatal("expected connected upstream before resync test")
	}
	_ = upBefore.Close()
	if err := g.Resync(context.Background(), c); err != nil {
		t.Fatalf("non-OAuth Resync failed to redial after dead session: %v", err)
	}
	if got := call(`{"name":"one__fetch","arguments":{"id":"c"}}`); !strings.Contains(got, "ok") || calls.Load() != 3 {
		t.Fatalf("call after Resync redial failed: %s", got)
	}

	// 5. Gateway.Close shuts down all active upstreams and is idempotent.
	if err := g.Close(); err != nil {
		t.Fatalf("Gateway.Close: %v", err)
	}
	if _, ok := g.upstreamFor("one"); ok {
		t.Fatal("Gateway.Close left upstream registered")
	}
	if err := g.Close(); err != nil {
		t.Fatalf("second Gateway.Close: %v", err)
	}
}
