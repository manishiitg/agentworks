package server

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"

	"github.com/gorilla/mux"
)

// A person's own Vault on this server, for their own Mac (Vault step 5A, PLAT-856). The Mac signed in to this server
// lists the person's Vault apps here, calls one, or reads one secret, and gets only what the same person may use in
// their own turn on this server: the same Vault grants, checked again on every request. Only the person's own sign-in
// is accepted; access tokens, MCP and bot callers are refused. Secret values are never logged.

var vaultMeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func (api *StreamingAPI) registerVaultMeRoutes(r *mux.Router) {
	r.HandleFunc("/vault/me/tools", api.handleVaultMeTools).Methods("GET")
	r.HandleFunc("/vault/me/mcp/{conn}", api.handleVaultMeMCP).Methods("GET", "POST", "DELETE")
	r.HandleFunc("/vault/me/secret/{name}", api.handleVaultMeSecret).Methods("GET")
	r.HandleFunc("/vault/me/secrets", api.handleVaultMeSecretCopy).Methods("POST")
	// Vault step 5B (vault_move.go): the server takes a moved sign-in; the Mac app's route moves one there.
	r.HandleFunc("/vault/me/connections/import", api.handleVaultMeImport).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/vault-apps/{id}/move", api.handleDesktopVaultMove).Methods("POST")
}

// vaultMeCaller is the signed-in person on a company server; anything else is refused.
func vaultMeCaller(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if IsLocalMode() {
		writeUsersError(w, http.StatusNotFound, "This is only on your company's server.")
		return "", false
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.AccessToken != nil || !plainSessionClaims(claims) || !activeMCPPerson(claims.UserID) {
		writeUsersError(w, http.StatusForbidden, "Sign in to this server from your Mac's Settings to use your Vault there.")
		return "", false
	}
	return claims.UserID, true
}

// handleVaultMeTools lists the person's Vault apps on this server with their tools: the rows their own turn here gets.
func (api *StreamingAPI) handleVaultMeTools(w http.ResponseWriter, r *http.Request) {
	person, ok := vaultMeCaller(w, r)
	if !ok {
		return
	}
	rows, err := vaultServersFor(r.Context(), person)
	if err != nil {
		log.Printf("[VAULT_ME] user=%s list result=unavailable err=%v", person, err)
		writeUsersError(w, http.StatusServiceUnavailable, "Your Vault on this server can't be read right now; try again.")
		return
	}
	if rows == nil {
		rows = []vaultRuntimeServer{}
	}
	log.Printf("[VAULT_ME] user=%s list result=ok apps=%d", person, len(rows))
	writeUsersJSON(w, http.StatusOK, map[string]any{"servers": rows})
}

// handleVaultMeMCP forwards one MCP request for one of the person's Vault apps to the gateway as that person, after
// checking that the app is in their current inventory; the gateway then checks their grants and argument rules.
func (api *StreamingAPI) handleVaultMeMCP(w http.ResponseWriter, r *http.Request) {
	person, ok := vaultMeCaller(w, r)
	if !ok {
		return
	}
	conn := mux.Vars(r)["conn"]
	if !vaultMeIDPattern.MatchString(conn) || r.Header.Get("Origin") != "" {
		writeUsersError(w, http.StatusBadRequest, "Choose one of your Vault apps.")
		return
	}
	target, secret, err := gatewayAdminServiceConfig()
	if err != nil || !vaultConfigured() {
		writeUsersError(w, http.StatusServiceUnavailable, "Vault is not set up on this server.")
		return
	}
	rows, err := vaultServersFor(r.Context(), person)
	if err != nil {
		log.Printf("[VAULT_ME] user=%s conn=%s result=unavailable err=%v", person, conn, err)
		writeUsersError(w, http.StatusServiceUnavailable, "Your Vault on this server can't be read right now; try again.")
		return
	}
	found := false
	for _, row := range rows {
		if row.ID == conn {
			found = true
			break
		}
	}
	if !found {
		log.Printf("[VAULT_ME] user=%s conn=%s result=refused reason=not_in_vault", person, conn)
		writeUsersError(w, http.StatusForbidden, "This app isn't in your Vault on this server, or you can't use it.")
		return
	}
	log.Printf("[VAULT_ME] user=%s conn=%s method=%s result=forwarded", person, conn, r.Method)
	vaultGatewayMCPProxy(target, secret, "/api/admin/runtime/mcp", vaultDelegation{Person: person, Connector: conn}).ServeHTTP(w, r)
}

// handleVaultMeSecret answers one secret value the person may use on this server (the Vault grants a turn here uses).
// The name is logged, never the value.
func (api *StreamingAPI) handleVaultMeSecret(w http.ResponseWriter, r *http.Request) {
	person, ok := vaultMeCaller(w, r)
	if !ok {
		return
	}
	name := mux.Vars(r)["name"]
	// Values go to the person's Mac (its server makes this request), never to a web page.
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeUsersError(w, http.StatusForbidden, "Secret values are only sent to your Mac.")
		return
	}
	if !vaultMeIDPattern.MatchString(name) {
		writeUsersError(w, http.StatusBadRequest, "Give a secret name, for example GITHUB_TOKEN.")
		return
	}
	rows, err := permittedGlobalSecrets(r.Context(), person)
	if err != nil {
		log.Printf("[VAULT_ME] user=%s secret=%s result=unavailable", person, name)
		writeUsersError(w, http.StatusServiceUnavailable, "Your Vault on this server can't be read right now; try again.")
		return
	}
	// The exact name first; otherwise the one secret whose plain name matches (a Vault secret reaches scripts by it).
	var match *globalSecretEntry
	for i := range rows {
		if rows[i].Name == name {
			match = &rows[i]
			break
		}
	}
	if match == nil {
		for i := range rows {
			if vaultSecretShortName(rows[i].Name) == name {
				if match != nil {
					match = nil
					break
				}
				match = &rows[i]
			}
		}
	}
	if match == nil || match.Value == "" {
		log.Printf("[VAULT_ME] user=%s secret=%s result=not_found", person, name)
		writeUsersError(w, http.StatusNotFound, name+" isn't in your Vault on this server, or you can't use it.")
		return
	}
	log.Printf("[VAULT_ME] user=%s secret=%s result=ok", person, name)
	writeUsersJSON(w, http.StatusOK, map[string]string{"name": match.Name, "value": match.Value})
}

