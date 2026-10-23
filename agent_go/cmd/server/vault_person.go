package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// Person-owned vaults (PLAT-507): a vault is a bundle of MCP connections that any person can create and share. Its owners
// (one or more) add, remove and update what is in it and decide who may use it; members use it and cannot re-share. The
// rules are enforced by the Vault service on every call (it checks the vault's owners); this file is the tool and the
// plumbing around it. The platform Vault stays administered by the platform administrator.

// personVaultActive reports whether person may own and share vaults: an enabled account that can create things (not a
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
// any connection, or an owner of the vault the connection belongs to. Rechecked on every call, so a change of role or
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

const manageMyVaultsDescription = "Create vaults and share MCP connections through them. A vault is a bundle of MCP connections that you own and other people can use. " +
	"You can own at most 5. Only an owner (a vault can have several) adds, removes or updates its connections and decides who may use it; members use it and cannot re-share. " +
	"Operations: laptops (your connected computers and one-time setup steps; choose where to use them with manage_laptop_access); list (your vaults, with members' emails and connection IDs); apps (the apps Vault can connect, by catalog name); create {name, description}; inspect {vault_id}; " +
	"add_member / remove_member {vault_id, email}; add_owner / remove_owner {vault_id, email}; " +
	"connect {vault_id, provider, label} for a server in the Vault catalog (returns the sign-in link for you to open); " +
	"sign_in {vault_id, connection_id, client_id?, client_secret?} to finish or renew sign-in in chat; " +
	"promote_secret {vault_id, name, confirm} copies a secret of this Crew, Code or workflow into your vault server-side (you never see the value; confirm as for promote); " +
	"set_secret {vault_id, name, value, replace} stores a value the person gave you (replace=true to overwrite an existing name); the answer names the secret and never repeats the value, and you must not either; remove_secret {vault_id, name}; " +
	"sync {vault_id, connection_id}; remove_connection {vault_id, connection_id}; delete {vault_id} (only when it holds no connections or secrets). " +
	"Members get a connection's tools as soon as it is in the vault. A vault owner can give you a secret value to store with set_secret, or run `agentworks secret set NAME` in their terminal, or promote an existing one."

