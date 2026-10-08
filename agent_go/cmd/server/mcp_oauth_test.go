package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestMCPOAuthAuthorizationRefreshAndRevocation(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "mcp-oauth-test-signing-secret")
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	api := &StreamingAPI{}
	router := mux.NewRouter()
	router.HandleFunc(mcpOAuthProtectedResourcePath, api.handleMCPOAuthProtectedResource)
	router.HandleFunc(mcpOAuthMetadataPath, api.handleMCPOAuthMetadata)
	router.HandleFunc(mcpOAuthRegisterPath, api.handleMCPOAuthRegister).Methods("POST")
	router.HandleFunc(mcpOAuthAuthorizePath, api.handleMCPOAuthAuthorize).Methods("GET")
	router.HandleFunc(mcpOAuthConsentPath, api.handleMCPOAuthConsent).Methods("GET", "POST")
	router.HandleFunc(mcpOAuthTokenPath, api.handleMCPOAuthToken).Methods("POST")
	router.HandleFunc(mcpOAuthConnectionsPath, api.handleMCPOAuthConnections).Methods("GET")
	router.HandleFunc(mcpOAuthConnectionsPath+"/{id}", api.handleMCPOAuthConnections).Methods("DELETE")
	router.HandleFunc(externalMCPPath, api.handleExternalMCP).Methods("GET", "POST", "DELETE")
	router.HandleFunc("/api/auth/access-tokens", api.handleAccessTokens).Methods("GET")
	handler := AuthMiddleware(router)
	request := func(method, path, body, token, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	resource := "https://agentworks.example.com" + externalMCPPath
	metadata := request("GET", mcpOAuthProtectedResourcePath, "", "", "")
	if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), resource) {
		t.Fatal(metadata.Code, metadata.Body)
	}
	challenge := request("GET", externalMCPPath, "", "", "")
	if challenge.Code != 401 || !strings.Contains(challenge.Header().Get("WWW-Authenticate"), mcpOAuthProtectedResourcePath) {
		t.Fatal(challenge.Code, challenge.Header())
	}
	badClient := request("POST", mcpOAuthRegisterPath, `{"client_name":"Bad","redirect_uris":["http://evil.example/callback"]}`, "", "application/json")
	if badClient.Code != 400 {
		t.Fatal(badClient.Code, badClient.Body)
	}
	created := request("POST", mcpOAuthRegisterPath, `{"client_name":"Test client","redirect_uris":["https://client.example/callback"],"token_endpoint_auth_method":"none"}`, "", "application/json")
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	var client struct {
		ID string `json:"client_id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &client); err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	pkce := base64.RawURLEncoding.EncodeToString(sum[:])
	authorize := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {"https://client.example/callback"}, "scope": {"workflows:read files:read"}, "state": {"csrf-state"}, "code_challenge": {pkce}, "code_challenge_method": {"S256"}, "resource": {resource}}
	wrongResource := url.Values{}
	for k, v := range authorize {
		wrongResource[k] = v
	}
	wrongResource.Set("resource", "https://evil.example/mcp")
	if w := request("GET", mcpOAuthAuthorizePath+"?"+wrongResource.Encode(), "", "", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	started := request("GET", mcpOAuthAuthorizePath+"?"+authorize.Encode(), "", "", "")
	if started.Code != 303 {
		t.Fatal(started.Code, started.Body)
	}
	consentPath := started.Header().Get("Location")
	jwt, err := GenerateJWT(GetDefaultUserID(), "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	consent := request("GET", strings.Replace(consentPath, "/oauth/consent", mcpOAuthConsentPath, 1), "", jwt, "")
	if consent.Code != 200 || !strings.Contains(consent.Body.String(), "Test client") {
		t.Fatal(consent.Code, consent.Body)
	}
	decided := request("POST", strings.Replace(consentPath, "/oauth/consent", mcpOAuthConsentPath, 1), `{"decision":"approve"}`, jwt, "application/json")
	if decided.Code != 200 {
		t.Fatal(decided.Code, decided.Body)
	}
	var redirect struct {
		URL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(decided.Body.Bytes(), &redirect); err != nil {
		t.Fatal(err)
	}
	callback, _ := url.Parse(redirect.URL)
	if callback.Query().Get("state") != "csrf-state" {
		t.Fatal(redirect.URL)
	}
	code := callback.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://client.example/callback"}, "client_id": {client.ID}, "resource": {resource}, "code_verifier": {verifier}}
	if w := request("POST", mcpOAuthTokenPath, strings.Replace(form.Encode(), "code_verifier="+verifier, "code_verifier="+strings.Repeat("b", 43), 1), "", "application/x-www-form-urlencoded"); w.Code != 400 {
		t.Fatal("wrong verifier accepted", w.Code)
	}
	issued := request("POST", mcpOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded")
	if issued.Code != 200 {
		t.Fatal(issued.Code, issued.Body)
	}
	var pair struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if pair.Access == "" || pair.Refresh == "" || pair.Scope != "workflows:read files:read" {
		t.Fatal(pair)
	}
	if w := request("POST", mcpOAuthTokenPath, form.Encode(), "", "application/x-www-form-urlencoded"); w.Code != 400 {
		t.Fatal("code replay accepted", w.Code)
	}
	if w := request("GET", "/api/auth/access-tokens", "", pair.Access, ""); w.Code != 403 {
		t.Fatal("OAuth token escaped MCP", w.Code)
	}
	initialize := request("POST", externalMCPPath, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"oauth-test","version":"1"}}}`, pair.Access, "application/json")
	if initialize.Code != 200 || !strings.Contains(initialize.Body.String(), "protocolVersion") {
		t.Fatal("OAuth token did not reach MCP", initialize.Code, initialize.Body)
	}
	if w := request("GET", externalMCPPath+"?token="+pair.Access, "", "", ""); w.Code != 403 {
		t.Fatal("OAuth URL token accepted", w.Code)
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	grant, err := store.Authenticate(t.Context(), pair.Access)
	if err != nil {
		t.Fatal(err)
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.Refresh}, "client_id": {client.ID}, "resource": {resource}}
	rotated := request("POST", mcpOAuthTokenPath, refreshForm.Encode(), "", "application/x-www-form-urlencoded")
	if rotated.Code != 200 {
		t.Fatal(rotated.Code, rotated.Body)
	}
	var pair2 struct {
		Access string `json:"access_token"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &pair2); err != nil {
		t.Fatal(err)
	}
	if pair2.Access == pair.Access || pair2.Access == "" {
		t.Fatal("refresh did not rotate")
	}
	if w := request("POST", mcpOAuthTokenPath, refreshForm.Encode(), "", "application/x-www-form-urlencoded"); w.Code != 400 {
		t.Fatal("refresh replay accepted", w.Code)
	}
	if _, err := store.Authenticate(t.Context(), pair2.Access); err == nil {
		t.Fatal("refresh replay did not revoke family")
	}
	// A second grant can be explicitly revoked by the user, without exposing tokens.
	fresh, err := store.RegisterClient(t.Context(), "Second client", []string{"https://second.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	reqID, err := store.SaveRequest(t.Context(), mcpOAuthRequest{ClientID: fresh.ID, RedirectURI: "https://second.example/callback", Resource: resource, State: "state", Scopes: []string{"workflows:read"}, Challenge: pkce, ExpiresAt: grant.Expires})
	if err != nil {
		t.Fatal(err)
	}
	_, freshCode, err := store.Decide(t.Context(), reqID, &UserClaims{UserID: GetDefaultUserID(), Username: "owner"}, true)
	if err != nil {
		t.Fatal(err)
	}
	freshGrant, freshAccess, _, err := store.ExchangeCode(t.Context(), freshCode, fresh.ID, "https://second.example/callback", resource, verifier)
	if err != nil {
		t.Fatal(err)
	}
	listed := request("GET", mcpOAuthConnectionsPath, "", jwt, "")
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), "Second client") || strings.Contains(listed.Body.String(), freshAccess) {
		t.Fatal(listed.Code, listed.Body)
	}
	revoked := request("DELETE", mcpOAuthConnectionsPath+"/"+freshGrant.FamilyID, "", jwt, "")
	if revoked.Code != 204 {
		t.Fatal(revoked.Code, revoked.Body)
	}
	if _, err := store.Authenticate(t.Context(), freshAccess); err == nil {
		t.Fatal("revoked access accepted")
	}
}

func TestMCPOAuthOriginAllowsConfiguredLoopbackOnly(t *testing.T) {
	for _, tc := range []struct {
		url      string
		resource string
	}{
		{"https://agentworks.example.com", "https://agentworks.example.com" + externalMCPPath},
		{"http://127.0.0.1:18743", "http://127.0.0.1:18743" + externalMCPPath},
		{"http://localhost:18743", "http://localhost:18743" + externalMCPPath},
		{"http://[::1]:18743", "http://[::1]:18743" + externalMCPPath},
		{"http://agentworks.example.com", ""},
		{"http://127.0.0.1.evil.example:18743", ""},
	} {
		t.Run(tc.url, func(t *testing.T) {
			t.Setenv("PUBLIC_URL", tc.url)
			_, resource, ok := mcpOAuthURLs()
			if ok != (tc.resource != "") || resource != tc.resource {
				t.Fatalf("mcpOAuthURLs() = %q, %v; want %q", resource, ok, tc.resource)
			}
		})
	}
}
