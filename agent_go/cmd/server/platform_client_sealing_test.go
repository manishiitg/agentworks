package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

// A platform OAuth client secret never stays in the MCP config overlay:
// connecting, the startup migration and the JSON editor all move it into the
// sealed platform client file, and mcpagent reads it back from there.
func TestPlatformClientSecretsLiveSealedOutsideTheOverlay(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withPersonalMCPRoot(t)
	dir := t.TempDir()
	api := &StreamingAPI{mcpConfigPath: filepath.Join(dir, "mcp.json"), logger: loggerv2.NewNoop()}
	if err := os.WriteFile(api.mcpConfigPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := func(secret string) mcpclient.MCPServerConfig {
		return mcpclient.MCPServerConfig{URL: "https://mcp.example.com/mcp", OAuth: &oauth.OAuthConfig{
			ClientID: "cid", ClientSecret: secret, AuthURL: "https://a.example.com/auth", TokenURL: "https://a.example.com/token"}}
	}

	// Connect persists the config.
	if err := api.persistOAuthConfig("Svc", server("s3cret-connect")); err != nil {
		t.Fatal(err)
	}
	overlay, _ := os.ReadFile(api.getUserConfigPath())
	clientFile, _ := os.ReadFile(expandPath(getUserClientFilePath(platformMCPTokenUserID, "Svc")))
	if strings.Contains(string(overlay), "s3cret") || strings.Contains(string(clientFile), "s3cret") || !strings.Contains(string(overlay), "client_secret_file") {
		t.Fatalf("overlay = %s\nclient file = %s", overlay, clientFile)
	}
	if info, _ := os.Stat(api.getUserConfigPath()); info.Mode().Perm() != 0o600 {
		t.Fatalf("overlay mode = %v", info.Mode().Perm())
	}
	loaded, _ := mcpclient.LoadConfig(api.getUserConfigPath(), api.logger)
	if secret, err := oauth.ReadClientSecretFile(loaded.MCPServers["Svc"].OAuth.ClientSecretFile); err != nil || secret != "s3cret-connect" {
		t.Fatalf("sealed secret = %q, %v", secret, err)
	}

	// An overlay written before the change is migrated at start.
	legacy := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Old": server("s3cret-legacy")}}
	if err := mcpclient.SaveConfig(api.getUserConfigPath(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := api.migratePlatformClientSecrets(); err != nil {
		t.Fatal(err)
	}
	overlay, _ = os.ReadFile(api.getUserConfigPath())
	loaded, _ = mcpclient.LoadConfig(api.getUserConfigPath(), api.logger)
	if strings.Contains(string(overlay), "s3cret") {
		t.Fatalf("migration left the secret inline: %s", overlay)
	}
	if secret, _ := oauth.ReadClientSecretFile(loaded.MCPServers["Old"].OAuth.ClientSecretFile); secret != "s3cret-legacy" {
		t.Fatalf("migrated secret = %q", secret)
	}

	// The editor may not point a server at another file.
	if isPlatformClientSecretFile("Old", "/etc/passwd") || !isPlatformClientSecretFile("Old", loaded.MCPServers["Old"].OAuth.ClientSecretFile) {
		t.Fatal("client_secret_file reference check")
	}
}

// Platform OAuth tokens and client registrations are never plain JSON on
// disk: new writes are sealed, files written before sealing are sealed at
// start, and both still read back through the token store.
func TestPlatformTokensAreSealedOnDisk(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withPersonalMCPRoot(t)
	tokenPath := getUserTokenFilePath(platformMCPTokenUserID, "Linear")
	store := oauth.NewTokenStore(tokenPath)
	if err := store.Save(&oauth2.Token{AccessToken: "at-plain-secret", RefreshToken: "rt-plain-secret", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	onDisk, _ := os.ReadFile(tokenPath)
	if strings.Contains(string(onDisk), "plain-secret") || strings.HasPrefix(strings.TrimSpace(string(onDisk)), "{") {
		t.Fatalf("platform token stored in the clear: %s", onDisk)
	}
	if loaded, err := store.Load(); err != nil || loaded.AccessToken != "at-plain-secret" {
		t.Fatalf("load = %+v %v", loaded, err)
	}

	// A legacy plain token (older catalog path) and a plain DCR client file.
	legacy := filepath.Join(mcpagentTokensRoot(), "default", "Notion.json")
	client := expandPath(getUserClientFilePath(platformMCPTokenUserID, "Notion"))
	for path, body := range map[string]string{
		legacy: `{"access_token":"legacy-secret","token_type":"Bearer"}`,
		client: `{"client_id":"cid","client_secret":"dcr-secret","redirect_uri":"https://x/cb"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := sealPlainPlatformCredentials(); err != nil || n != 2 {
		t.Fatalf("sealed %d, %v", n, err)
	}
	for _, path := range []string{legacy, client, tokenPath} {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "secret") {
			t.Fatalf("%s still plain: %s", path, raw)
		}
	}
	if secret, err := oauth.ReadClientSecretFile(client); err != nil || secret != "dcr-secret" {
		t.Fatalf("client after sealing = %q %v", secret, err)
	}
	if n, _ := sealPlainPlatformCredentials(); n != 0 {
		t.Fatalf("second pass sealed %d", n)
	}
}
