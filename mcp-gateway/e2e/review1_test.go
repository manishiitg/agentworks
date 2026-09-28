// Review-1 regressions: unique connector namespaces, resync reconciliation
// of removed tools, and full-schema snapshots with correct change detection.
package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

// mutableUpstream serves tools the test can change between resyncs.
func mutableUpstream(t *testing.T) (*httptest.Server, *server.MCPServer) {
	t.Helper()
	srv := server.NewMCPServer("mutable-upstream", "0.1.0")
	h := server.NewStreamableHTTPServer(srv, server.WithEndpointPath("/mcp"), server.WithStateLess(true))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, srv
}

func schemaTool(name, schema string) mcp.Tool {
	return mcp.Tool{
		Name:           name,
		Description:    name + " tool",
		RawInputSchema: json.RawMessage(schema),
	}
}

func echoHandler() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}},
		}, nil
	}
}

// TestDuplicateConnectorNamespaceRejected: two connectors sharing a public-name
// prefix would collide in the name-keyed registry, so the second add fails.
func TestDuplicateConnectorNamespaceRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	upstreamSrv := fakeUpstream(t)
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "ns"})
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	adm := &admin.Admin{Store: st, Gateway: gw, WorkspaceID: "w1"}

	first, err := adm.AddConnectorCustom(ctx, "Fake", "first", "", upstreamSrv.URL+"/mcp")
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if got := len(st.ListConnectors("w1")); got != 1 {
		t.Fatalf("connectors after first add = %d, want 1", got)
	}

	if _, err := adm.AddConnectorCustom(ctx, "Fake", "second", "", upstreamSrv.URL+"/mcp"); err == nil {
		t.Fatalf("duplicate provider add succeeded, want rejection")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate provider error = %q, want namespace complaint", err)
	}

	// Same provider with a distinct slug is a distinct namespace and succeeds.
	second, err := adm.AddConnectorCustom(ctx, "Fake", "acme", "acme", upstreamSrv.URL+"/mcp")
	if err != nil {
		t.Fatalf("slugged add: %v", err)
	}
	if _, err := adm.AddConnectorCustom(ctx, "Fake", "acme-again", "acme", upstreamSrv.URL+"/mcp"); err == nil {
		t.Fatalf("duplicate slug add succeeded, want rejection")
	}

	// The first connector's tools keep their names; nothing was overwritten.
	if _, ok := st.GetTool("fake__allowed_tool"); !ok {
		t.Fatalf("first connector tool missing after duplicate attempts")
	}
	_ = first
	_ = second
}

// TestResyncDisablesRemovedTools: a tool the upstream stops listing is
// disabled (hidden and denied) but keeps its grants; returning unchanged
// revives it.
func TestResyncDisablesRemovedTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts, srv := mutableUpstream(t)
	srv.AddTool(schemaTool("keep_tool", `{"type":"object"}`), echoHandler())
	srv.AddTool(schemaTool("drop_tool", `{"type":"object"}`), echoHandler())

	up, err := upstream.DialWithOptions(ctx, ts.URL+"/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatalf("dial upstream: %v", err)
	}
	defer up.Close()

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "reconcile"})
	c := store.Connector{ID: "c1", WorkspaceID: "w1", Provider: "mut", Label: "mut", UpstreamURL: ts.URL + "/mcp", Status: store.StatusActive}
	st.AddConnector(c)
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1"})
	st.AddGrant(store.Grant{UserID: "u1", PublicName: "mut__drop_tool"})
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{"c1": up}, nil)

	if err := gw.SyncTools(ctx, "w1"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	for _, name := range []string{"mut__drop_tool", "mut__keep_tool"} {
		snap, ok := st.GetTool(name)
		if !ok || snap.Status != store.StatusQuarantined {
			t.Fatalf("new tool %s = %+v, want quarantined", name, snap)
		}
		if _, approved := st.ApproveTool("w1", name, snap.Fingerprint, snap.Version); !approved {
			t.Fatalf("could not approve %s", name)
		}
	}
	id := auth.Identity{UserID: "u1", WorkspaceID: "w1"}
	if _, err := policy.Authorize(st, id, "mut__drop_tool"); err != nil {
		t.Fatalf("authorize before removal: %v", err)
	}

	srv.DeleteTools("drop_tool")
	if err := gw.Resync(ctx, c); err != nil {
		t.Fatalf("resync: %v", err)
	}
	snap, ok := st.GetTool("mut__drop_tool")
	if !ok {
		t.Fatalf("dropped tool snapshot missing, want a disabled tombstone")
	}
	if snap.Status != store.StatusDisabled {
		t.Fatalf("dropped tool status = %q, want disabled", snap.Status)
	}
	if _, err := policy.Authorize(st, id, "mut__drop_tool"); err != policy.ErrToolNotActive {
		t.Fatalf("authorize after removal = %v, want ErrToolNotActive", err)
	}
	if policy.Visible(st, id, "mut__drop_tool") {
		t.Fatalf("dropped tool still visible in tools/list")
	}
	if !st.HasGrant("u1", "mut__drop_tool") {
		t.Fatalf("grant for dropped tool was deleted, want it kept")
	}
	// The surviving tool is untouched.
	if keep, _ := st.GetTool("mut__keep_tool"); keep.Status != store.StatusActive {
		t.Fatalf("surviving tool status = %q, want active", keep.Status)
	}

	// Returning unchanged revives the tool with its grant intact.
	srv.AddTool(schemaTool("drop_tool", `{"type":"object"}`), echoHandler())
	if err := gw.Resync(ctx, c); err != nil {
		t.Fatalf("resync after return: %v", err)
	}
	if snap, _ := st.GetTool("mut__drop_tool"); snap.Status != store.StatusActive {
		t.Fatalf("returned tool status = %q, want active", snap.Status)
	}
	if _, err := policy.Authorize(st, id, "mut__drop_tool"); err != nil {
		t.Fatalf("authorize after return: %v", err)
	}
}

