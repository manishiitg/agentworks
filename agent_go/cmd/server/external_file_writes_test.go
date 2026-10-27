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
	return &UserClaims{UserID: user, Username: user, AccessToken: &accesstokens.Token{ID: "writer", Scopes: []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, WorkflowIDs: []string{"invoices"}}}
}
func TestExternalGuardedWriteOverMCPSeparateVolume(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
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
	result := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "files", "arguments": withFileTestAction(args, "write")})
	requireRemoteSuccess(t, result, "write")
	for _, field := range []string{`"user_id":"owner"`, `"connection_id":"writer"`, `"source":"public_mcp"`} {
		if !strings.Contains(marshalStructured(t, result), field) {
			t.Fatalf("missing identity %s: %s", field, marshalStructured(t, result))
		}
	}
	if f.read(t, "Workflow/invoices/docs/new.md") != "new documentation" {
		t.Fatal("write missing")
	}
	retry := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "files", "arguments": withFileTestAction(args, "write")})
	requireRemoteSuccess(t, retry, "retry")
	args["content"] = "different"
	if got := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "files", "arguments": withFileTestAction(args, "write")}); !got.IsError {
		t.Fatal("request reuse succeeded")
	}
	args["request_id"] = "edit-two"
	if got := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "files", "arguments": withFileTestAction(args, "write")}); !got.IsError {
		t.Fatal("stale revision succeeded")
	}
	for _, p := range []string{"planning/plan.json", "planning/changelog/change.json", "workflow.json", "db/db.sqlite", "docs/.env", "docs/locked/a.md", "docs/private/a.md", "code/ungranted.py", "../outside"} {
		args["path"] = p
		args["request_id"] = p
		got := callRemoteTool(t, ctx, client, "call_tool", map[string]any{"name": "files", "arguments": withFileTestAction(args, "write")})
		if !got.IsError {
			t.Fatalf("allowed %s", p)
		}
	}

	// Relay co-owners can edit source, memory and local skills with their existing
	// consent. No additional file permission is issued or attached to this client.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["relays"]}]}`)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","kind":"relay","access":{"owners":["other","owner"]}}`)
	relayClaims := writeTestClaims("owner")
	relayClaims.AccessToken.Scopes = []string{"workflows:read", "files:read", "runs:execute", "relays:write"}
	relaySrv := serveExternalMCP(t, f.api, relayClaims)
	relayClient := dialExternalMCP(t, ctx, relaySrv.URL)
	initializeExternalMCP(t, ctx, relayClient)
	for _, p := range []string{"relay.py", "MEMORY.md", "skills/check/SKILL.md"} {
		result := callRemoteTool(t, ctx, relayClient, "call_tool", map[string]any{"name": "files", "arguments": map[string]any{"action": "write", "workflow_id": "invoices", "path": p, "content": "saved", "expected_revision": "missing", "request_id": "relay-" + p}})
		requireRemoteSuccess(t, result, p)
		if f.read(t, "Workflow/invoices/"+p) != "saved" {
			t.Fatal("Relay file missing", p)
		}
	}
	// An old editor entry grants Run access, not Builder access.
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","kind":"relay","access":{"owners":["other"],"readers":["owner"]}}`)
	projectDenied := callRemoteTool(t, ctx, relayClient, "call_tool", map[string]any{"name": "files", "arguments": map[string]any{"action": "write", "workflow_id": "invoices", "path": "docs/reader.md", "content": "denied", "expected_revision": "missing", "request_id": "reader"}})
	if !projectDenied.IsError {
		t.Fatal("project downgrade retained file write access")
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","kind":"relay","access":{"owners":["owner"]}}`)
	// The same connected app loses editing rights as soon as the user's role changes.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"viewer","products":["relays"]}]}`)
	// A changed account role invalidates the retained MCP session. Reinitialize
	// to prove the write is refused by current authoring rules as well.
	currentClient := dialExternalMCP(t, ctx, relaySrv.URL)
	initializeExternalMCP(t, ctx, currentClient)
	denied := callRemoteTool(t, ctx, currentClient, "call_tool", map[string]any{"name": "files", "arguments": map[string]any{"action": "write", "workflow_id": "invoices", "path": "docs/revoked.md", "content": "denied", "expected_revision": "missing", "request_id": "revoked"}})
	if !denied.IsError {
		t.Fatal("role downgrade retained file write access")
	}
}
func TestExternalWritesRequireScopeLiveRoleAndWorkflowGrant(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	withMemoryUserDirectory(t, `{"users":[
 {"id":"owner","username":"owner","role":"creator","products":["agentworks","relays"]},
 {"id":"reader","username":"reader","role":"editor","products":["agentworks"]},
 {"id":"readonly-owner","username":"readonly-owner","role":"viewer","products":["agentworks"]},
 {"id":"disabled","username":"disabled","role":"creator","disabled":true,"products":["agentworks"]},
 {"id":"outsider","username":"outsider","role":"creator","products":["agentworks"]}]}`)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","access":{"owners":["owner","readonly-owner","disabled"],"readers":["reader"]}}`)
	for _, tc := range []struct {
		user   string
		scopes []string
		ids    []string
		want   int
	}{
		{"owner", []string{"workflows:read", "files:read"}, []string{"invoices"}, 403},
		{"owner", []string{"workflows:read", "files:read", "files:write"}, []string{"invoices"}, 403},
		{"owner", []string{"workflows:read", "files:read", "runs:execute", "relays:write"}, []string{"invoices"}, 403},
		{"reader", []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, []string{"invoices"}, 403},
		{"readonly-owner", []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, []string{"invoices"}, 403},
		{"disabled", []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, []string{"invoices"}, 403},
		{"outsider", []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, []string{"invoices"}, 404},
		{"owner", []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}, []string{"secret"}, 404},
	} {
		claims := writeTestClaims(tc.user)
		claims.AccessToken.Scopes = tc.scopes
		claims.AccessToken.WorkflowIDs = tc.ids
		data, _ := json.Marshal(map[string]any{"name": "files", "arguments": map[string]any{"action": "write", "workflow_id": "invoices", "path": "docs/denied.md", "content": "denied", "expected_revision": "missing", "request_id": "denied"}})
		response := httptest.NewRecorder()
		f.api.handleExternalCall(response, adminRequest(http.MethodPost, "/api/external/v1/call", string(data), claims, nil))
		if response.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.user, response.Code, response.Body.String())
		}
	}
}
