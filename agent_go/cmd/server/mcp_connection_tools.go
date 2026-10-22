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
func (api *StreamingAPI) mcpConnectionTool(ctx context.Context, person, operation string, args map[string]interface{}) (string, error) {
	if !activeMCPPerson(person) {
		return "", fmt.Errorf("active user required")
	}
	// Inside a workflow, Relay, Crew or Code the connections are that place's (shared with everyone who has
	// access to it); outside any place they are still the person's own.
	session := executor.SessionIDFromContext(ctx)
	place := api.mcpPlaceRoot(ctx, session)
	if place != "" {
		ctx = context.WithValue(ctx, placeScopedKey{}, true)
	}
	if operation == "list_mcp_servers" {
		if _, _, err := api.vaultBuilderAuthority(ctx, person); err != nil {
			return "", err
		}
		rows := []map[string]any{}
		if place != "" {
			attachments, _ := placeMCPAttachmentsFor(place)
			for _, a := range attachments {
				store := attachmentStore(a, place)
				servers, _ := listPlaceMCPServers(store)
				for _, s := range servers {
					if s.Name != a.Server {
						continue
					}
					dir, _ := placeMCPDir(store)
					rows = append(rows, map[string]any{"name": s.Name, "label": s.Label, "catalog": s.Catalog, "connected": placeMCPServerConnected(dir, store, s), "added_by": a.Owner})
				}
			}
		} else {
			own, _ := listPlaceMCPServers(person)
			for _, s := range own {
				dir, _ := placeMCPDir(person)
				rows = append(rows, map[string]any{"name": s.Name, "label": s.Label, "catalog": s.Catalog, "connected": placeMCPServerConnected(dir, person, s)})
			}
		}
		vault, err := vaultAccessFor(ctx, person)
		vaultError := ""
		if err != nil {
			vaultError = err.Error()
		}
		selection := builderProductSelection(GetUserFromContext(ctx))
		sharing := selection.Text("Connections added to this project are used by everyone with access to it.<!-- product:mcp-gateway --> Vault connections are shared by group permissions.<!-- /product -->")
		if _, builder, _ := api.vaultBuilderAuthority(ctx, person); builder {
			sharing = "Vault administrator setup access covers connected Vault MCPs and secret metadata independently of group grants. Other products and external clients remain group scoped. Secret values are excluded."
		}
		inventory := map[string]any{"connections": rows, "vault": vault.Servers, "vault_groups": vault.Groups, "vault_secrets": vault.Secrets, "vault_error": vaultError, "catalog": api.placeMCPCatalog(), "sharing": sharing}
		if !selection.Has("mcp-gateway") {
			for _, field := range []string{"vault", "vault_groups", "vault_secrets", "vault_error"} {
				delete(inventory, field)
			}
		}
		if vault.Users != nil {
			inventory["vault_users"] = vault.Users
		}
		data, _ := json.Marshal(inventory)
		return string(data), nil
	}
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name, _ = args["server_name"].(string)
		name = strings.TrimSpace(name)
	}
	if name == "" {
		name, _ = args["catalog"].(string)
		name = strings.TrimSpace(name)
	}
	if name == "" {
		return "", fmt.Errorf("name or catalog is required")
	}
	if operation == "remove_mcp_server" && place != "" {
		a, found := placeAttachmentNamed(place, name)
		if !found {
			return "", fmt.Errorf("this place has no connection named %q; use its exact connection name (list_mcp_servers)", name)
		}
		if !placeMCPCanAttach(ctx, person, place) {
			return "", fmt.Errorf("you can remove connections only where you can edit")
		}
		if err := removePlaceMCP(a.Owner, a.Server, place); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed %s from this place. It stops working here for everyone; other places that use the same login keep it.", a.Server), nil
	}
	if operation == "remove_mcp_server" {
		saved, found, lookupErr := lookupPersonalMCP(person, name)
		if lookupErr != nil {
			return "", lookupErr
		}
		if !found {
			return "", fmt.Errorf("connection not found")
		}
		if err := forgetPlaceMCPLogin(person, saved.Name); err != nil {
			return "", err
		}
		if err := removePlaceMCPServer(person, saved.Name); err != nil {
			return "", err
		}
		closePlaceMCPConnection(person, saved.Name)
		return "Removed your connection and its sign-in.", nil
	}
	if operation == "trigger_mcp_discovery" {
		var status *ToolStatus
		var err error
		if place != "" {
			if resolved, handled, resolveErr := api.resolvePlaceAttachedMCP(ctx, session, name); handled {
				if resolveErr != nil {
					return "", resolveErr
				}
				status, err = api.discoverResolvedServerTools(ctx, resolved)
			} else {
				status, err = api.discoverGovernedServerTools(ctx, person, name)
			}
		} else {
			status, err = api.discoverGovernedServerTools(ctx, person, name)
		}
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(map[string]any{"server": name, "tools": status.FunctionNames, "tool_count": status.ToolsEnabled, "message": "Discovered tools for this project's permitted connection."})
		return string(data), nil
	}

	apiKey, _ := args["api_key"].(string)
	clientID, _ := args["client_id"].(string)
	clientSecret, _ := args["client_secret"].(string)
	if clientSecret != "" && strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("client_id is required with client_secret; ask for the OAuth app's client ID without repeating its secret")
	}
	if apiKey != "" && (clientID != "" || clientSecret != "") {
		return "", fmt.Errorf("use either an API key or OAuth app credentials for this connection")
	}
	url, _ := args["url"].(string)
	if raw, ok := args["server"].(map[string]interface{}); ok {
		if url == "" {
			url, _ = raw["url"].(string)
		}
		if raw["headers"] != nil || raw["env"] != nil || raw["command"] != nil {
			return "", fmt.Errorf("remote MCP setup accepts a URL and the documented api_key/client_id/client_secret fields; arbitrary headers, environment and process commands are not supported")
		}
	}
	label, _ := args["label"].(string)
	catalog, _ := args["catalog"].(string)
	label, catalog = strings.TrimSpace(label), strings.TrimSpace(catalog)
	if catalog == "" && url == "" {
		// Prefer a saved exact connection (including a custom server) before
		// interpreting the selector as a provider from the catalog.
		existing, found, err := lookupPersonalMCP(person, name)
		if err != nil {
			return "", err
		}
		catalog = name
		if found {
			catalog = existing.Catalog
		}
	}
	if entry, found := api.placeMCPCatalogEntry(catalog); found {
		catalog = entry.Catalog
	}
	// Catalog and custom remote servers use the same live project/account
	// authority. URL validation, public-only auth probing and private credential
	// storage stay in the shared website setup path below.
	if place != "" && operation != "edit_mcp_server" && !placeMCPCanAttach(ctx, person, place) {
		return "", fmt.Errorf("you can add connections only where you can edit this workflow, Crew or Code")
	}
	var saved placeMCPServer
	var err error
	if operation == "edit_mcp_server" {
		if label != "" {
			return "", fmt.Errorf("use install_mcp_server with label for a new account")
		}
		existing, found, lookupErr := lookupPersonalMCP(person, name)
		if lookupErr != nil {
			return "", lookupErr
		}
		if !found {
			return "", fmt.Errorf("connection not found; use its exact connection name")
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
			if entry, found := api.placeMCPCatalogEntry(catalog); found && (strings.EqualFold(name, entry.Catalog) || strings.EqualFold(name, entry.Name)) {
				body.Name = entry.Name
			}
		}
		saved, _, err = api.ensurePersonalMCP(ctx, person, body, catalog)
	}
	if err != nil {
		return "", err
	}
	if apiKey != "" {
		header, _ := args["api_key_header"].(string)
		format, _ := args["api_key_format"].(string)
		saved, err = configurePlaceMCPAPIKey(person, saved, apiKey, header, format)
		if err != nil {
			return "", err
		}
	}
	// Remember this project attachment for the owner's Integrations panel.
	// Other users of the project never inherit this private account.
	root := place
	if root == "" {
		if pin, ok, _ := codeSessionPinFor(session); ok && pin.Person == person {
			root = pin.CodeRoot
		}
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
		if err := attachPersonalLoginToPlace(person, saved.Name, root); err != nil {
			return "", err
		}
	}
	if saved.OAuth == nil {
		return fmt.Sprintf(builderProductSelection(GetUserFromContext(ctx)).Text("Saved %s. Everyone with access to this project can use it once discovery succeeds. Run trigger_mcp_discovery and inspect the result before claiming it connected. Credentials are encrypted and are not returned.<!-- product:mcp-gateway --> Use Vault to share it across projects.<!-- /product -->"), saved.Name), nil
	}
	redirect := api.mcpToolOAuthRedirect(ctx, person)
	if redirect == "" {
		return fmt.Sprintf("Saved %s, but sign-in could not start: this server has no PUBLIC_URL OAuth callback address and this chat has no authenticated browser request address. Report this exact server configuration problem; do not invent an OAuth URL or send the user to find an Integrations tab. Reuse this exact connection name after the callback address is configured, with no label.", saved.Name), nil
	}
	var entered *registeredClient
	if strings.TrimSpace(clientID) != "" {
		entered = &registeredClient{ClientID: strings.TrimSpace(clientID), ClientSecret: clientSecret}
	}
	authURL, discovery, _, err := api.startPlaceMCPSignIn(person, saved.Name, redirect, chatSessionIDFromContext(ctx), entered)
	if err != nil {
		return "", err
	}
	if discovery != nil {
		return mcpOAuthAppSetupMessage(saved.Name, discovery), nil
	}
	return fmt.Sprintf("Added %s. Sign in: %s", saved.Name, authURL), nil
}

