package server

import "testing"

// Connecting an MCP server to a Code or Crew must change the retained
// session's fingerprint, so the next message relaunches the CLI with the new
// connection (resuming the same conversation) instead of the old process.
func TestAgentProfileSessionKeyTracksChatConnections(t *testing.T) {
	base := &resolvedAgentProfile{SelectedServers: []string{"a"}}
	withGmail := &resolvedAgentProfile{SelectedServers: []string{"a"}, ChatConnections: []string{"u1__googlegmail"}}
	reordered := &resolvedAgentProfile{SelectedServers: []string{"a"}, ChatConnections: []string{"u1__googlegmail"}}
	if agentProfileSessionKey(base) == agentProfileSessionKey(withGmail) {
		t.Fatal("adding a chat connection did not change the session key")
	}
	if agentProfileSessionKey(withGmail) != agentProfileSessionKey(reordered) {
		t.Fatal("the same connections must give the same key")
	}
	two := &resolvedAgentProfile{SelectedServers: []string{"a"}, ChatConnections: []string{"b", "c"}}
	swapped := &resolvedAgentProfile{SelectedServers: []string{"a"}, ChatConnections: []string{"c", "b"}}
	if agentProfileSessionKey(two) != agentProfileSessionKey(swapped) {
		t.Fatal("connection order must not change the key")
	}
}
