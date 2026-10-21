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
	code, bridge := externalWorkflowTransport(claims, true, gate)
	if code || !slices.Contains(bridge, "execute_step") || !slices.Contains(bridge, "run_full_workflow") {
		t.Fatalf("Run tools still require the unavailable shell: code=%v bridge=%v", code, bridge)
	}
	for _, name := range append(agentworksproduct.RunExternalDenylist(), "update_step", "write_file", "set_workflow_secret") {
		if slices.Contains(bridge, name) || gate.Allows(name) {
			t.Fatalf("token Run bridge admitted %s", name)
		}
	}
	gate.DenyWhere(func(name string) bool { return name == "execute_step" })
	_, filtered := externalWorkflowTransport(claims, true, gate)
	if slices.Contains(filtered, "execute_step") {
		t.Fatal("bridge bypasses the product registration gate")
	}
	for _, input := range []struct {
		claims *UserClaims
		run    bool
	}{{nil, true}, {&UserClaims{}, true}, {claims, false}} {
		if code, bridge := externalWorkflowTransport(input.claims, input.run, gate); !code || len(bridge) != 0 {
			t.Fatal("changed app or non-workflow transport")
		}
	}
	claims.ExternalBuilderOperationID = "operation"
	code, bridge = externalWorkflowTransport(claims, true, gate)
	if code || !slices.Contains(bridge, "update_step") || slices.Contains(bridge, "run_full_workflow") {
		t.Fatal("managed Builder inherited Run execution")
	}
}

// Exercise the real stdio bridge and registered workflow executors. Deliberately
// incomplete arguments reach their native validation without starting user work.
// The same bound execution context must reject a foreign session and revoked access.
func TestExternalRunToolsThroughMCPBridge(t *testing.T) {
	f := newExternalToolsFixture(t)
	const sessionID = "pat-token-run-bridge"
	claims := patClaims("reader", []string{"runs:execute"})
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/invoices", PinRunMode: true}
	gate := newWorkflowConversationToolGate(sessionID, req, readOnlyForRequest(WorkflowAccessRead, req))
	gate.DenyWhere(func(name string) bool { return accessTokenRunToolDenied(claims, name) })
	code, names := externalWorkflowTransport(claims, true, gate)
	if code {
		t.Fatal("token Run still uses shell transport")
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
	var schemas []map[string]any
	for _, name := range names {
		if tool, ok := draft.tools[name]; ok {
			schemas = append(schemas, map[string]any{"name": name, "description": tool.desc, "input_schema": tool.params, "type": "custom"})
		}
	}
	resolve := f.api.bindToolExecutionContext(context.WithValue(t.Context(), UserContextKey, claims), sessionID, req, true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/tools/custom/")
		tool, ok := draft.tools[name]
		if !ok || r.Header.Get("Authorization") != "Bearer run-bridge-test-token" {
			http.Error(w, "unknown tool or token", http.StatusForbidden)
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer api.Close()
	encoded, _ := json.Marshal(schemas)
	bridge, err := client.NewStdioMCPClient(buildPulseTestMCPBridge(t), append(os.Environ(), "MCP_API_URL="+api.URL, "MCP_API_TOKEN=run-bridge-test-token", "MCP_SESSION_ID="+sessionID, "MCP_TOOLS_FILE="+writeMCPToolsFileForTest(t, encoded)))
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err = bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "run-bridge-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	listed, err := bridge.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"execute_step", "run_full_workflow"} {
		if !slices.ContainsFunc(listed.Tools, func(tool mcp.Tool) bool { return tool.Name == name }) {
			t.Fatalf("native catalog omitted %s", name)
		}
		call := mcp.CallToolRequest{}
		call.Params.Name = name
		call.Params.Arguments = map[string]any{"step_id": "", "group_name": ""}
		result, err := bridge.CallTool(ctx, call)
		want := "step_id must be a non-empty string"
		if name == "run_full_workflow" {
			want = "group_name is required"
		}
		if err != nil || result.IsError || !strings.Contains(fmt.Sprint(result.Content), want) {
			t.Fatalf("%s did not reach its registered executor: %+v %v", name, result, err)
		}
	}
	if _, err := resolve(executor.WithSessionID(ctx, "foreign-session"), "run_full_workflow"); err == nil {
		t.Fatal("direct bridge accepted another session")
	}
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","created_by":"owner","access":{"owners":["owner"]}}`)
	call := mcp.CallToolRequest{}
	call.Params.Name = "run_full_workflow"
	call.Params.Arguments = map[string]any{"group_name": ""}
	result, err := bridge.CallTool(ctx, call)
	if err != nil || !strings.Contains(fmt.Sprint(result.Content), "workflow access denied") {
		t.Fatalf("native bridge retained revoked workflow access: %+v %v", result, err)
	}
}

// PLAT-856-199: a registered project reader must not need the shell that
// external tokens cannot use. Run and product restrictions still win.
func TestExternalProjectTransportUsesAdmittedToolsOnly(t *testing.T) {
	claims := &UserClaims{AccessToken: &accesstokens.Token{}}
	gate := newProductToolGateForAllowlist("project-example", []string{"list_project_chats", "write_file", "execute_shell_command", "google_workspace_cli"})
	gate.DenyReaderTools("write_file")
	code, names := externalProjectTransport(claims, gate)
	if code || len(names) != 1 || names[0] != "list_project_chats" {
		t.Fatalf("wrong token project bridge: code=%v names=%v", code, names)
	}
	gate.Declare("list_functions") // Product factories resolve names after the initial transport.
	_, names = externalProjectTransport(claims, gate)
	if !slices.Contains(names, "list_functions") {
		t.Fatal("late-declared product tool lost from final bridge")
	}
	if code, names := externalProjectTransport(&UserClaims{}, gate); !code || len(names) != 0 {
		t.Fatal("website transport changed")
	}
}
