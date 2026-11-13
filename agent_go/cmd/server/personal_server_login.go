package server

// A person's own AI subscription signed in once on the server and used everywhere (PLAT-856-441, owner 13 Nov: "one
// login on the server"). The login lives in the person's own model proxy (model_proxy_sidecar.go), never in a CLI's
// login file, so only one copy ever renews it: the person's server agents and their Mac app both send model requests
// through the server, holding only a placeholder key.
//
//   - server agents: Codex points at /api/personal-model-proxy/{account}/v1 on this server with the account's
//     placeholder as its key (providerConnectionRuntimeKeys);
//   - the Mac app: the person's connection reaches /api/model-proxy/{program}/v1 (company_model_proxy.go), which uses
//     the person's own proxy when they have such an account, before any company account.
//
// A person's proxy is theirs alone: its own folder, logins and client key, so no one else's agent can use the login.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// providerAuthServerLogin is an account whose login the server's model proxy holds for its owner.
const providerAuthServerLogin = "server_login"

// serverLoginPrograms are the programs that can sign in once on the server (Codex first; Claude, Muse and agy follow).
var serverLoginPrograms = map[string]bool{"codex-cli": true}

// serverLoginOffered reports whether this server can hold a login for the program.
func serverLoginOffered(provider string) bool {
	return serverLoginPrograms[provider] && modelProxyBinary() != ""
}

// newServerLoginPlaceholder is the key the account's programs present: worthless anywhere but this server's relay.
func newServerLoginPlaceholder() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return "sk-agentworks-" + hex.EncodeToString(secret), nil
}

// personalModelProxyBase is where a program on this server sends its model requests for a server-login account.
func personalModelProxyBase(record storedProviderConnection) string {
	base := "http://127.0.0.1:8000"
	if runningServerAPI != nil {
		base = runningServerAPI.GetCodeExecAPIURL()
	}
	return base + "/api/personal-model-proxy/" + record.ID + "/v1"
}

// ownServerLoginAccount is the person's own server-login account for a program, if they have one.
func ownServerLoginAccount(ctx context.Context, owner, provider string) (storedProviderConnection, bool) {
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return storedProviderConnection{}, false
	}
	for _, record := range records {
		if record.OwnerUserID == owner && record.Provider == provider && record.AuthMethod == providerAuthServerLogin {
			return record, true
		}
	}
	return storedProviderConnection{}, false
}

// serverLoginStatus is a server-login account's state, from the owner's own proxy.
func serverLoginStatus(record storedProviderConnection) providerAccountStatus {
	status := providerAccountStatus{State: "signed_out", Detail: "Not signed in on this server yet", CheckedAt: time.Now().UTC()}
	if email := modelProxyFor(record.OwnerUserID).signedInAs(record.Provider); email != "" {
		status.State, status.Identity, status.Detail = "signed_in", email, ""
	}
	return status
}

// serverLoginState is what the account card shows: who it is signed in as, or the sign-in waiting for approval.
type serverLoginState struct {
	SignedInAs string            `json:"signed_in_as,omitempty"`
	SigningIn  *modelProxySignIn `json:"signing_in,omitempty"`
}

// GET/POST /api/provider-connections/{connectionID}/server-login — the owner only. POST {"action": "sign_in"} starts
// the sign-in (a link and code to approve in the browser); GET reads its state. Sign-out is the account's Sign out.
func (api *StreamingAPI) handleServerLogin(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	record, ok := ownServerLoginAccountByID(r.Context(), caller, mux.Vars(r)["connectionID"])
	if !ok {
		desktopJSONError(w, 404, "connection unavailable")
		return
	}
	proxy := modelProxyFor(caller)
	if r.Method == http.MethodPost {
		var input struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&input)
		if input.Action != "sign_in" {
			desktopJSONError(w, 400, "Unknown action.")
			return
		}
		if _, err := proxy.startSignIn(record.Provider); err != nil {
			desktopJSONError(w, 502, err.Error())
			return
		}
		log.Printf("[SERVER_LOGIN] %s started the %s sign-in for account %s", caller, record.Provider, record.ID)
	}
	state := serverLoginState{SignedInAs: proxy.signedInAs(record.Provider)}
	if state.SignedInAs == "" {
		state.SigningIn = proxy.signingIn(record.Provider)
	} else {
		rememberProviderAccountStatus(record.ID, serverLoginStatus(record))
	}
	desktopJSON(w, state)
}

