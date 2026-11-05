package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/mcpagent/oauth"
	oauth2 "golang.org/x/oauth2"
)

// Move a signed-in Vault app from the Mac to the person's own Vault on their company's server (Vault step 5B, PLAT-856).
// It is a move, not a copy: two copies renewing the same sign-in log each other out when the provider rotates its
// refresh token (the reason desktop_provider_accounts.go does not copy self-renewing logins). The Mac opens its three
// sealed credential files (the token, the sign-in setup and the app registration; each is sealed to its own path, so
// only the plaintext can move) and sends them over the person's saved server sign-in. The server makes the app in the
// person's "My accounts" Vault, seals the files under its own connection id and renews the sign-in once with them. Only
// when that renewal works does the Mac delete its copy; otherwise the server deletes what it made and the Mac keeps
// its app. Values are never logged.

// vaultMoveRequest is what the Mac sends: the Mac app's provider details and its opened credential files.
type vaultMoveRequest struct {
	Label       string          `json:"label"`
	UpstreamURL string          `json:"upstream_url"`
	OAuthServer string          `json:"oauth_server"`
	Token       json.RawMessage `json:"token"`
	Config      json.RawMessage `json:"config"`
	Client      json.RawMessage `json:"client,omitempty"`
}

// vaultMoveAppName is the app's name in the plain lines: the catalog app, or a custom app's label.
func vaultMoveAppName(oauthServer, label string) string {
	if isCustomVaultOAuthServer(oauthServer) {
		return firstNonEmptyTrimmed(label, strings.TrimPrefix(oauthServer, customVaultOAuthPrefix), "This app")
	}
	return firstNonEmptyTrimmed(oauthServer, label, "This app")
}

func vaultMoveRefusedLine(app string) string {
	return app + " wouldn't move this sign-in. Sign in to " + app + " once on the server; your Mac keeps its own sign-in."
}

// errVaultMoveRefused means the server could not renew the sign-in with the moved files (or its setup for this app
// differs), so the person signs in on the server instead.
var errVaultMoveRefused = errors.New("sign-in not movable")

// POST /api/vault/me/connections/import (the company server, the person's own sign-in from their Mac)
// -> 200 {"id", "label", "synced"} | 409 {"error": "<App> wouldn't move this sign-in. ...", "open_vault": true}
func (api *StreamingAPI) handleVaultMeImport(w http.ResponseWriter, r *http.Request) {
	person, ok := vaultMeCaller(w, r)
	if !ok {
		return
	}
	// Credentials come from the person's Mac (its server makes this request), never from a web page.
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeUsersError(w, http.StatusForbidden, "Apps are moved here from your Mac.")
		return
	}
	if !personVaultActive(person) || !vaultConfigured() {
		writeUsersError(w, http.StatusForbidden, "Your account can't keep its own Vault apps on this server.")
		return
	}
	var in vaultMoveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(in.OAuthServer) == "" || strings.TrimSpace(in.UpstreamURL) == "" ||
		len(in.Token) == 0 || len(in.Config) == 0 || len(in.Label) > 100 {
		writeUsersError(w, http.StatusBadRequest, "Move a signed-in app from your Mac's Vault.")
		return
	}
	app := vaultMoveAppName(in.OAuthServer, in.Label)
	id, label, synced, err := api.importVaultConnection(r.Context(), person, in)
	switch {
	case errors.Is(err, errVaultMoveRefused):
		log.Printf("[VAULT_MOVE] user=%s provider=%s result=refresh_failed", person, in.OAuthServer)
		writeUsersJSON(w, http.StatusConflict, map[string]any{"error": vaultMoveRefusedLine(app), "open_vault": true})
	case err != nil:
		log.Printf("[VAULT_MOVE] user=%s provider=%s result=error err_type=%T", person, in.OAuthServer, err)
		writeUsersJSON(w, http.StatusBadGateway, map[string]any{"error": app + " couldn't be added to your Vault on this server; try again. Your Mac keeps its own sign-in.", "open_vault": true})
	default:
		log.Printf("[VAULT_MOVE] user=%s provider=%s conn=%s result=moved synced=%t", person, in.OAuthServer, id, synced)
		writeUsersJSON(w, http.StatusOK, map[string]any{"id": id, "label": label, "synced": synced})
	}
}

