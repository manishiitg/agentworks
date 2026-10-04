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
// app secrets are never taken in chat: a provider that needs the person's own
// app is finished in the Integrations tab.
func (api *StreamingAPI) registerPlaceMCPTool(reg definitionToolRegistrar, person, codeRoot, redirectURI string) error {
	root := cleanAttachRoot(codeRoot)
	return reg.RegisterCustomTool("manage_my_mcp_servers",
		"Manage the MCP connections of this Code (added with the owner's own login: Gmail, Drive, GitHub, ...). "+
			"list: the catalog, this Code's connections, and the signed-in user's Vault groups, permitted MCPs/tools and secret names (never values). Inspect this live inventory before proposing shared setup. "+
			"connect: add a catalog server (catalog) or an https URL (name + url) to this Code with the owner's own login, and return the sign-in link for them to open. "+
			"remove: detach a connection from this Code. select/deselect: choose a permitted Vault connection for this Code using its exact server name. Shared connections and grants are managed in Vault. "+
			"Never ask for passwords, API keys or OAuth client secrets in chat; when a provider needs the user's own OAuth app, send them to the Integrations > Plugins > Available to finish. Changes apply from the user's next message.",
		map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"properties": map[string]interface{}{
				"action":  map[string]interface{}{"type": "string", "enum": []string{"list", "connect", "remove", "select", "deselect"}},
				"label":   map[string]interface{}{"type": "string", "description": "Account label, e.g. Notion · Engineering. Creates a separate account; omit when reconnecting an existing exact connection ID."},
				"catalog": map[string]interface{}{"type": "string", "description": "Catalog server name from list (e.g. GitHub, GoogleGmail, Linear)."},
				"server":  map[string]interface{}{"type": "string", "description": "Exact Vault connection name returned by list, for select or deselect."},
				"name":    map[string]interface{}{"type": "string", "description": "The connection's name (lowercase), for a custom URL or remove."},
				"url":     map[string]interface{}{"type": "string", "description": "https MCP URL, for a server that is not in the catalog."},
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
				store := person
				if strings.TrimSpace(label) == "" {
					store = storeForAttachment(person, name, root)
				}
				saved, _, err := api.ensurePersonalMCP(ctx, store, placeMCPServer{Name: name, Label: label, URL: url}, catalogName)
				if err != nil {
					return "", err
				}
				if err := attachPersonalLoginToPlace(person, saved.Name, root); err != nil {
					return "", err
				}
				if saved.OAuth == nil {
					return fmt.Sprintf("Connected %s (no sign-in needed) to this Code. It is available from the user's next message.", saved.Name), nil
				}
				authURL, discovery, _, err := api.startPlaceMCPSignIn(store, saved.Name, redirectURI, chatSessionIDFromContext(ctx), nil)
				if err != nil {
					return "", err
				}
				if discovery != nil {
					return fmt.Sprintf("Added %s to this Code, but this provider needs the user's own OAuth app. Ask them to open Integrations → Plugins → Available, click Sign in on %s, and enter their app's client ID and secret there (never in chat). Callback URL to register on the app: %s", saved.Name, saved.Name, discovery.RedirectURI), nil
				}
				return fmt.Sprintf("Added %s to this Code. Ask the user to open this link to sign in with their own account: %s — it is available from their next message after signing in.", saved.Name, authURL), nil
			case "select", "deselect":
				if !placeMCPCanAttach(ctx, person, root) {
					return "", fmt.Errorf("only this Code's owner can select Vault connections")
				}
				requested, _ := args["server"].(string)
				requested = vaultSelectionName(strings.TrimSpace(requested))
				if !strings.HasPrefix(requested, "vault_") {
					return "", fmt.Errorf("use an exact Vault connection name returned by list")
				}
				if action == "select" {
					resolved, err := api.resolveGovernedMCP(ctx, person, requested)
					if err != nil {
						return "", err
					}
					requested = vaultSelectionName(resolved.Name)
				}
				if err := updateProductSelectedServers(ctx, "code", root, func(current []string) []string {
					next := []string{}
					for _, server := range current {
						if vaultSelectionName(server) != requested {
							next = append(next, server)
						}
					}
					if action == "select" {
						next = append(next, requested)
					}
					return next
				}); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s %s for this Code. Changes apply from the next message; Vault permissions are enforced on every call.", action, requested), nil
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
	data, _ := json.Marshal(map[string]interface{}{"this_code_has": have, "catalog": catalog, "vault": vault.Servers, "vault_groups": vault.Groups, "vault_secrets": vault.Secrets, "vault_error": vaultError, "you_can_connect": placeMCPCanAttach(ctx, person, root)})
	return string(data), nil
}
