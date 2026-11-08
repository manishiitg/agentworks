package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Vault step 5A (PLAT-856): the server's /api/vault/me routes give a person only their own Vault apps and secrets, under
// the grants their own turn uses, and refuse access-token callers.
func TestVaultMeGivesOnlyThePersonsOwnVault(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	secret := strings.Repeat("s", 32)
	var mcpCalls []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-CapLayer-Actor")
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("gateway called without the host identity")
		}
		switch r.URL.Path {
		case "/api/admin/runtime/servers":
			id := map[string]string{"alice": "c1", "bob": "c2"}[actor]
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": id, "label": actor + " Linear", "provider": "linear", "tools": []any{map[string]any{"name": "linear__search"}}}}})
		case "/api/admin/runtime/secrets":
			name := map[string]string{"alice": "ALICE_KEY", "bob": "BOB_KEY"}[actor]
			_ = json.NewEncoder(w).Encode(map[string]any{"secrets": []any{map[string]any{"name": name}}})
		case "/api/admin/runtime/mcp":
			mcpCalls = append(mcpCalls, actor+"/"+r.Header.Get("X-Vault-Connector"))
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	managedGlobalsMu.Lock()
	oldManaged := managedGlobals
	managedGlobals = map[string]string{"ALICE_KEY": "alice-value", "BOB_KEY": "bob-value"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() { managedGlobalsMu.Lock(); managedGlobals = oldManaged; managedGlobalsMu.Unlock() })

	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerVaultMeRoutes(router.PathPrefix("/api").Subrouter())
	call := func(claims *UserClaims, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	alice := &UserClaims{UserID: "alice", Username: "alice"}

	if w := call(alice, "GET", "/api/vault/me/tools", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"c1"`) || strings.Contains(w.Body.String(), `"c2"`) {
		t.Fatalf("own tools: %d %s", w.Code, w.Body.String())
	}
	if w := call(alice, "GET", "/api/vault/me/secret/ALICE_KEY", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "alice-value") {
		t.Fatalf("own secret: %d", w.Code)
	}
	if w := call(alice, "GET", "/api/vault/me/secret/BOB_KEY", ""); w.Code != 404 || strings.Contains(w.Body.String(), "bob-value") {
		t.Fatalf("another person's secret was returned: %d", w.Code)
	}
	if w := call(alice, "POST", "/api/vault/me/mcp/c1", `{"method":"tools/list"}`); w.Code != 200 {
		t.Fatalf("own app call: %d %s", w.Code, w.Body.String())
	}
	if w := call(alice, "POST", "/api/vault/me/mcp/c2", `{"method":"tools/list"}`); w.Code != 403 {
		t.Fatalf("another person's app was callable: %d", w.Code)
	}
	if len(mcpCalls) != 1 || mcpCalls[0] != "alice/c1" {
		t.Fatalf("gateway calls: %v", mcpCalls)
	}
	token := &UserClaims{UserID: "alice", Username: "alice", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}}}
	for _, path := range []string{"/api/vault/me/tools", "/api/vault/me/secret/ALICE_KEY"} {
		if w := call(token, "GET", path, ""); w.Code != 403 || strings.Contains(w.Body.String(), "alice-value") {
			t.Fatalf("access token admitted to %s: %d", path, w.Code)
		}
	}
}

// Vault step 5A (PLAT-856): on the Mac a server Vault app is srv_<conn8>_<id>; the agent's config holds only a local
// delegation, and the local route forwards the call with the saved server sign-in (renewed when the server says so).
func TestServerVaultAppOnTheMacForwardsWithTheSavedSignIn(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	var forwarded string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer remote-session" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/api/vault/me/tools":
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"id": "c9", "label": "Linear", "provider": "linear", "tools": []any{map[string]any{"name": "linear__search"}}}}})
		case "/api/vault/me/mcp/c9":
			body, _ := io.ReadAll(r.Body)
			forwarded = string(body)
			w.Header().Set("X-Renewed-Token", "remote-renewed")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	connID := strings.Repeat("ab", 16)
	if err := writeDesktopConnections("alice", []desktopConnection{{ID: connID, Name: "Company", Server: remote.URL, Token: "remote-session"}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{config: ServerConfig{Host: "127.0.0.1", Port: 18161}}
	name := serverVaultName(connID, "c9")
	if name != "srv_abababab_c9" {
		t.Fatalf("name: %s", name)
	}
	resolved, err := api.resolveGovernedMCP(personContext("alice"), "alice", name)
	if err != nil || vaultSelectionName(resolved.Name) != name || !strings.HasSuffix(resolved.Config.URL, "/internal/server-vault/mcp/"+connID+"/c9") || strings.Contains(resolved.Config.Headers["Authorization"], "remote-session") {
		t.Fatalf("resolved: %+v %v", resolved, err)
	}
	if _, err := api.resolveGovernedMCP(personContext("alice"), "alice", serverVaultName(connID, "c8")); err == nil {
		t.Fatal("an app missing from the server's list resolved")
	}
	router := mux.NewRouter()
	router.HandleFunc("/internal/server-vault/mcp/{conn}/{id}", api.handleServerVaultMCP)
	send := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"method":"tools/call","params":{"name":"linear__search"}}`))
		r.Header.Set("Authorization", resolved.Config.Headers["Authorization"])
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := send("/internal/server-vault/mcp/" + connID + "/c8"); w.Code != 401 {
		t.Fatalf("delegation reused for another app: %d", w.Code)
	}
	w := send("/internal/server-vault/mcp/" + connID + "/c9")
	if w.Code != 200 || w.Body.String() != `{"ok":true}` || w.Header().Get("X-Renewed-Token") != "" || !strings.Contains(forwarded, "linear__search") {
		t.Fatalf("forwarded call: %d %s", w.Code, w.Body.String())
	}
	if c, err := desktopConnectionFor("alice", connID); err != nil || c.Token != "remote-renewed" {
		t.Fatal("renewed server sign-in was not saved")
	}
}

