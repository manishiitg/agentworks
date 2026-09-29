package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// Proves the actual write path install_mcp_server relies on: persistOAuthConfig
// writes to <base>_user.json (getUserConfigPath), not just in-memory state, and
// a later load reads back the same server. This is the mechanism a user asked
// to have double-checked, not just re-explained from reading the code.
func TestPersistOAuthConfigWritesServerToUserConfigFile(t *testing.T) {
	baseDir := t.TempDir()
	basePath := filepath.Join(baseDir, "mcp_servers_clean.json")
	if err := mcpclient.SaveConfig(basePath, &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{}}); err != nil {
		t.Fatalf("failed to seed base config: %v", err)
	}

	api := &StreamingAPI{logger: loggerv2.NewNoop(), mcpConfigPath: basePath}

	wantConfig := mcpclient.MCPServerConfig{URL: "https://example.com/mcp", OAuth: &oauth.OAuthConfig{ClientID: "app", ClientSecret: "private-client-secret"}}
	if err := api.persistOAuthConfig("acme-test-server", wantConfig); err != nil {
		t.Fatalf("persistOAuthConfig failed: %v", err)
	}

	userConfigPath := api.getUserConfigPath()
	if userConfigPath != filepath.Join(baseDir, "mcp_servers_clean_user.json") {
		t.Fatalf("getUserConfigPath() = %q, want the _user.json sibling of the base path", userConfigPath)
	}
	info, err := os.Stat(userConfigPath)
	if err != nil {
		t.Fatalf("expected %s to exist on disk after persistOAuthConfig, got: %v", userConfigPath, err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("OAuth overlay permissions = %o, want 600", info.Mode().Perm())
	}

	// Read back via a fresh load, the same way loadMergedConfig/loadOverlay
	// do at request time -- not the in-memory api struct -- to prove this is
	// really durable on disk and not just held in memory.
	reloaded, err := mcpclient.LoadConfig(userConfigPath, loggerv2.NewNoop())
	if err != nil {
		t.Fatalf("failed to reload user config from disk: %v", err)
	}
	got, ok := reloaded.MCPServers["acme-test-server"]
	if !ok {
		t.Fatalf("acme-test-server not found in reloaded user config: %+v", reloaded.MCPServers)
	}
	if got.URL != wantConfig.URL {
		t.Fatalf("reloaded server URL = %q, want %q", got.URL, wantConfig.URL)
	}
	if got.OAuth == nil || got.OAuth.ClientSecret != wantConfig.OAuth.ClientSecret {
		t.Fatal("OAuth credentials did not survive the private overlay save")
	}
}

func TestDeriveOAuthRedirectURIUsesRequestHost(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")

	req := httptest.NewRequest("POST", "http://localhost:18743/api/oauth/start", nil)

	got := deriveOAuthRedirectURI(req)
	want := "http://localhost:18743/api/oauth/callback"
	if got != want {
		t.Fatalf("deriveOAuthRedirectURI() = %q, want %q", got, want)
	}
}

func TestDeriveOAuthRedirectURIUsesForwardedHeaders(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")

	req := httptest.NewRequest("POST", "http://internal:8000/api/oauth/start", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "app.example.com")

	got := deriveOAuthRedirectURI(req)
	want := "https://app.example.com/api/oauth/callback"
	if got != want {
		t.Fatalf("deriveOAuthRedirectURI() = %q, want %q", got, want)
	}
}

func TestDeriveOAuthRedirectURIUsesPublicURL(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://public.example.com/")

	req := httptest.NewRequest("POST", "http://localhost:18743/api/oauth/start", nil)

	got := deriveOAuthRedirectURI(req)
	want := "https://public.example.com/api/oauth/callback"
	if got != want {
		t.Fatalf("deriveOAuthRedirectURI() = %q, want %q", got, want)
	}
}
