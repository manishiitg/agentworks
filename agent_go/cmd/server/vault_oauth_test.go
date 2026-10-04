package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestVaultOAuthIndividualIdentityRefreshAndRevocation(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
	t.Setenv("AUTH_SECRET", strings.Repeat("test-auth-secret-", 3))
	t.Setenv("PUBLIC_URL", "https://platform.example")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL", "")
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"alice","role":"viewer","products":["mcp-gateway"]},{"id":"bob","role":"viewer","products":["mcp-gateway"]},{"id":"outsider","role":"viewer","products":["code"]}]}`)
	var calls []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/admin/runtime/external-mcp" || r.Header.Get("Authorization") != "Bearer service-test-private-token-at-least-32-characters" || r.Header.Get("X-Vault-Platform-User") != "1" || !strings.HasPrefix(r.Header.Get("X-Vault-OAuth-Client"), "mcp_client_") || r.Header.Get("Cookie") != "" || r.Header.Get("X-Vault-Connector") != "" || r.URL.RawQuery != "" {
			t.Error("unsafe proxy headers/path")
			w.WriteHeader(400)
			return
		}
		calls = append(calls, r.Header.Get("X-CapLayer-Actor"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer backend.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", backend.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", "service-test-private-token-at-least-32-characters")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerVaultOAuthRoutes(router)
	router.HandleFunc("/api/management", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	handler := AuthMiddleware(router)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if strings.HasSuffix(path, "/token") {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("X-CapLayer-Actor", "spoofed")
		r.Header.Set("X-Vault-Connector", "spoofed")
		r.Header.Set("Cookie", "fake=user")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, expected int) map[string]any {
		t.Helper()
		if w.Code != expected {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		out := map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	challenge := request("POST", vaultMCPPath, `{}`, "")
	if challenge.Code != 401 || !strings.Contains(challenge.Header().Get("WWW-Authenticate"), vaultResourceMetadataPath) {
		t.Fatal("missing Vault OAuth challenge")
	}
	metadata := decode(request("GET", vaultResourceMetadataPath, "", ""), 200)
	if metadata["resource"] != "https://platform.example"+vaultMCPPath || metadata["authorization_servers"].([]any)[0] != "https://platform.example/vault" {
		t.Fatal("wrong resource metadata")
	}
	as := decode(request("GET", vaultAuthorizationMetadataPath, "", ""), 200)
	if as["issuer"] != "https://platform.example/vault" {
		t.Fatal("wrong issuer")
	}
	client := decode(request("POST", vaultOAuthPrefix+"/register", `{"client_name":"Claude","redirect_uris":["http://127.0.0.1:4321/callback"]}`, ""), 201)["client_id"].(string)
	verifier := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(verifier))
	resource := "https://platform.example" + vaultMCPPath
	start := func() string {
		q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {"http://127.0.0.1:4321/callback"}, "resource": {resource}, "state": {"csrf"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}}
		w := request("GET", vaultOAuthPrefix+"/authorize?"+q.Encode(), "", "")
		if w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
		return strings.Replace(w.Header().Get("Location"), "/oauth/vault", vaultOAuthPrefix+"/consent", 1)
	}
	login := func(person string) string {
		jwt, err := GenerateJWT(person, person, person+"@example.com")
		if err != nil {
			t.Fatal(err)
		}
		return jwt
	}
	issue := func(person string) map[string]any {
		consent := start()
		response := decode(request("POST", consent, `{"decision":"approve"}`, login(person)), 200)
		dest, _ := url.Parse(response["redirect_url"].(string))
		form := url.Values{"grant_type": {"authorization_code"}, "code": {dest.Query().Get("code")}, "client_id": {client}, "redirect_uri": {"http://127.0.0.1:4321/callback"}, "resource": {resource}, "code_verifier": {verifier}}
		if w := request("POST", vaultOAuthPrefix+"/token", strings.Replace(form.Encode(), "code_verifier="+verifier, "code_verifier="+strings.Repeat("b", 43), 1), ""); w.Code != 400 {
			t.Fatal("wrong PKCE accepted")
		}
		pair := decode(request("POST", vaultOAuthPrefix+"/token", form.Encode(), ""), 200)
		if w := request("POST", vaultOAuthPrefix+"/token", form.Encode(), ""); w.Code != 400 {
			t.Fatal("code replay accepted")
		}
		return pair
	}
	alice, bob := issue("alice"), issue("bob")
	aliceConnections := decode(request("GET", vaultOAuthPrefix+"/connections", "", login("alice")), 200)["connections"].([]any)
	aliceFamily := aliceConnections[0].(map[string]any)["id"].(string)
	if w := request("DELETE", vaultOAuthPrefix+"/connections/"+aliceFamily, "", login("bob")); w.Code != 404 {
		t.Fatal("another user revoked Alice's connection")
	}
	for _, entry := range []struct {
		person string
		pair   map[string]any
	}{{"alice", alice}, {"bob", bob}} {
		if w := request("POST", vaultMCPPath+"?token=spoofed", `{}`, entry.pair["access_token"].(string)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if calls[len(calls)-1] != entry.person {
			t.Fatal("individual identities mixed")
		}
	}
	for _, token := range []string{login("alice"), "service-test-private-token-at-least-32-characters", "gwk_fake", alice["refresh_token"].(string)} {
		if w := request("POST", vaultMCPPath, `{}`, token); w.Code != 401 {
			t.Fatal("non-MCP token accepted")
		}
	}
	if w := request("GET", vaultMCPPath+"?token="+alice["access_token"].(string), "", ""); w.Code != 401 {
		t.Fatal("URL token accepted")
	}
	if w := request("GET", "/api/management", "", alice["access_token"].(string)); w.Code == 200 {
		t.Fatal("MCP token reached management")
	}
	if w := request("POST", start(), `{"decision":"approve"}`, alice["access_token"].(string)); w.Code == 200 {
		t.Fatal("MCP token approved consent")
	}
	if w := request("POST", start(), `{"decision":"approve"}`, login("outsider")); w.Code != 403 {
		t.Fatal("non-Vault account approved consent")
	}
	*directory = `{"users":[{"id":"alice","role":"viewer","products":["mcp-gateway"],"disabled":true},{"id":"bob","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	count := len(calls)
	if w := request("POST", vaultMCPPath, `{}`, alice["access_token"].(string)); w.Code != 401 || len(calls) != count {
		t.Fatal("disabled account reached backend")
	}
	refresh := func(pair map[string]any, target string) *httptest.ResponseRecorder {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair["refresh_token"].(string)}, "client_id": {client}, "resource": {target}}
		return request("POST", vaultOAuthPrefix+"/token", form.Encode(), "")
	}
	if w := refresh(alice, resource); w.Code != 400 {
		t.Fatal("disabled account refreshed")
	}
	*directory = `{"users":[{"id":"alice","role":"viewer","products":["mcp-gateway"],"disabled":true},{"id":"bob","role":"viewer","products":["code"]}]}`
	invalidateUserDirectoryCache()
	if w := request("POST", vaultMCPPath, `{}`, bob["access_token"].(string)); w.Code != 401 {
		t.Fatal("removed Vault entitlement reached backend")
	}
	if w := refresh(bob, resource); w.Code != 400 {
		t.Fatal("removed Vault entitlement refreshed")
	}
	*directory = `{"users":[{"id":"alice","role":"viewer","products":["mcp-gateway"],"disabled":true},{"id":"bob","role":"viewer","products":["mcp-gateway"]}]}`
	invalidateUserDirectoryCache()
	if w := refresh(bob, "https://evil.example/mcp"); w.Code != 400 {
		t.Fatal("wrong audience accepted")
	}
	renewed := decode(refresh(bob, resource), 200)
	families := decode(request("GET", vaultOAuthPrefix+"/connections", "", login("bob")), 200)["connections"].([]any)
	family := families[0].(map[string]any)["id"].(string)
	if w := request("DELETE", vaultOAuthPrefix+"/connections/"+family, "", login("bob")); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", vaultMCPPath, `{}`, renewed["access_token"].(string)); w.Code != 401 {
		t.Fatal("revoked grant reached backend")
	}
	if w := refresh(renewed, resource); w.Code != 400 {
		t.Fatal("revoked grant refreshed")
	}
}
