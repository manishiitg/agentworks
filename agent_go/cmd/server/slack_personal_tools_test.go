package server

import (
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// Owner decision 2026-10-16: a personal Slack account is fully usable by its owner; anyone else running an item that has
// it added can only send and look people up. A tool the list does not name is treated as a read.
func TestPersonalSlackToolsOwnerFullOthersSendOnly(t *testing.T) {
	cases := []struct {
		caller, tool string
		want         bool
	}{
		{"owner", "slack_read_channel", true},
		{"owner", "slack_search_public_and_private", true},
		{"colleague", "slack_send_message", true},
		{"colleague", "slack_search_users", true},
		{"colleague", "slack_read_channel", false},
		{"colleague", "slack_read_thread", false},
		{"colleague", "slack_search_public_and_private", false},
		{"colleague", "some_new_tool", false},
		{"", "slack_read_channel", false},
	}
	for _, c := range cases {
		if got := personalSlackToolAllowed(c.caller, "owner", c.tool); got != c.want {
			t.Errorf("caller=%q tool=%q allowed=%v, want %v", c.caller, c.tool, got, c.want)
		}
	}
}

// A turn for someone who does not own the Slack account lists only its send side; other servers keep every tool.
func TestRestrictSelectedToolsForSlackNarrowsOnlyTheSlackServer(t *testing.T) {
	restricted := map[string]bool{"u1__slack": true}
	entries := []string{"u1__slack:slack_send_message"}
	got := restrictSelectedToolsForSlack(nil, []string{"u1__slack", "notion"}, entries, restricted)
	want := []string{"notion:*", "u1__slack:slack_send_message"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("empty selection: got %v want %v", got, want)
	}
	got = restrictSelectedToolsForSlack([]string{"notion:search", "u1__slack:slack_read_channel"}, []string{"u1__slack", "notion"}, entries, restricted)
	want = []string{"notion:search", "u1__slack:slack_send_message"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("explicit selection: got %v want %v", got, want)
	}
}

// The narrowed selection, run through the agent's real tool filter: the Slack server keeps only its send side, while
// another MCP server, a system category (workspace) and a virtual tool all stay.
func TestSlackSendOnlySelectionKeepsEveryOtherTool(t *testing.T) {
	servers := []string{"u1__slack", "notion"}
	entries, restricted := []string{"u1__slack:slack_send_message", "u1__slack:slack_search_users"}, map[string]bool{"u1__slack": true}
	selected := restrictSelectedToolsForSlack(nil, servers, entries, restricted)
	filter := mcpagent.NewToolFilter(selected, servers, nil, []string{"workspace", "human_tools"}, nil)
	for _, c := range []struct {
		pkg, tool       string
		custom, virtual bool
		want            bool
	}{
		{"u1__slack", "slack_send_message", false, false, true},
		{"u1__slack", "slack_read_channel", false, false, false},
		{"u1__slack", "slack_search_public_and_private", false, false, false},
		{"notion", "notion_search", false, false, true},
		{"workspace_tools", "read_workspace_file", true, false, true},
		{"anything", "get_api_spec", false, true, true},
	} {
		if got := filter.ShouldIncludeTool(c.pkg, c.tool, c.custom, c.virtual); got != c.want {
			t.Errorf("%s:%s included=%v, want %v", c.pkg, c.tool, got, c.want)
		}
	}
}
