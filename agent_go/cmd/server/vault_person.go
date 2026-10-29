package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// A person's own Vault items (PLAT-856-400): each person has one personal vault holding their own sign-ins and secrets,
// used only by them and the projects they build. Nobody creates more vaults or adds members; sharing with colleagues
// goes through Company groups that the administrator manages. The Vault service enforces ownership on every call; this
// file is the tool and the plumbing around it.

// personVaultActive reports whether person may keep their own Vault items: an enabled account that can create things (not a
// read-only one). It is not the platform Vault's administrator check.
func personVaultActive(person string) bool {
	if !productEnabled("mcp-gateway") {
		return false
	}
	claims := &UserClaims{UserID: person}
	access := userAccessForClaims(claims)
	return person != "" && !access.Disabled && userAllowedProduct(claims, "mcp-gateway") && workflowAccessForClaims(claims) != WorkflowAccessRead
}

// vaultOwnerOf reports whether person owns vaultID, according to the Vault service.
func vaultOwnerOf(ctx context.Context, person, vaultID string) bool {
	data, err := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults/"+url.PathEscape(vaultID), nil)
	if err != nil {
		return false
	}
	var view struct {
		Role string `json:"role"`
	}
	return json.Unmarshal(data, &view) == nil && view.Role == "owner"
}

// vaultActorCanManage reports whether person may sign in, sync or disconnect connectionID: the platform administrator for
// any connection, or the owner of the vault the connection belongs to. Rechecked on every call, so a change of role or
// ownership takes effect at once.
func (api *StreamingAPI) vaultActorCanManage(ctx context.Context, person, connectionID string) bool {
	if vaultAdminActive(person) {
		return true
	}
	if !activeMCPPerson(person) || workflowAccessForClaims(&UserClaims{UserID: person}) == WorkflowAccessRead {
		return false
	}
	c, err := vaultSavedConnection(ctx, connectionID)
	if err != nil || c.VaultID == "" {
		return false
	}
	return vaultOwnerOf(ctx, person, c.VaultID)
}

const manageMyVaultsDescription = "Your own Vault items: your sign-ins (MCP apps), secrets and computers, used only by you and the projects you build. Sharing with colleagues goes through Company groups an administrator manages. " +
	"Operations: project_select {workspace_path, kind, item_id, selected} (tick one app, secret or computer for the project); project {workspace_path} (the project's Vault panel: accessible apps, secret names and computers with actual project selections; no values); laptops (your connected computers and one-time setup steps; choose where to use them with manage_laptop_access); apps (the apps Vault can connect, by catalog name); " +
	"connect {provider, label} for a server in the Vault catalog (returns the sign-in link for you to open); " +
	"sign_in {connection_id, client_id?, client_secret?} to finish or renew sign-in in chat; " +
	"promote_secret {name, confirm} copies a secret of this Crew, Code or workflow into your Vault server-side (you never see the value; confirm as for promote); " +
	"set_secret {name, value, replace} stores a value the person gave you (replace=true to overwrite an existing name); the answer names the secret and never repeats the value, and you must not either; remove_secret {name}; " +
	"github_status shows your GitHub connection. github_connect {method: gh|pat|oauth, token?} offers gh first on a single-user local installation, PAT on local or server, and optional OAuth last. gh copies your current local login into encrypted storage; later gh account changes do not change it. pat takes the token you provide. Never repeat the token. github_disconnect removes the saved connection. " +
	"sync {connection_id}; remove_connection {connection_id}. " +
	"A person can give you a secret value to store with set_secret, or run `agentworks secret set NAME` in their terminal, or promote an existing one."

