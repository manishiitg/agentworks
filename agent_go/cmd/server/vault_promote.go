package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/oauth"
)

// promoteConnectionToVault turns a sign-in connection that already works in this chat's Crew, Code or workflow (or in the
// person's own store) into a shared Vault connection, without a second sign-in (PLAT-503, owner decision 2026-10-05). It
// creates the Vault connection the same way connect_server does, then re-seals the existing login for the new connection:
// token files are bound to their own path, so the bytes are read and written again, never copied. No group is given
// access; that stays a separate, explicit step. The personal connection is left as it is.
//
// Only an active Vault administrator, only for a connection that person signed in themselves, and only after the caller
// confirmed: once it is in Vault, everyone in a group that is later granted it acts as the account that signed in.
func (api *StreamingAPI) promoteConnectionToVault(ctx context.Context, person, name string, confirm bool) (string, error) {
	if !vaultAdminActive(person) {
		return "", errors.New("Vault management requires an administrator account")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is required: the exact connection name from list_mcp_servers")
	}
	store, serverName, err := api.promotableConnection(ctx, person, name)
	if err != nil {
		return "", err
	}
	servers, err := listPlaceMCPServers(store)
	if err != nil {
		return "", err
	}
	var server placeMCPServer
	for _, candidate := range servers {
		if candidate.Name == serverName {
			server = candidate
			break
		}
	}
	if server.Name == "" {
		return "", fmt.Errorf("connection %q was not found", name)
	}
	if server.OAuth == nil {
		return "", errors.New("only sign-in (OAuth) connections can be promoted; for a key or header connection add the secret to Vault instead")
	}
	_, cfg, err := placeMCPServerConfig(store, serverName)
	if err != nil || cfg.OAuth == nil {
		if err == nil {
			err = errors.New("connection has no sign-in configuration")
		}
		return "", fmt.Errorf("read connection %q: %w", name, err)
	}
	token, err := oauth.ReadTokenFile(cfg.OAuth.TokenFile)
	if err != nil || len(token) == 0 {
		return "", fmt.Errorf("connection %q is not signed in here yet; sign in first, then promote it", name)
	}
	if !confirm {
		return fmt.Sprintf("Promoting %q copies its existing sign-in into Vault as a new shared connection (no new sign-in). Anyone in a group you later grant it to will use it as the account that signed in. No group is given access by this step, and the personal connection stays as it is. Call promote_place_connection again with confirm=true to go ahead.", name), nil
	}

	provider := strings.TrimSpace(server.Catalog)
	created := ""
	var createErr error
	if provider != "" {
		created, createErr = capLayerAgentAccess(ctx, person, "connect_server", vaultPromoteJSON(map[string]string{"provider": provider, "label": server.Label}))
	}
	if provider == "" || createErr != nil {
		created, createErr = capLayerAgentAccess(ctx, person, "connect_server", vaultPromoteJSON(map[string]string{"name": server.Name, "url": server.URL}))
	}
	if createErr != nil {
		return "", fmt.Errorf("create the Vault connection: %w", createErr)
	}
	var made struct {
		Connector struct {
			ID string `json:"id"`
		} `json:"connector"`
	}
	if json.Unmarshal([]byte(created), &made) != nil || !vaultConnectionID.MatchString(made.Connector.ID) {
		return "", errors.New("Vault created a connection but returned no usable connection ID")
	}
	id := made.Connector.ID
	connection, err := vaultConnection(ctx, id)
	if err != nil {
		return "", fmt.Errorf("new Vault connection %s: %w", id, err)
	}
	template, err := api.vaultOAuthTemplate(ctx, id, connection.OAuthServer, "")
	if err != nil {
		return "", fmt.Errorf("new Vault connection %s: %w", id, err)
	}

	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	oc := *template.OAuth
	oc.ClientID, oc.ClientSecret, oc.ClientSecretFile = cfg.OAuth.ClientID, cfg.OAuth.ClientSecret, ""
	oc.RedirectURL = cfg.OAuth.RedirectURL
	if len(cfg.OAuth.Scopes) > 0 {
		oc.Scopes = cfg.OAuth.Scopes
	}
	oc.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id))
	writeErr := func() error {
		data, err := json.Marshal(oc)
		if err != nil {
			return err
		}
		if err := oauth.WriteTokenFile(vaultCredentialConfigPath(id), data); err != nil {
			return err
		}
		if dir, dirErr := placeMCPDir(store); dirErr == nil {
			if client, _ := readPlaceMCPClient(dir, store, serverName); client != nil {
				clientData, err := json.Marshal(client)
				if err != nil {
					return err
				}
				if err := oauth.WriteTokenFile(getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id)), clientData); err != nil {
					return err
				}
			}
		}
		return oauth.WriteTokenFile(oc.TokenFile, token)
	}()
	if writeErr != nil {
		removeVaultCredentialFiles(id)
	}
	mutex.Unlock()
	if writeErr != nil {
		return "", fmt.Errorf("Vault connection %s was created but the sign-in could not be stored (%w); it is unsigned and can be signed in or removed in Vault", id, writeErr)
	}
	log.Printf("[VAULT_PROMOTE] %s promoted connection %q (store %s) to Vault connection %s", person, serverName, store, id)
	if _, err := vaultServiceRequest(ctx, person, http.MethodPost, "/api/admin/connectors/"+id+"/sync", nil); err != nil {
		return fmt.Sprintf("Created Vault connection %s with the existing sign-in, but tool discovery failed (%v). Run sync_connection with connection_id %s. No group has access yet.", id, err, id), nil
	}
	return fmt.Sprintf("Promoted %q to Vault connection %s with its existing sign-in; tools were discovered. No group has access yet: inspect the connection, then grant a group (save_permissions). The personal connection is unchanged. Some providers rotate refresh tokens, so if either copy stops working, sign that one in again.", name, id), nil
}

// promotableConnection finds the store and exact name of the sign-in connection the person means: the one attached to
// this chat's Crew, Code or workflow (it must be theirs), else one in their own store.
func (api *StreamingAPI) promotableConnection(ctx context.Context, person, name string) (store, serverName string, err error) {
	if place := api.placeRootForSession(executor.SessionIDFromContext(ctx)); place != "" {
		if a, found := placeAttachmentNamed(place, name); found {
			if a.Owner != person {
				return "", "", errors.New("only the person who signed this connection in can promote it")
			}
			return attachmentStore(a, place), a.Server, nil
		}
	}
	saved, found, lookupErr := lookupPersonalMCP(person, name)
	if lookupErr != nil {
		return "", "", lookupErr
	}
	if !found {
		return "", "", fmt.Errorf("no connection named %q; use its exact name from list_mcp_servers", name)
	}
	return person, saved.Name, nil
}

func vaultPromoteJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
