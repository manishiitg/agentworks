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

	names, overrides := personalMCPServersForTurn("alice", code)
	if len(names) != 1 || names[0] != aliceServer || overrides[aliceServer].Server == nil || !overrides[aliceServer].Server.PublicOnly {
		t.Fatalf("turn servers = %v %+v", names, overrides)
	}
	if names, _ := personalMCPServersForTurn("bob", code); len(names) != 0 {
		t.Fatalf("bob got servers in alice's Code: %v", names)
	}
	if got := mergeServerLists([]string{"NO_SERVERS"}, names); len(got) != 1 || got[0] != aliceServer {
		t.Fatalf("merge = %v", got)
	}
	if got := mergeServerLists([]string{"NO_SERVERS"}, nil); len(got) != 1 || got[0] != "NO_SERVERS" {
		t.Fatalf("merge with nothing personal = %v", got)
	}
}