// TestSchemaChangeQuarantinesTool: the snapshot stores the full normalized
// input schema, and a property-level change quarantines the tool while an
// unchanged resync keeps it stable.
func TestSchemaChangeQuarantinesTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ts, srv := mutableUpstream(t)
	srv.AddTool(schemaTool("shaped_tool", `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`), echoHandler())

	up, err := upstream.DialWithOptions(ctx, ts.URL+"/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatalf("dial upstream: %v", err)
	}
	defer up.Close()

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "schema"})
	c := store.Connector{ID: "c1", WorkspaceID: "w1", Provider: "mut", Label: "mut", UpstreamURL: ts.URL + "/mcp", Status: store.StatusActive}
	st.AddConnector(c)
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{"c1": up}, nil)

	if err := gw.SyncTools(ctx, "w1"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	snap, ok := st.GetTool("mut__shaped_tool")
	if !ok {
		t.Fatalf("shaped tool missing after sync")
	}
	if !strings.Contains(string(snap.InputSchema), `"required"`) || !strings.Contains(string(snap.InputSchema), `"q"`) {
		t.Fatalf("stored schema = %q, want full normalized input schema", snap.InputSchema)
	}
	if snap.Version != 1 || snap.Status != store.StatusQuarantined {
		t.Fatalf("fresh snapshot = v%d %q, want v1 quarantined", snap.Version, snap.Status)
	}
	if _, approved := st.ApproveTool("w1", snap.PublicName, snap.Fingerprint, snap.Version); !approved {
		t.Fatal("could not approve initial schema")
	}

	// Unchanged resync: stable version, still active.
	if err := gw.Resync(ctx, c); err != nil {
		t.Fatalf("resync unchanged: %v", err)
	}
	if snap, _ := st.GetTool("mut__shaped_tool"); snap.Version != 1 || snap.Status != store.StatusActive {
		t.Fatalf("after unchanged resync = v%d %q, want v1 active", snap.Version, snap.Status)
	}

	// A property-level change quarantines with a new version.
	srv.DeleteTools("shaped_tool")
	srv.AddTool(schemaTool("shaped_tool", `{"type":"object","properties":{"q":{"type":"string"},"n":{"type":"number"}},"required":["q","n"]}`), echoHandler())
	if err := gw.Resync(ctx, c); err != nil {
		t.Fatalf("resync changed: %v", err)
	}
	snap, _ = st.GetTool("mut__shaped_tool")
	if snap.Version != 2 || snap.Status != store.StatusQuarantined {
		t.Fatalf("after schema change = v%d %q, want v2 quarantined", snap.Version, snap.Status)
	}
	if !strings.Contains(string(snap.InputSchema), `"n"`) {
		t.Fatalf("quarantined snapshot schema = %q, want the new definition", snap.InputSchema)
	}

	// Output-only schema changes also require review and retain the prior
	// definition for an admin to compare.
	srv.DeleteTools("shaped_tool")
	changedOutput := schemaTool("shaped_tool", `{"type":"object","properties":{"q":{"type":"string"},"n":{"type":"number"}},"required":["q","n"]}`)
	changedOutput.RawOutputSchema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`)
	srv.AddTool(changedOutput, echoHandler())
	if err := gw.Resync(ctx, c); err != nil {
		t.Fatalf("output resync: %v", err)
	}
	snap, _ = st.GetTool("mut__shaped_tool")
	if snap.Version != 3 || snap.Status != store.StatusQuarantined || !strings.Contains(string(snap.OutputSchema), "answer") {
		t.Fatalf("output schema change = %+v", snap)
	}
	if versions := st.ListToolVersions("w1", snap.PublicName); len(versions) != 2 {
		t.Fatalf("tool history = %d versions, want 2", len(versions))
	}
}

func TestServerGrantDoesNotAutoApproveFutureTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts, srv := mutableUpstream(t)
	srv.AddTool(schemaTool("first", `{"type":"object"}`), echoHandler())
	up, err := upstream.DialWithOptions(ctx, ts.URL+"/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1"})
	st.AddGroup(store.Group{ID: "g1", WorkspaceID: "w1"})
	st.AddMember("g1", "u1")
	st.AddConnector(store.Connector{ID: "c1", WorkspaceID: "w1", Provider: "mut", Status: store.StatusActive})
	st.AddGroupServerGrant("g1", "c1")
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{"c1": up}, nil)
	if err := gw.SyncTools(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	id := auth.Identity{UserID: "u1", WorkspaceID: "w1"}
	if _, err := policy.Authorize(st, id, "mut__first"); err != policy.ErrToolNotActive {
		t.Fatalf("first tool before approval: %v", err)
	}
	first, _ := st.GetTool("mut__first")
	if _, approved := st.ApproveTool("w1", first.PublicName, first.Fingerprint, first.Version); !approved {
		t.Fatal("approve first")
	}
	if _, err := policy.Authorize(st, id, "mut__first"); err != nil {
		t.Fatalf("approved tool denied: %v", err)
	}
	srv.AddTool(schemaTool("future", `{"type":"object"}`), echoHandler())
	connector, _ := st.GetConnector("c1")
	if err := gw.Resync(ctx, connector); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Authorize(st, id, "mut__future"); err != policy.ErrToolNotActive {
		t.Fatalf("future tool auto-granted: %v", err)
	}
}
