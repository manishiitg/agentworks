package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

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
