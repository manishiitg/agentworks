package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func writeTestClaims(user string) *UserClaims {
	return &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{ID: "writer", Scopes: []string{"workflows:read", "files:read", "files:write"}, WorkflowIDs: []string{"invoices"}}}
}
func TestExternalGuardedWriteOverMCPSeparateVolume(t *testing.T) {
	f := newExternalToolsFixture(t)
	// Manifest discovery and writes go through the authenticated workspace API,
	// while the agent's direct filesystem root is absent.
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir()+"/not-mounted")
	claims := writeTestClaims("owner")
	claims.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"docs"}, ReadOnlyPaths: []string{"docs/locked"}, BlockedPaths: []string{"docs/private"}}
	srv := serveExternalMCP(t, f.api, claims)
	ctx := context.Background()
	client := dialExternalMCP(t, ctx, srv.URL)
	initializeExternalMCP(t, ctx, client)
	args := map[string]any{"workflow_id": "invoices", "path": "docs/new.md", "content": "new documentation", "expected_revision": "missing", "request_id": "edit-one"}
	result := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "write_file", "arguments": args})
	requireRemoteSuccess(t, result, "write")
	for _, field := range []string{`"user_id":"owner"`, `"connection_id":"writer"`, `"source":"public_mcp"`} {
		if !strings.Contains(marshalStructured(t, result), field) {
			t.Fatalf("missing identity %s: %s", field, marshalStructured(t, result))
		}
	}
	if f.read(t, "Workflow/invoices/docs/new.md") != "new documentation" {
		t.Fatal("write missing")
	}
	retry := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "write_file", "arguments": args})
	requireRemoteSuccess(t, retry, "retry")
	args["content"] = "different"
	if got := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "write_file", "arguments": args}); !got.IsError {
		t.Fatal("request reuse succeeded")
	}
	args["request_id"] = "edit-two"
	if got := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "write_file", "arguments": args}); !got.IsError {
		t.Fatal("stale revision succeeded")
	}
	for _, p := range []string{"planning/plan.json", "planning/changelog/change.json", "workflow.json", "db/db.sqlite", "docs/.env", "docs/locked/a.md", "docs/private/a.md", "code/ungranted.py", "../outside"} {
		args["path"] = p
		args["request_id"] = p
		got := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "write_file", "arguments": args})
		if !got.IsError {
			t.Fatalf("allowed %s", p)
		}
	}
}
func TestExternalWritesRequireScopeLiveRoleAndWorkflowGrant(t *testing.T) {
	f := newExternalToolsFixture(t)
	for _, tc := range []struct {
		user   string
		scopes []string
		ids    []string
		want   int
	}{
		{"owner", []string{"workflows:read", "files:read"}, []string{"invoices"}, 403},
		{"reader", []string{"workflows:read", "files:read", "files:write"}, []string{"invoices"}, 403},
		{"readonly-owner", []string{"workflows:read", "files:read", "files:write"}, []string{"invoices"}, 403},
		{"outsider", []string{"workflows:read", "files:read", "files:write"}, []string{"invoices"}, 404},
		{"owner", []string{"workflows:read", "files:read", "files:write"}, []string{"secret"}, 404},
	} {
		claims := writeTestClaims(tc.user)
		claims.AccessToken.Scopes = tc.scopes
		claims.AccessToken.WorkflowIDs = tc.ids
		data, _ := json.Marshal(map[string]any{"name": "write_file", "arguments": map[string]any{"workflow_id": "invoices", "path": "docs/denied.md", "content": "denied", "expected_revision": "missing", "request_id": "denied"}})
		response := httptest.NewRecorder()
		f.api.handleExternalCall(response, adminRequest(http.MethodPost, "/api/external/v1/call", string(data), claims, nil))
		if response.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.user, response.Code, response.Body.String())
		}
	}
}
