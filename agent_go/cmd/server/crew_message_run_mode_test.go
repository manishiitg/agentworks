package server

import "testing"

// A Crew has no Run mode for its owner (856-59): the owner's own message to their Crew is the owner's turn; a guest, and
// any session that is already restricted, stay restricted.
func TestCrewMessageRunModeOwnerIsNotRestricted(t *testing.T) {
	owner := agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew}
	cases := []struct {
		name         string
		dest, source agentMessageEndpoint
		guest        string
		want         bool
	}{
		{"owner's own message to the Crew", owner, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerUser}, "owner-1", false},
		{"someone else's message", owner, agentMessageEndpoint{UserID: "guest-9", Kind: triggerCallerUser}, "guest-9", true},
		{"owner's builder chat receiving", agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "builder"}, agentMessageEndpoint{UserID: "guest-9"}, "guest-9", false},
		{"receiving chat already restricted", agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "run"}, agentMessageEndpoint{UserID: "owner-1"}, "owner-1", true},
		{"sender is itself a guest session", owner, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "run"}, "owner-1", true},
		{"no sender identity", owner, agentMessageEndpoint{}, "", true},
	}
	for _, tc := range cases {
		if got := crewMessageRunMode(tc.dest, tc.source, tc.guest); got != tc.want {
			t.Errorf("%s: crewMessageRunMode = %v, want %v", tc.name, got, tc.want)
		}
	}
}
