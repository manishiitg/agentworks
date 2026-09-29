package server

import (
	"reflect"
	"testing"

	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

func googleTestServer(name string, scopes ...string) personalMCPServer {
	return personalMCPServer{
		Name: name, URL: "https://" + name + "mcp.googleapis.com/mcp/v1", Transport: "http", AppKey: "google",
		OAuth: &oauth.OAuthConfig{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", Scopes: scopes},
	}
}

// Gmail and Drive share one Google login with the union of their scopes; a
// server shows connected only when that login covers its scopes; the last
// server's removal deletes the shared login.
func TestPersonalMCPGroupSharesOneLogin(t *testing.T) {
	withPersonalMCPRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	const user = "alice"
	for _, s := range []personalMCPServer{googleTestServer("gmail", "gmail.readonly"), googleTestServer("drive", "drive.readonly")} {
		if _, err := addPersonalMCPServer(user, s); err != nil {
			t.Fatal(err)
		}
	}
	dir, _ := personalMCPDir(user)
	group := personalMCPGroupTokenFile(dir, "google")
	for _, name := range []string{"gmail", "drive"} {
		_, cfg, err := personalMCPServerConfig(user, name)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OAuth.TokenFile != group || !reflect.DeepEqual(cfg.OAuth.Scopes, []string{"drive.readonly", "gmail.readonly"}) {
			t.Fatalf("%s: token %s scopes %v", name, cfg.OAuth.TokenFile, cfg.OAuth.Scopes)
		}
	}

	servers, _ := listPersonalMCPServers(user)
	byName := map[string]personalMCPServer{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if personalMCPServerConnected(dir, user, byName["gmail"]) {
		t.Fatal("connected before any sign-in")
	}
	// A sign-in that granted only Gmail's scopes (made before Drive was added).
	if err := oauth.NewTokenStore(group).Save(&oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	_ = recordPersonalMCPGroupConsent(dir, "google", []string{"gmail.readonly"})
	if !personalMCPServerConnected(dir, user, byName["gmail"]) || personalMCPServerConnected(dir, user, byName["drive"]) {
		t.Fatal("Drive must need a sign-in for its extra scope; Gmail stays connected")
	}
	_ = recordPersonalMCPGroupConsent(dir, "google", []string{"drive.readonly", "gmail.readonly"})
	if !personalMCPServerConnected(dir, user, byName["drive"]) {
		t.Fatal("Drive not connected after the group sign-in covered it")
	}

	// Removing one keeps the shared login; removing the last deletes it.
	if err := removePersonalMCPServer(user, "gmail"); err != nil {
		t.Fatal(err)
	}
	_ = forgetPersonalMCPLogin(user, "gmail")
	if !fileExists(group) {
		t.Fatal("shared login deleted while Drive still uses it")
	}
	if err := removePersonalMCPServer(user, "drive"); err != nil {
		t.Fatal(err)
	}
	_ = forgetPersonalMCPLogin(user, "drive")
	if fileExists(group) {
		t.Fatal("shared login left behind after its last server")
	}
}

// A server with its own (older) login keeps it until the next sign-in, which
// targets the group's login; a person's own OAuth client keeps its own login.
func TestPersonalMCPGroupLegacyLoginAndOwnClient(t *testing.T) {
	withPersonalMCPRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	const user = "bob"
	if _, err := addPersonalMCPServer(user, googleTestServer("gmail", "gmail.readonly")); err != nil {
		t.Fatal(err)
	}
	dir, _ := personalMCPDir(user)
	own := personalMCPTokenFile(dir, user, "gmail")
	_ = oauth.NewTokenStore(own).Save(&oauth2.Token{AccessToken: "a"})
	if _, cfg, _ := personalMCPServerConfig(user, "gmail"); cfg.OAuth.TokenFile != own {
		t.Fatalf("an existing login must keep working: %s", cfg.OAuth.TokenFile)
	}
	if _, cfg, _ := personalMCPServerConfigFor(user, "gmail", true); cfg.OAuth.TokenFile != personalMCPGroupTokenFile(dir, "google") {
		t.Fatalf("sign-in must target the group's login: %s", cfg.OAuth.TokenFile)
	}
	// With the person's own client the server is not grouped.
	if err := writePersonalMCPClient(user, "gmail", registeredClient{ClientID: "mine"}); err != nil {
		t.Fatal(err)
	}
	servers, _ := listPersonalMCPServers(user)
	if personalMCPGroupOf(dir, user, servers[0]) != "" {
		t.Fatal("a server with the person's own client must keep its own login")
	}
}
