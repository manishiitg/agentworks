package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// connectCodeForTest adds a server to a Code as its owner's connection.
func connectCodeForTest(t *testing.T, owner, root, name string) string {
	t.Helper()
	store := placeMCPStoreID(owner, root)
	if _, err := addPlaceMCPServer(store, placeMCPServer{Name: name, URL: "https://mcp.deepwiki.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := recordPlaceMCP(owner, name, root); err != nil {
		t.Fatal(err)
	}
	internal, _, err := placeMCPServerConfig(store, name)
	if err != nil {
		t.Fatal(err)
	}
	return internal
}

// Every MCP call from a Code session is decided here: only this Code's own
// connections resolve (by internal or plain name); another Code's, another
// person's, a removed one, or a Code session without a pin is refused and never
// handed to the platform catalog; a non-Code session is left to the other
// scopes.
func TestCodeSessionsResolveOnlyTheirCodesConnections(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	api := &StreamingAPI{}
	ctx := context.Background()
	codeX := "_users/alice/Chats/Code/projects/x"
	codeY := "_users/alice/Chats/Code/projects/y"
	inX := connectCodeForTest(t, "alice", codeX, "deepwiki")
	bobs := connectCodeForTest(t, "bob", "_users/bob/Chats/Code/projects/x", "deepwiki")
	if inX == bobs {
		t.Fatal("two Codes' connections of the same plain name share an internal name")
	}
	if err := pinCodeSession("alice-chat", "alice", codeX); err != nil {
		t.Fatal(err)
	}
	if err := pinCodeSession("alice-chat-y", "alice", codeY); err != nil {
		t.Fatal(err)
	}

	resolved, isCode, err := api.resolveCodeMCPServer(ctx, "alice-chat", inX, "ask")
	if err != nil || !isCode || resolved == nil || resolved.Name != inX || !resolved.Config.PublicOnly {
		t.Fatalf("own connection by internal name = %+v %v %v", resolved, isCode, err)
	}
	// Bob's connection, and Alice's connection from another of her Codes, are
	// not reachable from this chat.
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "alice-chat", bobs, "ask"); !isCode || err == nil {
		t.Fatalf("another person's connection resolved: %v %v", isCode, err)
	}
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "alice-chat-y", inX, "ask"); !isCode || err == nil {
		t.Fatalf("another Code's connection resolved: %v %v", isCode, err)
	}
	// A removed connection stops resolving at once.
	if err := removePlaceMCP("alice", "deepwiki", codeX); err != nil {
		t.Fatal(err)
	}
	if _, _, err := api.resolveCodeMCPServer(ctx, "alice-chat", inX, "ask"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("a removed connection resolved: %v", err)
	}

	common.MarkCodeSession("lost-pin", codeX)
	t.Cleanup(func() { common.ClearSessionShellConfig("lost-pin") })
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "lost-pin", "linear", "list"); !isCode || err == nil {
		t.Fatalf("a Code session without a pin fell through: %v %v", isCode, err)
	}
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "crew-chat", "linear", "list"); isCode || err != nil {
		t.Fatalf("a non-Code session was claimed: %v %v", isCode, err)
	}

	// The turn gets the Code's connections as ordinary servers with complete
	// configs, exactly as a Crew does; two Codes never share a name.
	connectCodeForTest(t, "alice", codeX, "deepwiki")
	names, overrides := attachedMCPServersForRoot(ctx, codeX)
	if len(names) != 1 || overrides[names[0]].Server == nil || !overrides[names[0]].Server.PublicOnly {
		t.Fatalf("turn servers = %v %+v", names, overrides)
	}
	if other, _ := attachedMCPServersForRoot(ctx, codeY); len(other) != 0 {
		t.Fatalf("connection leaked to another Code: %v", other)
	}
	if got := mergeServerLists([]string{"NO_SERVERS"}, names); len(got) != 1 || got[0] != names[0] {
		t.Fatalf("merge = %v", got)
	}
	if got := mergeServerLists([]string{"NO_SERVERS"}, nil); len(got) != 1 || got[0] != "NO_SERVERS" {
		t.Fatalf("merge with nothing connected = %v", got)
	}
}

// The bridge resolves a Code's connection by its plain name (what the person
// and the agent see), for that Code only.
func TestCodeBridgeResolvesPlainConnectionNames(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	ctx := context.Background()
	internal := connectCodeForTest(t, "owner", codePrivacyOwnerRoot, "deepwiki")
	if err := pinCodeSession("owner-chat", "owner", codePrivacyOwnerRoot); err != nil {
		t.Fatal(err)
	}
	resolved, isCode, err := api.resolveCodeMCPServer(ctx, "owner-chat", "deepwiki", "ask")
	if err != nil || !isCode || resolved == nil || resolved.Name != internal || !resolved.Config.PublicOnly {
		t.Fatalf("plain connection name = %+v %v %v", resolved, isCode, err)
	}
	// Someone else's chat in the owner's Code sees the Code's connection (it
	// belongs to the Code, like a Crew's); an unrelated Code has none.
	if err := pinCodeSession("other-chat", "other", "_users/other/Chats/Code/projects/mine"); err != nil {
		t.Fatal(err)
	}
	if resolved, _, _ := api.resolveCodeMCPServer(ctx, "other-chat", "deepwiki", "ask"); resolved != nil && resolved.Name == internal {
		t.Fatalf("an unrelated Code reached the owner's connection: %+v", resolved)
	}
}
