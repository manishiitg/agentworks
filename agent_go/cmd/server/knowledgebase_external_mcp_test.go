package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
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
	if strings.Contains(init.Instructions, "Every tool reads") || !strings.Contains(init.Instructions, "Brain") {
		t.Fatal("write MCP connection has incorrect instructions", init.Instructions)
	}
	discovery := callRemoteTool(t, t.Context(), cli, externalMCPToolSpec, nil)
	requireRemoteSuccess(t, discovery, "five-tool discovery")
	var catalog struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err = json.Unmarshal([]byte(marshalStructured(t, discovery)), &catalog); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range catalog.Tools {
		if strings.Contains(tool.Name, "_knowledgebase") {
			found[tool.Name] = true
		}
	}
	if !reflect.DeepEqual(found, map[string]bool{"browse_knowledgebase": true, "read_knowledgebase": true, "update_knowledgebase": true, "backup_knowledgebase": true, "manage_knowledgebase_access": true}) {
		t.Fatal("MCP exposes legacy or missing tools", found)
	}
	spec := callRemoteTool(t, t.Context(), cli, externalMCPToolSpec, map[string]any{"names": []any{"read_knowledgebase", "update_knowledgebase"}})
	requireRemoteSuccess(t, spec, "Brain schemas")
	read := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "read_knowledgebase", "arguments": map[string]any{"action": "read", "path": "Payments/Checkout/retries.md"}})
	requireRemoteSuccess(t, read, "MCP read")
	var body struct {
		Result struct {
			Version string `json:"version"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(marshalStructured(t, read)), &body); err != nil || body.Result.Version == "" {
		t.Fatalf("missing MCP version: %+v %v", read, err)
	}
	invalid := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "update_knowledgebase", "arguments": map[string]any{"action": "update", "path": "Payments/Checkout/retries.md", "expected_version": body.Result.Version, "content": "Binary\u0000content", "request_id": "hosted-invalid-text"}})
	requireRemoteError(t, invalid, "MCP binary content validation", "INVALID_ARGUMENT")
	write := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "update_knowledgebase", "arguments": map[string]any{"action": "update", "path": "Payments/Checkout/retries.md", "expected_version": body.Result.Version, "content": "# Retries\r\nSaved through hosted MCP.\r\n", "request_id": "hosted-save"}})
	requireRemoteSuccess(t, write, "MCP save despite workflow viewer role")
	r := (&http.Request{}).WithContext(t.Context())
	p := knowledgebasePrincipal(r, &UserClaims{UserID: "priya", Username: "priya"})
	live, err := service.Call(t.Context(), p, "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"})
	if err != nil || live.(map[string]any)["content"] != "# Retries\nSaved through hosted MCP.\n" {
		t.Fatal("save is not live before Git backup", live, err)
	}
	denied := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "read_knowledgebase", "arguments": map[string]any{"action": "read", "path": "Payments/Billing/private.md"}})
	requireRemoteError(t, denied, "MCP folder cap", "NOT_FOUND")
	legacy := callRemoteTool(t, t.Context(), cli, externalMCPToolSpec, map[string]any{"names": "commit_knowledgebase"})
	requireRemoteError(t, legacy, "legacy alias hidden", "unknown_tool")
	grantAttempt := callRemoteTool(t, t.Context(), cli, externalMCPToolCall, map[string]any{"name": "manage_knowledgebase_access", "arguments": map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "outsider", "role": "Reader", "expected_acl_version": access.(map[string]any)["acl_version"], "request_id": "remote-grant"}})
	requireRemoteError(t, grantAttempt, "content connection cannot grant", "insufficient_scope")

	readToken, _, err := store.Issue(t.Context(), accesstokens.Token{UserID: "priya", Username: "priya", Name: "Reader", Scopes: []string{"knowledgebase:read"}, KnowledgebaseFolders: &caps, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	readSrv := serveExternalMCP(t, api, &UserClaims{UserID: "priya", Username: "priya", AccessToken: &readToken})
	reader := dialExternalMCP(t, t.Context(), readSrv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), reader)
	readSpec := callRemoteTool(t, t.Context(), reader, externalMCPToolSpec, map[string]any{"names": []any{"backup_knowledgebase", "manage_knowledgebase_access"}})
	requireRemoteSuccess(t, readSpec, "read-only action schemas")
	var schemas struct {
		Schemas map[string]struct {
			InputSchema struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"schemas"`
	}
	if err = json.Unmarshal([]byte(marshalStructured(t, readSpec)), &schemas); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schemas.Schemas["backup_knowledgebase"].InputSchema.Properties["action"].Enum, []string{"status"}) || !reflect.DeepEqual(schemas.Schemas["manage_knowledgebase_access"].InputSchema.Properties["action"].Enum, []string{"inspect"}) {
		t.Fatal("reader schema contains write actions", schemas)
	}
	for _, operation := range []struct {
		name string
		args map[string]any
	}{
		{"backup_knowledgebase", map[string]any{"action": "status", "folder_path": "Payments/Checkout"}},
		{"manage_knowledgebase_access", map[string]any{"action": "inspect", "folder_path": "Payments/Checkout"}},
	} {
		requireRemoteSuccess(t, callRemoteTool(t, t.Context(), reader, externalMCPToolCall, map[string]any{"name": operation.name, "arguments": operation.args}), "reader inspect action")
	}
	for _, operation := range []struct {
		name string
		args map[string]any
	}{
		{"backup_knowledgebase", map[string]any{"action": "commit", "message": "Unauthorized backup", "request_id": "reader-commit"}},
		{"backup_knowledgebase", map[string]any{"action": "push", "receipt_id": "receipt_unknown", "request_id": "reader-push"}},
		{"update_knowledgebase", map[string]any{"action": "delete", "path": "Payments/Checkout/retries.md", "expected_version": body.Result.Version, "request_id": "reader-delete"}},
	} {
		requireRemoteError(t, callRemoteTool(t, t.Context(), reader, externalMCPToolCall, map[string]any{"name": operation.name, "arguments": operation.args}), "reader mutation action", "insufficient_scope")
	}
}
