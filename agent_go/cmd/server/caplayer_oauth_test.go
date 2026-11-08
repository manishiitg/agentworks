package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	oauth2 "golang.org/x/oauth2"
)

// The broker serves a live Vault connection's sign-in only (the shared platform identity was retired, 6f8a64143f):
// its refresh is serialized, and a deleted credential stops being served.
func TestCapLayerOAuthReusesRefreshAndObservesRevocation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", strings.Repeat("k", 32))
	secret := strings.Repeat("s", 32)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", secret)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	oauth.SetTokenSealer(platformClientSealer{})
	defer oauth.SetTokenSealer(nil)
	const id = "c-aaaaaaaa"
	vaultService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret || r.URL.Path != "/api/admin/connectors/"+id {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(vaultOAuthConnection{ID: id, OAuthServer: "Test", OAuthCredentialID: id, UpstreamURL: "https://example.com/mcp", Label: id, Status: "active"})
	}))
	defer vaultService.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", vaultService.URL)
	refreshes := 0
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" {
			t.Error("unexpected token exchange")
		}
		refreshes++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"rotated","token_type":"Bearer","expires_in":3600,"refresh_token":"refresh"}`))
	}))
	defer issuer.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	setup := &oauth.OAuthConfig{ClientID: "app", AuthURL: "https://example.com/authorize", TokenURL: issuer.URL}
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"Test": {URL: "https://example.com/mcp", OAuth: setup},
	}}
	if err := mcpclient.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	savedSetup, err := json.Marshal(setup)
	if err != nil {
		t.Fatal(err)
	}
	if err := oauth.WriteTokenFile(vaultCredentialConfigPath(id), savedSetup); err != nil {
		t.Fatal(err)
	}
	tokens := oauth.NewTokenStore(getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)))
	if err := tokens.Save(&oauth2.Token{AccessToken: "expired", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: path, logger: loggerv2.NewNoop()}
	request := func(auth, origin, resource string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/internal/caplayer/oauth-token", strings.NewReader(`{"server_name":"Test","url":"`+resource+`","connection_id":"`+id+`"}`))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		AuthMiddleware(http.HandlerFunc(api.handleCapLayerOAuthToken)).ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		auth, origin, url string
		want              int
	}{
		{"browser-jwt", "", "https://example.com/mcp", 401},
		{secret, "http://localhost", "https://example.com/mcp", 401},
		{secret, "", "https://attacker.example/mcp", 403},
	} {
		if rec := request(tc.auth, tc.origin, tc.url); rec.Code != tc.want {
			t.Fatalf("got %d want %d", rec.Code, tc.want)
		}
	}
	if refreshes != 0 {
		t.Fatal("unauthorized request refreshed credential")
	}
	var concurrent sync.WaitGroup
	for i := 0; i < 8; i++ {
		concurrent.Add(1)
		go func() {
			defer concurrent.Done()
			if rec := request(secret, "", "https://example.com/mcp"); rec.Code != 200 {
				t.Errorf("concurrent request failed: %d", rec.Code)
			}
		}()
	}
	concurrent.Wait()
	for i := 0; i < 2; i++ {
		rec := request(secret, "", "https://example.com/mcp")
		var result map[string]string
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result["access_token"] != "rotated" || len(result) != 1 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected broker result %d %s", rec.Code, rec.Body)
		}
	}
	if refreshes != 1 {
		t.Fatalf("refresh count=%d", refreshes)
	}
	if err := tokens.Delete(); err != nil {
		t.Fatal(err)
	}
	if rec := request(secret, "", "https://example.com/mcp"); rec.Code != 401 || strings.Contains(rec.Body.String(), "rotated") {
		t.Fatal("revoked credential still accessible")
	}
}
