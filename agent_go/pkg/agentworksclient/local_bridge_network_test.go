package agentworksclient

import "testing"

// Explicit selectors and replies keep their network. Unqualified first sends
// are resolved separately against joined directories before any write.
func TestJoinedNetworkSelectorsAndReplyRouting(t *testing.T) {
	primary := localBridgeKey("claude", "11111111-2222-4333-8444-555555555555")
	own := &localSession{Attachment: AgentAttachment{AgentName: "worker"}}
	other := &localSession{Also: true, Network: "partner", ParentConfigPath: "/logins/partner.json", Attachment: AgentAttachment{AgentName: "worker"},
		ReadPosition: 1}
	b := &localBridge{sessions: map[string]*localSession{primary: own, localAlsoKey(primary, other.ParentConfigPath): other}}
	if got := b.extrasOf(primary); len(got) != 1 || got[0] != other {
		t.Fatal("joined network not found", got)
	}
	for _, c := range []struct {
		in   LocalBridgeRequest
		want *localSession
	}{
		{LocalBridgeRequest{Action: "send", Target: "reviewer"}, own},
		{LocalBridgeRequest{Action: "send", Target: "reviewer@partner"}, other},
		{LocalBridgeRequest{Action: "send", Target: "reviewer@unknown"}, own},
		{LocalBridgeRequest{Action: "send", Target: "ops@example.com"}, own},
		{LocalBridgeRequest{Action: "reply_inbox", InboxID: "inbox-partner", Network: "partner"}, other},
		{LocalBridgeRequest{Action: "peers", Network: "partner"}, other},
	} {
		if got := b.networkFor(primary, own, c.in); got != c.want {
			t.Fatalf("%+v went to the wrong network", c.in)
		}
	}
	if networkTarget("reviewer@partner", "partner") != "reviewer" || networkTarget("ops@example.com", "partner") != "ops@example.com" {
		t.Fatal("the network suffix was not removed, or another name was changed")
	}
	if b.networkFor(primary, own, LocalBridgeRequest{Action: "read", Network: "nowhere"}) != nil {
		t.Fatal("an unknown network was treated as the chat's own")
	}
}
