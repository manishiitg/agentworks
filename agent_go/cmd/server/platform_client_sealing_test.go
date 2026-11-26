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
// connecting and the JSON editor both move it into the
// sealed platform client file, and mcpagent reads it back from there.
func TestPlatformClientSecretsLiveSealedOutsideTheOverlay(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withMCPConnectionsRoot(t)
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

	// The editor may not point a server at another file.
	if isPlatformClientSecretFile("Svc", "/etc/passwd") || !isPlatformClientSecretFile("Svc", loaded.MCPServers["Svc"].OAuth.ClientSecretFile) {
		t.Fatal("client_secret_file reference check")
	}
}

// Platform OAuth tokens and client registrations are never plain JSON on
// disk: every write is sealed and reads back through the token store.
func TestPlatformTokensAreSealedOnDisk(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withMCPConnectionsRoot(t)
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

	// PLAT-746-260: GitHub account records are sealed and bound to their person; the reader opens them.
	githubAccount := githubConnectionPath("alice", "0123456789abcdef0123456789abcdef")
	if err := writeGitHubRecord("alice", githubAccountRecord{ID: "0123456789abcdef0123456789abcdef", Login: "example-user", Source: "gh", VaultID: "v-example", SecretName: "VLT_example__GITHUB_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	account := readGitHubConnection("alice", "0123456789abcdef0123456789abcdef")
	if account == nil || account.Login != "example-user" || account.Source != "gh" || account.SecretName != "VLT_example__GITHUB_TOKEN" {
		t.Fatal("GitHub account could not be read back")
	}
	if readGitHubConnection("bob", "0123456789abcdef0123456789abcdef") != nil {
		t.Fatal("another person inherited the GitHub connection")
	}
	account.Scopes = "repo"
	if err := writeGitHubRecord("alice", *account); err != nil {
		t.Fatal(err)
	}
	rawAccount, err := os.ReadFile(githubAccount)
	if err != nil || strings.HasPrefix(strings.TrimSpace(string(rawAccount)), "{") || strings.Contains(string(rawAccount), "example-user") {
		t.Fatal("GitHub account update must stay sealed")
	}
	if account = readGitHubConnection("alice", "0123456789abcdef0123456789abcdef"); account == nil || account.Scopes != "repo" {
		t.Fatal("updated GitHub account could not be read")
	}
	if err := os.MkdirAll(filepath.Dir(githubConnectionPath("bob", "0123456789abcdef0123456789abcdef")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(githubConnectionPath("bob", "0123456789abcdef0123456789abcdef"), rawAccount, 0o600); err != nil {
		t.Fatal(err)
	}
	if readGitHubConnection("bob", "0123456789abcdef0123456789abcdef") != nil {
		t.Fatal("copied GitHub connection bypassed person-bound sealing")
	}
	setRegistrationFailure("https://example.com/register", "https://example.com/callback", "Still needs setup")
	registrationRecord, err := os.ReadFile(registrationFailuresPath())
	if err != nil || strings.HasPrefix(strings.TrimSpace(string(registrationRecord)), "{") || !registrationFailed("https://example.com/register", "https://example.com/callback") {
		t.Fatal("provider registration record must be sealed and readable")
	}
}