func manageMyVaultsParameters() map[string]interface{} {
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"required": []string{"operation"},
		"properties": map[string]interface{}{
			"operation":      map[string]interface{}{"type": "string", "enum": []string{"project", "project_select", "laptops", "github_status", "github_connect", "github_disconnect", "apps", "connect", "sign_in", "promote_secret", "set_secret", "remove_secret", "sync", "remove_connection"}},
			"kind":           str("project_select: mcp, project_secret, shared_secret, google, slack or computer; all_vault_apps with selected true/false turns 'Use all my apps automatically' on or off."),
			"item_id":        str("project_select: exact item ID from project inventory."),
			"selected":       map[string]interface{}{"type": "boolean", "description": "project_select: whether this project uses the item."},
			"workspace_path": str("project / project_select: the Crew or Goal folder; defaults to this chat's project. Access is rechecked."),
			"name":           str("promote_secret / set_secret / remove_secret: the secret's name. connect with url: the custom server's name."),
			"provider":       str("connect: the catalog server's name."),
			"method":         str("github_connect: gh (local owner only), pat (personal access token), or oauth (browser sign-in)."),
			"token":          str("github_connect with method=pat: the supplied personal access token. Stored encrypted; never return or repeat it."),
			"url":            str("Verified custom MCP URL, with name instead of provider."),
			"label":          str("connect: an account label, e.g. Notion · Engineering."),
			"connection_id":  str("The connection's ID (c-...) from project."),
			"confirm":        map[string]interface{}{"type": "boolean", "description": "promote: true after the explanation has been shown."},
			"value":          str("set_secret: the secret value the person gave you. Stored on the server; never repeat it."),
			"api_key":        str("User-provided connection key; encrypted in Vault, never repeat."),
			"api_key_header": str("Provider's documented API-key header."), "api_key_format": str("Header format, such as Bearer {}."),
			"client_id": str("OAuth app client ID when required."), "client_secret": str("OAuth app secret when required; encrypted, never repeat."),
			"replace": map[string]interface{}{"type": "boolean", "description": "set_secret: replace an existing secret with this name."},
		},
	}
}

// registerMyVaultsTool offers manage_my_vaults in a chat to any enabled, non-read-only account.
func (api *StreamingAPI) registerMyVaultsTool(reg definitionToolRegistrar, person string) error {
	if !personVaultActive(person) {
		return nil
	}
	err := reg.RegisterCustomTool("manage_my_vaults", manageMyVaultsDescription, manageMyVaultsParameters(),
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			if !personVaultActive(person) {
				return "", errors.New("your account cannot manage vaults")
			}
			return api.myVaultsOperation(ctx, person, args)
		}, "vault")
	if err != nil && (strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "duplicate")) {
		return nil
	}
	return err
}

func argString(args map[string]interface{}, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func (api *StreamingAPI) myVaultsOperation(ctx context.Context, person string, args map[string]interface{}) (string, error) {
	// In a project's own chat the project is the chat's; callers often leave workspace_path out.
	if op := argString(args, "operation"); (op == "project" || op == "project_select") && argString(args, "workspace_path") == "" {
		if root := api.projectRootForSession(sessionIDFromContextForVaults(ctx)); root != "" {
			args["workspace_path"] = root
		}
	}
	if argString(args, "operation") == "project_select" {
		selected, ok := args["selected"].(bool)
		if !ok {
			return "", errors.New("selected must say whether the project uses this item")
		}
		data, err := api.selectProjectVaultItem(ctx, person, argString(args, "workspace_path"), argString(args, "kind"), argString(args, "item_id"), selected)
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(data)
		return string(raw), err
	}
	if argString(args, "operation") == "project" {
		data, err := api.projectVaultInventory(ctx, person, argString(args, "workspace_path"))
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(data)
		return string(raw), err
	}
	if op := argString(args, "operation"); strings.HasPrefix(op, "github_") {
		return api.githubVaultOperation(ctx, person, op, args)
	}
	if argString(args, "operation") == "laptops" {
		if !personVaultActive(person) {
			return "", errors.New("Your account cannot keep its own Vault items")
		}
		data, err := json.Marshal(api.personalLaptopConnections(&UserClaims{UserID: person}))
		return string(data), err
	}
	if _, _, err := capLayerServiceConfig(); err != nil {
		return "", errors.New("Vault is not set up on this server")
	}
	operation := argString(args, "operation")
	connectionID := argString(args, "connection_id")
	call := func(method, path string, body any) ([]byte, error) {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		return vaultPersonRequest(ctx, person, method, path, payload)
	}
	// Every change goes to the person's own vault (PLAT-856-400); there is no other vault to choose.
	vaultID := ""
	if operation != "apps" {
		var err error
		if vaultID, err = ensurePersonalVault(ctx, person); err != nil {
			return "", err
		}
	}
	switch operation {
	case "apps":
		// The Vault catalog: what connect accepts as provider. The gateway route is the admin catalog, which the host's
		// service credential reaches; only names and whether a sign-in is needed go to the person, never upstream URLs.
		data, err := call(http.MethodGet, "/api/admin/catalog", nil)
		if err != nil {
			return "", err
		}
		// Mark OAuth apps whose sign-in cannot start on its own here (PLAT-708), so people know before they pick one.
		needsAdminSetup := func(string) bool { return false }
		if config, e := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger); e == nil {
			redirect := deriveOAuthRedirectURIFromEnv()
			needsAdminSetup = func(name string) bool { return oauthNeedsAdminSetup(config, name, redirect) }
		}
		return vaultCatalogApps(data, needsAdminSetup)
	case "connect", "sign_in":
		in := map[string]interface{}{"_vault_only": true, "vault_id": vaultID, "catalog": argString(args, "provider"), "label": argString(args, "label"), "name": argString(args, "name"), "url": argString(args, "url")}
		if operation == "sign_in" {
			in["name"] = vaultServerName(connectionID)
			delete(in, "catalog")
			delete(in, "label")
			delete(in, "url")
		}
		for _, field := range []string{"api_key", "api_key_header", "api_key_format", "client_id", "client_secret"} {
			if value, ok := args[field]; ok {
				in[field] = value
			}
		}
		return api.mcpConnectionTool(ctx, person, "install_mcp_server", in)
	case "promote_secret":
		return api.promoteSecretToVault(ctx, person, vaultID, argString(args, "name"), args["confirm"] == true)
	case "set_secret":
		name := argString(args, "name")
		replace := args["replace"] == true
		rawValue, _ := args["value"].(string)
		if err := api.setVaultSecret(ctx, person, vaultID, name, rawValue, replace); err != nil {
			return "", err
		}
		if replace {
			return "Stored " + name + " in the vault (replaced if it existed). The value is not shown again.", nil
		}
		return "Stored " + name + " in the vault. The value is not shown again.", nil
	case "remove_secret":
		if err := api.removeVaultSecret(ctx, person, vaultID, argString(args, "name")); err != nil {
			return "", err
		}
		return "Removed the secret from the vault; its value is deleted.", nil
	case "sync":
		if _, err := call(http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+url.PathEscape(connectionID)+"/sync", nil); err != nil {
			return "", err
		}
		return "Synced; the connection's tools are up to date.", nil
	case "remove_connection":
		if _, err := call(http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+url.PathEscape(connectionID), nil); err != nil {
			return "", err
		}
		mutex := platformMCPOAuthMutex("vault:" + connectionID)
		mutex.Lock()
		advanceVaultOAuthGeneration(connectionID)
		removeVaultCredentialFiles(connectionID)
		mutex.Unlock()
		return "Removed the connection and its sign-in from the vault.", nil
	}
	return "", errors.New("unknown operation")
}

