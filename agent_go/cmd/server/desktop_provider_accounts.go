package server

// "Also use on <server>": the Mac app adds one of the person's own AI accounts on this Mac to their private
// accounts on a connected server. A key is copied by this backend straight to the server, never through the
// page. A login that renews itself is not copied (two copies renewing log each other out): the server gets a
// private browser-login account and the person signs in once there.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/mux"
)

// desktopAccountToCopy is what a local account becomes on the server: a key, or a browser login to sign in to.
type desktopAccountToCopy struct {
	Provider, Name, Credential, AuthMethod, UnderlyingProvider, BaseURL string
}

// localAccountForServer resolves one account the caller may copy: their own account, or, for the installation's
// owner, this Mac's own login or key for a program.
func localAccountForServer(records []storedProviderConnection, stored *StoredProviderKeys, owner, accountID string, admin bool) (desktopAccountToCopy, error) {
	if provider, ok := strings.CutPrefix(accountID, "global:"); ok {
		if !admin || !providerEnabled(provider) {
			return desktopAccountToCopy{}, fmt.Errorf("This account cannot be added to a server.")
		}
		account := desktopAccountToCopy{Provider: provider, Name: providerAccountProductName(provider) + " (from my Mac)", AuthMethod: "cli_login"}
		if stored != nil {
			switch provider {
			case "codex-cli":
				account.Credential = stored.CodexCLI
			case "cursor-cli":
				account.Credential = stored.CursorCLI
			case "pi-cli":
				if account.Credential = stored.PiCLI; account.Credential == "" && len(stored.PiProviderKeys) > 0 {
					return desktopAccountToCopy{}, fmt.Errorf("Add this Pi account on the server with the key for the service you want.")
				}
			}
		}
		for _, name := range serverAccountEnvNames[provider] {
			if account.Credential == "" {
				account.Credential = strings.TrimSpace(os.Getenv(name))
			}
		}
		if account.Credential = strings.TrimSpace(account.Credential); account.Credential != "" {
			account.AuthMethod = "api_key"
		} else if provider == "pi-cli" || provider == "agy-cli" {
			return desktopAccountToCopy{}, fmt.Errorf("This program has no key on this Mac to add. Add one on the server instead.")
		}
		return account, nil
	}
	for _, record := range records {
		if record.ID != accountID {
			continue
		}
		if record.OwnerUserID != owner || !providerEnabled(record.Provider) {
			break
		}
		if record.ViaConnection != "" {
			// A company account is the server's own login reached from this Mac; its key here is only this Mac's
			// placeholder.
			return desktopAccountToCopy{}, fmt.Errorf("A company account already lives on its server; it can't be added there.")
		}
		account := desktopAccountToCopy{Provider: record.Provider, Name: record.DisplayName, AuthMethod: "api_key", Credential: strings.TrimSpace(record.Credential),
			UnderlyingProvider: record.UnderlyingProvider, BaseURL: record.BaseURL}
		if record.AuthMethod == "cli_login" || account.Credential == "" {
			account.AuthMethod, account.Credential = "cli_login", ""
		}
		return account, nil
	}
	return desktopAccountToCopy{}, fmt.Errorf("Account not found.")
}

// remoteOwnAccounts lists the caller's own accounts on the connected server (metadata only, never values).
func remoteOwnAccounts(r *http.Request, c desktopConnection) ([]providerAccountView, int, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.Server+"/api/provider-connections", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var list struct {
		Connections []providerAccountView `json:"connections"`
	}
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list)
	}
	own := []providerAccountView{}
	for _, view := range list.Connections {
		if view.Relation == "own" {
			own = append(own, view)
		}
	}
	return own, resp.StatusCode, nil
}

// remoteMatch is the server account a local account was added as: same program and name.
func remoteMatch(own []providerAccountView, account desktopAccountToCopy) *providerAccountView {
	for i := range own {
		if own[i].Provider == account.Provider && own[i].DisplayName == account.Name {
			return &own[i]
		}
	}
	return nil
}