// ownServerLoginAccountByID is one of the caller's own server-login accounts.
func ownServerLoginAccountByID(ctx context.Context, owner, id string) (storedProviderConnection, bool) {
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return storedProviderConnection{}, false
	}
	for _, record := range records {
		if record.ID == id && record.OwnerUserID == owner && record.AuthMethod == providerAuthServerLogin {
			return record, true
		}
	}
	return storedProviderConnection{}, false
}

// ANY /api/personal-model-proxy/{account}/v1/{path} — a program on this server running on a server-login account:
// its model request goes to the owner's own proxy. The program proves itself with the account's placeholder, which
// only that account's runs hold.
func (api *StreamingAPI) handlePersonalModelProxy(w http.ResponseWriter, r *http.Request) {
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(r.Context())
	providerConnectionsMu.Unlock()
	if err != nil {
		desktopJSONError(w, 500, "Cannot read the accounts.")
		return
	}
	var record storedProviderConnection
	found := false
	for _, candidate := range records {
		if candidate.ID == mux.Vars(r)["account"] && candidate.AuthMethod == providerAuthServerLogin {
			record, found = candidate, true
			break
		}
	}
	token := proxyCallerKey(r)
	if !found || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(record.Credential)) != 1 {
		desktopJSONError(w, 401, "Unknown account.")
		return
	}
	path := mux.Vars(r)["path"]
	if !companyProxyPaths[path] {
		desktopJSONError(w, 404, "Not found.")
		return
	}
	api.forwardToPersonalProxy(w, r, record, path, "server")
}

// forwardToPersonalProxy sends one model request to the owner's own proxy and logs it (where: "server" or "mac").
func (api *StreamingAPI) forwardToPersonalProxy(w http.ResponseWriter, r *http.Request, record storedProviderConnection, path, where string) {
	base, key, ok := modelProxyFor(record.OwnerUserID).upstream()
	if !ok {
		desktopJSONError(w, 502, "The server's model proxy did not start.")
		return
	}
	var body io.Reader = http.NoBody
	if r.Method == http.MethodPost {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			desktopJSONError(w, 400, "Cannot read the request.")
			return
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base+"/v1/"+path, body)
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the model proxy.")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	copyProxyHeaders(req, r)
	req.Header.Set("Authorization", "Bearer "+key)
	started := time.Now()
	resp, err := companyProxyClient.Do(req)
	if err != nil {
		log.Printf("[SERVER_LOGIN] %s %s %s from the %s: proxy unreachable: %v", record.OwnerUserID, record.Provider, path, where, err)
		desktopJSONError(w, 502, "The server's model proxy is not answering.")
		return
	}
	defer resp.Body.Close()
	usage := &proxyUsage{stream: strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")}
	copyProxyResponse(w, resp, usage)
	input, output, _, cached, counted := usage.tokens()
	log.Printf("[SERVER_LOGIN] %s used own %s login (account %s) from the %s: %s -> %d (%s) tokens in=%d out=%d cached=%d counted=%v",
		record.OwnerUserID, record.Provider, record.ID, where, path, resp.StatusCode, time.Since(started).Round(time.Millisecond), input, output, cached, counted)
}

// serverLoginAccountCheck refuses a second server-login account for the same program: the person's proxy holds one
// login per program.
func serverLoginAccountCheck(records []storedProviderConnection, record storedProviderConnection) error {
	for _, existing := range records {
		if existing.OwnerUserID == record.OwnerUserID && existing.Provider == record.Provider && existing.AuthMethod == providerAuthServerLogin {
			return fmt.Errorf("you already have %q signed in on this server for %s; sign it in again instead", existing.DisplayName, providerAccountProductName(record.Provider))
		}
	}
	return nil
}
