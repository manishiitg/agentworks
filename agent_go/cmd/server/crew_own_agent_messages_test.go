package server

import "testing"

// PLAT-746-852: a message from the agent's own person (their agent on another computer or login) is theirs, not
// answer-only. Only admitted endpoint identities decide: a label, a Run chat or a guest-driven chat never counts.
func TestOwnAgentMessageNeedsAdmittedSameOwnerAndNonRunEndpoints(t *testing.T) {
	dest := agentMessageEndpoint{UserID: "owner", Mode: "builder"}
	for _, tc := range []struct {
		name         string
		source, dest agentMessageEndpoint
		want         bool
	}{
		{"own agent", agentMessageEndpoint{UserID: "owner", Mode: "builder"}, dest, true},
		{"other person's agent with an owner-like label", agentMessageEndpoint{UserID: "other", Label: "owner", OwnerAuthority: true}, dest, false},
		{"unknown sender", agentMessageEndpoint{}, dest, false},
		{"Run sender", agentMessageEndpoint{UserID: "owner", Mode: "run"}, dest, false},
		{"guest-driven sender", agentMessageEndpoint{UserID: "owner", Guest: "other"}, dest, false},
		{"Run recipient", agentMessageEndpoint{UserID: "owner"}, agentMessageEndpoint{UserID: "owner", Mode: "run"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crewOwnAgentMessage(tc.dest, tc.source); got != tc.want {
				t.Fatalf("own agent = %v, want %v", got, tc.want)
			}
		})
	}
}
