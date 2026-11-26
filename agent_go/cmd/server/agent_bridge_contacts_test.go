package server

import "testing"

// #3822: the Ask box sends as the person, so the thread's reads and the Team
// agent's reply must accept a person-to-Team-agent conversation. A person
// talking to a plain chat (no Team agent) stays a retired route.
func TestBridgeConversationAcceptsPersonTeamAsk(t *testing.T) {
	person := agentMessageEndpoint{UserID: "owner", External: true}
	team := agentMessageEndpoint{UserID: "teammate", TeamAgentID: "team-agent"}
	chat := agentMessageEndpoint{UserID: "teammate", ID: "chat"}
	for _, tc := range []struct {
		name string
		a, b agentMessageEndpoint
		want bool
	}{
		{"person asks Team agent", person, team, true},
		{"Team agent answers person", team, person, true},
		{"person to plain chat", person, chat, false},
	} {
		if got := bridgeOrProductConversation(agentConversation{Endpoints: [2]agentMessageEndpoint{tc.a, tc.b}}); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