// Keep a pending account recoverable in chat without duplicating it.
func mcpOAuthAppSetupMessage(name string, discovery *OAuthDiscoveryResponse) string {
	credentials := "client ID"
	if discovery.NeedsClientSecret {
		credentials += " and client secret"
	}
	return fmt.Sprintf("Saved %s, but it is not connected: this provider requires the user's own OAuth app %s before a sign-in link can be created. Guide the user in this chat through creating that app with the provider, registering callback URL %s, and supplying only the missing app details. Then call install_mcp_server again with this exact name, client_id and client_secret when required; omit label and catalog to reuse the pending account. Never repeat credential values. Return the resulting real sign-in link in chat; the user signs in and approves at the provider. Do not send them to find an AgentWorks UI section or create another connection.", name, credentials, discovery.RedirectURI)
}

// Browser queries supply a server-owned callback address to their retained chat.
// A tool cannot supply or borrow another person's address through its arguments.
func (api *StreamingAPI) mcpToolOAuthRedirect(ctx context.Context, person string) string {
	if redirect := deriveOAuthRedirectURIFromEnv(); redirect != "" {
		return redirect
	}
	api.lastQueryMu.RLock()
	request, found := api.lastQueryRequests[chatSessionIDFromContext(ctx)]
	api.lastQueryMu.RUnlock()
	if found && request.userID == person {
		return request.oauthRedirectURI
	}
	return ""
}