// Vault step 5B (PLAT-856): a sign-in moved from the Mac is kept only once the server renewed it with the moved refresh
// token. A refused renewal leaves no app and no credential files behind and answers the plain line; access-token
// callers and web pages are refused.
func TestVaultMeImportKeepsAMovedSignInOnlyAfterOneRenewal(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", strings.Repeat("k", 32))
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	oauth.SetTokenSealer(platformClientSealer{})
	t.Cleanup(func() { oauth.SetTokenSealer(credentialSealer{}) }) // the production sealer, not nil
	const upstream = "https://mcp.example.com/mcp"
	var rejectRefresh atomic.Bool
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if rejectRefresh.Load() || r.Form.Get("refresh_token") != "mac-refresh" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"server-access","token_type":"Bearer","refresh_token":"server-refresh","expires_in":3600}`))
	}))
	defer issuer.Close()
	signIn := oauth.OAuthConfig{AuthURL: issuer.URL + "/authorize", TokenURL: issuer.URL + "/token", Resource: upstream}
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(configPath, &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Notion": {URL: upstream, OAuth: &signIn}}}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connectors := map[string]vaultOAuthConnection{}
	made := []string{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		actor := r.Header.Get("X-CapLayer-Actor")
		switch {
		case r.URL.Path == "/api/vaults" && r.Method == "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"vaults": []any{map[string]any{"group": map[string]any{"ID": "v-" + actor, "Name": personalVaultName}, "role": "owner"}}})
		case r.URL.Path == "/api/vaults/v-alice/connectors" && r.Method == "POST" && actor == "alice":
			id := fmt.Sprintf("c-%08x", len(made)+1)
			made = append(made, id)
			connectors[id] = vaultOAuthConnection{ID: id, OAuthServer: "Notion", OAuthCredentialID: id, UpstreamURL: upstream, Label: "Notion", Status: "authentication_required", VaultID: "v-alice"}
			_ = json.NewEncoder(w).Encode(connectors[id])
		case strings.HasPrefix(r.URL.Path, "/api/vaults/v-alice/connectors/") && strings.HasSuffix(r.URL.Path, "/sync"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/vaults/v-alice/connectors/"), "/sync")
			c := connectors[id]
			c.Status = "active"
			connectors[id] = c
			_, _ = w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/api/vaults/v-alice/connectors/") && r.Method == "DELETE":
			delete(connectors, strings.TrimPrefix(r.URL.Path, "/api/vaults/v-alice/connectors/"))
			w.WriteHeader(204)
		case strings.HasPrefix(r.URL.Path, "/api/admin/connectors/"):
			c, ok := connectors[strings.TrimPrefix(r.URL.Path, "/api/admin/connectors/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(c)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)

	api := &StreamingAPI{mcpConfigPath: configPath, logger: loggerv2.NewNoop()}
	router := mux.NewRouter()
	api.registerVaultMeRoutes(router.PathPrefix("/api").Subrouter())
	saved, _ := json.Marshal(signIn)
	body := `{"label":"Notion","oauth_server":"Notion","upstream_url":"` + upstream + `","config":` + string(saved) +
		`,"token":{"access_token":"mac-access","token_type":"Bearer","refresh_token":"mac-refresh","expiry":"2099-01-01T00:00:00Z"},"client":{"client_id":"dcr-client"}}`
	call := func(claims *UserClaims, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/vault/me/connections/import", strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	alice := &UserClaims{UserID: "alice", Username: "alice"}
	state := func() ([]string, map[string]vaultOAuthConnection) {
		mu.Lock()
		defer mu.Unlock()
		left := map[string]vaultOAuthConnection{}
		for id, c := range connectors {
			left[id] = c
		}
		return append([]string(nil), made...), left
	}
	credentialFiles := func(id string) []string {
		return []string{getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)), vaultCredentialConfigPath(id), getUserClientFilePath(platformMCPTokenUserID, vaultCredentialName(id))}
	}

	token := &UserClaims{UserID: "alice", Username: "alice", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}}}
	if w := call(token, ""); w.Code != 403 {
		t.Fatalf("access token admitted: %d", w.Code)
	}
	if w := call(alice, "https://page.example.com"); w.Code != 403 {
		t.Fatalf("a web page could move a sign-in: %d", w.Code)
	}
	if ids, _ := state(); len(ids) != 0 {
		t.Fatal("a refused caller created an app")
	}

	rejectRefresh.Store(true)
	w := call(alice, "")
	var refused struct {
		Error     string `json:"error"`
		OpenVault bool   `json:"open_vault"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &refused)
	if w.Code != 409 || refused.Error != "Notion wouldn't move this sign-in. Sign in to Notion once on the server; your Mac keeps its own sign-in." || !refused.OpenVault {
		t.Fatalf("refused renewal: %d %s", w.Code, w.Body.String())
	}
	ids, left := state()
	if len(ids) != 1 || len(left) != 0 {
		t.Fatalf("a refused move left an app behind: ids=%v left=%v", ids, left)
	}
	refusedID := ids[0]
	for _, path := range credentialFiles(refusedID) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("a refused move left %s behind", filepath.Base(path))
		}
	}

	rejectRefresh.Store(false)
	w = call(alice, "")
	var moved struct {
		ID     string `json:"id"`
		Synced bool   `json:"synced"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &moved)
	ids, left = state()
	if w.Code != 200 || len(ids) != 2 || moved.ID != ids[1] || !moved.Synced || left[moved.ID].VaultID != "v-alice" || left[moved.ID].Status != "active" {
		t.Fatalf("move: %d %s %+v", w.Code, w.Body.String(), left)
	}
	for _, path := range credentialFiles(moved.ID) {
		raw, err := os.ReadFile(path)
		if err != nil || strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
			t.Fatalf("%s is not sealed on the server: %v", filepath.Base(path), err)
		}
	}
	// The Vault broker now hands out the renewed sign-in for this app, and none for the removed one.
	broker := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/internal/caplayer/oauth/token", strings.NewReader(`{"server_name":"Notion","url":"`+upstream+`","connection_id":"`+id+`"}`))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		api.handleCapLayerOAuthToken(w, r)
		return w
	}
	if w := broker(moved.ID); w.Code != 200 || !strings.Contains(w.Body.String(), "server-access") {
		t.Fatalf("moved app unusable: %d %s", w.Code, w.Body.String())
	}
	if w := broker(refusedID); w.Code == 200 {
		t.Fatal("the removed app still had a sign-in")
	}
}
