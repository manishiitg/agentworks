package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// serveExternalMCP exposes handleExternalMCP over a real HTTP server with the
// given claims, so tests can drive it with an actual Streamable HTTP client.
func serveExternalMCP(t *testing.T, api *StreamingAPI, claims *UserClaims) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.handleExternalMCP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dialExternalMCP(t *testing.T, ctx context.Context, url string) *client.Client {
	t.Helper()
	httpTransport, err := transport.NewStreamableHTTP(url)
	if err != nil {
		t.Fatal(err)
	}
	cli := client.NewClient(httpTransport)
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(ctx); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}
	return cli
}

// reconnectAfterCatalogChange checks that a session whose tools changed (a
// revoked role) ends with 404, and returns a fresh initialized connection.
func reconnectAfterCatalogChange(t *testing.T, ctx context.Context, cli *client.Client, url string) *client.Client {
	t.Helper()
	if _, err := cli.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: externalMCPToolSpec, Arguments: map[string]any{}}}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a session whose tools changed must end with 404, got %v", err)
	}
	fresh := dialExternalMCP(t, ctx, url)
	initializeExternalMCP(t, ctx, fresh)
	return fresh
}

func initializeExternalMCP(t *testing.T, ctx context.Context, cli *client.Client) *mcp.InitializeResult {
	t.Helper()
	result, err := cli.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: "2024-11-05",
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo:      mcp.Implementation{Name: "external-mcp-test", Version: "0.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return result
}

func listExternalMCPTools(t *testing.T, ctx context.Context, cli *client.Client) map[string]mcp.Tool {
	t.Helper()
	result, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	tools := make(map[string]mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func marshalStructured(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func marshalContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExternalMCPRequiresIdentity(t *testing.T) {
	api := &StreamingAPI{}
	w := httptest.NewRecorder()
	api.handleExternalMCP(w, httptest.NewRequest(http.MethodPost, externalMCPPath, nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
	}
}

func TestExternalMCPLocalProductSelection(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	srv := serveExternalMCP(t, f.api, &UserClaims{UserID: "owner", Username: "owner"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	init := initializeExternalMCP(t, ctx, cli)
	for _, absent := range []string{"Brain stores", "Vault management", "Relays:", "<!-- product:"} {
		if strings.Contains(init.Instructions, absent) {
			t.Fatalf("local initialize advertised %s", absent)
		}
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, spec, "local catalog")
	var inventory struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(marshalStructured(t, spec)), &inventory); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range inventory.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"brain_read", "manage_vault_access", "run_relay", "relay"} {
		if names[name] {
			t.Fatalf("local MCP catalog advertised %s", name)
		}
		result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": name, "arguments": map[string]any{}})
		if !result.IsError {
			t.Fatalf("local MCP accepted disabled tool %s", name)
		}
	}
	if !names["workflow"] || !names["crew"] {
		t.Fatal("local project tools disappeared")
	}

}

func callRemoteTool(t *testing.T, ctx context.Context, cli *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := cli.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func requireRemoteSuccess(t *testing.T, result *mcp.CallToolResult, what string) {
	t.Helper()
	if result.IsError {
		t.Fatalf("%s returned tool error: %+v", what, result.Content)
	}
}

func requireRemoteError(t *testing.T, result *mcp.CallToolResult, what, wantCode string) {
	t.Helper()
	if !result.IsError {
		t.Fatalf("%s unexpectedly succeeded", what)
	}
	if got := marshalContent(t, result); !strings.Contains(got, wantCode) {
		t.Fatalf("%s missing code %q: %s", what, wantCode, got)
	}
}

func TestExternalMCPStreamableSpecAndCall(t *testing.T) {
	f := newExternalToolsFixture(t)
	srv := serveExternalMCP(t, f.api, &UserClaims{UserID: "owner", Username: "owner"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initResult := initializeExternalMCP(t, ctx, cli)
	// A session login holds every scope, so Crew authoring is announced too.
	if !strings.HasPrefix(initResult.Instructions, (productpolicy.Selection{}).Text(externalMCPInstructions)) || !strings.Contains(initResult.Instructions, externalMCPCrewAuthoringInstructions) || !strings.Contains(initResult.Instructions, externalMCPRelayInstructions) {
		t.Fatalf("run-capable connection got read-only instructions: %q", initResult.Instructions)
	}

	// The remote surface is exactly two tools; the catalog resolves inside.
	tools := listExternalMCPTools(t, ctx, cli)
	if len(tools) != 2 {
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		t.Fatalf("remote tools %v, want exactly [get_api_spec call_tool]", names)
	}
	for _, name := range []string{externalMCPToolSpec, externalMCPToolCall} {
		if _, ok := tools[name]; !ok {
			t.Fatalf("remote surface missing %q", name)
		}
	}

	// Spec with no arguments lists the whole scope-filtered catalog.
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, spec, "get_api_spec")
	listed := marshalStructured(t, spec)
	for _, name := range []string{"workflow", "files", "execute_step", "help"} {
		if !strings.Contains(listed, name) {
			t.Fatalf("spec list missing %q: %s", name, listed)
		}
	}
	var decoded struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(listed), &decoded); err != nil {
		t.Fatal(err)
	}
	// The Code review tools are listed only for admins and Code reviewers.
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	allowed := []externalTool{}
	for _, tool := range catalog {
		if externalTokenAllows(owner, tool) {
			allowed = append(allowed, tool)
		}
	}
	want := len(externalListedTools(owner, allowed))

	if decoded.Count != want {
		t.Fatalf("spec count %d, want %d", decoded.Count, want)
	}

	// Spec accepts a single name or an array and returns JSON schemas.
	single := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": "read_file"})
	requireRemoteSuccess(t, single, "get_api_spec single name")
	if got := marshalStructured(t, single); !strings.Contains(got, "inputSchema") || !strings.Contains(got, "workflow_id") {
		t.Fatalf("single-name spec missing schema: %s", got)
	}
	multi := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": []any{"read_file", "list_workflows"}})
	requireRemoteSuccess(t, multi, "get_api_spec name array")
	if got := marshalStructured(t, multi); !strings.Contains(got, "read_file") || !strings.Contains(got, "list_workflows") {
		t.Fatalf("array spec missing entries: %s", got)
	}
	unknown := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": "update_workflow_config"})
	requireRemoteError(t, unknown, "spec of unexposed tool", "unknown_tool")

	// Calls dispatch through the REST surface.
	workflows := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "list_workflows", "arguments": map[string]any{}})
	requireRemoteSuccess(t, workflows, "call list_workflows")
	if got := marshalStructured(t, workflows); !strings.Contains(got, "invoices") {
		t.Fatalf("list_workflows result missing invoices workflow: %s", got)
	}
	process := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{
		"name": "read_file", "arguments": map[string]any{"workflow_id": "invoices", "path": "docs/process.md"},
	})
	requireRemoteSuccess(t, process, "call read_file")
	if got := marshalStructured(t, process); !strings.Contains(got, "reviewed weekly") {
		t.Fatalf("read_file result missing expected content: %s", got)
	}
	// A workflow the caller cannot see surfaces as a tool error carrying the
	// REST error code, not a protocol failure.
	denied := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{
		"name": "read_file", "arguments": map[string]any{"workflow_id": "secret", "path": "docs/process.md"},
	})
	requireRemoteError(t, denied, "read of invisible workflow", "workflow_not_found")

	bogus := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "update_workflow_config"})
	requireRemoteError(t, bogus, "call of unexposed tool", "unknown_tool")
	nameless := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{})
	requireRemoteError(t, nameless, "call without name", "invalid_arguments")
}

