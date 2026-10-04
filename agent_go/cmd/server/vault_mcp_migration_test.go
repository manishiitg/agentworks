package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

func TestVaultOAuthMigrationResealsWithoutExposingOrOverwritingCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", strings.Repeat("k", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	oauth.SetTokenSealer(credentialSealer{})
	defer oauth.SetTokenSealer(credentialSealer{})
	const id = "c-12345678"
	const endpoint = "https://example.com/mcp"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(vaultOAuthConnection{ID: id, OAuthCredentialID: id, OAuthServer: "Linear", UpstreamURL: endpoint, Status: "authentication_required"})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	source := expandPath(getUserTokenFilePath("legacy-owner", "Linear"))
	data := []byte(`{"access_token":"fixture-access","refresh_token":"fixture-refresh"}`)
	if err := oauth.WriteTokenFile(source, data); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(source)
	path := filepath.Join(t.TempDir(), "catalog.json")
	cfg := mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Linear": {URL: endpoint, OAuth: &oauth.OAuthConfig{ClientID: "original-client", AuthURL: "https://example.com/authorize", TokenURL: "https://example.com/token", TokenFile: source}}}}
	writeConfig := func() {
		t.Helper()
		raw, _ := json.Marshal(cfg)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	api := &StreamingAPI{mcpConfigPath: path}
	dest := expandPath(getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)))
	status, err := importVaultOAuthCredential(context.Background(), api, id, false)
	if err != nil || status != "ready to import" {
		t.Fatal(status, err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dry run wrote credential")
	}
	status, err = importVaultOAuthCredential(context.Background(), api, id, true)
	if err != nil || status != "imported" {
		t.Fatal(status, err)
	}
	sealed, err := os.ReadFile(dest)
	if err != nil || strings.Contains(string(sealed), "fixture-access") || string(sealed) == string(before) {
		t.Fatal("credential not resealed")
	}
	got, err := oauth.ReadTokenFile(dest)
	if err != nil || string(got) != string(data) {
		t.Fatal("imported credential unreadable", err)
	}
	after, _ := os.ReadFile(source)
	if string(after) != string(before) {
		t.Fatal("legacy credential changed")
	}
	info, _ := os.Stat(dest)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions widened")
	}
	saved, err := api.vaultOAuthConfig(context.Background(), id, "Linear", endpoint)
	if err != nil || saved.OAuth.ClientID != "original-client" || saved.OAuth.TokenFile != dest {
		t.Fatal("original client/binding lost", err)
	}
	refreshed := []byte(`{"access_token":"new-fixture"}`)
	if err := oauth.WriteTokenFile(dest, refreshed); err != nil {
		t.Fatal(err)
	}
	status, err = importVaultOAuthCredential(context.Background(), api, id, true)
	if err != nil || status != "already imported" {
		t.Fatal(status, err)
	}
	got, _ = oauth.ReadTokenFile(dest)
	if string(got) != string(refreshed) {
		t.Fatal("refreshed token overwritten")
	}
	if err := writeImportedVaultCredential(dest, data); err == nil {
		t.Fatal("exclusive destination write overwritten")
	}
	// Remove only test destinations to exercise absent and out-of-root sources.
	os.Remove(dest)
	os.Remove(expandPath(vaultCredentialConfigPath(id)))
	os.Remove(source)
	status, err = importVaultOAuthCredential(context.Background(), api, id, true)
	if err != nil || status != "sign-in required" {
		t.Fatal(status, err)
	}
	row := cfg.MCPServers["Linear"]
	row.OAuth.TokenFile = filepath.Join(t.TempDir(), "external.json")
	writeConfig()
	if _, err := importVaultOAuthCredential(context.Background(), api, id, true); err == nil {
		t.Fatal("source outside credential root accepted")
	}
}
