package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Every MCP call from a Code session is decided here: the pinned person's own
// server switched on for this Code resolves to its own config; switched off,
// someone else's, or a Code session without a pin is refused, never handed to
// the platform catalog; a non-Code session is left to the other scopes.
func TestCodeSessionsResolveOnlyTheirPersonsServers(t *testing.T) {
	withPersonalMCPRoot(t)
	api := &StreamingAPI{}
	ctx := context.Background()
	code := "_users/alice/Chats/Code/projects/x"
	if _, err := addPersonalMCPServer("alice", personalMCPServer{Name: "deepwiki", URL: "https://mcp.deepwiki.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := addPersonalMCPServer("bob", personalMCPServer{Name: "deepwiki", URL: "https://mcp.deepwiki.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := pinCodeSession("alice-chat", "alice", code); err != nil {
		t.Fatal(err)
	}
	aliceServer := personalMCPInternalName("alice", "deepwiki")
	bobServer := personalMCPInternalName("bob", "deepwiki")

	if _, _, err := api.resolveCodeMCPServer(ctx, "alice-chat", aliceServer, "ask"); err == nil || !strings.Contains(err.Error(), "not switched on") {
		t.Fatalf("a switched-off server resolved: %v", err)
	}
	if err := setPersonalMCPEnabled("alice", code, "deepwiki", true); err != nil {
		t.Fatal(err)
	}
	resolved, isCode, err := api.resolveCodeMCPServer(ctx, "alice-chat", aliceServer, "ask")
	if err != nil || !isCode || resolved == nil || resolved.Name != aliceServer || !resolved.Config.PublicOnly {
		t.Fatalf("own server = %+v %v %v", resolved, isCode, err)
	}
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "alice-chat", bobServer, "ask"); !isCode || err == nil {
		t.Fatalf("another person's server resolved: %v %v", isCode, err)
	}

	common.MarkCodeSession("lost-pin", code)
	t.Cleanup(func() { common.ClearSessionShellConfig("lost-pin") })
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "lost-pin", "linear", "list"); !isCode || err == nil {
		t.Fatalf("a Code session without a pin fell through: %v %v", isCode, err)
	}
	if _, isCode, err := api.resolveCodeMCPServer(ctx, "crew-chat", "linear", "list"); isCode || err != nil {
		t.Fatalf("a non-Code session was claimed: %v %v", isCode, err)
	}

	// The model sees the plain name; a selected global server of the same
	// name keeps the personal one under its internal name.
	names, overrides := personalMCPServersForTurn("alice", code, nil)
	if len(names) != 1 || names[0] != "deepwiki" || overrides["deepwiki"].Server == nil || !overrides["deepwiki"].Server.PublicOnly {
		t.Fatalf("turn servers = %v %+v", names, overrides)
	}
	if clashing, _ := personalMCPServersForTurn("alice", code, []string{"DeepWiki"}); len(clashing) != 1 || clashing[0] != aliceServer {
		t.Fatalf("clashing turn servers = %v", clashing)
	}
	if names, _ := personalMCPServersForTurn("bob", code, nil); len(names) != 0 {
		t.Fatalf("bob got servers in alice's Code: %v", names)
	}
	if got := mergeServerLists([]string{"NO_SERVERS"}, names); len(got) != 1 || got[0] != "deepwiki" {
		t.Fatalf("merge = %v", got)
	}
	if got := mergeServerLists([]string{"NO_SERVERS"}, nil); len(got) != 1 || got[0] != "NO_SERVERS" {
		t.Fatalf("merge with nothing personal = %v", got)
	}
}

// The bridge resolves a personal server by its plain name for the pinned
// person only, and a global server the Code selected keeps that name.
func TestCodeBridgeResolvesPlainPersonalNames(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	withPersonalMCPRoot(t)
	ctx := context.Background()
	if _, err := addPersonalMCPServer("owner", personalMCPServer{Name: "deepwiki", URL: "https://mcp.deepwiki.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := setPersonalMCPEnabled("owner", codePrivacyOwnerRoot, "deepwiki", true); err != nil {
		t.Fatal(err)
	}
	if err := pinCodeSession("owner-chat", "owner", codePrivacyOwnerRoot); err != nil {
		t.Fatal(err)
	}
	resolved, isCode, err := api.resolveCodeMCPServer(ctx, "owner-chat", "deepwiki", "ask")
	if err != nil || !isCode || resolved == nil || resolved.Name != personalMCPInternalName("owner", "deepwiki") || !resolved.Config.PublicOnly {
		t.Fatalf("plain personal name = %+v %v %v", resolved, isCode, err)
	}
	// Another person's plain name in their own chat is never the owner's.
	if err := pinCodeSession("other-chat", "other", codePrivacyOwnerRoot); err != nil {
		t.Fatal(err)
	}
	if resolved, _, _ := api.resolveCodeMCPServer(ctx, "other-chat", "deepwiki", "ask"); resolved != nil && resolved.Name == personalMCPInternalName("owner", "deepwiki") {
		t.Fatalf("other reached the owner's server: %+v", resolved)
	}
}
