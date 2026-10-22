package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// registerPlaceMCPTool gives a Code chat's agent this Code's MCP
// connections (docs/design/personal_mcp_attach.md): list the catalog and the
// Code's connections, connect one (added with the owner's own login, on for
// this Code from the next message, sign-in link returned for them to open), or
// remove one. A Code is a place like a Crew: the tool never touches a
// connection that is not this Code's. Only the Code's owner connects. OAuth
// app credentials supplied in chat use the same encrypted storage as website setup.
func (api *StreamingAPI) registerPlaceMCPTool(reg definitionToolRegistrar, person, codeRoot, redirectURI string) error {
	root := cleanAttachRoot(codeRoot)
	return reg.RegisterCustomTool("manage_my_mcp_servers",
		"Manage the MCP connections of this Code (added with the owner's own login: Gmail, Drive, GitHub, ...). "+
			"list: the catalog and this Code's connections. <!-- product:mcp-gateway -->Also shows the signed-in user's Vault groups, permitted MCPs/tools and secret names (never values).<!-- /product --> Inspect this live inventory before proposing setup. "+
			"connect: add a catalog server (catalog) or an https URL (name + url) to this Code with the owner's own login, and return the sign-in link for them to open. "+
			"remove: detach a connection from this Code. <!-- product:mcp-gateway -->Vault MCPs are available automatically through the signed-in user/group permissions; select/deselect are obsolete for Vault. Shared connections and grants are managed in Vault.<!-- /product --> "+
			"Guide setup in this chat. Accept user-provided api_key, or client_id and client_secret when the provider needs an OAuth app; credentials are stored encrypted and must never be repeated. Return the actual sign-in link for the user to open and approve at the provider; never request their password. Reuse the exact saved name without label to finish pending setup, then verify discovery. Do not send the user to an Integrations screen. Changes apply from the user's next message.",
		map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"properties": map[string]interface{}{
				"action":         map[string]interface{}{"type": "string", "enum": []string{"list", "connect", "remove", "select", "deselect"}},
				"label":          map[string]interface{}{"type": "string", "description": "Account label, e.g. Notion · Engineering. Creates a separate account; omit when reconnecting an existing exact connection ID."},
				"catalog":        map[string]interface{}{"type": "string", "description": "Catalog server name from list (e.g. GitHub, GoogleGmail, Linear)."},
				"server":         map[string]interface{}{"type": "string", "description": "Legacy connection name; use connect or remove for project connections."},
				"name":           map[string]interface{}{"type": "string", "description": "The connection's name (lowercase), for a custom URL or remove."},
				"url":            map[string]interface{}{"type": "string", "description": "https MCP URL, for a server that is not in the catalog."},
				"api_key":        map[string]interface{}{"type": "string", "description": "User-provided key; stored encrypted and never returned."},
				"api_key_header": map[string]interface{}{"type": "string", "description": "Provider's documented key header. Default Authorization."},
				"api_key_format": map[string]interface{}{"type": "string", "description": "Header format with {} for the key; default Bearer {} for Authorization."},
				"client_id":      map[string]interface{}{"type": "string", "description": "User-provided OAuth app client ID for a provider without dynamic registration."},
				"client_secret":  map[string]interface{}{"type": "string", "description": "User-provided app secret, with client_id. Encrypted after successful authorization; never repeat it."},
			},
			"required": []string{"action"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if root == "" {
				return "", fmt.Errorf("this chat has no Code to connect to")
			}
			action, _ := args["action"].(string)
			catalogName, _ := args["catalog"].(string)
			name, _ := args["name"].(string)
			url, _ := args["url"].(string)
			name = strings.ToLower(strings.TrimSpace(name))
			label, _ := args["label"].(string)
			switch action {
			case "list":
				return api.placeMCPToolList(ctx, person, root)
			case "connect":
				if !placeMCPCanAttach(ctx, person, root) {
					return "", fmt.Errorf("only this Code's owner can connect servers to it")
				}
				apiKey, _ := args["api_key"].(string)
				clientID, _ := args["client_id"].(string)
				clientSecret, _ := args["client_secret"].(string)
				clientID = strings.TrimSpace(clientID)
				if clientSecret != "" && clientID == "" {
					return "", fmt.Errorf("client_id is required with client_secret")
				}
				if apiKey != "" && (clientID != "" || clientSecret != "") {
					return "", fmt.Errorf("use either an API key or OAuth app credentials for this connection")
				}
				store := person
				if strings.TrimSpace(label) == "" {
					store = storeForAttachment(person, name, root)
				}
				saved, _, err := api.ensurePersonalMCP(ctx, store, placeMCPServer{Name: name, Label: label, URL: url}, catalogName)
				if err != nil {
					return "", err
				}
				if apiKey != "" {
					header, _ := args["api_key_header"].(string)
					format, _ := args["api_key_format"].(string)
					saved, err = configurePlaceMCPAPIKey(store, saved, apiKey, header, format)
					if err != nil {
						return "", err
					}
				}
				if err := attachPersonalLoginToPlace(person, saved.Name, root); err != nil {
					return "", err
				}
				if saved.OAuth == nil {
					return fmt.Sprintf("Saved %s to this project. Verify discovery before saying it connected. It is available from the user's next message once discovery succeeds; credential values are never returned.", saved.Name), nil
				}
				callback := redirectURI
				if callback == "" {
					callback = api.mcpToolOAuthRedirect(ctx, person)
				}
				if callback == "" {
					return fmt.Sprintf("Saved %s, but this server has no OAuth callback address. Report the missing server callback configuration and reuse this exact name after it is configured. Do not invent a sign-in URL or send the user to an AgentWorks settings screen.", saved.Name), nil
				}
				var entered *registeredClient
				if clientID != "" {
					entered = &registeredClient{ClientID: clientID, ClientSecret: clientSecret}
				}
				authURL, discovery, _, err := api.startPlaceMCPSignIn(store, saved.Name, callback, chatSessionIDFromContext(ctx), entered)
				if err != nil {
					return "", err
				}
				if discovery != nil {
					return mcpOAuthAppSetupMessage(saved.Name, discovery), nil
				}
				return fmt.Sprintf("Added %s to this Code. Ask the user to open this link to sign in with their own account: %s — it is available from their next message after signing in.", saved.Name, authURL), nil
			case "select", "deselect":
				return "", fmt.Errorf("Vault MCPs are available automatically through your user/group permissions; manage access in Vault instead of selecting or deselecting them for Code")
			case "remove":
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				if !placeMCPCanAttach(ctx, person, root) {
					return "", fmt.Errorf("only this Code's owner can remove its connections")
				}
				if err := removePlaceMCP(person, name, root); err != nil {
					return "", err
				}
				return fmt.Sprintf("Removed %s from this Code. Your private login is kept for other projects.", name), nil
			}
			return "", fmt.Errorf("action must be list, connect or remove")
		}, "mcp_connections")
}