// handleVaultMeSecretCopy takes a secret the person copies from their Mac (Team card, "Copy to the server"): saved in
// their own Vault on this server and selected for their Crew here, so the Team's agents use it. Only the Crew's owner
// (or someone who adds secrets to it, as on the website) can; never from a web page; the value is never logged.
func (api *StreamingAPI) handleVaultMeSecretCopy(w http.ResponseWriter, r *http.Request) {
	person, ok := vaultMeCaller(w, r)
	if !ok {
		return
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeUsersError(w, http.StatusForbidden, "Secrets are copied from your Mac.")
		return
	}
	var in struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Crew  string `json:"crew"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.Value == "" || !vaultMeIDPattern.MatchString(in.Crew) {
		writeUsersError(w, http.StatusBadRequest, "Send the secret's name and value and the Crew.")
		return
	}
	root := crewProjectRootByID(in.Crew)
	if root == "" {
		writeUsersError(w, http.StatusNotFound, "That Crew isn't on this server.")
		return
	}
	full, err := api.saveProjectSecret(r.Context(), person, root, in.Name, in.Value)
	if err == nil {
		err = selectProjectSecret(r.Context(), crewProfileID, root, full)
	}
	if err != nil {
		log.Printf("[VAULT_ME] user=%s secret=%s crew=%s copy result=error err_type=%T", person, in.Name, in.Crew, err)
		writeUsersError(w, http.StatusConflict, err.Error())
		return
	}
	log.Printf("[VAULT_ME] user=%s secret=%s crew=%s copy result=ok", person, in.Name, in.Crew)
	writeUsersJSON(w, http.StatusOK, map[string]string{"name": in.Name})
}