// Match the existing website's private key storage and retain the connection's
// account identity and other headers. Values never enter servers.json or replies.
func configurePlaceMCPAPIKey(person string, saved placeMCPServer, value, header, format string) (placeMCPServer, error) {
	if saved.OAuth != nil {
		return saved, fmt.Errorf("this connection uses OAuth; request its sign-in link instead of replacing it with an API key")
	}
	if len(value) > 65536 {
		return saved, fmt.Errorf("API key is too large")
	}
	header = strings.TrimSpace(header)
	if header == "" {
		header = "Authorization"
	}
	if format == "" && strings.EqualFold(header, "Authorization") {
		format = "Bearer {}"
	}
	secretName := "MCP_" + strings.ToUpper(saved.Name) + "_TOKEN"
	headers := make(map[string]placeMCPHeader, len(saved.Headers)+1)
	for key, ref := range saved.Headers {
		headers[key] = ref
	}
	headers[header] = placeMCPHeader{Secret: secretName, Format: format}
	saved.Headers = headers
	if err := validatePlaceMCPServer(&saved); err != nil {
		return saved, err
	}
	if err := setPersonalSecret(person, secretName, value); err != nil {
		return saved, err
	}
	stored, err := addPlaceMCPServer(person, saved)
	if err == nil {
		closePlaceMCPConnection(person, saved.Name)
	}
	return stored, err
}
