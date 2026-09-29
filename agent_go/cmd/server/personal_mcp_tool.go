package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// registerPersonalMCPTool gives a Code chat's agent the person's own MCP
// connections (docs/design/code_private_mcp.md): list the catalog and their
// servers, connect one (added as theirs, switched on in this Code, sign-in
// link returned for them to open), switch one on or off here, or remove it.
// It acts only for the chat's pinned person and Code. OAuth app secrets are
// never taken in chat: a provider that needs the person's own app is
// finished in the MCPs tab.
func (api *StreamingAPI) registerPersonalMCPTool(reg definitionToolRegistrar, person, codeRoot, redirectURI string) error {
	return reg.RegisterCustomTool("manage_my_mcp_servers",
		"Connect the user's OWN MCP servers in this Code (never shared with anyone else in it). "+
			"list: the catalog of servers they can connect and the ones they have. "+
			"connect: add a catalog server (catalog) or their own https URL (name + url) as theirs, switch it on in this Code, and return the sign-in link for the user to open. "+
			"enable / disable: switch one of their servers on or off in this Code. remove: delete it and its login. "+
			"Never ask for passwords, API keys or OAuth client secrets in chat; when a provider needs the user's own OAuth app, send them to the MCPs tab (Integrations) to finish. Changes apply from the user's next message.",
		map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"properties": map[string]interface{}{
				"action":  map[string]interface{}{"type": "string", "enum": []string{"list", "connect", "enable", "disable", "remove"}},
				"catalog": map[string]interface{}{"type": "string", "description": "Catalog server name from list (e.g. GitHub, GoogleGmail, Linear)."},
				"name":    map[string]interface{}{"type": "string", "description": "The user's server name (lowercase), for a custom URL or enable/disable/remove."},
				"url":     map[string]interface{}{"type": "string", "description": "https MCP URL, for a server that is not in the catalog."},
			},
			"required": []string{"action"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			action, _ := args["action"].(string)
			catalogName, _ := args["catalog"].(string)
			name, _ := args["name"].(string)
			url, _ := args["url"].(string)
			name = strings.ToLower(strings.TrimSpace(name))
			switch action {
			case "list":
				return api.personalMCPToolList(person, codeRoot)
			case "connect":
				saved, _, err := api.addPersonalMCP(ctx, person, personalMCPServer{Name: name, URL: url}, catalogName)
				if err != nil {
					return "", err
				}
				if err := setPersonalMCPEnabled(person, codeRoot, saved.Name, true); err != nil {
					return "", err
				}
				if saved.OAuth == nil {
					return fmt.Sprintf("Connected %s (no sign-in needed) and switched it on in this Code. It is available from the user's next message.", saved.Name), nil
				}
				authURL, discovery, _, err := api.startPersonalMCPSignIn(person, saved.Name, redirectURI, nil)
				if err != nil {
					return "", err
				}
				if discovery != nil {
					return fmt.Sprintf("Added %s and switched it on in this Code, but this provider needs the user's own OAuth app. Ask them to open Integrations → MCPs, click Sign in on %s, and enter their app's client ID and secret there (never in chat). Callback URL to register on the app: %s", saved.Name, saved.Name, discovery.RedirectURI), nil
				}
				return fmt.Sprintf("Added %s and switched it on in this Code. Ask the user to open this link to sign in with their own account: %s — it is available from their next message after signing in.", saved.Name, authURL), nil
			case "enable", "disable":
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				if err := setPersonalMCPEnabled(person, codeRoot, name, action == "enable"); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s is %sd in this Code from the user's next message.", name, action), nil
			case "remove":
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				if err := removePersonalMCPServer(person, name); err != nil {
					return "", err
				}
				closePersonalMCPConnection(person, name)
				_ = forgetPersonalMCPLogin(person, name)
				return fmt.Sprintf("Removed %s and its login.", name), nil
			}
			return "", fmt.Errorf("action must be list, connect, enable, disable or remove")
		}, "personal_mcp")
}

func (api *StreamingAPI) personalMCPToolList(person, codeRoot string) (string, error) {
	servers, err := listPersonalMCPServers(person)
	if err != nil {
		return "", err
	}
	enabled := map[string]bool{}
	if names, err := personalMCPEnabled(person, codeRoot); err == nil {
		for _, name := range names {
			enabled[name] = true
		}
	}
	type mine struct {
		Name       string `json:"name"`
		Catalog    string `json:"catalog,omitempty"`
		SignIn     bool   `json:"sign_in"`
		InThisCode bool   `json:"on_in_this_code"`
	}
	yours := []mine{}
	for _, server := range servers {
		yours = append(yours, mine{Name: server.Name, Catalog: server.Catalog, SignIn: server.OAuth != nil, InThisCode: enabled[server.Name]})
	}
	catalog := []map[string]interface{}{}
	for _, entry := range api.personalMCPCatalog() {
		catalog = append(catalog, map[string]interface{}{"catalog": entry.Catalog, "description": entry.Description, "needs_own_oauth_app": entry.NeedsClient})
	}
	data, _ := json.Marshal(map[string]interface{}{"your_servers": yours, "catalog": catalog})
	return string(data), nil
}
