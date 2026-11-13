package server

// "Move my Mac login to the server" (PLAT-856-441, owner 13 Nov: "move mac login is fine.. least friction for user who
// has a local mac app setup"). A Codex login renews with a single-use ticket, so two copies of one login file sign each
// other out; a move keeps exactly one copy. The Mac sends its login file to the person's own model proxy on the server
// and deletes its own copy; from then on the Mac and the online agents both use it through the server
// (personal_server_login.go), with no second sign-in.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// codexLoginFile is where an account's Codex keeps its login: $CODEX_HOME/auth.json, else $HOME/.codex/auth.json.
func codexLoginFile(target providerAccountTarget) string {
	codexHome := envValue(target.Env, "CODEX_HOME")
	if codexHome == "" {
		home := envValue(target.Env, "HOME")
		if home == "" {
			home = target.Home
		}
		codexHome = filepath.Join(home, ".codex")
	}
	return filepath.Join(codexHome, "auth.json")
}

// jwtClaims reads a token's claims without checking its signature: for naming the login only.
func jwtClaims(token string, into any) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	return err == nil && json.Unmarshal(payload, into) == nil
}

// codexChatGPTLogin is the part of Codex's auth.json the move carries: a ChatGPT sign-in (not an API key).
type codexChatGPTLogin struct {
	Tokens struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

var unsafeLoginFileChars = regexp.MustCompile(`[^A-Za-z0-9@._-]`)

// codexProxyLogin turns Codex's auth.json into the model proxy's saved Codex login and its email.
func codexProxyLogin(raw []byte) ([]byte, string, error) {
	var login codexChatGPTLogin
	if json.Unmarshal(raw, &login) != nil || login.Tokens.RefreshToken == "" || login.Tokens.AccessToken == "" {
		return nil, "", fmt.Errorf("this is not a ChatGPT sign-in of Codex")
	}
	var identity struct {
		Email string `json:"email"`
	}
	jwtClaims(login.Tokens.IDToken, &identity)
	var access struct {
		Exp int64 `json:"exp"`
	}
	expired := ""
	if jwtClaims(login.Tokens.AccessToken, &access) && access.Exp > 0 {
		expired = time.Unix(access.Exp, 0).UTC().Format(time.RFC3339)
	}
	lastRefresh := login.LastRefresh
	if lastRefresh == "" {
		lastRefresh = time.Now().UTC().Format(time.RFC3339)
	}
	stored, err := json.Marshal(map[string]string{
		"type": "codex", "id_token": login.Tokens.IDToken, "access_token": login.Tokens.AccessToken,
		"refresh_token": login.Tokens.RefreshToken, "account_id": login.Tokens.AccountID,
		"email": identity.Email, "last_refresh": lastRefresh, "expired": expired,
	})
	return stored, identity.Email, err
}

// POST /api/server-login/move-in {"provider": "codex-cli", "login": <Codex auth.json>} — the signed-in person's Mac
// hands over its login. It becomes the login of their own server-login account (created when they have none),
// replacing any earlier one.
func (api *StreamingAPI) handleServerLoginMoveIn(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	var input struct {
		Provider string          `json:"provider"`
		Login    json.RawMessage `json:"login"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&input) != nil {
		desktopJSONError(w, 400, "Cannot read the login.")
		return
	}
	if input.Provider != "codex-cli" || !serverLoginOffered(input.Provider) || personalProviderConnectionsLocked(input.Provider) || !providerEnabled(input.Provider) {
		desktopJSONError(w, 400, "This server cannot keep a "+providerAccountProductName(input.Provider)+" login for you.")
		return
	}
	stored, email, err := codexProxyLogin(input.Login)
	if err != nil {
		desktopJSONError(w, 400, "That Codex login is not a ChatGPT sign-in, so there is nothing to move.")
		return
	}
	record, err := ensureServerLoginAccount(r.Context(), caller, input.Provider)
	if err != nil {
		desktopJSONError(w, 500, "Cannot create your account here.")
		return
	}
	proxy := modelProxyFor(caller)
	proxy.signOut(input.Provider) // the earlier login, and any sign-in still waiting
	dir, err := proxy.dir()
	if err != nil {
		desktopJSONError(w, 500, "Cannot keep the login here.")
		return
	}
	name := modelProxyLogins[input.Provider].prefix + unsafeLoginFileChars.ReplaceAllString(firstNonEmptyTrimmed(email, record.ID), "_") + ".json"
	if err := os.WriteFile(filepath.Join(dir, "auth", name), stored, 0o600); err != nil {
		desktopJSONError(w, 500, "Cannot keep the login here.")
		return
	}
	proxy.upstream() // a running proxy picks the file up itself; a stopped one reads it on start
	rememberProviderAccountStatus(record.ID, serverLoginStatus(record))
	log.Printf("[SERVER_LOGIN] %s moved their %s login from the Mac into account %s", caller, input.Provider, record.ID)
	desktopJSON(w, map[string]string{"account_id": record.ID, "signed_in_as": proxy.signedInAs(input.Provider)})
}

// ensureServerLoginAccount is the person's server-login account for a program, created when they have none.
func ensureServerLoginAccount(ctx context.Context, owner, provider string) (storedProviderConnection, error) {
	if record, ok := ownServerLoginAccount(ctx, owner, provider); ok {
		return record, nil
	}
	placeholder, err := newServerLoginPlaceholder()
	if err != nil {
		return storedProviderConnection{}, err
	}
	record := storedProviderConnection{ProviderConnection: ProviderConnection{ID: uuid.NewString(), Provider: provider,
		DisplayName: providerAccountProductName(provider) + " (moved from my Mac)", OwnerUserID: owner, Scope: "user",
		AuthMethod: providerAuthServerLogin, UpdatedAt: time.Now().UTC()}, Credential: placeholder}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(ctx)
	if err != nil {
		return storedProviderConnection{}, err
	}
	if err := serverLoginAccountCheck(records, record); err != nil {
		for _, existing := range records { // created a moment ago by another request
			if existing.OwnerUserID == owner && existing.Provider == provider && existing.AuthMethod == providerAuthServerLogin {
				return existing, nil
			}
		}
	}
	if err := saveProviderConnections(ctx, append(records, record)); err != nil {
		return storedProviderConnection{}, err
	}
	log.Printf("[PROVIDER_ACCOUNT] %s added %s account %s (%s, private) by moving their Mac login", owner, provider, record.ID, providerAuthServerLogin)
	return record, nil
}

// POST /api/provider-connections/{connectionID}/move-to-server {"server": "<connection id>"} — on the Mac: send this
// account's Codex login to the person's server and delete it here. "server" may be left out with one server connected.
// The account then shows as "<server> · Codex (yours, Mac + online)".
func (api *StreamingAPI) handleMoveLoginToServer(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	if desktopBootstrapHost == "" {
		desktopJSONError(w, 404, "Only the Mac app moves a login.")
		return
	}
	id := mux.Vars(r)["connectionID"]
	target, err := api.resolveProviderAccountTarget(r.Context(), id, "")
	if err != nil || target.Provider != "codex-cli" || !providerAccountManagedBy(target, caller, currentUserIsAdmin(r)) ||
		(target.Record != nil && target.Record.AuthMethod != "cli_login") {
		desktopJSONError(w, 404, "Only a Codex browser login on this Mac can be moved.")
		return
	}
	var input struct {
		Server string `json:"server"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&input)
	desktopConnectionsMu.Lock()
	connections, err := readDesktopConnections(caller)
	desktopConnectionsMu.Unlock()
	var c *desktopConnection
	for i := range connections {
		if connections[i].ID == input.Server || (input.Server == "" && len(connections) == 1) {
			c = &connections[i]
		}
	}
	if err != nil || c == nil {
		desktopJSONError(w, 400, "Connect this Mac to your server first, then move the login.")
		return
	}
	file := codexLoginFile(target)
	raw, err := os.ReadFile(file)
	if err != nil {
		desktopJSONError(w, 400, "Codex is not signed in on this Mac, so there is nothing to move.")
		return
	}
	if _, _, err := codexProxyLogin(raw); err != nil {
		desktopJSONError(w, 400, "That Codex login is not a ChatGPT sign-in, so there is nothing to move.")
		return
	}
	// Codex still running on this login could renew it after the move and sign the server's copy out.
	closed := api.closeSessionsOnProviderAccount(id)
	body, _ := json.Marshal(map[string]any{"provider": target.Provider, "login": json.RawMessage(raw)})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, c.Server+"/api/server-login/move-in", bytes.NewReader(body))
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the server.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach "+c.Name+". Nothing was moved.")
		return
	}
	defer resp.Body.Close()
	var answer struct {
		SignedInAs string `json:"signed_in_as"`
		Error      string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)
	if resp.StatusCode != http.StatusOK {
		message := answer.Error
		if message == "" {
			message = fmt.Sprintf("%s answered %d.", c.Name, resp.StatusCode)
		}
		log.Printf("[SERVER_LOGIN] %s: moving the %s login of account %s to %s failed: %d", caller, target.Provider, id, c.ID, resp.StatusCode)
		desktopJSONError(w, 502, message+" Nothing was moved.")
		return
	}
	// Exactly one copy from here on: the server's.
	if err := os.Remove(file); err != nil {
		log.Printf("[SERVER_LOGIN] %s: login moved to %s but the Mac copy of account %s was not removed: %v", caller, c.ID, id, err)
	}
	if target.Record != nil {
		removeMovedMacAccount(r.Context(), id)
	}
	forgetCompanySync(caller)
	api.syncCompanyAccounts(r.Context(), caller)
	log.Printf("[SERVER_LOGIN] %s moved the %s login of account %s to %s (stopped %d session(s))", caller, target.Provider, id, c.ID, closed)
	desktopJSON(w, map[string]string{"signed_in_as": answer.SignedInAs, "server": friendlyServerName(c.Name, c.Server)})
}

// removeMovedMacAccount drops a Mac account whose login moved to the server: the server's account replaces it.
func removeMovedMacAccount(ctx context.Context, id string) {
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(ctx)
	if err != nil {
		return
	}
	next := records[:0]
	for _, record := range records {
		if record.ID != id {
			next = append(next, record)
		}
	}
	if err := saveProviderConnections(ctx, next); err == nil {
		if home, err := providerConnectionHome(id); err == nil {
			_ = os.RemoveAll(filepath.Dir(home))
		}
	}
}
