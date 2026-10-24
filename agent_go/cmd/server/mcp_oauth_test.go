package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	if metadata.Code != 200 || !strings.Contains(metadata.Body.String(), resource) || (strings.Contains(metadata.Body.String(), "files:write") || strings.Contains(metadata.Body.String(), "dashboards:write")) {
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
	authorize := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {"https://client.example/callback"}, "scope": {"workflows:read files:read dashboards:read"}, "state": {"csrf-state"}, "code_challenge": {pkce}, "code_challenge_method": {"S256"}, "resource": {resource}}
	wrongResource := url.Values{}
	for k, v := range authorize {
		wrongResource[k] = v
	}
	wrongResource.Set("resource", "https://evil.example/mcp")
	if w := request("GET", mcpOAuthAuthorizePath+"?"+wrongResource.Encode(), "", "", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	for _, retired := range []string{"files:write", "dashboards:write"} {
		authorize.Set("scope", "workflows:read files:read dashboards:read "+retired)
		if w := request("GET", mcpOAuthAuthorizePath+"?"+authorize.Encode(), "", "", ""); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_scope") {
			t.Fatal("retired permission accepted", w.Code, w.Body)
		}
	}
	authorize.Set("scope", "workflows:read files:read dashboards:read")
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
	granted := strings.Fields(pair.Scope)
	if pair.Access == "" || pair.Refresh == "" || !slices.Contains(granted, "workflows:read") || !slices.Contains(granted, "files:read") || !slices.Contains(granted, "dashboards:read") {
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
	// Legacy read/file-write grants renew with their remaining permissions,
	// without acquiring Builder or Relay authoring access.
	if _, err := store.db.Exec(`UPDATE tokens SET scopes=? WHERE family_id=?`, `["workflows:read","files:read","dashboards:read","files:write","dashboards:write"]`, grant.FamilyID); err != nil {
		t.Fatal(err)
	}
	legacyGrant, err := store.Authenticate(t.Context(), pair.Access)
	if err != nil || !slices.Equal(legacyGrant.Scopes, []string{"workflows:read", "files:read", "dashboards:read"}) {
		t.Fatal("legacy access grant changed", legacyGrant.Scopes, err)
	}
	connections, err := store.Connections(t.Context(), GetDefaultUserID())
	if err != nil || len(connections) != 1 || !slices.Equal(connections[0].Scopes, legacyGrant.Scopes) {
		t.Fatal("retired permission exposed in connection metadata", connections, err)
	}
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {pair.Refresh}, "client_id": {client.ID}, "resource": {resource}}
	renewed := request("POST", mcpOAuthTokenPath, refreshForm.Encode(), "", "application/x-www-form-urlencoded")
	if renewed.Code != 200 {
		t.Fatal(renewed.Code, renewed.Body)
	}
	var pair2 struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(renewed.Body.Bytes(), &pair2); err != nil {
		t.Fatal(err)
	}
	if pair2.Access == pair.Access || pair2.Access == "" || pair2.Refresh == "" || pair2.Refresh != pair.Refresh || pair2.Scope != "workflows:read files:read dashboards:read" {
		t.Fatal("refresh must keep its credential and issue a new access token")
	}
	// A request for the wrong client or resource must not revoke the
	// connection that actually owns the credential.
	for _, mismatch := range [][2]string{{"other-client", resource}, {client.ID, "https://other.example.com/mcp"}} {
		if _, _, _, err := store.Refresh(t.Context(), pair.Refresh, mismatch[0], mismatch[1]); err == nil {
			t.Fatal("mismatched refresh accepted")
		}
		if _, err := store.Authenticate(t.Context(), pair2.Access); err != nil {
			t.Fatal("mismatched replay revoked the real connection", err)
		}
	}
	// Losing a response does not lose the login: the same credential still works.
	if w := request("POST", mcpOAuthTokenPath, refreshForm.Encode(), "", "application/x-www-form-urlencoded"); w.Code != 200 {
		t.Fatal("refresh retry failed", w.Code)
	}
	if _, err := store.Authenticate(t.Context(), pair2.Access); err != nil {
		t.Fatal("refresh retry revoked an existing access token", err)
	}
	if w := request("POST", externalMCPPath, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"oauth-test","version":"1"}}}`, pair2.Access, "application/json"); w.Code != 200 {
		t.Fatal("current access token lost MCP access", w.Code)
	}
	refreshForm.Set("refresh_token", pair2.Refresh)
	if w := request("POST", mcpOAuthTokenPath, refreshForm.Encode(), "", "application/x-www-form-urlencoded"); w.Code != 200 {
		t.Fatal("stable refresh retry failed", w.Code)
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
	freshGrant, freshAccess, freshRefresh, err := store.ExchangeCode(t.Context(), freshCode, fresh.ID, "https://second.example/callback", resource, verifier)
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
	if _, _, _, err := store.Refresh(t.Context(), freshRefresh, fresh.ID, resource); err == nil {
		t.Fatal("explicitly revoked refresh accepted")
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

// Separate connections reproduce two clients refreshing one saved credential.
func TestMCPOAuthStableRefreshConcurrent(t *testing.T) {
	for _, clientKind := range []string{"mcp", "cli"} {
		t.Run(clientKind, func(t *testing.T) {
			t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
			t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
			t.Setenv("AUTH_SECRET", "stable-refresh-test-signing-secret")
			first, err := openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			clientID := cliOAuthClientID
			if clientKind == "mcp" {
				client, err := first.RegisterClient(t.Context(), "Concurrent test", []string{"https://client.example/callback"})
				if err != nil {
					t.Fatal(err)
				}
				clientID = client.ID
			}
			resource := "https://agentworks.example.com/api/external/v1"
			if clientKind == "mcp" {
				resource += "/mcp"
			}
			grant := mcpOAuthGrant{FamilyID: "stable-refresh-test", ClientID: clientID, Resource: resource, UserID: "owner", Username: "owner", Scopes: []string{"workflows:read"}, WorkflowIDs: []string{"example-workflow"}}
			tx, err := first.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			initialAccess, refresh, err := issueMCPOAuthPair(t.Context(), tx, &grant)
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := first.db.Exec(`UPDATE tokens SET expires_at=? WHERE hash=?`, time.Now().Add(-time.Second).Unix(), mcpOAuthHash(initialAccess)); err != nil {
				t.Fatal(err)
			}
			var originalExpiry int64
			if err := first.db.QueryRow(`SELECT expires_at FROM tokens WHERE hash=?`, mcpOAuthHash(refresh)).Scan(&originalExpiry); err != nil {
				t.Fatal(err)
			}
			type result struct {
				access, refresh string
				err             error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for _, store := range []*mcpOAuthStore{first, second} {
				workers.Add(1)
				go func(store *mcpOAuthStore) {
					defer workers.Done()
					<-start
					_, access, renewed, err := store.Refresh(t.Context(), refresh, clientID, grant.Resource)
					results <- result{access, renewed, err}
				}(store)
			}
			close(start)
			workers.Wait()
			close(results)
			accesses := map[string]bool{}
			for result := range results {
				if result.err != nil {
					t.Fatal(result.err)
				}
				if result.refresh != refresh || result.access == "" || accesses[result.access] {
					t.Fatal("concurrent refresh did not keep one credential with distinct access tokens")
				}
				accesses[result.access] = true
				current, err := first.Authenticate(t.Context(), result.access)
				if err != nil || current.FamilyID != grant.FamilyID || !slices.Equal(current.WorkflowIDs, grant.WorkflowIDs) || !slices.Equal(current.Scopes, grant.Scopes) {
					t.Fatal("refresh changed the grant", err)
				}
			}
			var accessCount int
			if err := first.db.QueryRow(`SELECT count(*) FROM tokens WHERE kind='access' AND family_id=?`, grant.FamilyID).Scan(&accessCount); err != nil || accessCount != 2 {
				t.Fatal("expired access was not pruned", accessCount, err)
			}
			// Ignore both responses and retry, as after a lost response or restart.
			if _, _, renewed, err := second.Refresh(t.Context(), refresh, clientID, grant.Resource); err != nil || renewed != refresh {
				t.Fatal("ignored response lost the login", err)
			}
			var count, expiry int64
			if err := first.db.QueryRow(`SELECT count(*),max(expires_at) FROM tokens WHERE kind='refresh' AND family_id=?`, grant.FamilyID).Scan(&count, &expiry); err != nil || count != 1 || expiry != originalExpiry {
				t.Fatal("refresh minted another credential or extended its expiry", count, expiry, err)
			}
			if _, err := first.db.Exec(`UPDATE tokens SET expires_at=? WHERE hash=?`, time.Now().Add(-time.Second).Unix(), mcpOAuthHash(refresh)); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := second.Refresh(t.Context(), refresh, clientID, grant.Resource); err == nil {
				t.Fatal("expired refresh accepted")
			}
			if _, err := first.db.Exec(`UPDATE tokens SET expires_at=? WHERE hash=?`, originalExpiry, mcpOAuthHash(refresh)); err != nil {
				t.Fatal(err)
			}
			if err := first.RevokeFamily(t.Context(), grant.FamilyID, grant.UserID); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := second.Refresh(t.Context(), refresh, clientID, grant.Resource); err == nil {
				t.Fatal("revoked stable refresh accepted")
			}
		})
	}
}

func TestMCPOAuthStableRefreshMigration(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
	t.Setenv("AUTH_SECRET", "stable-refresh-migration-test-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	grant := mcpOAuthGrant{FamilyID: "migration-example", ClientID: cliOAuthClientID, Resource: "https://agentworks.example.com/api/external/v1", UserID: "owner", Scopes: []string{"workflows:read"}}
	tx, err := store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	oldAccess, consumed, err := issueMCPOAuthPair(t.Context(), tx, &grant)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_, active, err := issueMCPOAuthPair(t.Context(), tx, &grant)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`ALTER TABLE tokens ADD COLUMN used_at INTEGER`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE tokens SET used_at=? WHERE hash=?`, time.Now().Unix(), mcpOAuthHash(consumed)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE tokens SET expires_at=? WHERE hash=?`, time.Now().Add(-time.Second).Unix(), mcpOAuthHash(oldAccess)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO tokens SELECT 'revoked-example',kind,family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at,9,workflow_ids,project_identity,NULL FROM tokens WHERE hash=?`, mcpOAuthHash(active)); err != nil {
		t.Fatal(err)
	}
	var expiry int64
	if err := store.db.QueryRow(`SELECT expires_at FROM tokens WHERE hash=?`, mcpOAuthHash(active)).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if _, _, _, err := migrated.Refresh(t.Context(), consumed, grant.ClientID, grant.Resource); err == nil {
		t.Fatal("migration revived a consumed credential")
	}
	var remaining, revoked, savedExpiry int64
	if err := migrated.db.QueryRow(`SELECT count(*) FROM tokens WHERE hash=?`, mcpOAuthHash(oldAccess)).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("expired access remained", err)
	}
	if err := migrated.db.QueryRow(`SELECT revoked_at FROM tokens WHERE hash='revoked-example'`).Scan(&revoked); err != nil || revoked != 9 {
		t.Fatal("migration changed revocation", err)
	}
	if err := migrated.db.QueryRow(`SELECT expires_at FROM tokens WHERE hash=?`, mcpOAuthHash(active)).Scan(&savedExpiry); err != nil || savedExpiry != expiry {
		t.Fatal("migration changed refresh expiry", err)
	}
	if _, _, renewed, err := migrated.Refresh(t.Context(), active, grant.ClientID, grant.Resource); err != nil || renewed != active {
		t.Fatal("migration lost the active credential", err)
	}
	path, err := mcpOAuthSecret()
	if err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(path + ".before-stable-refresh-*")
	if err != nil || len(backups) != 1 {
		t.Fatal("missing migration backup", err)
	}
	info, err := os.Stat(backups[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("migration backup is not private", err)
	}
	backup, err := sql.Open("sqlite", "file:"+backups[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := backup.QueryRow(`SELECT count(*) FROM tokens WHERE used_at IS NOT NULL`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("backup did not retain the pre-migration state", err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	backups, err = filepath.Glob(path + ".before-stable-refresh-*")
	if err != nil || len(backups) != 1 {
		t.Fatal("migration reran", err)
	}
}
