package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// PLAT-708 (Confida, 2026-10-07): Vercel rejected our registration for a hosted callback, yet the person was
// linked to vercel.com/oauth/authorize with no client and saw "The app ID is invalid". A failed registration must
// return no authorize URL at all, say why in words, and mark the app "Needs admin setup" in Vault's Add app until a
// client is configured.
func TestFailedRegistrationReturnsNoAuthorizeURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	register := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_redirect_uri","error_description":"The provided redirect URIs are not approved for use by this authorization server."}`))
	}))
	defer register.Close()

	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	cfg := mcpclient.MCPServerConfig{URL: "https://mcp.vercel.com", OAuth: &oauth.OAuthConfig{
		AuthURL: "https://vercel.com/oauth/authorize", TokenURL: "https://api.vercel.com/login/oauth/token",
		RegistrationEndpoint: register.URL, TokenFile: filepath.Join(t.TempDir(), "token.json"),
	}}
	const callback = "https://app.example.com/api/oauth/callback"
	start, discovery, err := api.runOAuthFlow("", callback, oauthFlowTarget{
		Name: "Vercel", Config: cfg, ClientFile: filepath.Join(t.TempDir(), "client.json"), Notify: func(bool, string) {},
	})
	if err != nil || start != nil || discovery == nil || discovery.Status != "needs_client_id" {
		t.Fatalf("want a needs_client_id answer and no flow: start=%+v discovery=%+v err=%v", start, discovery, err)
	}
	data, _ := json.Marshal(discovery)
	if strings.Contains(string(data), "auth_url") || strings.Contains(string(data), "vercel.com/oauth/authorize") {
		t.Fatalf("the answer carries an authorize URL: %s", data)
	}
	want := "Vercel sign-in couldn't be set up automatically: the provider rejected the app registration: The provided redirect URIs are not approved for use by this authorization server (invalid_redirect_uri). An admin can register an OAuth app for it, or store its API key as a Vault secret."
	if discovery.Message != want {
		t.Fatalf("message = %q\nwant      %q", discovery.Message, want)
	}

	// The Add app list: Vercel is marked and sorted after an app that signs in on its own; a client clears the mark.
	catalog := []byte(`{"providers":[{"Name":"Vercel","OAuth":true},{"Name":"Zeta","OAuth":true}]}`)
	config := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"Vercel": cfg,
		"Zeta":   {URL: "https://mcp.zeta.example", OAuth: &oauth.OAuthConfig{AuthURL: "https://zeta.example/a", TokenURL: "https://zeta.example/t", RegistrationEndpoint: "https://zeta.example/r"}},
	}}
	apps, err := vaultCatalogApps(catalog, func(name string) bool { return oauthNeedsAdminSetup(config, name, callback) })
	if want := `{"apps":[{"name":"Zeta","oauth":true},{"name":"Vercel","oauth":true,"needs_admin_setup":true}]}`; err != nil || apps != want {
		t.Fatalf("apps = %s (%v)\nwant   %s", apps, err, want)
	}
	configured := *cfg.OAuth
	configured.ClientID = "admin-registered"
	config.MCPServers["Vercel"] = mcpclient.MCPServerConfig{URL: cfg.URL, OAuth: &configured}
	if oauthNeedsAdminSetup(config, "Vercel", callback) {
		t.Fatal("a configured client must clear Needs admin setup")
	}
}
