package mcpoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCIMDURLValidationAndSSRFRefusal(t *testing.T) {
	invalid := []string{
		"",
		"mcp_client_123",
		"http://client.example/oauth/metadata.json",
		"https://client.example",
		"https://client.example/",
		"https://client.example/oauth/../secret.json",
		"https://client.example/oauth/%2e%2e/secret.json",
		"https://client.example/oauth/metadata.json?foo=bar",
		"https://client.example/oauth/metadata.json#frag",
		"https://user:pass@client.example/oauth/metadata.json",
		"https://localhost/oauth/metadata.json",
		"https://foo.localhost/oauth/metadata.json",
		"https://metadata.google.internal/computeMetadata/v1/",
		"https://internal.local/oauth/metadata.json",
		"https://127.0.0.1/oauth/metadata.json",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/oauth/metadata.json",
		"https://[::1]/oauth/metadata.json",
	}
	for _, raw := range invalid {
		if _, err := DefaultCIMDResolver.Resolve(context.Background(), raw); err == nil {
			t.Fatalf("DefaultCIMDResolver.Resolve(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestCIMDMetadataValidationAndCaching(t *testing.T) {
	var fetches atomic.Int32
	var mode atomic.Value
	mode.Store("valid")

	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		id := ts.URL + r.URL.Path
		switch mode.Load().(string) {
		case "valid":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "public, max-age=120")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  id,
				"client_name":                "CIMD Test Client",
				"redirect_uris":              []string{"https://client.example/callback"},
				"grant_types":                []string{"authorization_code", "refresh_token"},
				"response_types":             []string{"code"},
				"token_endpoint_auth_method": "none",
			})
		case "no-store":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":     id,
				"client_name":   "No-Store Client",
				"redirect_uris": []string{"https://client.example/callback"},
			})
		case "mismatch-id":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":     "https://other.example/meta.json",
				"client_name":   "Spoof",
				"redirect_uris": []string{"https://client.example/callback"},
			})
		case "bad-auth":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  id,
				"client_name":                "Confidential",
				"redirect_uris":              []string{"https://client.example/callback"},
				"token_endpoint_auth_method": "client_secret_post",
			})
		case "redirect":
			http.Redirect(w, r, ts.URL+"/other.json", http.StatusFound)
		case "too-large":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"client_id":"` + id + `","pad":"` + strings.Repeat("a", maxCIMDBodyBytes+100) + `"}`))
		}
	}))
	defer ts.Close()

	resolver := NewCIMDResolverWithClient(ts.Client(), true)

	// Valid fetch is cached according to Cache-Control: max-age=120.
	c1, err := resolver.Resolve(context.Background(), ts.URL+"/oauth/client.json")
	if err != nil || c1.Name != "CIMD Test Client" || len(c1.RedirectURIs) != 1 {
		t.Fatalf("valid CIMD resolve failed: %+v err=%v", c1, err)
	}
	c2, err := resolver.Resolve(context.Background(), ts.URL+"/oauth/client.json")
	if err != nil || c2.Name != "CIMD Test Client" {
		t.Fatalf("cached CIMD resolve failed: %+v err=%v", c2, err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("expected 1 HTTP fetch for cached CIMD doc, got %d", got)
	}

	// no-store must not cache.
	mode.Store("no-store")
	if _, err := resolver.Resolve(context.Background(), ts.URL+"/oauth/nostore.json"); err != nil {
		t.Fatalf("no-store resolve 1 failed: %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), ts.URL+"/oauth/nostore.json"); err != nil {
		t.Fatalf("no-store resolve 2 failed: %v", err)
	}
	if got := fetches.Load(); got != 3 {
		t.Fatalf("expected 3 total HTTP fetches (1 cached + 2 no-store), got %d", got)
	}

	for _, bad := range []string{"mismatch-id", "bad-auth", "redirect", "too-large"} {
		mode.Store(bad)
		if _, err := resolver.Resolve(context.Background(), ts.URL+"/oauth/"+bad+".json"); err == nil {
			t.Fatalf("mode %q unexpectedly succeeded", bad)
		}
	}
}

func TestCIMDAuthorizationConsentTokenRefreshAndConnections(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  ts.URL + r.URL.Path,
			"client_name":                "Modern MCP App",
			"redirect_uris":              []string{"https://app.example/oauth/callback"},
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"token_endpoint_auth_method": "none",
		})
	}))
	defer ts.Close()

	clientID := ts.URL + "/oauth/client-metadata.json"
	path := filepath.Join(t.TempDir(), "oauth.sqlite")
	cfg := Config{
		PublicURL:             "https://gateway.example",
		ResourcePath:          "/mcp",
		Scopes:                []string{"mcp"},
		AccessPrefix:          "gw_mcp_",
		RefreshPrefix:         "gw_mcp_refresh_",
		ConsentUIPath:         "/oauth/consent",
		ProtectedResourcePath: "/.well-known/oauth-protected-resource/mcp",
		RegisterPath:          "/api/oauth/mcp/register",
		AuthorizePath:         "/api/oauth/mcp/authorize",
		TokenPath:             "/api/oauth/mcp/token",
		CIMDResolver:          NewCIMDResolverWithClient(ts.Client(), true),
		CurrentUser: func(*http.Request) (*User, bool) {
			return &User{ID: "u1", Username: "alice", Email: "alice@example.com"}, true
		},
	}
	cfg.OpenStore = func() (*Store, error) { return OpenStore(path, cfg) }
	srv := NewServer(cfg)

	// Metadata advertises SEP-991 support.
	metaRec := httptest.NewRecorder()
	srv.HandleMetadata(metaRec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	var meta map[string]any
	if metaRec.Code != http.StatusOK || json.Unmarshal(metaRec.Body.Bytes(), &meta) != nil || meta["client_id_metadata_document_supported"] != true {
		t.Fatalf("metadata missing client_id_metadata_document_supported=true: %d %s", metaRec.Code, metaRec.Body.String())
	}

	verifier := strings.Repeat("c", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// Wrong redirect_uri must be rejected.
	badAuthz := url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {"https://evil.example/cb"}, "scope": {"mcp"},
		"state": {"s1"}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "resource": {"https://gateway.example/mcp"},
	}
	badRec := httptest.NewRecorder()
	srv.HandleAuthorize(badRec, httptest.NewRequest(http.MethodGet, "/authorize?"+badAuthz.Encode(), nil))
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("unlisted redirect_uri accepted: %d %s", badRec.Code, badRec.Body.String())
	}

	// Valid redirect_uri starts consent without prior DCR.
	goodAuthz := url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {"https://app.example/oauth/callback"}, "scope": {"mcp"},
		"state": {"s1"}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "resource": {"https://gateway.example/mcp"},
	}
	authzRec := httptest.NewRecorder()
	srv.HandleAuthorize(authzRec, httptest.NewRequest(http.MethodGet, "/authorize?"+goodAuthz.Encode(), nil))
	if authzRec.Code != http.StatusSeeOther {
		t.Fatalf("CIMD authorize failed: %d %s", authzRec.Code, authzRec.Body.String())
	}
	loc, err := url.Parse(authzRec.Header().Get("Location"))
	if err != nil || loc.Query().Get("request") == "" {
		t.Fatalf("missing request id in redirect %q", authzRec.Header().Get("Location"))
	}
	reqID := loc.Query().Get("request")

	// Consent displays the verified CIMD client_name.
	consentGet := httptest.NewRecorder()
	srv.HandleConsent(consentGet, httptest.NewRequest(http.MethodGet, "/consent?request="+url.QueryEscape(reqID), nil))
	if consentGet.Code != http.StatusOK || !strings.Contains(consentGet.Body.String(), "Modern MCP App") {
		t.Fatalf("consent GET missing CIMD client_name: %d %s", consentGet.Code, consentGet.Body.String())
	}
	consentPost := httptest.NewRecorder()
	srv.HandleConsent(consentPost, httptest.NewRequest(http.MethodPost, "/consent?request="+url.QueryEscape(reqID), strings.NewReader(`{"decision":"approve"}`)))
	var approval struct {
		RedirectURL string `json:"redirect_url"`
	}
	if consentPost.Code != http.StatusOK || json.Unmarshal(consentPost.Body.Bytes(), &approval) != nil {
		t.Fatalf("consent POST failed: %d %s", consentPost.Code, consentPost.Body.String())
	}
	cb, _ := url.Parse(approval.RedirectURL)
	code := cb.Query().Get("code")

	// Token endpoint rejects client_secret / Authorization header for auth_method=none CIMD clients.
	secretForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"https://app.example/oauth/callback"}, "client_id": {clientID},
		"client_secret": {"should-not-be-sent"},
		"resource": {"https://gateway.example/mcp"}, "code_verifier": {verifier},
	}
	secretReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(secretForm.Encode()))
	secretReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secretRec := httptest.NewRecorder()
	srv.HandleToken(secretRec, secretReq)
	if secretRec.Code != http.StatusUnauthorized {
		t.Fatalf("CIMD token request with client_secret accepted: %d %s", secretRec.Code, secretRec.Body.String())
	}

	// Token exchange without client credentials succeeds.
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"https://app.example/oauth/callback"}, "client_id": {clientID},
		"resource": {"https://gateway.example/mcp"}, "code_verifier": {verifier},
	}
	tokReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRec := httptest.NewRecorder()
	srv.HandleToken(tokRec, tokReq)
	var pair struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if tokRec.Code != http.StatusOK || json.Unmarshal(tokRec.Body.Bytes(), &pair) != nil || pair.Access == "" || pair.Refresh == "" {
		t.Fatalf("CIMD token exchange failed: %d %s", tokRec.Code, tokRec.Body.String())
	}

	// Refresh succeeds and connections list displays the CIMD client name.
	refForm := url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {pair.Refresh},
		"client_id": {clientID}, "resource": {"https://gateway.example/mcp"},
	}
	refReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refForm.Encode()))
	refReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	refRec := httptest.NewRecorder()
	srv.HandleToken(refRec, refReq)
	if refRec.Code != http.StatusOK {
		t.Fatalf("CIMD refresh failed: %d %s", refRec.Code, refRec.Body.String())
	}

	connRec := httptest.NewRecorder()
	srv.HandleConnections(connRec, httptest.NewRequest(http.MethodGet, "/connections", nil))
	if connRec.Code != http.StatusOK || !strings.Contains(connRec.Body.String(), "Modern MCP App") {
		t.Fatalf("connections missing CIMD client: %d %s", connRec.Code, connRec.Body.String())
	}
}
