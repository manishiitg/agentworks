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

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestExternalWorkflowTransportKeepsRunBoundary(t *testing.T) {
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"runs:execute"}}}
	gate := newAgentWorksChatToolGate("run")
	gate.AllowWorkflowNotifications(true)
	gate.DenyWhere(func(name string) bool { return accessTokenRunToolDenied(claims, name) })
	code, bridge := externalWorkflowTransport(claims, true, gate, false)
	if code || !slices.Contains(bridge, "execute_step") || !slices.Contains(bridge, "run_full_workflow") {
		t.Fatalf("Run tools still require the unavailable shell: code=%v bridge=%v", code, bridge)
	}
	for _, name := range append(agentworksproduct.RunExternalDenylist(), "update_step", "write_file", "set_workflow_secret") {
		if slices.Contains(bridge, name) || gate.Allows(name) {
			t.Fatalf("token Run bridge admitted %s", name)
		}
	}
	gate.DenyWhere(func(name string) bool { return name == "execute_step" })
	_, filtered := externalWorkflowTransport(claims, true, gate, false)
	if slices.Contains(filtered, "execute_step") {
		t.Fatal("bridge bypasses the product registration gate")
	}
	for _, input := range []struct {
		claims *UserClaims
		run    bool
	}{{nil, true}, {&UserClaims{}, true}, {claims, false}} {
		if code, bridge := externalWorkflowTransport(input.claims, input.run, gate, false); !code || len(bridge) != 0 {
			t.Fatal("changed app or non-workflow transport")
		}
	}
}

// A token Run chat on a coding CLI reaches tools only through call_tool, over
// the real stdio bridge. call_tool must reach the registered Run executors and
// nothing the gate refused: a Builder-only action is not callable, and the
// bound execution context still rejects a Run-denied tool, a foreign session
// and revoked access.
func TestExternalRunToolsThroughMCPBridge(t *testing.T) {
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	const sessionID = "pat-token-run-bridge"
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.Issue(t.Context(), accesstokens.Token{Name: "Run", UserID: "reader", Username: "reader", AllWorkflows: true, Scopes: []string{"runs:execute"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/invoices", PinRunMode: true}
	gate := newWorkflowConversationToolGate(sessionID, req, readOnlyForRequest(WorkflowAccessRead, req))
	gate.DenyWhere(func(name string) bool { return accessTokenRunToolDenied(claims, name) })
	if code, names := externalWorkflowTransport(claims, true, gate, true); !code || len(names) != 0 {
		t.Fatalf("token Run on a bridge model kept direct tools: code=%v names=%v", code, names)
	}
	logger := loggerv2.NewNoop()
	session, err := workflow.NewWorkshopChatSession(t.Context(), &workflow.WorkshopConfig{Logger: logger, WorkspacePath: req.SelectedFolder})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	draft := &productSurfaceDraft{gate: gate}
	workflow.RegisterWorkshopChatTools(draft, session, logger)
	workflow.RegisterRunFullWorkflowTool(draft, session, logger)
	builderRan := false
	_ = draft.RegisterCustomTool("update_step", "Builder-only", map[string]interface{}{"type": "object"}, func(context.Context, map[string]interface{}) (string, error) {
		builderRan = true
		return "updated", nil
	}, "workflow")
	if _, filtered := gate.summary(); !slices.Contains(filtered, "update_step") {
		t.Fatal("update_step was not refused by the Run gate")
	}
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
	for _, name := range []string{"execute_step", "run_full_workflow"} {
		want := "step_id must be a non-empty string"
		if name == "run_full_workflow" {
			want = "group_name is retired" // the executor refuses the retired argument, so the call reached it
		}
		if content, _ := via(name, map[string]any{"step_id": "", "group_name": ""}); !strings.Contains(content, want) {
			t.Fatalf("%s did not reach its registered executor through call_tool: %s", name, content)
		}
	}
	if content, _ := via("update_step", map[string]any{"step_id": "s1"}); builderRan || !strings.Contains(content, "not registered") {
		t.Fatalf("Run token chat reached a Builder-only action through call_tool: ran=%v %s", builderRan, content)
	}
	if _, err := resolve(executor.WithSessionID(ctx, sessionID), "execute_shell_command"); err == nil {
		t.Fatal("execution context admitted a Run-denied tool for a token chat")
	}
	if _, err := resolve(executor.WithSessionID(ctx, "foreign-session"), "run_full_workflow"); err == nil {
		t.Fatal("bridge accepted another session")
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","created_by":"owner","access":{"owners":["owner"]}}`)
	if content, _ := via("run_full_workflow", map[string]any{"group_name": ""}); !strings.Contains(content, "workflow access denied") {
		t.Fatalf("call_tool retained revoked workflow access: %s", content)
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
