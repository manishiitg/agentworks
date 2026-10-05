package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// These records contain provider registration metadata, not group permissions.
// Both metadata and tokens use the platform's existing sealed credential store.
type vaultOAuthConnection struct {
	ID, OAuthServer, OAuthCredentialID, UpstreamURL, Label, Status string
	// VaultID is the person-owned vault the connection belongs to (PLAT-507); empty for a platform connection.
	VaultID string
}

var vaultConnectionID = regexp.MustCompile(`^c-[a-f0-9]{8,32}$`)
var vaultOAuthGenerations sync.Map         // connection ID -> uint64; accessed under its OAuth mutex
func vaultCredentialName(id string) string { return "vault_" + id }
func vaultCredentialConfigPath(id string) string {
	return getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)+".connection")
}

func vaultServiceRequest(ctx context.Context, actor, method, path string, payload []byte) ([]byte, error) {
	return vaultServiceRequestAs(ctx, actor, false, method, path, payload)
}

// vaultPersonRequest is a call on behalf of a person the platform already authenticated: the vault routes act only for
// that person and check the vault's owners (PLAT-507).
func vaultPersonRequest(ctx context.Context, person, method, path string, payload []byte) ([]byte, error) {
	return vaultServiceRequestAs(ctx, person, true, method, path, payload)
}

