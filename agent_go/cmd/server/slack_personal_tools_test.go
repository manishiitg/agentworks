package server

import "testing"

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
