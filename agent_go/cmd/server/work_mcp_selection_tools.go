package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
)

const updateProjectMCPServerSelectionTool = "update_project_mcp_server_selection"

// registerWorkMCPSelectionTool closes the gap between platform-wide MCP
// installation and per-project MCP authorization. The workspace is bound at
// registration time so the model cannot mutate another Crew project.
func (api *StreamingAPI) registerWorkMCPSelectionTool(registrar definitionToolRegistrar, userID, workspacePath string) error {
	cleanWorkspace, err := cleanAgentProfileWorkspace(workspacePath, userID)
	if err != nil || cleanWorkspace != workspacePath || !isActiveWorkProjectWorkspace(userID, cleanWorkspace) {
		return fmt.Errorf("Crew MCP selection requires an active Crew project")
	}

	return registrar.RegisterCustomTool(updateProjectMCPServerSelectionTool, "Select or deselect an exact Vault connection for this Crew. Inspect list_mcp_servers first. Existing Vault grants and owner-lent account rules still apply. Changes take effect on the next message.", map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"action", "server"},
		"properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string", "enum": []string{"select", "deselect"}},
			"server": map[string]interface{}{"type": "string", "description": "Exact connection ID returned by list_mcp_servers. Different accounts have separate IDs; verify the intended workspace before selecting."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		action := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["action"])))
		requested := strings.TrimSpace(fmt.Sprint(args["server"]))
		if action != "select" && action != "deselect" {
			return "", fmt.Errorf("action must be select or deselect")
		}
		if requested == "" || requested == "<nil>" {
			return "", fmt.Errorf("server is required")
		}

		id := strings.TrimPrefix(requested, "vault_")
		if !vaultConnectionID.MatchString(id) {
			return "", fmt.Errorf("use an exact Vault connection ID from list_mcp_servers")
		}
		if err := api.selectVaultServer(ctx, userID, workspacePath, id, action == "select"); err != nil {
			return "", err
		}
		result, _ := json.Marshal(map[string]any{"server": requested, "selected": action == "select", "effective": "next message"})
		return string(result), nil

	}, "work_mcp")
}

func resolveMCPServerName(catalog *mcpclient.MCPConfig, requested string) (string, mcpclient.MCPServerConfig, error) {
	if canonical, config, err := catalog.ResolveServer(requested); err == nil {
		return canonical, config, nil
	}
	for name, config := range catalog.MCPServers {
		if strings.EqualFold(name, requested) {
			return name, config, nil
		}
	}
	return "", mcpclient.MCPServerConfig{}, fmt.Errorf("MCP server %q is not configured; use list_mcp_servers or search_mcp_catalog first", requested)
}