// importVaultConnection makes the person's own app here with the moved files and renews its sign-in once. Anything
// short of a renewed sign-in removes what it made.
func (api *StreamingAPI) importVaultConnection(ctx context.Context, person string, in vaultMoveRequest) (string, string, bool, error) {
	vaultID, err := ensurePersonalVault(ctx, person)
	if err != nil {
		return "", "", false, err
	}
	label := strings.TrimSpace(in.Label)
	payload := map[string]any{"Provider": in.OAuthServer, "Label": label}
	if isCustomVaultOAuthServer(in.OAuthServer) {
		payload = map[string]any{"Import": true, "OAuth": true, "Name": strings.TrimPrefix(in.OAuthServer, customVaultOAuthPrefix), "URL": in.UpstreamURL, "Label": label}
	}
	data, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors", vaultPromoteJSON(payload))
	if err != nil {
		return "", "", false, err
	}
	var made struct{ ID, Label string }
	if json.Unmarshal(data, &made) != nil || !vaultConnectionID.MatchString(made.ID) {
		return "", "", false, errors.New("Vault returned no connection ID")
	}
	id := made.ID
	mutex := platformMCPOAuthMutex("vault:" + id)
	undo := func() {
		mutex.Lock()
		advanceVaultOAuthGeneration(id)
		removeVaultCredentialFiles(id)
		mutex.Unlock()
		if _, err := vaultPersonRequest(context.WithoutCancel(ctx), person, http.MethodDelete, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+url.PathEscape(id), nil); err != nil {
			log.Printf("[VAULT_MOVE] user=%s conn=%s undo failed err_type=%T", person, id, err)
		}
	}
	if err := api.proveMovedSignIn(ctx, id, in); err != nil {
		undo()
		return "", "", false, err
	}
	// The renewed sign-in now lives only here: from this point nothing is undone (the provider may have rotated the
	// Mac's refresh token). The tool sync makes the app active; if it fails, Refresh in Vault retries it.
	_, syncErr := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors/"+url.PathEscape(id)+"/sync", nil)
	if syncErr != nil {
		log.Printf("[VAULT_MOVE] user=%s conn=%s tool sync after move failed err_type=%T", person, id, syncErr)
	}
	return id, firstNonEmptyTrimmed(made.Label, label), syncErr == nil, nil
}

// proveMovedSignIn writes the moved files under connection id (sealed to their new paths) and renews the sign-in once
// with them, through the same reader and refresh the Vault broker uses.
func (api *StreamingAPI) proveMovedSignIn(ctx context.Context, id string, in vaultMoveRequest) error {
	c, err := vaultConnection(ctx, id)
	if err != nil {
		return err
	}
	if c.UpstreamURL != in.UpstreamURL {
		return fmt.Errorf("%w: upstream differs", errVaultMoveRefused)
	}
	template, err := api.vaultOAuthTemplate(ctx, id, c.OAuthServer, c.UpstreamURL)
	if err != nil {
		return fmt.Errorf("%w: setup unavailable", errVaultMoveRefused)
	}
	var saved oauth.OAuthConfig
	if json.Unmarshal(in.Config, &saved) != nil || saved.AuthURL != template.OAuth.AuthURL || saved.TokenURL != template.OAuth.TokenURL || saved.Resource != template.OAuth.Resource {
		return fmt.Errorf("%w: sign-in setup differs", errVaultMoveRefused)
	}
	saved.TokenFile = template.OAuth.TokenFile
	saved.ClientSecretFile = ""
	var token oauth2.Token
	if json.Unmarshal(in.Token, &token) != nil || token.RefreshToken == "" {
		return fmt.Errorf("%w: no refresh token", errVaultMoveRefused)
	}
	// Mark the moved token expired, so the one renewal below must use the moved refresh token.
	token.Expiry = time.Now().Add(-time.Hour)
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	defer mutex.Unlock()
	config, _ := json.Marshal(saved)
	if err := oauth.WriteTokenFile(vaultCredentialConfigPath(id), config); err != nil {
		return err
	}
	if len(in.Client) > 0 && string(in.Client) != "null" {
		var client registeredClient
		if json.Unmarshal(in.Client, &client) != nil {
			return errors.New("invalid app registration")
		}
		raw, _ := json.Marshal(client)
		if err := oauth.WriteTokenFile(getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id)), raw); err != nil {
			return err
		}
	}
	if err := oauth.NewTokenStore(saved.TokenFile).Save(&token); err != nil {
		return err
	}
	cfg, err := api.vaultOAuthConfig(ctx, id, c.OAuthServer, c.UpstreamURL)
	if err != nil {
		return fmt.Errorf("%w: setup unreadable", errVaultMoveRefused)
	}
	renewCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if access, err := oauth.NewManager(cfg.OAuth, api.logger).GetAccessToken(renewCtx); err != nil || access == "" {
		return fmt.Errorf("%w: renewal failed", errVaultMoveRefused)
	}
	return nil
}

