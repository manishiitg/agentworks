package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Exercise setup, grants, live editing and revocation through the global MCP
// transport, rather than invoking the domain or external HTTP handler directly.
func TestKnowledgebaseExternalMCPBackupSetupAndUserWriteAccess(t *testing.T) {
	tokenTestSetup(t)
	api, service := knowledgebaseServerTest(t)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connect := func(identity string) *client.Client {
		t.Helper()
		token, _, err := store.Issue(t.Context(), accesstokens.Token{
			UserID: identity, Username: identity, Name: "KB test client",
			Scopes:    []string{"knowledgebase:read", "knowledgebase:write"},
			ExpiresAt: time.Now().Add(time.Hour),
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		srv := serveExternalMCP(t, api, &UserClaims{UserID: identity, Username: identity, AccessToken: &token})
		cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
		initializeExternalMCP(t, t.Context(), cli)
		return cli
	}
	admin, priya := connect("admin"), connect("priya")
	call := func(cli *client.Client, tool string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		return callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": tool, "arguments": args})
	}
	result := func(cli *client.Client, tool string, args map[string]any) map[string]any {
		t.Helper()
		response := call(cli, tool, args)
		requireRemoteSuccess(t, response, tool)
		var body struct {
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal([]byte(marshalStructured(t, response)), &body); err != nil || body.Result == nil {
			t.Fatalf("missing result: %v %s", err, marshalStructured(t, response))
		}
		return body.Result
	}
	setup := map[string]any{"action": "configure_backup", "username": "kb-user", "remote_url": "https://github.com/org/knowledge-backup.git", "pat": "mcp-test-only-secret", "branch": "main", "request_id": "mcp-backup-setup"}
	requireRemoteError(t, call(priya, "manage_knowledgebase_access", setup), "non-admin backup setup", "FORBIDDEN")
	if got := result(admin, "brain_access", setup); got["configured"] != true || got["pat_configured"] != true || got["pat"] != nil || got["encrypted_pat"] != nil {
		t.Fatal("backup setup not applied", got)
	}
	// Exact retries are safe, and configuration is visible to an ordinary reader.
	result(admin, "brain_access", setup)
	if configured, err := service.BackupConfigured(); err != nil || !configured {
		t.Fatal("external setup did not persist", configured, err)
	}
	// The destination may change (PLAT-633); a new username still needs its own token.
	requireRemoteError(t, call(admin, "manage_knowledgebase_access", map[string]any{"action": "configure_backup", "username": "git", "remote_url": "https://github.com/org/other.git", "request_id": "mcp-backup-redirect"}), "backup redirect with a new username but no token", "INVALID_ARGUMENT")

	inspect := func() map[string]any {
		return result(admin, "brain_access", map[string]any{"action": "inspect", "folder_path": "Payments/Checkout"})
	}
	result(admin, "brain_access", map[string]any{"action": "list", "folder_path": "Payments/Checkout"})
	readArgs := map[string]any{"action": "read", "path": "Payments/Checkout/retries.md"}
	entry := result(priya, "brain_read", readArgs)
	write := map[string]any{"action": "update", "path": readArgs["path"], "expected_version": entry["version"], "content": "# Retries\nWritten by Priya through MCP.\n", "request_id": "mcp-priya-write"}
	requireRemoteError(t, call(priya, "update_knowledgebase", write), "Reader cannot edit", "FORBIDDEN")
	result(admin, "brain_access", map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "priya", "role": "Editor", "expected_acl_version": inspect()["acl_version"], "request_id": "mcp-grant-editor"})
	result(priya, "brain_update", write)
	if got := result(admin, "brain_read", readArgs); got["content"] != write["content"] {
		t.Fatal("new Editor save not immediately shared", got)
	}
	requireRemoteError(t, call(priya, "manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "outsider", "role": "Reader", "expected_acl_version": inspect()["acl_version"], "request_id": "mcp-editor-grant"}), "Editor cannot manage access", "FORBIDDEN")
	requireRemoteError(t, call(priya, "read_knowledgebase", map[string]any{"action": "read", "path": "Payments/Billing/private.md"}), "grant cannot escape folder", "NOT_FOUND")
	result(admin, "brain_access", map[string]any{"action": "revoke", "folder_path": "Payments/Checkout", "identity_id": "priya", "expected_acl_version": inspect()["acl_version"], "request_id": "mcp-revoke-editor"})
	requireRemoteError(t, call(priya, "read_knowledgebase", readArgs), "revocation takes effect immediately", "NOT_FOUND")
}
