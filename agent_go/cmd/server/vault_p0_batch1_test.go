package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

func TestOAuthCallbackRejectsMismatchedBrowserCookie(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	flow := registerTestOAuthFlow(t, "state-cookie-1", "owner")

	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/api/oauth/start", nil)
	bindOAuthStartBrowser(startRec, startReq, "state-cookie-1")
	cookies := startRec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != oauthBindCookieName || !cookies[0].HttpOnly {
		t.Fatalf("expected HttpOnly browser binding cookie, got %+v", cookies)
	}

	// Callback without the binding cookie (e.g. victim's browser) must be rejected and discard the flow.
	rec := httptest.NewRecorder()
	api.handleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state=state-cookie-1&code=victim-code", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-browser callback returned %d, want 403", rec.Code)
	}
	if len(flow.CodeChan) != 0 {
		t.Fatal("cross-browser callback delivered code to flow")
	}

	// Same-browser callback with matching cookie succeeds and mints a fresh completion nonce.
	flow2 := registerTestOAuthFlow(t, "state-cookie-2", "owner")
	startRec2 := httptest.NewRecorder()
	bindOAuthStartBrowser(startRec2, startReq, "state-cookie-2")
	cbReq := httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state=state-cookie-2&code=good-code", nil)
	for _, c := range startRec2.Result().Cookies() {
		cbReq.AddCookie(c)
	}
	cbRec := httptest.NewRecorder()
	api.handleOAuthCallback(cbRec, cbReq)
	if cbRec.Code != http.StatusFound {
		t.Fatalf("same-browser callback returned %d, want 302", cbRec.Code)
	}
	loc, _ := url.Parse(cbRec.Header().Get("Location"))
	nonce := loc.Query().Get(oauthReturnParam)
	if nonce == "" || nonce == "state-cookie-2" {
		t.Fatalf("expected fresh nonce, got %q", nonce)
	}
	compReq := httptest.NewRequest(http.MethodPost, "/api/oauth/complete", strings.NewReader(`{"state":"`+nonce+`"}`))
	compReq = compReq.WithContext(context.WithValue(compReq.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
	compRec := httptest.NewRecorder()
	api.handleOAuthReturnComplete(compRec, compReq)
	if compRec.Code != http.StatusOK || <-flow2.CodeChan != "good-code" {
		t.Fatalf("same-browser completion failed: %d", compRec.Code)
	}
}

func TestGmailOAuthRedirectURIPrefersPublicURLAndSanitizesForwardedHeaders(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://agents.example.com/")
	req := httptest.NewRequest(http.MethodPost, "http://evil.invalid/start", nil)
	req.Header.Set("X-Forwarded-Proto", "javascript")
	if got := gmailOAuthRedirectURI(req); got != "https://agents.example.com"+gmailOAuthCallbackPath {
		t.Fatalf("PUBLIC_URL not preferred: %q", got)
	}

	t.Setenv("PUBLIC_URL", "")
	req2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8000/start", nil)
	req2.Header.Set("X-Forwarded-Proto", "https, http")
	req2.Header.Set("X-Forwarded-Host", "app.example.com, internal.local")
	if got := gmailOAuthRedirectURI(req2); got != "https://app.example.com"+gmailOAuthCallbackPath {
		t.Fatalf("forwarded multi-hop headers mishandled: %q", got)
	}

	req3 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8000/start", nil)
	req3.Header.Set("X-Forwarded-Proto", "javascript")
	req3.Header.Set("X-Forwarded-Host", "evil.example.com/inject")
	if got := gmailOAuthRedirectURI(req3); got != "http://127.0.0.1:8000"+gmailOAuthCallbackPath {
		t.Fatalf("invalid forwarded headers were trusted: %q", got)
	}
}

func TestGmailOAuthFinishRechecksLiveAuthorization(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", filepath.Join(dir, "clients"))
	t.Setenv("GMAIL_OAUTH_TOKEN_DIR", filepath.Join(dir, "tokens"))
	t.Setenv("GOG_HOME", filepath.Join(dir, "gog"))
	restoreGog := services.SetGogClientStore(services.StoreGogClientForTest)
	t.Cleanup(restoreGog)
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "gog"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prevSvc := services.GetGmailService()
	services.SetGmailService(nil)
	t.Cleanup(func() { services.SetGmailService(prevSvc) })
	mockWS := &mockWorkspaceAPI{files: map[string]string{
		"config/gmail-config.json": `{"enabled":true,"connections":[]}`,
	}}
	wsSrv := httptest.NewServer(mockWS)
	t.Cleanup(wsSrv.Close)
	t.Setenv("WORKSPACE_API_URL", wsSrv.URL)

	if _, err := services.EnsurePlatformOAuthClient(context.Background(), "cid.apps.googleusercontent.com", "csec"); err != nil {
		t.Fatal(err)
	}
	svc, err := ensureGmailService()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := svc.CreateConnection(context.Background(), services.GmailConnectionInput{
		Personal:    true,
		OwnerID:     "alice",
		DisplayName: "Alice Gmail",
		ClientName:  services.PlatformGoogleClientName,
	})
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := services.BeginGmailOAuth("alice", conn.ID, services.PlatformGoogleClientName, "https://agents.example.com/api/oauth/callback", true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := oauthURLStateParam(authURL)
	if state == "" {
		t.Fatal("missing state in Gmail auth URL")
	}

	// Non-owner attempting finishGmailOAuth must be refused before token exchange and state discarded.
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/complete", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "bob", Username: "bob"}))
	if _, err := api.finishGmailOAuth(req.Context(), req, state, "code-123"); err == nil {
		t.Fatal("unauthorized user passed finishGmailOAuth live re-check")
	}
	if _, stillPending := services.GmailOAuthPendingConnection(state); stillPending {
		t.Fatal("pending Gmail OAuth state was not discarded after live authorization refusal")
	}
}

func TestDropboxOAuthSetsOfflineTokenAccessType(t *testing.T) {
	// 1. Default helper adds token_access_type=offline for Dropbox HTTPS authorize URLs only.
	got := defaultOAuthExtraParams("https://www.dropbox.com/oauth2/authorize", nil)
	if got["token_access_type"] != "offline" {
		t.Fatalf("expected token_access_type=offline for Dropbox, got %v", got)
	}
	if other := defaultOAuthExtraParams("https://example.com/oauth/authorize", nil); len(other) != 0 {
		t.Fatalf("unexpected extra params for non-Dropbox URL: %v", other)
	}

	// 2. Saved connection config without ExtraAuthParams inherits catalog/Dropbox offline param on load.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy-123456")
	connID := "c-51bb180ddb62751eafc105aba4540530"
	savedCfg := oauth.OAuthConfig{
		ClientID: "dropbox-client",
		AuthURL:  "https://www.dropbox.com/oauth2/authorize",
		TokenURL: "https://api.dropboxapi.com/oauth2/token",
		Resource: "https://mcp.dropbox.com/mcp",
		UsePKCE:  true,
	}
	raw, _ := json.Marshal(savedCfg)
	if err := oauth.WriteTokenFile(vaultCredentialConfigPath(connID), raw); err != nil {
		t.Fatal(err)
	}
	templateCfg := mcpclient.MCPServerConfig{
		URL: "https://mcp.dropbox.com/mcp",
		OAuth: &oauth.OAuthConfig{
			AuthURL:         "https://www.dropbox.com/oauth2/authorize",
			TokenURL:        "https://api.dropboxapi.com/oauth2/token",
			Resource:        "https://mcp.dropbox.com/mcp",
			TokenFile:       getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(connID)),
			ExtraAuthParams: map[string]string{"token_access_type": "offline"},
		},
	}
	merged, err := vaultSavedOAuthConfig(connID, templateCfg)
	if err != nil {
		t.Fatal(err)
	}
	if merged.OAuth.ExtraAuthParams["token_access_type"] != "offline" {
		t.Fatalf("saved Dropbox config did not merge token_access_type=offline: %+v", merged.OAuth.ExtraAuthParams)
	}
	mgr := oauth.NewManager(merged.OAuth, loggerv2.NewNoop())
	_, authURL, err := mgr.GenerateAuthURL()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(authURL, "token_access_type=offline") {
		t.Fatalf("Dropbox authorize URL missing token_access_type=offline: %s", authURL)
	}

	// 3. Embedded catalog config contains token_access_type=offline for Dropbox.
	cfgBytes, err := os.ReadFile(filepath.Join("..", "..", "configs", "mcp_servers_clean.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			OAuth struct {
				ExtraAuthParams map[string]string `json:"extra_auth_params"`
			} `json:"oauth"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfgBytes, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.MCPServers["Dropbox"].OAuth.ExtraAuthParams["token_access_type"] != "offline" {
		t.Fatalf("configs/mcp_servers_clean.json Dropbox entry missing token_access_type=offline")
	}
}

func TestVaultDelegationForwardsCrewVaultHeader(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"
	in := vaultDelegation{
		Person:    "alice",
		Connector: "c-crew-1",
		CrewVault: "v-crew-p1",
		Expires:   time.Now().Add(time.Hour).Unix(),
	}
	tok := signVaultDelegation(secret, in)
	out, err := verifyVaultDelegation(secret, tok)
	if err != nil || out.CrewVault != "v-crew-p1" {
		t.Fatalf("verifyVaultDelegation = %+v, %v", out, err)
	}

	var gotCrewHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCrewHeader = r.Header.Get("X-Vault-Crew-Vault")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)

	proxy := vaultGatewayMCPProxy(target, secret, "/api/admin/runtime/mcp", out)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/vault/mcp", strings.NewReader(`{}`)))
	if rec.Code != http.StatusOK || gotCrewHeader != "v-crew-p1" {
		t.Fatalf("proxy status=%d X-Vault-Crew-Vault=%q, want v-crew-p1", rec.Code, gotCrewHeader)
	}

	// Empty CrewVault must NOT set X-Vault-Crew-Vault.
	gotCrewHeader = "sentinel"
	in.CrewVault = ""
	proxy2 := vaultGatewayMCPProxy(target, secret, "/api/admin/runtime/mcp", in)
	rec2 := httptest.NewRecorder()
	proxy2.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/internal/vault/mcp", strings.NewReader(`{}`)))
	if gotCrewHeader != "" {
		t.Fatalf("expected empty X-Vault-Crew-Vault, got %q", gotCrewHeader)
	}
}