func (api *StreamingAPI) placeMCPToolList(ctx context.Context, person, root string) (string, error) {
	attachments, err := placeMCPAttachmentsFor(root)
	if err != nil {
		return "", err
	}
	type connection struct {
		Name      string `json:"name"`
		Label     string `json:"label,omitempty"`
		Catalog   string `json:"catalog,omitempty"`
		SignIn    bool   `json:"sign_in"`
		Connected bool   `json:"connected"`
		Active    bool   `json:"active"`
	}
	have := []connection{}
	for _, a := range attachments {
		if a.Owner != person {
			continue
		}
		store := attachmentStore(a, root)
		servers, _ := listPlaceMCPServers(store)
		for _, server := range servers {
			if server.Name != a.Server {
				continue
			}
			dir, _ := placeMCPDir(store)
			have = append(have, connection{
				Name: server.Name, Label: server.Label, Catalog: server.Catalog, SignIn: server.OAuth != nil,
				Connected: placeMCPServerConnected(dir, store, server), Active: placeMCPCanAttach(ctx, a.Owner, root),
			})
		}
	}
	catalog := []map[string]interface{}{}
	for _, entry := range api.placeMCPCatalog() {
		catalog = append(catalog, map[string]interface{}{"catalog": entry.Catalog, "description": entry.Description, "needs_own_oauth_app": entry.NeedsClient})
	}
	vault, vaultErr := vaultAccessFor(ctx, person)
	vaultError := ""
	if vaultErr != nil {
		vaultError = vaultErr.Error()
	}
	data, _ := json.Marshal(map[string]interface{}{"this_project_has": have, "catalog": catalog, "vault": vault.Servers, "vault_groups": vault.Groups, "vault_secrets": vault.Secrets, "vault_error": vaultError, "you_can_connect": placeMCPCanAttach(ctx, person, root)})
	return string(data), nil
}
