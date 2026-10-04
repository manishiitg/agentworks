package mcpserver

import (
	"context"
	"errors"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"net/http/httptest"
	"strings"
	"testing"
)

type brokenAudit struct{}

func (brokenAudit) Append(context.Context, store.AuditEvent) error { return errors.New("disk full") }
func (brokenAudit) Query(context.Context, store.AuditFilter) ([]store.AuditEvent, error) {
	return nil, errors.New("unavailable")
}
func (brokenAudit) Summary(context.Context, store.AuditFilter) (store.AuditSummary, error) {
	return store.AuditSummary{}, errors.New("unavailable")
}
func (brokenAudit) Info() store.AuditInfo { return store.AuditInfo{Provider: "sqlite", Enabled: true} }
func (brokenAudit) Close() error          { return nil }
func TestAuditFailureIsReturnedToCaller(t *testing.T) {
	st := store.NewMemoryStore()
	st.SetAuditBackend(brokenAudit{})
	g := New(st, nil, nil, nil)
	ctx := auth.WithIdentity(context.Background(), auth.Identity{UserID: "alice", WorkspaceID: "w"})
	res, err := g.handleCall(ctx, store.ToolSnapshot{WorkspaceID: "w", PublicName: "test__read"}, mcp.CallToolRequest{})
	if res != nil || err == nil || !strings.Contains(err.Error(), "audit persistence failed") || !strings.Contains(err.Error(), "do not retry automatically") {
		t.Fatalf("audit failure hidden: %+v %v", res, err)
	}
}

func TestMCPErrorResultAuditedAsUpstreamError(t *testing.T) {
	srv := server.NewMCPServer("error-test", "1")
	srv.AddTool(mcp.NewTool("read"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("private upstream detail"), nil
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
	st.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Provider: "test", Status: store.StatusActive})
	g := New(st, nil, map[string]*upstream.Client{"c": up}, nil)
	if err = g.SyncTools(context.Background(), "w"); err != nil {
		t.Fatal(err)
	}
	snap, _ := st.GetTool("test__read")
	st.ApproveTool("w", snap.PublicName, snap.Fingerprint, snap.Version)
	st.AddGrant(store.Grant{UserID: "alice", PublicName: snap.PublicName})
	ctx := auth.WithIdentity(context.Background(), auth.Identity{UserID: "alice", WorkspaceID: "w"})
	res, err := g.handleCall(ctx, snap, mcp.CallToolRequest{})
	if err != nil || res == nil || !res.IsError {
		t.Fatalf("tool error result changed: %+v %v", res, err)
	}
	events := st.ListAudit()
	if len(events) != 1 || events[0].Outcome != store.OutcomeUpstreamError || strings.Contains(events[0].ErrorText, "private") {
		t.Fatalf("upstream error outcome or redaction wrong: %+v", events)
	}
}
