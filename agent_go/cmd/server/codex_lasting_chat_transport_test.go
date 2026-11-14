package server

import "testing"

// PLAT-746-941: a durable chat kind must not turn every Codex message into a
// new process. Internal child tasks retain their one-shot transport boundary.
func TestCodexLastingChatKindRetainsProcessButChildDoesNot(t *testing.T) {
	for _, tc := range []struct {
		kind, parent string
		want         bool
	}{
		{"", "", true}, {"generic_agent", "", true}, {"agent_chat", "", true},
		{"generic_agent", "parent", false}, {"", "parent", false},
	} {
		req := &QueryRequest{SessionKind: tc.kind, ParentSessionID: tc.parent}
		allows := codingAgentRequestAllowsPersistentInteractive(req, "codex-cli")
		structured := codingAgentUsesStructuredTransportForChat("codex-cli", allows)
		_, persistent, _, _, _, _ := codingAgentPersistentInteractiveFlags("codex-cli", allows, structured)
		if persistent != tc.want {
			t.Fatalf("kind=%q child=%t persistent=%t", tc.kind, tc.parent != "", persistent)
		}
	}
}
