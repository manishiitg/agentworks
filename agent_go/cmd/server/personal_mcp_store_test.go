package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

func withPersonalMCPRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	return root
}

// A person's servers are remote, public https, and their headers only name
// that person's own secrets.
func TestPersonalMCPServersAreRemotePublicAndSecretFree(t *testing.T) {
	withPersonalMCPRoot(t)
	for name, server := range map[string]personalMCPServer{
		"plain http":     {Name: "a", URL: "http://mcp.example.com/mcp"},
		"loopback":       {Name: "a", URL: "https://127.0.0.1/mcp"},
		"metadata":       {Name: "a", URL: "https://169.254.169.254/"},
		"internal name":  {Name: "a", URL: "https://db.internal/mcp"},
		"stdio":          {Name: "a", URL: "https://mcp.example.com/mcp", Transport: "stdio"},
		"bad name":       {Name: "../x", URL: "https://mcp.example.com/mcp"},
		"header value":   {Name: "a", URL: "https://mcp.example.com/mcp", Headers: map[string]personalMCPHeader{"X-Key": {Secret: "sk-live-123"}}},
		"header newline": {Name: "a", URL: "https://mcp.example.com/mcp", Headers: map[string]personalMCPHeader{"X\r\nY": {Secret: "KEY"}}},
	} {
		if _, err := addPersonalMCPServer("alice", server); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	server, err := addPersonalMCPServer("alice", personalMCPServer{Name: "Linear", URL: "https://mcp.linear.app/mcp",
		OAuth: &oauth.OAuthConfig{AuthURL: "https://linear.app/oauth", TokenURL: "https://api.linear.app/token", TokenFile: "/etc/passwd"}})
	if err != nil || server.Name != "linear" || server.OAuth.TokenFile != "" || !server.OAuth.PublicOnly {
		t.Fatalf("add = %+v %v", server, err)
	}
}

// Legacy personal secrets (read only by the migration) are per person and
// bound to their owner: a ciphertext copied into another person's store does
// not open.
func TestPersonalSecretsAreBoundToTheirOwner(t *testing.T) {
	root := withPersonalMCPRoot(t)
	if err := setPersonalSecret("alice", "LINEAR_API_KEY", "lin_secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := personalSecretValue("alice", "LINEAR_API_KEY"); err != nil || got != "lin_secret" {
		t.Fatalf("value = %q %v", got, err)
	}
	if _, err := personalSecretValue("bob", "LINEAR_API_KEY"); err == nil {
		t.Fatal("bob resolved alice's secret")
	}
	raw, _ := os.ReadFile(filepath.Join(root, personalMCPDirName, personalMCPStoreID("alice"), "secrets.json"))
	if bytes.Contains(raw, []byte("lin_secret")) {
		t.Fatal("secret stored in plaintext")
	}
	bobDir, _ := personalMCPDir("bob")
	if err := os.WriteFile(filepath.Join(bobDir, "secrets.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := personalSecretValue("bob", "LINEAR_API_KEY"); err == nil {
		t.Fatal("a copied ciphertext opened in another person's store")
	}
}

// Header values come from the place's own project secrets at connect time;
// the config is public-only, and its token file is sealed at rest.
func TestPlaceMCPServerConfigResolvesProjectSecretsAndSealsTokens(t *testing.T) {
	withPersonalMCPRoot(t)
	root := "_users/alice/Chats/Code/projects/x"
	store := placeMCPStoreID("alice", root)
	previous := projectSecretReader
	t.Cleanup(func() { projectSecretReader = previous })
	projectSecretReader = func(gotRoot, name string) (string, error) {
		if gotRoot != root || name != "KEY" {
			return "", os.ErrNotExist
		}
		return "abc123", nil
	}
	if _, err := addPersonalMCPServer(store, personalMCPServer{Name: "svc", URL: "https://mcp.example.com/mcp",
		Headers: map[string]personalMCPHeader{"Authorization": {Secret: "KEY", Format: "Bearer {}"}},
		OAuth:   &oauth.OAuthConfig{AuthURL: "https://example.com/authorize", TokenURL: "https://example.com/token"}}); err != nil {
		t.Fatal(err)
	}
	internal, cfg, err := personalMCPServerConfig(store, "svc")
	if err != nil || !cfg.PublicOnly || cfg.Headers["Authorization"] != "Bearer abc123" || !strings.HasSuffix(internal, "__svc") {
		t.Fatalf("config = %s %+v %v", internal, cfg, err)
	}
	if other := personalMCPInternalName(placeMCPStoreID("alice", "_users/alice/Chats/Code/projects/y"), "svc"); other == internal || !strings.HasSuffix(other, "__svc") {
		t.Fatalf("internal names collide: %s %s", internal, other)
	}
	store2 := oauth.NewTokenStore(cfg.OAuth.TokenFile)
	if err := store2.Save(&oauth2.Token{AccessToken: "tok-alice"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg.OAuth.TokenFile)
	if bytes.Contains(raw, []byte("tok-alice")) || json.Valid(raw) {
		t.Fatalf("token file is plaintext: %s", raw)
	}
	if loaded, err := store2.Load(); err != nil || loaded.AccessToken != "tok-alice" {
		t.Fatalf("load = %+v %v", loaded, err)
	}
	other := filepath.Join(t.TempDir(), "platform.json")
	if err := oauth.NewTokenStore(other).Save(&oauth2.Token{AccessToken: "tok-platform"}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(other); !bytes.Contains(raw, []byte("tok-platform")) {
		t.Fatal("a token outside the personal store changed format")
	}
}