func vaultServiceRequestAs(ctx context.Context, actor string, platformUser bool, method, path string, payload []byte) ([]byte, error) {
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		return nil, errors.New("Vault service is not configured")
	}
	target.Path = strings.TrimRight(target.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req.Header.Set("X-CapLayer-Actor", actor)
	}
	if platformUser {
		req.Header.Set("X-Vault-Platform-User", "1")
	}
	client := &http.Client{Transport: capLayerTransport, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Vault service unavailable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(body) > 2*1024*1024 {
		return nil, errors.New("invalid Vault response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The Vault service's own refusal text (for example "only an owner of this vault can do that") helps the caller.
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &refusal) == nil && strings.TrimSpace(refusal.Error) != "" && len(refusal.Error) <= 300 {
			return nil, fmt.Errorf("Vault operation failed (%d): %s", resp.StatusCode, strings.TrimSpace(refusal.Error))
		}
		return nil, fmt.Errorf("Vault operation failed (%d)", resp.StatusCode)
	}
	return body, nil
}
func vaultConnection(ctx context.Context, id string) (vaultOAuthConnection, error) {
	var c vaultOAuthConnection
	if !vaultConnectionID.MatchString(id) {
		return c, errors.New("invalid connection ID")
	}
	data, err := vaultServiceRequest(ctx, "", http.MethodGet, "/api/admin/connectors/"+id, nil)
	if err != nil {
		return c, err
	}
	if json.Unmarshal(data, &c) != nil || c.ID != id || c.OAuthCredentialID != id || c.OAuthServer == "" || (c.Status != "active" && c.Status != "authentication_required") {
		return c, errors.New("OAuth connection unavailable")
	}
	return c, nil
}
func (api *StreamingAPI) vaultOAuthTemplate(ctx context.Context, id, name, resource string) (mcpclient.MCPServerConfig, error) {
	c, err := vaultConnection(ctx, id)
	if err != nil {
		return mcpclient.MCPServerConfig{}, err
	}
	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return mcpclient.MCPServerConfig{}, err
	}
	canonical, cfg, err := config.ResolveServer(c.OAuthServer)
	if err != nil || cfg.OAuth == nil || canonical != name || cfg.URL != c.UpstreamURL || (resource != "" && cfg.URL != resource) {
		return mcpclient.MCPServerConfig{}, errors.New("connection does not match the configured OAuth provider")
	}
	copied := *cfg.OAuth
	cfg.OAuth = &copied
	cfg.OAuth.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id))
	return cfg, nil
}
func (api *StreamingAPI) vaultOAuthConfig(ctx context.Context, id, name, resource string) (mcpclient.MCPServerConfig, error) {
	cfg, err := api.vaultOAuthTemplate(ctx, id, name, resource)
	if err != nil {
		return cfg, err
	}
	data, err := oauth.ReadTokenFile(vaultCredentialConfigPath(id))
	if err != nil {
		return cfg, errors.New("sign in to this connection first")
	}
	var saved oauth.OAuthConfig
	if json.Unmarshal(data, &saved) != nil || saved.AuthURL != cfg.OAuth.AuthURL || saved.TokenURL != cfg.OAuth.TokenURL || saved.Resource != cfg.OAuth.Resource {
		return cfg, errors.New("connection OAuth configuration changed; sign in again")
	}
	// Ignore saved paths: the live connector ID owns all file locations.
	saved.TokenFile = cfg.OAuth.TokenFile
	saved.ClientSecretFile = ""
	cfg.OAuth = &saved
	return cfg, nil
}
func vaultAdminActive(userID string) bool {
	claims := &UserClaims{UserID: userID}
	access := userAccessForClaims(claims)
	return userID != "" && access.Admin && !access.Disabled && userAllowedProduct(claims, "mcp-gateway")
}
func advanceVaultOAuthGeneration(id string) uint64 {
	value, _ := vaultOAuthGenerations.Load(id)
	n, _ := value.(uint64)
	n++
	vaultOAuthGenerations.Store(id, n)
	return n
}
func (api *StreamingAPI) beginVaultConnectionOAuth(ctx context.Context, userID, sessionID, id, name, redirect, clientID, clientSecret string) (*OAuthStartResponse, *OAuthDiscoveryResponse, error) {
	if !api.vaultActorCanManage(ctx, userID, id) {
		return nil, nil, errors.New("Vault management requires an administrator account or ownership of this vault")
	}
	if _, err := api.vaultOAuthTemplate(ctx, id, name, ""); err != nil {
		return nil, nil, err
	}
	// Drop the previous account's session outside the credential mutex.
	if _, err := vaultServiceRequest(ctx, userID, http.MethodPost, "/api/admin/connectors/"+id+"/oauth/disconnect", nil); err != nil {
		return nil, nil, err
	}
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	defer mutex.Unlock()
	cfg, err := api.vaultOAuthTemplate(ctx, id, name, "")
	if err != nil {
		return nil, nil, err
	}
	if redirect == "" {
		return nil, nil, errors.New("connect from the UI or configure PUBLIC_URL for chat sign-in")
	}
	if cfg.OAuth.AuthURL == "" || cfg.OAuth.TokenURL == "" {
		return nil, nil, errors.New("provider OAuth endpoints are not configured")
	}
	// Reuse only this connection's registration. Never reuse another account's token.
	if saved, e := api.vaultOAuthConfig(ctx, id, name, ""); e == nil {
		cfg = saved
	} else if cfg.OAuth.RegistrationEndpoint != "" {
		cfg.OAuth.ClientID = ""
		cfg.OAuth.ClientSecret = ""
		cfg.OAuth.ClientSecretFile = ""
	}
	if clientID != "" {
		cfg.OAuth.ClientID = clientID
		cfg.OAuth.ClientSecret = strings.TrimSpace(clientSecret)
		cfg.OAuth.ClientSecretFile = ""
	}
	if cfg.OAuth.ClientID == "" {
		config, e := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
		if e == nil {
			if key := mcpAppKeyIndex(config.MCPServers)[name]; key != "" {
				if app, e := readMCPApp(key); e == nil && app != nil {
					cfg.OAuth.ClientID = app.ClientID
					cfg.OAuth.ClientSecret = app.ClientSecret
					cfg.OAuth.ClientSecretFile = ""
				}
			}
		}
	}
	if cfg.OAuth.ClientSecretFile != "" {
		secret, e := oauth.ReadClientSecretFile(cfg.OAuth.ClientSecretFile)
		if e != nil {
			return nil, nil, e
		}
		cfg.OAuth.ClientSecret = secret
		cfg.OAuth.ClientSecretFile = ""
	}
	cfg.OAuth.RedirectURL = redirect
	if requiresRegisteredMCPClientSecret(name) && (cfg.OAuth.ClientID == "" || !hasRegisteredMCPClientSecret(cfg.OAuth)) {
		return nil, &OAuthDiscoveryResponse{Status: "needs_client_id", ServerName: name, RedirectURI: redirect, NeedsClientSecret: true, Message: "Enter the OAuth app credentials in this connection’s sign-in form."}, nil
	}
	generation := advanceVaultOAuthGeneration(id)
	notificationID := "vault-oauth:" + id + ":" + newSteerMessageID()
	start, discovery, flowErr := api.runOAuthFlow(sessionID, redirect, oauthFlowTarget{
		Name: name, Config: cfg, ClientFile: getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id)), ConnectionID: id, LockKey: "vault:" + id,
		BeforeExchange: func(flow *OAuthFlowState) error {
			latest, _ := vaultOAuthGenerations.Load(id)
			if latest != generation || !api.vaultActorCanManage(context.Background(), userID, id) {
				return errors.New("sign-in was cancelled or administrator access changed")
			}
			if _, err := api.vaultOAuthTemplate(context.Background(), id, name, cfg.URL); err != nil {
				return err
			}
			data, err := json.Marshal(flow.ServerConfig.OAuth)
			if err != nil {
				return err
			}
			return oauth.WriteTokenFile(vaultCredentialConfigPath(id), data)
		},
		AfterExchange: func(flow *OAuthFlowState) error {
			if !api.vaultActorCanManage(context.Background(), userID, id) {
				return errors.New("administrator or vault ownership changed")
			}
			// Sync runs outside the credential mutex: upstream discovery calls
			// the broker, which uses that same mutex to serialize refresh.
			_, err := vaultServiceRequest(context.Background(), userID, http.MethodPost, "/api/admin/connectors/"+id+"/sync", nil)
			return err
		},
		Notify: func(success bool, detail string) {
			if sessionID == "" {
				return
			}
			status := "completed"
			message := fmt.Sprintf("Vault connection %q (%s) is signed in and tools were discovered. No group access was assigned. Inspect this connection before assigning its tools to a group.", name, id)
			if !success {
				status = "failed"
				message = fmt.Sprintf("Vault sign-in for connection %s failed: %s. Report this and offer to retry this connection.", id, detail)
			}
			api.emitSyntheticTurnReady(sessionID, notificationID, name, status, message)
			// The shared browser chat queue owns continuation. It reconstructs the
			// product query after a restart and queues behind any running user turn.

		},
	})
	if flowErr == nil && start != nil {
		_ = os.Remove(cfg.OAuth.TokenFile)
	}
	return start, discovery, flowErr
}
func (api *StreamingAPI) vaultConnectionOAuthStatus(w http.ResponseWriter, r *http.Request, id, name string) {
	w.Header().Set("Cache-Control", "no-store")
	if state := r.URL.Query().Get("state"); state != "" {
		oauthFlowsMu.RLock()
		flow := oauthFlows[state]
		outcome := ""
		matches := flow != nil && flow.ConnectionID == id
		if matches {
			outcome = flow.Outcome
		}
		oauthFlowsMu.RUnlock()
		if !matches || outcome != "completed" {
			writeUsersJSON(w, 200, map[string]any{"server_name": name, "valid": false, "flow_status": outcome})
			return
		}
	}
	cfg, err := api.vaultOAuthConfig(r.Context(), id, name, "")
	if err != nil {
		writeUsersJSON(w, 200, map[string]any{"server_name": name, "valid": false})
		return
	}
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	defer mutex.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	token, err := oauth.NewManager(cfg.OAuth, api.logger).GetAccessToken(ctx)
	writeUsersJSON(w, 200, map[string]any{"server_name": name, "valid": err == nil && token != "", "has_oauth": true})
}
func removeVaultCredentialFiles(id string) {
	for _, path := range []string{getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)), vaultCredentialConfigPath(id), getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id))} {
		_ = os.Remove(path)
	}
}
func (api *StreamingAPI) logoutVaultConnection(ctx context.Context, userID, id, name string) error {
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	if _, err := api.vaultOAuthTemplate(ctx, id, name, ""); err != nil {
		mutex.Unlock()
		return err
	}
	advanceVaultOAuthGeneration(id)
	removeVaultCredentialFiles(id)
	mutex.Unlock()
	_, err := vaultServiceRequest(ctx, userID, http.MethodPost, "/api/admin/connectors/"+id+"/oauth/disconnect", nil)
	return err
}

