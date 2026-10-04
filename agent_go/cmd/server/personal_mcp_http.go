package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

// Exact connection IDs always win. Provider aliases are accepted only when
// they identify one account, so runtime and management never guess an account.
func personalMCPByCatalog(person, name string) (placeMCPServer, bool) {
	saved, found, _ := lookupPersonalMCP(person, name)
	return saved, found
}

func lookupPersonalMCP(person, name string) (placeMCPServer, bool, error) {
	servers, err := listPlaceMCPServers(person)
	if err != nil {
		return placeMCPServer{}, false, err
	}
	for _, s := range servers {
		if strings.EqualFold(s.Name, name) {
			return s, true, nil
		}
	}
	var match placeMCPServer
	count := 0
	for _, s := range servers {
		if name != "" && strings.EqualFold(s.Catalog, name) {
			match = s
			count++
		}
	}
	if count > 1 {
		return placeMCPServer{}, false, fmt.Errorf("multiple accounts for %q; use the exact connection name returned by list_mcp_servers", name)
	}
	return match, count == 1, nil
}

func (api *StreamingAPI) startPrivateOAuth(w http.ResponseWriter, r *http.Request, req OAuthLoginRequest) {
	person := mcpCaller(r.Context())
	if !activeMCPPerson(person) {
		writeUsersError(w, 401, "sign in first")
		return
	}
	sessionID, sessionErr := api.oauthNotificationSession(r, req.SessionID)
	if sessionErr != nil {
		writeUsersError(w, http.StatusForbidden, "chat session not found or access denied")
		return
	}
	saved, found, lookupErr := lookupPersonalMCP(person, req.ServerName)
	if lookupErr != nil {
		writeUsersError(w, http.StatusBadRequest, lookupErr.Error())
		return
	}
	if !found {
		var status int
		var err error
		saved, status, err = api.addPlaceMCP(r.Context(), person, placeMCPServer{}, req.ServerName)
		if err != nil {
			writeUsersError(w, status, err.Error())
			return
		}
	}
	var entered *registeredClient
	if req.ClientID != "" {
		entered = &registeredClient{ClientID: req.ClientID, ClientSecret: req.ClientSecret}
	}
	authURL, discovery, status, err := api.startPlaceMCPSignIn(person, saved.Name, deriveOAuthRedirectURI(r), sessionID, entered)
	if err != nil {
		writeUsersError(w, status, err.Error())
		return
	}
	if discovery != nil {
		writeUsersJSON(w, 200, discovery)
		return
	}
	writeUsersJSON(w, 200, map[string]string{"server_name": req.ServerName, "auth_url": authURL, "message": "Sign in to this MCP connection"})
}
func privateOAuthStatus(w http.ResponseWriter, r *http.Request, name string) {
	person := mcpCaller(r.Context())
	saved, found := personalMCPByCatalog(person, name)
	valid := false
	if found {
		dir, _ := placeMCPDir(person)
		valid = placeMCPServerConnected(dir, person, saved)
	}
	writeUsersJSON(w, 200, map[string]any{"server_name": name, "valid": valid, "has_oauth": !found || saved.OAuth != nil})
}
func (api *StreamingAPI) connectPrivateCatalog(w http.ResponseWriter, r *http.Request, req MCPConnectRequest) {
	person := mcpCaller(r.Context())
	if !activeMCPPerson(person) {
		writeUsersError(w, 401, "sign in first")
		return
	}
	saved, found, lookupErr := lookupPersonalMCP(person, req.ServerName)
	if lookupErr != nil {
		writeUsersError(w, http.StatusBadRequest, lookupErr.Error())
		return
	}
	if !found {
		var status int
		var err error
		saved, status, err = api.addPlaceMCP(r.Context(), person, placeMCPServer{}, req.ServerName)
		if err != nil {
			writeUsersError(w, status, err.Error())
			return
		}
	}
	if saved.OAuth != nil {
		writeUsersJSON(w, 200, map[string]string{"status": "oauth_required", "server_name": req.ServerName})
		return
	}
	if req.APIKey != "" {
		secret := "MCP_" + strings.ToUpper(saved.Name) + "_TOKEN"
		if err := setPersonalSecret(person, secret, req.APIKey); err != nil {
			writeUsersError(w, 400, err.Error())
			return
		}
		saved.Headers = map[string]placeMCPHeader{"Authorization": {Secret: secret, Format: "Bearer {}"}}
		if _, err := addPlaceMCPServer(person, saved); err != nil {
			writeUsersError(w, 500, "could not save the connection")
			return
		}
	}
	closePlaceMCPConnection(person, saved.Name)
	writeUsersJSON(w, 200, map[string]string{"status": "connected", "server_name": req.ServerName})
}
func disconnectPrivateCatalog(w http.ResponseWriter, r *http.Request, name string) {
	person := mcpCaller(r.Context())
	saved, found, lookupErr := lookupPersonalMCP(person, name)
	if lookupErr != nil {
		writeUsersError(w, http.StatusBadRequest, lookupErr.Error())
		return
	}
	if found {
		if err := forgetPlaceMCPLogin(person, saved.Name); err != nil {
			writeUsersError(w, 500, "could not clear private sign-in")
			return
		}
		if err := removePlaceMCPServer(person, saved.Name); err != nil {
			writeUsersError(w, 500, "could not remove the connection")
			return
		}
		closePlaceMCPConnection(person, saved.Name)
	}
	writeUsersJSON(w, 200, map[string]string{"status": "disconnected", "server_name": name})
}

// The legacy platform namespace is now a Vault credential store only. An
// explicit scope plus central admin permission is required to manage it.
func vaultOAuthScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	if scope == "vault" && currentUserIsAdmin(r) && userAllowedProduct(GetUserFromContext(r.Context()), "mcp-gateway") {
		return true
	}
	writeUsersError(w, 403, fmt.Sprintf("invalid MCP scope %q; Vault sign-in requires an administrator", scope))
	return false
}
func handlePersonalMCPSecrets(w http.ResponseWriter, r *http.Request) {
	person := mcpCaller(r.Context())
	if !activeMCPPerson(person) {
		writeUsersError(w, 401, "sign in first")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost {
		var in struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
			writeUsersError(w, 400, "invalid private credential")
			return
		}
		if err := setPersonalSecret(person, in.Name, in.Value); err != nil {
			writeUsersError(w, 400, err.Error())
			return
		}
		writeUsersJSON(w, 200, map[string]string{"name": in.Name})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	dir, err := placeMCPDir(person)
	if err != nil {
		writeUsersError(w, 500, "private storage unavailable")
		return
	}
	values := map[string]string{}
	placeMCPMu.Lock()
	err = readPlaceMCPJSON(filepath.Join(dir, "secrets.json"), &values)
	placeMCPMu.Unlock()
	if err != nil {
		writeUsersError(w, 500, "private secrets unavailable")
		return
	}
	rows := []map[string]string{}
	for name := range values {
		rows = append(rows, map[string]string{"name": name})
	}
	writeUsersJSON(w, 200, map[string]any{"secrets": rows})
}
