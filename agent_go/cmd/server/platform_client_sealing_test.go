package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
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
