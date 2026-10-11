package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
)

// A native bridge entry for profiles without a shell. Reuse the same executor
// as /api/mcp/execute; its resolver and the Vault gateway authorize every call.
func (api *StreamingAPI) callMCPTool(ctx context.Context, args map[string]interface{}) (string, error) {
	person, err := api.mcpToolUserID(ctx)
	if err != nil {
		return "", err
	}
	if !activeMCPPerson(person) {
		return "", fmt.Errorf("active MCP user required")
	}
	session := executor.SessionIDFromContext(ctx)
	if session == "" {
		session, _ = ctx.Value(common.ChatSessionIDKey).(string)
	}
	if session == "" {
		return "", fmt.Errorf("MCP execution requires the current chat session")
	}
	server, _ := args["server"].(string)
	tool, _ := args["tool"].(string)
	arguments, ok := args["arguments"].(map[string]interface{})
	if strings.TrimSpace(server) == "" || strings.TrimSpace(tool) == "" || !ok {
		return "", fmt.Errorf("exact server, tool and arguments object required; inspect list_mcp_servers first")
	}
	payload, err := json.Marshal(executor.MCPExecuteRequest{Server: server, Tool: tool, Args: arguments, SessionID: session})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/mcp/execute", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	handler := executor.NewExecutorHandlers(api.mcpConfigPath, api.logger)
	handler.SetMCPServerResolver(api.resolveMCPServer)
	response := &accessToolResponse{header: make(http.Header)}
	handler.HandleMCPExecute(response, req)
	var result executor.MCPExecuteResponse
	if err := json.Unmarshal(response.Bytes(), &result); err != nil {
		return "", fmt.Errorf("MCP executor returned an invalid response")
	}
	if !result.Success {
		return "", fmt.Errorf("MCP call failed: %s", result.Error)
	}
	return result.Result, nil
}

func (api *StreamingAPI) registerMCPCallTool(reg interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool) error {
	if disabled != nil && disabled("call_mcp_tool") {
		return nil
	}
	return reg.RegisterCustomTool("call_mcp_tool", "Call an MCP tool using the current chat's verified identity and live MCP authority. <!-- product:mcp-gateway -->The Vault administrator builder can use active approved Vault tools independently of group grants; other product chats and external clients use existing user/group permissions.<!-- /product --> First use list_mcp_servers for the exact connection name, tool name and input schema. Pass arguments as an object. Use searches/fetches to resolve real resource IDs before drafting restrictions. Read/write hints are not authorization; call mutations only when explicitly requested. This does not connect servers or grant access. Permission failures must be reported, never bypassed by changing grants.", map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": []string{"server", "tool", "arguments"},
		"properties": map[string]interface{}{
			"server":    map[string]interface{}{"type": "string", "description": "Exact connection name from list_mcp_servers. <!-- product:mcp-gateway -->A permitted Vault connection uses vault_<connection ID>.<!-- /product -->"},
			"tool":      map[string]interface{}{"type": "string", "description": "Exact tool name on that connection, from its live inventory."},
			"arguments": map[string]interface{}{"type": "object", "description": "Arguments conforming to that tool's input schema; use {} for no arguments."},
		},
	}, api.callMCPTool, "mcp_server_tools")
}
