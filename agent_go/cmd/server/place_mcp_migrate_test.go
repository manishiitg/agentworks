package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

func legacyEnabled(t *testing.T, person, root string) []string {
	t.Helper()
	dir, err := placeMCPDir(person)
	if err != nil {
		t.Fatal(err)
	}
	enabled := map[string][]string{}
	if err := readPlaceMCPJSON(dir+"/enabled.json", &enabled); err != nil {
		t.Fatal(err)
	}
	return enabled[cleanCodeRoot(root)]
}

// The old per-person, per-Code switches become each Code's own connections:
// servers, logins and header secrets move; a login moves to the first Code
// only (a rotating refresh token must not live in two places); a switch in
// someone else's Code is left alone; nothing is deleted from the old store; a
// second run changes nothing.
func TestMigrationMovesCodeConnectionsOntoPlaces(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	chat, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{chatStore: chat}
	previous := projectSecretReader
	t.Cleanup(func() { projectSecretReader = previous })
	projectSecretReader = api.projectSecretValue
	ctx := context.Background()
	codeX := "_users/alice/Chats/Code/projects/x"
	codeY := "_users/alice/Chats/Code/projects/y"
	bobs := "_users/bob/Chats/Code/projects/z"

	gmail := placeMCPServer{Name: "gmail", URL: "https://mcp.example.com/gmail", OAuth: &oauth.OAuthConfig{AuthURL: "https://example.com/authorize", TokenURL: "https://example.com/token"}}
	keyed := placeMCPServer{Name: "keyed", URL: "https://mcp.example.com/keyed", Headers: map[string]placeMCPHeader{"Authorization": {Secret: "API_KEY", Format: "Bearer {}"}}}
	for _, server := range []placeMCPServer{gmail, keyed} {
		if _, err := addPlaceMCPServer("alice", server); err != nil {
			t.Fatal(err)
		}
	}
	if err := setPersonalSecret("alice", "API_KEY", "sk-legacy"); err != nil {
		t.Fatal(err)
	}
	dir, _ := placeMCPDir("alice")
	if err := oauth.NewTokenStore(placeMCPTokenFile(dir, "alice", "gmail")).Save(&oauth2.Token{AccessToken: "tok-alice"}); err != nil {
		t.Fatal(err)
	}
	for root, names := range map[string][]string{codeX: {"gmail", "keyed"}, codeY: {"gmail"}, bobs: {"keyed"}} {
		if err := setPersonalMCPEnabledNames("alice", root, names); err != nil {
			t.Fatal(err)
		}
	}

	moved, err := api.migrateCodePersonalMCPFor(ctx, "alice")
	if err != nil || moved != 3 {
		t.Fatalf("moved %d, %v", moved, err)
	}
	xNames, _ := attachedMCPServersForRoot(ctx, codeX)
	yNames, _ := attachedMCPServersForRoot(ctx, codeY)
	if len(xNames) != 2 || len(yNames) != 1 {
		t.Fatalf("Code x has %v, Code y has %v", xNames, yNames)
	}
	if other, _ := attachedMCPServersForRoot(ctx, bobs); len(other) != 0 {
		t.Fatalf("a switch in someone else's Code was moved: %v", other)
	}

	// The login came along to the first Code, sealed for its new path.
	_, cfg, err := placeMCPServerConfig(placeMCPStoreID("alice", codeX), "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if loaded, err := oauth.NewTokenStore(cfg.OAuth.TokenFile).Load(); err != nil || loaded.AccessToken != "tok-alice" {
		t.Fatalf("login in Code x = %+v %v", loaded, err)
	}
	_, cfgY, err := placeMCPServerConfig(placeMCPStoreID("alice", codeY), "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.NewTokenStore(cfgY.OAuth.TokenFile).Load(); err == nil {
		t.Fatal("the login was copied to a second Code")
	}

	// The header server's secret is now the Code's own project secret.
	_, keyedCfg, err := placeMCPServerConfig(placeMCPStoreID("alice", codeX), "keyed")
	if err != nil || keyedCfg.Headers["Authorization"] != "Bearer sk-legacy" {
		t.Fatalf("header server = %+v %v", keyedCfg.Headers, err)
	}
	if value, err := api.projectSecretValue(codeX, "API_KEY"); err != nil || value != "sk-legacy" {
		t.Fatalf("project secret = %q %v", value, err)
	}

	// Switches are cleared where the move happened, kept where it did not,
	// and the old store is untouched.
	if got := legacyEnabled(t, "alice", codeX); len(got) != 0 {
		t.Fatalf("Code x still switched on: %v", got)
	}
	if got := legacyEnabled(t, "alice", bobs); len(got) != 1 {
		t.Fatalf("someone else's Code lost its switch: %v", got)
	}
	if old, _ := listPlaceMCPServers("alice"); len(old) != 2 {
		t.Fatalf("the old store changed: %+v", old)
	}
	again, err := api.migrateCodePersonalMCPFor(ctx, "alice")
	if err != nil || again != 0 {
		t.Fatalf("second run moved %d, %v", again, err)
	}
}
