package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/manishiitg/mcpagent/executor"
)

// All setup and discovery act on Vault. Project config contains selection IDs,
// never an independent account or credential store.
func (api *StreamingAPI) mcpConnectionTool(ctx context.Context, person, operation string, args map[string]interface{}) (string, error) {
	if !activeMCPPerson(person) {
		return "", fmt.Errorf("active user required")
	}
	session := executor.SessionIDFromContext(ctx)
	root := api.mcpPlaceRoot(ctx, session)
	if args["_vault_only"] == true {
		root = ""
	}
	if operation == "list_mcp_servers" {
		access, err := vaultAccessFor(ctx, person)
		if err != nil {
			return "", err
		}
		own, err := personalVaults(ctx, person)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(map[string]any{"vault": access, "own_vaults": own.Vaults, "catalog": api.placeMCPCatalog(), "setup": "Use Vault in this chat. Return the real provider sign-in link, verify discovery and select the requested account. Never repeat credentials."})
		return string(data), nil
	}
	name, _ := args["name"].(string)
	if name == "" {
		name, _ = args["server_name"].(string)
	}
	catalog, _ := args["catalog"].(string)
	label, _ := args["label"].(string)
	rawURL, _ := args["url"].(string)
	apiKey, _ := args["api_key"].(string)
	clientID, _ := args["client_id"].(string)
	clientSecret, _ := args["client_secret"].(string)
	if clientSecret != "" && strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("client_id is required with client_secret")
	}
	if apiKey != "" && (clientID != "" || clientSecret != "") {
		return "", fmt.Errorf("choose API key or OAuth app credentials")
	}
	// Existing native project authoring gates remain authoritative.
	if operation != "trigger_mcp_discovery" && root != "" && !placeMCPCanAttach(ctx, person, root) {
		return "", fmt.Errorf("only a current Builder can change this project's connections")
	}
	id := strings.TrimPrefix(strings.TrimSpace(name), "vault_")
	vaultID, _ := args["vault_id"].(string)
	if vaultID != "" && !vaultOwnerOf(ctx, person, vaultID) {
		return "", fmt.Errorf("you may add connections only to a vault you own")
	}
	if !vaultConnectionID.MatchString(id) && label == "" && name != "" {
		own, err := personalVaults(ctx, person)
		if err != nil {
			return "", err
		}
		matches := 0
		for _, v := range own.Vaults {
			if v.Role != "owner" {
				continue
			}
			for _, c := range v.Connections {
				if strings.EqualFold(c.Label, name) || strings.EqualFold(c.Provider, name) {
					id = c.ID
					vaultID = v.Group.ID
					matches++
				}
			}
		}
		if matches > 1 {
			return "", fmt.Errorf("several accounts match; use the exact vault_ connection ID from list_mcp_servers")
		}
	}
	exists := vaultConnectionID.MatchString(id)
	if exists {
		if operation != "trigger_mcp_discovery" && !api.vaultActorCanManage(ctx, person, id) {
			return "", fmt.Errorf("only an owner of this Vault connection can change it")
		}
		if vaultID == "" {
			data, err := vaultServiceRequest(ctx, person, http.MethodGet, "/api/admin/connectors/"+id, nil)
			if err != nil {
				return "", err
			}
			var c struct{ VaultID string }
			if json.Unmarshal(data, &c) != nil {
				return "", fmt.Errorf("invalid Vault connection")
			}
			vaultID = c.VaultID
		}
	}
	if operation == "trigger_mcp_discovery" {
		if !exists {
			return "", fmt.Errorf("use an exact Vault connection ID from list_mcp_servers")
		}
		allowed, err := vaultServersFor(ctx, person)
		if err != nil {
			return "", err
		}
		permitted := false
		for _, row := range allowed {
			if row.ID == id {
				permitted = true
			}
		}
		if !permitted && !api.vaultActorCanManage(ctx, person, id) {
			return "", fmt.Errorf("this connection is not available to your account")
		}
		if api.vaultActorCanManage(ctx, person, id) {
			var syncErr error
			if vaultID == "" {
				_, syncErr = vaultServiceRequest(ctx, person, http.MethodPost, "/api/admin/connectors/"+id+"/sync", nil)
			} else {
				_, syncErr = vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+id+"/sync", nil)
			}
			if err := syncErr; err != nil {
				return "", err
			}
		}
		rows, err := vaultServersFor(ctx, person)
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			if row.ID == id {
				data, _ := json.Marshal(row)
				return string(data), nil
			}
		}
		return "", fmt.Errorf("Vault has not discovered an active connection; complete its sign-in first")
	}
	if operation == "remove_mcp_server" {
		if !exists {
			return "", fmt.Errorf("use an exact Vault connection ID")
		}
		if root != "" {
			if err := api.selectVaultServer(ctx, person, root, id, false); err != nil {
				return "", err
			}
			return "Removed the connection from this project; its Vault account is kept.", nil
		}
		if _, err := vaultPersonRequest(ctx, person, http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+id, nil); err != nil {
			return "", err
		}
		removeVaultCredentialFiles(id)
		return "Removed the connection from Vault.", nil
	}
	if !exists {
		if operation == "edit_mcp_server" {
			return "", fmt.Errorf("use an exact saved Vault connection ID")
		}
		var err error
		if vaultID == "" {
			vaultID, err = ensurePersonalVault(ctx, person)
			if err != nil {
				return "", err
			}
		}
		payload := map[string]any{"Label": label}
		if transport, _ := args["transport"].(string); transport != "" {
			payload["Transport"] = transport
		}
		if catalog == "" && rawURL == "" {
			catalog = name
		}
		if rawURL == "" {
			if entry, ok := api.placeMCPCatalogEntry(catalog); ok {
				if apiKey != "" {
					rawURL = entry.URL
					if name == "" {
						name = entry.Name
					}
				} else {
					payload["Provider"] = entry.Catalog
				}
			} else {
				return "", fmt.Errorf("choose a provider from the catalog or supply its verified MCP URL")
			}
		}
		if rawURL != "" {
			payload["URL"] = rawURL
			payload["Name"] = name
			if name == "" {
				return "", fmt.Errorf("name is required for a custom server")
			}
		}
		if apiKey != "" {
			credential, err := vaultAPIKeyHeaders(args, apiKey)
			if err != nil {
				return "", err
			}
			payload["Credential"] = credential
			payload["AuthScheme"] = "Headers"
		}
		data, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors", vaultPromoteJSON(payload))
		if err != nil {
			return "", err
		}
		var made struct{ ID string }
		if json.Unmarshal(data, &made) != nil || !vaultConnectionID.MatchString(made.ID) {
			return "", fmt.Errorf("Vault returned no connection ID")
		}
		id = made.ID
	} else if apiKey != "" {
		credential, err := vaultAPIKeyHeaders(args, apiKey)
		if err != nil {
			return "", err
		}
		if _, err = vaultPersonRequest(ctx, person, http.MethodPut, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+id+"/credentials", vaultPromoteJSON(map[string]string{"Credential": credential, "AuthScheme": "Headers"})); err != nil {
			return "", err
		}
	}
	if exists && operation == "edit_mcp_server" && (label != "" || rawURL != "") {
		if _, err := vaultPersonRequest(ctx, person, http.MethodPut, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+id, vaultPromoteJSON(map[string]string{"Label": label, "URL": rawURL})); err != nil {
			return "", err
		}
		if rawURL != "" {
			removeVaultCredentialFiles(id)
		}
	}
	if root != "" {
		if err := api.selectVaultServer(ctx, person, root, id, true); err != nil {
			return "", err
		}
	}
	c, err := vaultConnection(ctx, id)
	if err != nil {
		return "Saved " + vaultServerName(id) + " in Vault and selected it for this project. Run trigger_mcp_discovery with that exact ID to verify it before claiming it connected.", nil
	}
	redirect := api.mcpToolOAuthRedirect(ctx, person)
	if redirect == "" {
		return "Saved " + vaultServerName(id) + " in Vault. The server needs its OAuth callback address configured before sign-in can start; do not invent a link or send the user to an old UI path.", nil
	}
	start, discovery, err := api.beginVaultConnectionOAuth(ctx, person, chatSessionIDFromContext(ctx), id, c.OAuthServer, redirect, clientID, clientSecret)
	if err != nil {
		return "", err
	}
	if discovery != nil {
		return mcpOAuthAppSetupMessage(vaultServerName(id), discovery), nil
	}
	data, _ := json.Marshal(start)
	return "Saved " + vaultServerName(id) + " in Vault. Open the real provider sign-in link in this result, approve access there, then verify discovery and finish the original request: " + string(data), nil
}

func vaultAPIKeyHeaders(args map[string]interface{}, value string) (string, error) {
	header, _ := args["api_key_header"].(string)
	if header == "" {
		header = "Authorization"
	}
	format, _ := args["api_key_format"].(string)
	if format == "" {
		if strings.EqualFold(header, "Authorization") {
			format = "Bearer {}"
		} else {
			format = "{}"
		}
	}
	data, err := json.Marshal(map[string]string{header: strings.ReplaceAll(format, "{}", value)})
	return string(data), err
}

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
