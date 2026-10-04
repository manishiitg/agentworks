package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestCallsPreservePayloadsAndRecordAuditPayloads(t *testing.T) {
	const message = "alice@example.test 123-45-6789 4111 1111 1111 1111"
	var calls atomic.Int32
	srv := server.NewMCPServer("payload-upstream", "0.1")
	srv.AddTool(mcp.Tool{Name: "echo"}, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		args, _ := req.Params.Arguments.(map[string]any)
		if args["message"] != message {
			t.Errorf("arguments changed: %+v", args)
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{mcp.TextContent{Type: "text", Text: message}, mcp.ImageContent{Type: "image", Data: "aGVsbG8=", MIMEType: "image/png"}},
			StructuredContent: map[string]any{"message": message},
		}, nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithEndpointPath("/mcp"), server.WithStateLess(true)))
	defer ts.Close()
	ctx := context.Background()
	up, err := upstream.DialWithOptions(ctx, ts.URL+"/mcp", upstream.DialOptions{AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1"})
	st.AddConnector(store.Connector{ID: "c1", WorkspaceID: "w1", Provider: "test", Status: store.StatusActive})
	g := New(st, nil, map[string]*upstream.Client{"c1": up}, nil)
	if err := g.SyncTools(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	snap, _ := st.GetTool("test__echo")
	if _, ok := st.ApproveTool("w1", snap.PublicName, snap.Fingerprint, snap.Version); !ok {
		t.Fatal("approval failed")
	}
	snap, _ = st.GetTool(snap.PublicName)
	ctx = auth.WithIdentity(ctx, auth.Identity{UserID: "u1", WorkspaceID: "w1", ClientID: "claude-test"})
	req := mcp.CallToolRequest{}
	req.Params.Name = snap.PublicName
	req.Params.Arguments = map[string]any{"message": message}
	if _, err := g.handleCall(ctx, snap, req); err == nil || calls.Load() != 0 {
		t.Fatal("ungranted tool reached upstream")
	}
	st.AddGrant(store.Grant{UserID: "u1", PublicName: snap.PublicName})
	res, err := g.handleCall(ctx, snap, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(res.Content) != 2 || res.Content[0].(mcp.TextContent).Text != message {
		t.Fatalf("result changed: %+v", res)
	}
	if image, ok := res.Content[1].(mcp.ImageContent); !ok || image.Data != "aGVsbG8=" {
		t.Fatalf("image dropped: %+v", res.Content)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil || !strings.Contains(string(data), message) {
		t.Fatalf("structured result changed: %s, %v", data, err)
	}
	events := st.ListAudit()
	if len(events) != 2 {
		t.Fatalf("audit events: %+v", events)
	}
	for _, event := range events {
		if event.UserID != "u1" || event.ClientID != "claude-test" || event.ConnectorID != "c1" {
			t.Fatalf("audit attribution lost: %+v", event)
		}
		if !strings.Contains(string(event.Input), message) || event.InputTruncated || event.OutputTruncated {
			t.Fatal("audit lost unmodified input payload")
		}
	}
	if len(events[0].Output) != 0 || !strings.Contains(string(events[1].Output), message) || !strings.Contains(string(events[1].Output), "aGVsbG8=") {
		t.Fatal("audit denied/success output incorrect")
	}
	var recorded, expected any
	json.Unmarshal(events[1].Output, &recorded)
	expectedBytes, _ := json.Marshal(map[string]any{"content": res.Content, "structuredContent": res.StructuredContent, "isError": res.IsError})
	json.Unmarshal(expectedBytes, &expected)
	if !reflect.DeepEqual(recorded, expected) {
		t.Fatalf("MCP output shape changed: %s", events[1].Output)
	}
}
