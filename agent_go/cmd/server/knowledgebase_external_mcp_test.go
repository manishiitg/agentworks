package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

func TestKnowledgebaseExternalMCPWritesAreImmediatelyShared(t *testing.T) {
	api, service := knowledgebaseServerTest(t)
	t.Setenv("AUTH_SECRET", "knowledgebase-mcp-test-signing-secret")
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	access, err := service.Call(t.Context(), admin, "get_knowledgebase_access", map[string]any{"folder_path": "Payments/Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Call(t.Context(), admin, "manage_knowledgebase_access", map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "priya", "role": "Editor", "request_id": "mcp-editor", "expected_acl_version": access.(map[string]any)["acl_version"]})
	if err != nil {
		t.Fatal(err)
	}
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	caps := []knowledgebase.Cap{{FolderPath: "Payments/Checkout", Role: "editor"}}
	token, _, err := store.Issue(t.Context(), accesstokens.Token{UserID: "priya", Username: "priya", Name: "Local agent", Scopes: []string{"knowledgebase:read", "knowledgebase:write"}, KnowledgebaseFolders: &caps, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "priya", Username: "priya", AccessToken: &token}
	srv := serveExternalMCP(t, api, claims)
	cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	init := initializeExternalMCP(t, t.Context(), cli)
	if strings.Contains(init.Instructions, "Every tool reads") || !strings.Contains(init.Instructions, "Knowledge Base") {
		t.Fatal("write MCP connection has incorrect instructions", init.Instructions)
	}
	spec := callRemoteTool(t, t.Context(), cli, externalMCPToolSpec, map[string]any{"names": []any{"read_knowledgebase", "update_knowledgebase"}})
	requireRemoteSuccess(t, spec, "Knowledge Base schemas")
	read := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "read_knowledgebase", "arguments": map[string]any{"path": "Payments/Checkout/retries.md"}})
	requireRemoteSuccess(t, read, "MCP read")
	var body struct {
		Result struct {
			Version string `json:"version"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(marshalStructured(t, read)), &body); err != nil || body.Result.Version == "" {
		t.Fatalf("missing MCP version: %+v %v", read, err)
	}
	write := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "update_knowledgebase", "arguments": map[string]any{"path": "Payments/Checkout/retries.md", "expected_version": body.Result.Version, "content": "# Retries\nSaved through hosted MCP.\n", "request_id": "hosted-save"}})
	requireRemoteSuccess(t, write, "MCP save despite workflow viewer role")
	r := (&http.Request{}).WithContext(t.Context())
	p := knowledgebasePrincipal(r, &UserClaims{UserID: "priya", Username: "priya"})
	live, err := service.Call(t.Context(), p, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"})
	if err != nil || !strings.Contains(live.(map[string]any)["content"].(string), "Saved through hosted MCP") {
		t.Fatal("save is not live before Git backup", live, err)
	}
	denied := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "read_knowledgebase", "arguments": map[string]any{"path": "Payments/Billing/private.md"}})
	requireRemoteError(t, denied, "MCP folder cap", "NOT_FOUND")
}
