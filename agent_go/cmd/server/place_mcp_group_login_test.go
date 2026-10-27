package server

import (
	"testing"

	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

func googleTestServer(name string, scopes ...string) placeMCPServer {
	return placeMCPServer{
		Name: name, URL: "https://" + name + "mcp.googleapis.com/mcp/v1", Transport: "http", AppKey: "google",
		OAuth: &oauth.OAuthConfig{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", Scopes: scopes},
	}
}

// A server with its own (older) login keeps it until the next sign-in, which
// targets the group's login; a person's own OAuth client keeps its own login.
func TestPlaceMCPGroupLegacyLoginAndOwnClient(t *testing.T) {
	withMCPConnectionsRoot(t)
	// The sign-in app is read from the tokens root, which follows XDG_CONFIG_HOME; without this the
	// test read a real app from the developer's own ~/.config.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	const user = "bob"
	if _, err := addPlaceMCPServer(user, googleTestServer("gmail", "gmail.readonly")); err != nil {
		t.Fatal(err)
	}
	dir, _ := placeMCPDir(user)
	own := placeMCPTokenFile(dir, user, "gmail")
	_ = oauth.NewTokenStore(own).Save(&oauth2.Token{AccessToken: "a"})
	if _, cfg, err := placeMCPServerConfig(user, "gmail"); err != nil {
		t.Fatalf("config: %v", err)
	} else if cfg.OAuth.TokenFile != own {
		t.Fatalf("an existing login must keep working: %s", cfg.OAuth.TokenFile)
	}
	if _, cfg, _ := placeMCPServerConfigFor(user, "gmail", true); cfg.OAuth.TokenFile != placeMCPGroupTokenFile(dir, "google") {
		t.Fatalf("sign-in must target the group's login: %s", cfg.OAuth.TokenFile)
	}
	// With the person's own client the server is not grouped.
	if err := writePlaceMCPClient(user, "gmail", registeredClient{ClientID: "mine"}); err != nil {
		t.Fatal(err)
	}
	servers, _ := listPlaceMCPServers(user)
	if placeMCPGroupOf(dir, user, servers[0]) != "" {
		t.Fatal("a server with the person's own client must keep its own login")
	}
}
