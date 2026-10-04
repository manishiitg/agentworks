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

	return registrar.RegisterCustomTool(updateProjectMCPServerSelectionTool, "Select or deselect one MCP server for the active Crew project. First use list_mcp_servers to inspect your private connections and permitted Vault servers. Vault MCPs are available automatically through the signed-in user/group permissions and cannot be selected or deselected here. Selecting is allowed only for a connected private server; this tool does not install, authenticate, reconnect, edit, or remove a server. The durable project selection is written to workflow.json. Newly selected server tools become available on the next user message because the current agent turn was launched with its previous MCP scope.", map[string]interface{}{
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

		if strings.HasPrefix(requested, "vault_") {
			return "", fmt.Errorf("Vault MCPs are available automatically through your user/group permissions; manage access in Vault")
		}

		catalog, err := api.loadMergedConfig()
		if err != nil {
			return "", fmt.Errorf("load MCP configuration: %w", err)
		}
		canonical := requested
		if action == "select" {
			resolved, resolveErr := api.resolveGovernedMCP(ctx, userID, requested)
			if resolveErr != nil {
				return "", resolveErr
			}
			if strings.HasPrefix(resolved.Name, "vault_") {
				return "", fmt.Errorf("Vault MCPs are available automatically through your user/group permissions; manage access in Vault")
			}
			canonical = vaultSelectionName(resolved.Name)
			if private, found := personalMCPByCatalog(userID, requested); found {
				canonical = private.Name
			}
		}

		if err := updateProductSelectedServers(ctx, "work", workspacePath, func(current []string) []string {
			next := make([]string, 0, len(current)+1)
			for _, name := range current {
				resolved, _, resolveErr := resolveMCPServerName(catalog, name)
				if resolveErr == nil && resolved == canonical {
					continue
				}
				if resolveErr != nil && strings.EqualFold(strings.TrimSpace(name), canonical) {
					continue
				}
				next = append(next, name)
			}
			if action == "select" {
				next = append(next, canonical)
			}
			return next
		}); err != nil {
			return "", fmt.Errorf("update Crew project MCP selection: %w", err)
		}
		selected, _, err := productSelectedServers(ctx, "work", workspacePath)
		if err != nil {
			return "", fmt.Errorf("read updated Crew project MCP selection: %w", err)
		}
		result := map[string]interface{}{
			"action":           action,
			"server":           canonical,
			"selected_servers": selected,
			"status":           "updated",
		}
		if action == "select" {
			result["takes_effect"] = "next_user_message"
			result["message"] = fmt.Sprintf("%s is selected for this Crew project. Its tools will be available on the next user message.", canonical)
		} else {
			result["message"] = fmt.Sprintf("%s is no longer selected for this Crew project.", canonical)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
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
