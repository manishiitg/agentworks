package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// A shared Crew chat on a coding CLI reaches tools only through call_tool, over
// the real stdio bridge. call_tool must reach the registered Run executors and
// nothing the gate refused: a Builder-only action is not callable, and the
// bound execution context still rejects shell access, a foreign session
// and revoked access.
func TestCrewRunToolsThroughMCPBridge(t *testing.T) {
	tokenTestSetup(t)
	f, _ := newExternalCrewCommandFixture(t)
	const sessionID = "pat-token-run-bridge"
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Issue(t.Context(), accesstokens.Token{Name: "Crew use", UserID: "other", Username: "other", AllCrews: true, Scopes: []string{"crews:read", "crews:run"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", AgentProfileConversationKey: "team", SelectedFolder: externalCommandCrewRoot, PinRunMode: true}
	gate := newProductToolGateForAllowlist("work", []string{crewPublishedCallToolName, crewPublishedGetToolName, "set_work_identity"})
	gate.DenyWhere(func(name string) bool { return accessTokenRunToolDenied(claims, name) })
	gate.DenyReaderTools(crewReaderDeniedTools()...)
	if code, names := externalProjectTransport(claims, gate, true); !code || len(names) != 0 {
		t.Fatalf("Crew token on a bridge model kept direct tools: code=%v names=%v", code, names)
	}
	draft := &productSurfaceDraft{gate: gate}
	if err := f.api.registerCrewPublishedFunctionTools(draft, "other", sessionID, req.SelectedFolder); err != nil {
		t.Fatal(err)
	}
	builderRan := false
	_ = draft.RegisterCustomTool("set_work_identity", "Owner-only", map[string]interface{}{"type": "object"}, func(context.Context, map[string]interface{}) (string, error) {
		builderRan = true
		return "updated", nil
	}, "workflow")
	resolve := f.api.bindToolExecutionContext(context.WithValue(t.Context(), UserContextKey, claims), sessionID, req, true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/tools/custom/")
		w.Header().Set("Content-Type", "application/json")
		tool, ok := draft.tools[name]
		if !ok || r.Header.Get("Authorization") != "Bearer run-bridge-test-token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "tool not registered for session: " + name})
			return
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, "invalid arguments", http.StatusBadRequest)
			return
		}
		ctx, err := resolve(executor.WithSessionID(r.Context(), r.Header.Get("X-Session-ID")), name)
		var result string
		if err == nil {
			result, err = tool.exec(ctx, args)
		}
		response := map[string]any{"success": err == nil, "result": result}
		if err != nil {
			response["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer api.Close()
	callTool := []map[string]any{{"name": "call_tool", "description": "Call a discovered tool", "type": "call", "input_schema": map[string]any{
		"type": "object", "required": []string{"tool_name"},
		"properties": map[string]any{"tool_name": map[string]any{"type": "string"}, "server_name": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object"}},
	}}}
	encoded, _ := json.Marshal(callTool)
	bridge, err := client.NewStdioMCPClient(buildTestMCPBridge(t), append(os.Environ(), "MCP_API_URL="+api.URL, "MCP_API_TOKEN=run-bridge-test-token", "MCP_SESSION_ID="+sessionID, "MCP_TOOLS_FILE="+writeMCPToolsFileForTest(t, encoded)))
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err = bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "run-bridge-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	via := func(name string, args map[string]any) (string, bool) {
		call := mcp.CallToolRequest{}
		call.Params.Name = "call_tool"
		call.Params.Arguments = map[string]any{"tool_name": name, "arguments": args}
		result, err := bridge.CallTool(ctx, call)
		if err != nil {
			return err.Error(), true
		}
		return fmt.Sprint(result.Content), result.IsError
	}
	if content, _ := via(crewPublishedCallToolName, map[string]any{"function": "missing"}); !strings.Contains(content, "no published function") {
		t.Fatalf("Crew call did not reach its registered executor through call_tool: %s", content)
	}
	if content, _ := via("set_work_identity", map[string]any{}); builderRan || !strings.Contains(content, "not registered") {
		t.Fatalf("Run chat reached an owner-only action through call_tool: ran=%v %s", builderRan, content)
	}
	if _, err := resolve(executor.WithSessionID(ctx, sessionID), "execute_shell_command"); err == nil || !strings.Contains(err.Error(), "unavailable to external access tokens") {
		t.Fatalf("wrong Run shell refusal: %v", err)
	}
	if _, err := resolve(executor.WithSessionID(ctx, "foreign-session"), crewPublishedCallToolName); err == nil || !strings.Contains(err.Error(), "does not own this tool session") {
		t.Fatalf("wrong foreign-session refusal: %v", err)
	}
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, nil); err != nil {
		t.Fatal(err)
	}
	_, refusal := resolve(executor.WithSessionID(ctx, sessionID), crewPublishedCallToolName)
	if refusal == nil {
		t.Fatal("bound Crew tools retained removed access")
	}
	if content, _ := via(crewPublishedCallToolName, map[string]any{"function": "missing"}); !strings.Contains(content, refusal.Error()) {
		t.Fatalf("call_tool did not carry the current Crew access refusal: %s", content)
	}

}

// PLAT-856-199: a token project chat on a coding CLI takes the website route
// (call_tool reaches only gate-admitted tools); a model without the bridge has
// no call_tool and keeps direct tools instead of the shell a Run token lacks.
func TestExternalProjectTransportUsesAdmittedToolsOnly(t *testing.T) {
	claims := &UserClaims{AccessToken: &accesstokens.Token{}}
	gate := newProductToolGateForAllowlist("project-example", []string{"list_project_chats", "read_file", "write_file", "execute_shell_command", "diff_patch_workspace_file", "google_workspace_cli"})
	// The Run project token chat's restrictions, as server.go applies them.
	gate.DenyWhere(func(name string) bool { return accessTokenRunToolDenied(claims, name) })
	gate.DenyReaderTools(crewReaderDeniedTools()...)
	// The allowlist holds grouped names (files.write); the rules name registered
	// tools (write_file). A Run token chat must not be told the denied ones exist.
	code, names := externalProjectTransport(claims, gate, false)
	if code {
		t.Fatal("token project chat without the bridge lost its direct tools")
	}
	for _, kept := range []string{"list_project_chats", "read_file"} {
		if !slices.Contains(names, platformtools.Identity(kept)) {
			t.Fatalf("Run token direct list lost %s: %v", platformtools.Identity(kept), names)
		}
	}
	for _, denied := range []string{"execute_shell_command", "diff_patch_workspace_file", "google_workspace_cli"} {
		if slices.Contains(names, platformtools.Identity(denied)) || slices.Contains(names, denied) {
			t.Fatalf("Run token direct list offers denied %s: %v", platformtools.Identity(denied), names)
		}
	}
	// A reader rule naming a member's alias also removes the grouped name.
	gate.DenyReaderTools("write_crew_file")
	if _, names = externalProjectTransport(claims, gate, false); slices.Contains(names, platformtools.Identity("write_file")) {
		t.Fatalf("reader-denied write still listed: %v", names)
	}
	if code, names := externalProjectTransport(&UserClaims{}, gate, false); !code || len(names) != 0 {
		t.Fatal("website transport changed")
	}
	if code, names := externalProjectTransport(claims, gate, true); !code || len(names) != 0 {
		t.Fatal("token chat on a bridge model did not take the website route")
	}
}
