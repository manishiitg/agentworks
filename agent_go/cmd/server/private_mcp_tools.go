package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/mcpagent/executor"
	"strings"
)

// All legacy chat MCP management names now act on the authenticated person's
// private store. Shared setup is exclusively the Vault agent's management tool.
func (api *StreamingAPI) privateMCPTool(ctx context.Context, person, operation string, args map[string]interface{}) (string, error) {
	if !activeMCPPerson(person) {
		return "", fmt.Errorf("active user required")
	}
	if operation == "list_mcp_servers" {
		private, _ := listPlaceMCPServers(person)
		rows := []map[string]any{}
		for _, s := range private {
			dir, _ := placeMCPDir(person)
			rows = append(rows, map[string]any{"name": s.Name, "catalog": s.Catalog, "connected": placeMCPServerConnected(dir, person, s)})
		}
		vault, err := vaultServersFor(ctx, person)
		vaultError := ""
		if err != nil {
			vaultError = err.Error()
		}
		data, _ := json.Marshal(map[string]any{"private": rows, "vault": vault, "vault_error": vaultError, "catalog": api.placeMCPCatalog(), "sharing": "Private connections run only for their owner. Shared MCPs require Vault group permissions."})
		return string(data), nil
	}
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name, _ = args["server_name"].(string)
	}
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	if operation == "remove_mcp_server" {
		saved, found := privateMCPByCatalog(person, name)
		if !found {
			return "", fmt.Errorf("private MCP not found")
		}
		if err := forgetPlaceMCPLogin(person, saved.Name); err != nil {
			return "", err
		}
		if err := removePlaceMCPServer(person, saved.Name); err != nil {
			return "", err
		}
		closePlaceMCPConnection(person, saved.Name)
		return "Removed your private MCP connection.", nil
	}
	if operation == "trigger_mcp_discovery" {
		status, err := api.discoverGovernedServerTools(ctx, person, name)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(map[string]any{"server": name, "tools": status.FunctionNames, "tool_count": status.ToolsEnabled, "message": "Discovered tools for your private or permitted Vault connection."})
		return string(data), nil
	}

	for _, field := range []string{"api_key", "client_secret"} {
		if value, _ := args[field].(string); value != "" {
			return "", fmt.Errorf("enter credentials in the MCP connection UI, never in chat")
		}
	}
	url, _ := args["url"].(string)
	if raw, ok := args["server"].(map[string]interface{}); ok {
		if url == "" {
			url, _ = raw["url"].(string)
		}
		if raw["headers"] != nil || raw["env"] != nil || raw["command"] != nil {
			return "", fmt.Errorf("private remote MCP setup accepts a URL; enter credentials in the MCP UI")
		}
	}
	saved, found := privateMCPByCatalog(person, name)
	if !found || operation == "edit_mcp_server" {
		catalog := name
		if url != "" {
			catalog = ""
		}
		var err error
		saved, _, err = api.addPlaceMCP(ctx, person, placeMCPServer{Name: strings.ToLower(name), URL: url}, catalog)
		if err != nil {
			return "", err
		}
	}
	// Remember this project attachment for the owner's Integrations panel.
	// Other users of the project never inherit this private account.
	session := executor.SessionIDFromContext(ctx)
	root := ""
	if pin, ok, _ := codeSessionPinFor(session); ok && pin.Person == person {
		root = pin.CodeRoot
	}
	if root == "" {
		api.lastQueryMu.RLock()
		request, found := api.lastQueryRequests[session]
		api.lastQueryMu.RUnlock()
		if found && request.userID == person {
			root = attachRootForCaller(person, request.SelectedFolder)
		}
	}
	if root != "" && placeMCPCanAttach(ctx, person, root) {
		if err := recordPrivateMCP(person, saved.Name, root); err != nil {
			return "", err
		}
	}
	if saved.OAuth == nil {
		return fmt.Sprintf("Connected %s privately. Select it for this project; use Vault to share it.", saved.Name), nil
	}
	redirect := deriveOAuthRedirectURIFromEnv()
	if redirect == "" {
		return fmt.Sprintf("Added %s privately. Finish sign-in in Integrations → My MCPs.", saved.Name), nil
	}
	authURL, discovery, _, err := api.startPlaceMCPSignIn(person, saved.Name, redirect, nil)
	if err != nil {
		return "", err
	}
	if discovery != nil {
		return fmt.Sprintf("Added %s privately. Finish its OAuth app setup in Integrations → My MCPs; never paste credentials into chat.", saved.Name), nil
	}
	return fmt.Sprintf("Added %s privately. Sign in: %s", saved.Name, authURL), nil
}
