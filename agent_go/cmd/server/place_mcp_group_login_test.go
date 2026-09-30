package server

import (
	"reflect"
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

// Gmail and Drive share one Google login with the union of their scopes; a
// server shows connected only when that login covers its scopes; the last
// server's removal deletes the shared login.
func TestPlaceMCPGroupSharesOneLogin(t *testing.T) {
	withMCPConnectionsRoot(t)
	// The sign-in app is read from the tokens root, which follows XDG_CONFIG_HOME; without this the
	// test read a real app from the developer's own ~/.config.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	const user = "alice"
	for _, s := range []placeMCPServer{googleTestServer("gmail", "gmail.readonly"), googleTestServer("drive", "drive.readonly")} {
		if _, err := addPlaceMCPServer(user, s); err != nil {
			t.Fatal(err)
		}
	}
	dir, _ := placeMCPDir(user)
	group := placeMCPGroupTokenFile(dir, "google")
	for _, name := range []string{"gmail", "drive"} {
		_, cfg, err := placeMCPServerConfig(user, name)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OAuth.TokenFile != group || !reflect.DeepEqual(cfg.OAuth.Scopes, []string{"drive.readonly", "gmail.readonly"}) {
			t.Fatalf("%s: token %s scopes %v", name, cfg.OAuth.TokenFile, cfg.OAuth.Scopes)
		}
	}

	servers, _ := listPlaceMCPServers(user)
	byName := map[string]placeMCPServer{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if placeMCPServerConnected(dir, user, byName["gmail"]) {
		t.Fatal("connected before any sign-in")
	}
	// A sign-in that granted only Gmail's scopes (made before Drive was added).
	if err := oauth.NewTokenStore(group).Save(&oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	_ = recordPlaceMCPGroupConsent(dir, "google", []string{"gmail.readonly"})
	if !placeMCPServerConnected(dir, user, byName["gmail"]) || placeMCPServerConnected(dir, user, byName["drive"]) {
		t.Fatal("Drive must need a sign-in for its extra scope; Gmail stays connected")
	}
	_ = recordPlaceMCPGroupConsent(dir, "google", []string{"drive.readonly", "gmail.readonly"})
	if !placeMCPServerConnected(dir, user, byName["drive"]) {
		t.Fatal("Drive not connected after the group sign-in covered it")
	}

	// Removing one keeps the shared login; removing the last deletes it.
	if err := removePlaceMCPServer(user, "gmail"); err != nil {
		t.Fatal(err)
	}
	_ = forgetPlaceMCPLogin(user, "gmail")
	if !fileExists(group) {
		t.Fatal("shared login deleted while Drive still uses it")
	}
	if err := removePlaceMCPServer(user, "drive"); err != nil {
		t.Fatal(err)
	}
	_ = forgetPlaceMCPLogin(user, "drive")
	if fileExists(group) {
		t.Fatal("shared login left behind after its last server")
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