func TestExternalMCPRespectsTokenScopes(t *testing.T) {
	f := newExternalToolsFixture(t)
	claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{
		ID: "read-only-token", Scopes: []string{"workflows:read", "files:read"}, AllWorkflows: true,
	}}
	srv := serveExternalMCP(t, f.api, claims)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initResult := initializeExternalMCP(t, ctx, cli)
	if initResult.Instructions != (productpolicy.Selection{}).Text(externalMCPReadOnlyInstructions) {
		t.Fatalf("read-only connection got run instructions: %q", initResult.Instructions)
	}
	// Same two tools; the scope filter applies inside the spec and the calls.
	tools := listExternalMCPTools(t, ctx, cli)
	if len(tools) != 2 {
		t.Fatalf("read-only surface has %d tools, want 2", len(tools))
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{})
	requireRemoteSuccess(t, spec, "read-only spec list")
	listed := marshalStructured(t, spec)
	for _, name := range []string{"workflow", "files", "help"} {
		if !strings.Contains(listed, name) {
			t.Fatalf("read-only spec missing %q", name)
		}
	}
	for _, name := range []string{"execute_step", "chat", "trigger_schedule", "run_reply_input"} {
		if strings.Contains(listed, `"`+name+`"`) {
			t.Fatalf("read-only spec unexpectedly exposes %q", name)
		}
	}
	blocked := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "execute_step"})
	requireRemoteError(t, blocked, "read-only run call", "insufficient_scope")
}

// A client keeps its tool menu until it starts a new session. The session ID
// is the connection's tool fingerprint, so after a deploy or role change that
// alters the tools, the old ID answers 404 and the client reconnects and
// re-reads the menu, whose get_api_spec description names the current tools.
func TestExternalMCPToolMenuNamesToolsAndExpiresWhenToolsChange(t *testing.T) {
	f := newExternalToolsFixture(t)
	srv := serveExternalMCP(t, f.api, &UserClaims{UserID: "owner", Username: "owner"})
	post := func(sessionID, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+externalMCPPath, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	opened := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	session := opened.Header.Get("Mcp-Session-Id")
	if opened.StatusCode != 200 || !strings.HasPrefix(session, "aw1-") {
		t.Fatalf("initialize: status %d session %q", opened.StatusCode, session)
	}
	list := post(session, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	body, _ := io.ReadAll(list.Body)
	if list.StatusCode != 200 || !strings.Contains(string(body), "Tools you can use now") || !strings.Contains(string(body), "settings") {
		t.Fatalf("tools/list: %d %s", list.StatusCode, body)
	}
	if stale := post("aw1-000000000000000000000000", `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`); stale.StatusCode != http.StatusNotFound {
		t.Fatalf("stale session answered %d, want 404", stale.StatusCode)
	}
}