func manageMyVaultsParameters() map[string]interface{} {
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return map[string]interface{}{
		"type": "object", "additionalProperties": false,
		"required": []string{"operation"},
		"properties": map[string]interface{}{
			"operation":      map[string]interface{}{"type": "string", "enum": []string{"list", "laptops", "apps", "create", "inspect", "add_member", "remove_member", "add_owner", "remove_owner", "connect", "sign_in", "promote_secret", "set_secret", "remove_secret", "sync", "remove_connection", "delete"}},
			"vault_id":       str("The vault's ID from list."),
			"name":           str("create: the vault's name. promote: the exact connection name from list_mcp_servers. promote_secret / remove_secret: the secret's name."),
			"description":    str("create: what the vault is for."),
			"email":          str("A person's email, for the member and owner operations."),
			"provider":       str("connect: the catalog server's name."),
			"url":            str("Verified custom MCP URL, with name instead of provider."),
			"label":          str("connect: an account label, e.g. Notion · Engineering."),
			"connection_id":  str("The connection's ID (c-...) from inspect."),
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
	if argString(args, "operation") == "laptops" {
		if !personVaultActive(person) {
			return "", errors.New("Your account cannot manage My Vault")
		}
		devices := api.localDeviceListForOwner(&UserClaims{UserID: person})
		data, err := json.Marshal(map[string]any{"devices": devices, "setup": laptopSetupSteps(len(devices) == 0), "next": "Connect your computer once in My Vault, then select it with manage_laptop_access in any Goal or Crew you build. Each project chooses folders and terminal commands. Steps, schedules and Autopilot never inherit laptop access."})
		return string(data), err
	}
	if _, _, err := capLayerServiceConfig(); err != nil {
		return "", errors.New("Vault is not set up on this server")
	}
	operation := argString(args, "operation")
	vaultID := argString(args, "vault_id")
	connectionID := argString(args, "connection_id")
	call := func(method, path string, body any) ([]byte, error) {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		return vaultPersonRequest(ctx, person, method, path, payload)
	}
	needVault := func() error {
		if vaultID == "" {
			return errors.New("vault_id is required (see list)")
		}
		return nil
	}
	switch operation {
	case "list":
		data, err := call(http.MethodGet, "/api/vaults", nil)
		if err != nil {
			return "", err
		}
		return withMemberEmails(withVaultSignInErrors(data)), nil
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
	case "create":
		name := argString(args, "name")
		if name == "" {
			return "", errors.New("name is required")
		}
		data, err := call(http.MethodPost, "/api/vaults", map[string]string{"Name": name, "Description": argString(args, "description")})
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "inspect":
		if err := needVault(); err != nil {
			return "", err
		}
		data, err := call(http.MethodGet, "/api/vaults/"+url.PathEscape(vaultID), nil)
		if err != nil {
			return "", err
		}
		return withMemberEmails(withVaultSignInErrors(data)), nil
	case "add_member", "remove_member", "add_owner", "remove_owner":
		if err := needVault(); err != nil {
			return "", err
		}
		target, err := directoryUserByEmail(argString(args, "email"))
		if err != nil {
			return "", err
		}
		if err := syncVaultDirectoryUser(ctx, target); err != nil {
			return "", err
		}
		kind := "members"
		if strings.HasSuffix(operation, "_owner") {
			kind = "owners"
		}
		base := "/api/vaults/" + url.PathEscape(vaultID) + "/" + kind
		if strings.HasPrefix(operation, "add_") {
			_, err = call(http.MethodPost, base, map[string]string{"user_id": target.ID})
		} else {
			_, err = call(http.MethodDelete, base+"/"+url.PathEscape(target.ID), nil)
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Done: %s %s.", strings.ReplaceAll(operation, "_", " "), target.Email), nil
	case "connect", "sign_in":
		if err := needVault(); err != nil {
			return "", err
		}
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
		if err := needVault(); err != nil {
			return "", err
		}
		return api.promoteSecretToVault(ctx, person, vaultID, argString(args, "name"), args["confirm"] == true)
	case "set_secret":
		if err := needVault(); err != nil {
			return "", err
		}
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
		if err := needVault(); err != nil {
			return "", err
		}
		if err := api.removeVaultSecret(ctx, person, vaultID, argString(args, "name")); err != nil {
			return "", err
		}
		return "Removed the secret from the vault; its value is deleted.", nil
	case "sync":
		if err := needVault(); err != nil {
			return "", err
		}
		if _, err := call(http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+url.PathEscape(connectionID)+"/sync", nil); err != nil {
			return "", err
		}
		return "Synced; the connection's tools are up to date.", nil
	case "remove_connection":
		if err := needVault(); err != nil {
			return "", err
		}
		if _, err := call(http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+url.PathEscape(connectionID), nil); err != nil {
			return "", err
		}
		mutex := platformMCPOAuthMutex("vault:" + connectionID)
		mutex.Lock()
		advanceVaultOAuthGeneration(connectionID)
		removeVaultCredentialFiles(connectionID)
		mutex.Unlock()
		return "Removed the connection and its sign-in from the vault.", nil
	case "delete":
		if err := needVault(); err != nil {
			return "", err
		}
		if _, err := call(http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID), nil); err != nil {
			return "", err
		}
		return "Deleted the vault.", nil
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

// directoryUserByEmail finds an active platform account by email.
func directoryUserByEmail(email string) (*UserRecord, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, errors.New("email is required")
	}
	dir, err := readUserDirectoryFile()
	if err != nil {
		return nil, errors.New("user directory unavailable")
	}
	user := dir.byEmail(email)
	if user == nil || user.Disabled {
		return nil, errors.New("no active account with that email")
	}
	return user, nil
}

// syncVaultDirectoryUser binds a directory account to the Vault service (the same step the Vault console takes before
// adding a member), so it can be added to a vault.
func syncVaultDirectoryUser(ctx context.Context, user *UserRecord) error {
	payload, _ := json.Marshal(map[string]string{"ID": user.ID, "Email": user.Email})
	if _, err := vaultServiceRequest(ctx, "", http.MethodPost, "/api/admin/users/sync", payload); err != nil {
		return errors.New("could not bind that account to the Vault service")
	}
	return nil
}

// withMemberEmails shows member IDs as emails where the directory knows them.
func withMemberEmails(data []byte) string {
	dir, err := readUserDirectoryFile()
	if err != nil {
		return string(data)
	}
	label := func(id string) string {
		if u := dir.byID(id); u != nil && u.Email != "" {
			return u.Email
		}
		return id
	}
	var generic any
	if json.Unmarshal(data, &generic) != nil {
		return string(data)
	}
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if members, ok := value["members"].([]any); ok {
				emails := make([]string, 0, len(members))
				for _, member := range members {
					if id, ok := member.(string); ok {
						emails = append(emails, label(id))
					}
				}
				sort.Strings(emails)
				value["members"] = emails
			}
			if names, ok := value["secret_names"].([]any); ok {
				short := make([]string, 0, len(names))
				for _, name := range names {
					if full, ok := name.(string); ok {
						short = append(short, vaultSecretShortName(full))
					}
				}
				value["secret_names"] = short
			}
			if group, ok := value["group"].(map[string]any); ok {
				if owners, ok := group["Owners"].([]any); ok {
					emails := make([]string, 0, len(owners))
					for _, owner := range owners {
						if id, ok := owner.(string); ok {
							emails = append(emails, label(id))
						}
					}
					group["Owners"] = emails
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(generic)
	var buffer bytes.Buffer
	enc := json.NewEncoder(&buffer)
	enc.SetEscapeHTML(false)
	if enc.Encode(generic) != nil {
		return string(data)
	}
	return strings.TrimSpace(buffer.String())
}