// remoteAccountLogin asks the connected server who its account is signed in as (its own free status check).
func remoteAccountLogin(r *http.Request, c desktopConnection, id string) (providerAccountStatus, error) {
	var status providerAccountStatus
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.Server+"/api/provider-connections/"+url.PathEscape(id)+"/status", nil)
	if err != nil {
		return status, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return status, fmt.Errorf("status %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&status)
	return status, err
}

// desktopServerAccount is where one of your accounts on this Mac stands on a server, and the login on each side.
type desktopServerAccount struct {
	// State is ready, sign_in, different_login or missing.
	State       string `json:"state"`
	MacLogin    string `json:"mac_login,omitempty"`
	ServerLogin string `json:"server_login,omitempty"`
}

// sameLogin compares two logins as the CLIs report them (an email, usually); unknown on either side is not a mismatch.
func sameLogin(mac, server string) bool {
	return mac == "" || server == "" || strings.EqualFold(strings.TrimSpace(mac), strings.TrimSpace(server))
}

// GET /api/desktop-connections/{connection}/provider-accounts?provider=<program>
// -> {"accounts": {"<local account>": {"state", "mac_login", "server_login"}}, "signed_in_as": "..."}: where each of
// your accounts on this Mac stands on that server, for the "Use it on your servers too" row. A browser login is
// signed in separately on each side, so both sides' logins are read and compared.
func (api *StreamingAPI) handleDesktopProviderAccountStatus(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	c, err := desktopConnectionFor(owner, mux.Vars(r)["connection"])
	if err != nil {
		desktopJSONError(w, 404, "Connection is unavailable. Connect to the server again.")
		return
	}
	provider := r.URL.Query().Get("provider")
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		desktopJSONError(w, 500, "Cannot read your accounts.")
		return
	}
	own, status, err := remoteOwnAccounts(r, c)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach this server.")
		return
	}
	if status == http.StatusUnauthorized {
		desktopJSONError(w, 401, "Sign in to this server again.")
		return
	}
	stored, _ := LoadProviderKeys(r.Context())
	ids := []string{"global:" + provider}
	for _, record := range records {
		if record.Provider == provider && record.OwnerUserID == owner {
			ids = append(ids, record.ID)
		}
	}
	accounts := map[string]desktopServerAccount{}
	for _, id := range ids {
		account, err := localAccountForServer(records, stored, owner, id, currentUserIsAdmin(r))
		if err != nil {
			continue
		}
		match := remoteMatch(own, account)
		switch {
		case match == nil:
			accounts[id] = desktopServerAccount{State: "missing"}
			continue
		case match.Configured != nil && !*match.Configured:
			accounts[id] = desktopServerAccount{State: "sign_in"}
			continue
		case account.AuthMethod != "cli_login":
			// A copied key is the same key on both sides.
			accounts[id] = desktopServerAccount{State: "ready"}
			continue
		}
		result := desktopServerAccount{State: "ready"}
		if remote, err := remoteAccountLogin(r, c, match.ID); err == nil {
			if remote.State == "signed_out" {
				result.State = "sign_in"
			}
			result.ServerLogin = remote.Identity
		}
		if target, err := api.resolveProviderAccountTarget(r.Context(), id, provider); err == nil {
			result.MacLogin = checkProviderAccountStatus(r.Context(), target, false).Identity
		}
		if result.State == "ready" && !sameLogin(result.MacLogin, result.ServerLogin) {
			result.State = "different_login"
		}
		accounts[id] = result
	}
	desktopJSON(w, map[string]any{"accounts": accounts, "signed_in_as": c.User.Email})
}

// POST /api/desktop-connections/{connection}/provider-accounts {"account_id": "<local account>"}
// -> {"status": "copied" | "sign_in" | "exists", "account_id": "<the account on the server>", "server_url": "..."}
func (api *StreamingAPI) handleDesktopProviderAccountCopy(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	c, err := desktopConnectionFor(owner, mux.Vars(r)["connection"])
	if err != nil {
		http.Error(w, `{"error":"Connection is unavailable. Connect to the server again."}`, 404)
		return
	}
	var input struct {
		AccountID string `json:"account_id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&input) != nil || strings.TrimSpace(input.AccountID) == "" {
		http.Error(w, `{"error":"Choose an account."}`, 400)
		return
	}
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		http.Error(w, `{"error":"Cannot read your accounts."}`, 500)
		return
	}
	stored, _ := LoadProviderKeys(r.Context())
	account, err := localAccountForServer(records, stored, owner, input.AccountID, currentUserIsAdmin(r))
	if err != nil {
		desktopJSONError(w, 400, err.Error())
		return
	}
	call := func(method string, body any) (*http.Response, error) {
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(r.Context(), method, c.Server+"/api/provider-connections", reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")
		return desktopHTTPClient().Do(req)
	}
	// The server's AI accounts page, where a browser-login account is signed in; with the account named, the page
	// starts its sign-in at once.
	signIn := func(id string) string {
		return c.Server + "/settings/ai-accounts?" + url.Values{"provider": {account.Provider}, "sign_in": {id}}.Encode()
	}
	// An account already added under the same name is not added again.
	own, status, err := remoteOwnAccounts(r, c)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach this server. Try again.")
		return
	}
	if status == http.StatusUnauthorized {
		desktopJSONError(w, 401, "Sign in to this server again, then retry.")
		return
	}
	if view := remoteMatch(own, account); view != nil {
		// Added before but not signed in there yet: the person still has to sign in, so say that, not "exists".
		if view.Configured != nil && !*view.Configured {
			desktopJSON(w, map[string]string{"status": "sign_in", "account_id": view.ID, "server_url": signIn(view.ID)})
			return
		}
		// "exists" says only that it was added before and the server counts it as set up.
		if (view.AuthMethod == "cli_login") != (account.AuthMethod == "cli_login") {
			desktopJSONError(w, 409, "You already have an account named \""+account.Name+"\" on this server that signs in another way. Rename one of them, then try again.")
			return
		}
		desktopJSON(w, map[string]string{"status": "exists", "account_id": view.ID, "server_url": signIn(view.ID)})
		return
	}
	request := map[string]any{"provider": account.Provider, "display_name": account.Name, "auth_method": account.AuthMethod,
		"sharing": map[string]string{"mode": providerSharingPrivate}}
	if account.AuthMethod != "cli_login" {
		request["credential"] = account.Credential
	}
	if account.UnderlyingProvider != "" {
		request["underlying_provider"] = account.UnderlyingProvider
	}
	if account.BaseURL != "" {
		request["base_url"] = account.BaseURL
	}
	resp, err := call(http.MethodPost, request)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach this server. Try again.")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		// The server's own reason (personal accounts locked, program not offered there, key refused) is plain text.
		// Only its first line, and never the key itself should a provider's message quote it.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		reason, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
		if account.Credential != "" {
			reason = strings.ReplaceAll(reason, account.Credential, "[your key]")
		}
		desktopJSONError(w, 400, "The server did not add the account: "+reason)
		return
	}
	var created ProviderConnection
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&created)
	result := "copied"
	if account.AuthMethod == "cli_login" {
		result = "sign_in"
	}
	log.Printf("[DESKTOP_ACCOUNT] %s added %s account %s to connection %s as %s (%s)", owner, account.Provider, input.AccountID, c.ID, created.ID, result)
	desktopJSON(w, map[string]string{"status": result, "account_id": created.ID, "server_url": signIn(created.ID)})
}

func desktopJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