// Chat uses the exact same connection OAuth flow as the shared UI.
func (api *StreamingAPI) capLayerConnectionAccess(ctx context.Context, userID, operation string, args json.RawMessage) (string, error) {
	if !vaultAdminActive(userID) {
		return "", errors.New("Vault management requires an administrator account")
	}
	if operation != "sign_in_connection" && operation != "connection_status" && operation != "sync_connection" && operation != "disconnect_connection" {
		return capLayerAgentAccess(ctx, userID, operation, args)
	}
	var in struct {
		ConnectionID string `json:"connection_id"`
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return "", errors.New("provide only connection_id")
	}
	c, err := vaultConnection(ctx, in.ConnectionID)
	if err != nil {
		return "", err
	}
	var result any
	switch operation {
	case "sign_in_connection":
		start, discovery, err := api.beginVaultConnectionOAuth(ctx, userID, chatSessionIDFromContext(ctx), c.ID, c.OAuthServer, deriveOAuthRedirectURIFromEnv(), "", "")
		if err != nil {
			return "", err
		}
		if discovery != nil {
			result = discovery
		} else {
			result = start
		}
	case "sync_connection":
		return capLayerAgentRequest(ctx, userID, "/api/admin/connectors/"+c.ID+"/sync", nil)
	case "disconnect_connection":
		if _, err := vaultServiceRequest(ctx, userID, http.MethodDelete, "/api/admin/connectors/"+c.ID, nil); err != nil {
			return "", err
		}
		mutex := platformMCPOAuthMutex("vault:" + c.ID)
		mutex.Lock()
		advanceVaultOAuthGeneration(c.ID)
		removeVaultCredentialFiles(c.ID)
		mutex.Unlock()
		return `{"disconnected":true,"group_permissions_removed":true}`, nil
	case "connection_status":
		valid := false
		cfg, err := api.vaultOAuthConfig(ctx, c.ID, c.OAuthServer, c.UpstreamURL)
		if err == nil {
			mutex := platformMCPOAuthMutex("vault:" + c.ID)
			mutex.Lock()
			token, e := oauth.NewManager(cfg.OAuth, api.logger).GetAccessToken(ctx)
			mutex.Unlock()
			valid = e == nil && token != ""
		}
		result = map[string]any{"connection": c, "authenticated": valid, "connected": valid && c.Status == "active"}
	}
	data, err := json.Marshal(result)
	return string(data), err
}
