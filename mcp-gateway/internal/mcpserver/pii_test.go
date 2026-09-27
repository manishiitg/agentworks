package mcpserver

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"net/http/httptest"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/pii"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestPIIGuardsCallAndReviewRetry(t *testing.T) {
	var calls atomic.Int32
	srv := server.NewMCPServer("pii-upstream", "0.1")
	srv.AddTool(mcp.Tool{Name: "echo"}, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		args, _ := req.Params.Arguments.(map[string]any)
		message := args["message"].(string)
		if strings.Contains(message, "123-45-6789") {
			message = "accepted"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: message}}}, nil
	})
	srv.AddTool(mcp.Tool{Name: "card"}, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "4111 1111 1111 1111"}}}, nil
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
	for _, name := range []string{"test__echo", "test__card"} {
		snap, _ := st.GetTool(name)
		if _, ok := st.ApproveTool("w1", name, snap.Fingerprint, snap.Version); !ok {
			t.Fatalf("approve %s", name)
		}
		st.AddGrant(store.Grant{UserID: "u1", PublicName: name})
	}
	ctx = auth.WithIdentity(ctx, auth.Identity{UserID: "u1", WorkspaceID: "w1"})
	call := func(name, message string) (*mcp.CallToolResult, error) {
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = map[string]any{"message": message}
		snap, _ := st.GetTool(name)
		return g.handleCall(ctx, snap, req)
	}

	if _, err := call("test__echo", "123-45-6789"); err == nil || calls.Load() != 0 {
		t.Fatalf("SSN input reached upstream: calls=%d err=%v", calls.Load(), err)
	}
	result, err := call("test__echo", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Content[0].(mcp.TextContent).Text; strings.Contains(got, "alice@example.com") || !strings.Contains(got, "[REDACTED:email]") {
		t.Fatalf("unmasked output: %q", got)
	}
	if _, err := call("test__card", "hello"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("card result was not blocked: %v", err)
	}
	// A legacy output review rule must never create a retry approval: the
	// upstream call has already happened and may have had side effects.
	st.PutPIIRule(pii.Rule{ID: "legacy-output-review", WorkspaceID: "w1", PublicName: "test__card", DataType: "credit_card", Direction: pii.Output, Action: pii.Review})
	if _, err := call("test__card", "hello"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("output review was not blocked: %v", err)
	}
	if reviews := st.ListPIIReviews("w1"); len(reviews) != 0 {
		t.Fatalf("output review incorrectly queued: %+v", reviews)
	}
	for _, event := range st.ListAudit() {
		if strings.Contains(event.ErrorText, "123-45-6789") || strings.Contains(event.ErrorText, "4111") {
			t.Fatalf("raw PII in audit: %+v", event)
		}
	}

	st.PutPIIRule(pii.Rule{ID: "review-ssn", WorkspaceID: "w1", PublicName: "test__echo", DataType: "ssn", Direction: pii.Input, Action: pii.Review})
	before := calls.Load()
	if _, err := call("test__echo", "123-45-6789"); err == nil || !strings.Contains(err.Error(), "requires admin review") || calls.Load() != before {
		t.Fatalf("review did not pause call: calls=%d err=%v", calls.Load(), err)
	}
	reviews := st.ListPIIReviews("w1")
	if len(reviews) != 1 || reviews[0].Status != "pending" || reviews[0].PayloadHash == "" {
		t.Fatalf("review queue: %+v", reviews)
	}
	if !st.ApprovePIIReview("w1", reviews[0].ID) {
		t.Fatal("approve review")
	}
	if _, err := call("test__echo", "123-45-6789"); err != nil || calls.Load() != before+1 {
		t.Fatalf("approved retry: calls=%d err=%v", calls.Load(), err)
	}
	if _, err := call("test__echo", "123-45-6789"); err == nil {
		t.Fatal("review approval was reused more than once")
	}
}
