package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Exercise the same stdio bridge used by the coding providers, with the
// registered product factory and trusted execution principal.
func TestKnowledgebaseAccessFactoryThroughMCPBridge(t *testing.T) {
	_, service := knowledgebaseServerTest(t)
	registry := agentprofiles.NewRegistry()
	if err := knowledgebaseproduct.RegisterAgentProfileRuntime(registry, knowledgebaseAccessExecutor); err != nil {
		t.Fatal(err)
	}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "knowledgebase.manage-access"}, agentprofiles.ToolRuntimeContext{UserID: "admin", SessionID: "kb-access-bridge", Product: "knowledgebase"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tools/custom/manage_knowledgebase_access" || r.Header.Get("Authorization") != "Bearer bridge-test-token" || r.Header.Get("X-Session-ID") != "kb-access-bridge" {
			http.Error(w, "unknown tool", 404)
			return
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, "bad arguments", 400)
			return
		}
		result, err := tool.Execute(ctx, args)
		response := map[string]any{"success": err == nil, "result": result}
		if err != nil {
			response["error"] = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer api.Close()
	schemas, _ := json.Marshal([]map[string]any{{"name": tool.Name, "description": tool.Description, "type": "custom", "input_schema": tool.Parameters}})
	bridge, err := client.NewStdioMCPClient(buildPulseTestMCPBridge(t), append(os.Environ(), "MCP_API_URL="+api.URL, "MCP_API_TOKEN=bridge-test-token", "MCP_SESSION_ID=kb-access-bridge", "MCP_TOOLS="+string(schemas)))
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	callCtx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err = bridge.Initialize(callCtx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "knowledgebase-access-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	listed, err := bridge.ListTools(callCtx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != tool.Name {
		t.Fatalf("unexpected bridge surface: %+v", listed.Tools)
	}
	grant := mcp.CallToolRequest{}
	grant.Params.Name = tool.Name
	inspect := mcp.CallToolRequest{}
	inspect.Params.Name = tool.Name
	inspect.Params.Arguments = map[string]any{"action": "list", "folder_path": "Payments/Checkout"}
	inspection, err := bridge.CallTool(callCtx, inspect)
	if err != nil || inspection.IsError || len(inspection.Content) == 0 {
		t.Fatalf("bridge access inspection: %+v %v", inspection, err)
	}
	var access map[string]any
	if err := json.Unmarshal([]byte(inspection.Content[0].(mcp.TextContent).Text), &access); err != nil {
		t.Fatal(err)
	}
	grant.Params.Arguments = map[string]any{"action": "grant", "folder_path": "Payments/Checkout", "identity_id": "outsider", "role": "Reader", "request_id": "bridge-grant", "expected_acl_version": access["acl_version"]}
	result, err := bridge.CallTool(callCtx, grant)
	if err != nil || result.IsError {
		t.Fatalf("bridge access call: %+v %v", result, err)
	}
	if !strings.Contains(fmt.Sprint(result.Content), "outsider") {
		t.Fatalf("missing result: %+v", result)
	}
	_, err = service.Call(t.Context(), knowledgebasePrincipal((&http.Request{}).WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "outsider", Username: "outsider"})), &UserClaims{UserID: "outsider", Username: "outsider"}), "read_knowledgebase", map[string]any{"path": "Payments/Checkout/retries.md"})
	if err != nil {
		t.Fatal("bridge grant did not reach live permissions:", err)
	}
	blocked := mcp.CallToolRequest{}
	blocked.Params.Name = "update_knowledgebase"
	blocked.Params.Arguments = map[string]any{}
	result, err = bridge.CallTool(callCtx, blocked)
	if err == nil && !result.IsError {
		t.Fatal("content mutation reachable through access bridge")
	}
}