// vaultCatalogApps trims the gateway catalog ({providers:[{Name,Key,URL,OAuth}]}) to {apps:[{name,oauth,needs_admin_setup}]},
// apps that need an admin's setup last.
func vaultCatalogApps(data []byte, needsAdminSetup func(name string) bool) (string, error) {
	var catalog struct {
		Providers []struct {
			Name  string
			OAuth bool
		}
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return "", errors.New("invalid Vault catalog")
	}
	type app struct {
		Name            string `json:"name"`
		OAuth           bool   `json:"oauth"`
		NeedsAdminSetup bool   `json:"needs_admin_setup,omitempty"`
	}
	apps := make([]app, 0, len(catalog.Providers))
	for _, p := range catalog.Providers {
		if name := strings.TrimSpace(p.Name); name != "" {
			apps = append(apps, app{Name: name, OAuth: p.OAuth, NeedsAdminSetup: p.OAuth && needsAdminSetup(name)})
		}
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].NeedsAdminSetup != apps[j].NeedsAdminSetup {
			return !apps[i].NeedsAdminSetup
		}
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	out, _ := json.Marshal(map[string]any{"apps": apps})
	return string(out), nil
}

// vaultConnectionInVault reports whether connectionID is in vaultID and person owns it.
func vaultConnectionInVault(ctx context.Context, person, vaultID, connectionID string) bool {
	if !vaultConnectionID.MatchString(connectionID) || !vaultOwnerOf(ctx, person, vaultID) {
		return false
	}
	c, err := vaultSavedConnection(ctx, connectionID)
	return err == nil && c.VaultID == vaultID
}

// vaultSignInLink starts the owner's sign-in for a vault connection, or reports that none is needed.
func (api *StreamingAPI) vaultSignInLink(ctx context.Context, person, connectionID, prefix string) (string, error) {
	c, err := vaultSavedConnection(ctx, connectionID)
	if err != nil {
		return prefix + "It needs no sign-in or is not ready yet; use sync to discover its tools.", nil
	}
	start, discovery, err := api.beginVaultConnectionOAuth(ctx, person, chatSessionIDFromContext(ctx), c.ID, c.OAuthServer, api.mcpToolOAuthRedirect(ctx, person), "", "")
	if err != nil {
		return "", err
	}
	if discovery != nil {
		return prefix + mcpOAuthAppSetupMessage(vaultServerName(c.ID), discovery), nil
	}

	data, err := json.Marshal(start)
	if err != nil {
		return "", err
	}
	return prefix + "Connection " + c.ID + ". Sign-in: " + string(data), nil
}
