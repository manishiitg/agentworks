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
		if _, _, err := api.vaultBuilderAuthority(ctx, person); err != nil {
			return "", err
		}
		private, _ := listPlaceMCPServers(person)
		rows := []map[string]any{}
		for _, s := range private {
			dir, _ := placeMCPDir(person)
			rows = append(rows, map[string]any{"name": s.Name, "label": s.Label, "catalog": s.Catalog, "connected": placeMCPServerConnected(dir, person, s)})
		}
		vault, err := vaultAccessFor(ctx, person)
		vaultError := ""
		if err != nil {
			vaultError = err.Error()
		}
		sharing := "Private connections run only for their owner. Shared MCPs require Vault group permissions."
		if _, builder, _ := api.vaultBuilderAuthority(ctx, person); builder {
			sharing = "Vault administrator setup access covers connected Vault MCPs and secret metadata independently of group grants. Other products and external clients remain group scoped. Secret values are excluded."
		}
		data, _ := json.Marshal(map[string]any{"private": rows, "vault": vault.Servers, "vault_groups": vault.Groups, "vault_secrets": vault.Secrets, "vault_error": vaultError, "catalog": api.placeMCPCatalog(), "sharing": sharing})
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
		saved, found, lookupErr := lookupPrivateMCP(person, name)
		if lookupErr != nil {
			return "", lookupErr
		}
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
	label, _ := args["label"].(string)
	catalog, _ := args["catalog"].(string)
	if catalog == "" && url == "" {
		catalog = name
	}
	var saved placeMCPServer
	var err error
	if operation == "edit_mcp_server" {
		if label != "" {
			return "", fmt.Errorf("use install_mcp_server with label for a new account")
		}
		existing, found, lookupErr := lookupPrivateMCP(person, name)
		if lookupErr != nil {
			return "", lookupErr
		}
		if !found {
			return "", fmt.Errorf("private MCP not found; use its exact connection name")
		}
		body := existing
		if url != "" {
			body.URL = url
			catalog = ""
		} else {
			catalog = existing.Catalog
		}
		// Updating an exact account must retain its ID and isolated-login marker.
		saved, _, err = api.addPlaceMCP(ctx, person, body, catalog)
	} else {
		body := placeMCPServer{Label: label, URL: url}
		if catalog == "" {
			body.Name = strings.ToLower(name)
		} else if label == "" {
			body.Name = name
		}
		saved, _, err = api.ensurePrivateMCP(ctx, person, body, catalog)
	}
	if err != nil {
		return "", err
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
	authURL, discovery, _, err := api.startPlaceMCPSignIn(person, saved.Name, redirect, chatSessionIDFromContext(ctx), nil)
	if err != nil {
		return "", err
	}
	if discovery != nil {
		return fmt.Sprintf("Added %s privately. Finish its OAuth app setup in Integrations → My MCPs; never paste credentials into chat.", saved.Name), nil
	}
	return fmt.Sprintf("Added %s privately. Sign in: %s", saved.Name, authURL), nil
}