// POST /api/desktop-connections/{connection}/vault-apps/{id}/move (the Mac app; the person's own saved server sign-in)
// -> 200 {"moved": true, "server_id", "label"}
// -> 4xx/5xx {"moved": false, "error": "<plain line>", "open_vault_url"?: "<server>/vault"}
func (api *StreamingAPI) handleDesktopVaultMove(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !IsLocalMode() {
		desktopJSONError(w, http.StatusNotFound, "Apps move from the Mac app.")
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	c, err := desktopConnectionFor(owner, mux.Vars(r)["connection"])
	if err != nil {
		desktopJSONError(w, http.StatusNotFound, "Connection is unavailable. Connect to the server again.")
		return
	}
	status, out := api.moveVaultAppToServer(r.Context(), owner, c, mux.Vars(r)["id"])
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

// moveVaultAppToServer moves one of the person's signed-in Vault apps on this Mac to their Vault on c's server. The
// app's sign-in lock is held throughout, so this Mac never renews the sign-in while it moves.
func (api *StreamingAPI) moveVaultAppToServer(ctx context.Context, owner string, c desktopConnection, id string) (int, map[string]any) {
	refuse := func(status int, line string) (int, map[string]any) {
		return status, map[string]any{"moved": false, "error": line}
	}
	// Apps that run on this Mac (its own commands) are not Vault connections.
	if !vaultConnectionID.MatchString(id) {
		return refuse(http.StatusBadRequest, "Runs on your Mac; it can't move.")
	}
	conn, err := vaultSavedConnection(ctx, id)
	if err != nil {
		return refuse(http.StatusNotFound, "This app isn't in your Vault on this Mac.")
	}
	if !api.vaultActorCanManage(ctx, owner, id) {
		return refuse(http.StatusForbidden, "Only the app's owner can move it.")
	}
	if conn.OAuthServer == "" || conn.OAuthCredentialID != id {
		return refuse(http.StatusBadRequest, "Only apps you signed in to can move to the server.")
	}
	app := vaultMoveAppName(conn.OAuthServer, conn.Label)
	logMove := func(result string) {
		log.Printf("[VAULT_MOVE] user=%s conn=%s server=%s provider=%s result=%s", owner, id, serverVaultConnShort(c.ID), conn.OAuthServer, result)
	}
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	locked := true
	defer func() {
		if locked {
			mutex.Unlock()
		}
	}()
	token, tokenErr := oauth.ReadTokenFile(getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)))
	rawConfig, configErr := oauth.ReadTokenFile(vaultCredentialConfigPath(id))
	var config oauth.OAuthConfig
	if tokenErr != nil || configErr != nil || json.Unmarshal(rawConfig, &config) != nil {
		return refuse(http.StatusConflict, "Sign in to "+app+" on this Mac first, or sign in to it on the server.")
	}
	// The setup moves without this Mac's file paths; a client secret kept in its own file travels inline.
	if config.ClientSecret == "" && config.ClientSecretFile != "" {
		if secret, err := oauth.ReadClientSecretFile(config.ClientSecretFile); err == nil {
			config.ClientSecret = secret
		}
	}
	config.TokenFile, config.ClientSecretFile = "", ""
	cleanConfig, _ := json.Marshal(config)
	in := vaultMoveRequest{Label: conn.Label, UpstreamURL: conn.UpstreamURL, OAuthServer: conn.OAuthServer, Token: token, Config: cleanConfig}
	if client, err := oauth.ReadTokenFile(getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id))); err == nil && len(client) > 0 {
		in.Client = client
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		logMove("error")
		return refuse(http.StatusConflict, "Sign in to "+app+" on this Mac first, or sign in to it on the server.")
	}
	body, _ := json.Marshal(in)
	moveCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(moveCtx, http.MethodPost, strings.TrimRight(c.Server, "/")+"/api/vault/me/connections/import", bytes.NewReader(body))
	if err != nil {
		logMove("error")
		return refuse(http.StatusBadRequest, "Connect to the server again.")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := crewTeamHTTPClient(moveCtx).Do(req)
	if err != nil {
		logMove("error")
		return refuse(http.StatusBadGateway, "Your company's server can't be reached right now; try again when this Mac is online. "+app+" stays on your Mac.")
	}
	defer resp.Body.Close()
	if err := saveRenewedDesktopToken(owner, c, resp.Header.Get("X-Renewed-Token")); err != nil {
		log.Printf("[VAULT_MOVE] user=%s server=%s renewed sign-in not saved", owner, serverVaultConnShort(c.ID))
	}
	var answer struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Error     string `json:"error"`
		OpenVault bool   `json:"open_vault"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)
	if resp.StatusCode != http.StatusOK || !vaultConnectionID.MatchString(answer.ID) {
		result, status, line := "error", http.StatusBadGateway, strings.TrimSpace(answer.Error)
		switch {
		case resp.StatusCode == http.StatusConflict:
			result, status = "refresh_failed", http.StatusConflict
		case resp.StatusCode == http.StatusUnauthorized:
			status, line = http.StatusUnauthorized, "Sign in to "+firstNonEmptyTrimmed(c.Name, "your company's server")+" again from Settings, then try again."
		case resp.StatusCode == http.StatusNotFound:
			line = firstNonEmptyTrimmed(c.Name, "Your company's server") + " can't take Vault apps yet; update it first."
		}
		if line == "" {
			line = app + " couldn't move; try again. Your Mac keeps its own sign-in."
		}
		logMove(result)
		out := map[string]any{"moved": false, "error": line}
		if answer.OpenVault {
			out["open_vault_url"] = strings.TrimRight(c.Server, "/") + "/vault"
		}
		return status, out
	}
	// The server renewed the sign-in: from now on only it renews. Remove this Mac's copy, then its app.
	advanceVaultOAuthGeneration(id)
	removeVaultCredentialFiles(id)
	mutex.Unlock()
	locked = false
	var removeErr error
	if conn.VaultID != "" {
		_, removeErr = vaultPersonRequest(ctx, owner, http.MethodDelete, "/api/vaults/"+url.PathEscape(conn.VaultID)+"/connectors/"+url.PathEscape(id), nil)
	} else {
		_, removeErr = vaultServiceRequest(ctx, owner, http.MethodDelete, "/api/admin/connectors/"+id, nil)
	}
	if removeErr != nil {
		log.Printf("[VAULT_MOVE] user=%s conn=%s local app not removed err_type=%T", owner, id, removeErr)
	}
	logMove("moved")
	api.retickMovedVaultApp(ctx, owner, id, serverVaultName(c.ID, answer.ID))
	return http.StatusOK, map[string]any{"moved": true, "server_id": answer.ID, "label": firstNonEmptyTrimmed(answer.Label, conn.Label)}
}

// retickMovedVaultApp keeps a moved app chosen where it was: a Crew on "chosen apps only" that picked the Mac's app
// now picks the same app from the server (reviewer, 5 Nov), so nobody re-picks it.
func (api *StreamingAPI) retickMovedVaultApp(ctx context.Context, owner, oldID, serverName string) {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return
	}
	old := vaultServerName(oldID)
	for _, rec := range records {
		if rec.OwnerID != owner {
			continue
		}
		root := crewProjectRootByID(rec.ProjectID)
		if root == "" {
			continue
		}
		selected, chosenOnly, err := projectAppSelection(ctx, root)
		if err != nil || !chosenOnly || !containsExactString(selected, old) {
			continue
		}
		err = api.setProjectSelectedServers(ctx, root, func(current []string) []string {
			out := []string{}
			for _, name := range current {
				if name != old {
					out = append(out, name)
				}
			}
			if !containsExactString(out, serverName) {
				out = append(out, serverName)
			}
			return out
		})
		log.Printf("[VAULT_MOVE] user=%s project=%s reticked=%s ok=%t", owner, rec.ProjectID, serverName, err == nil)
	}
}
