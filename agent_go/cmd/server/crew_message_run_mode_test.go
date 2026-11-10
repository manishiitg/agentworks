package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// A Crew has no Run mode for its owner (856-59). A message from any of the owner's own agents is the owner's turn
// (owner, 5 Nov); other people and Run-mode senders stay restricted.
func TestCrewMessageRunModeOwnAgentsAreBuilder(t *testing.T) {
	crewDest := agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew}
	cases := []struct {
		name         string
		dest, source agentMessageEndpoint
		guest        string
		want         bool
	}{
		{"owner's own unrestricted connection", crewDest, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerUser, External: true, OwnerAuthority: true}, "owner-1", false},
		{"owner's Crew builder chat", crewDest, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "builder"}, "owner-1", false},
		{"owner's capped or agent-bound connection", crewDest, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerUser, External: true}, "owner-1", false},
		{"owner's workflow run", crewDest, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerWorkflow}, "owner-1", false},
		{"owner's Crew session with no recorded mode", crewDest, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew}, "owner-1", false},
		{"guest-driven Crew session", crewDest, agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "run"}, "owner-1", true},
		{"someone else, even with the authority flag", crewDest, agentMessageEndpoint{UserID: "guest-9", Kind: triggerCallerUser, External: true, OwnerAuthority: true}, "guest-9", true},
		{"receiving chat already restricted", agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "run"}, agentMessageEndpoint{UserID: "owner-1", OwnerAuthority: true}, "owner-1", true},
		{"receiving chat has a recorded guest", agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Guest: "guest-9"}, agentMessageEndpoint{UserID: "owner-1", OwnerAuthority: true}, "owner-1", true},
		{"receiving owner builder chat", agentMessageEndpoint{UserID: "owner-1", Kind: triggerCallerCrew, Mode: "builder"}, agentMessageEndpoint{UserID: "guest-9"}, "guest-9", false},
		{"no sender identity", crewDest, agentMessageEndpoint{OwnerAuthority: true}, "", true},
	}
	for _, tc := range cases {
		if got := crewMessageRunMode(tc.dest, tc.source, tc.guest); got != tc.want {
			t.Errorf("%s: crewMessageRunMode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Only the person's own unrestricted connection to a Crew it may use is owner authority.
func TestExternalOwnerAuthorityNeedsAnUnrestrictedOwnConnection(t *testing.T) {
	target := triggerTarget{Kind: triggerCallerCrew, CrewID: "crew-1"}
	full := func() *accesstokens.Token {
		return &accesstokens.Token{Scopes: []string{"crews:read", "crews:run", "crews:write"}, AllCrews: true}
	}
	with := func(edit func(*accesstokens.Token)) *UserClaims {
		token := full()
		edit(token)
		return &UserClaims{UserID: "owner-1", AccessToken: token}
	}
	if !externalOwnerAuthority(&UserClaims{UserID: "owner-1", AccessToken: full()}, target) {
		t.Fatal("the person's own unrestricted connection must qualify")
	}
	for name, claims := range map[string]*UserClaims{
		"no token":            {UserID: "owner-1"},
		"run only":            with(func(tk *accesstokens.Token) { tk.Scopes = []string{"crews:read", "crews:run"} }),
		"read only":           with(func(tk *accesstokens.Token) { tk.Scopes = []string{"crews:read"} }),
		"narrowed to others":  with(func(tk *accesstokens.Token) { tk.AllCrews, tk.CrewIDs = false, []string{"crew-2"} }),
		"bound to one agent":  with(func(tk *accesstokens.Token) { tk.BoundAgentName = "some-agent" }),
		"bound by agent id":   with(func(tk *accesstokens.Token) { tk.BoundAgentID = "agent-1" }),
		"folder guard capped": with(func(tk *accesstokens.Token) { tk.FileGuard = &wf.FolderGuard{} }),
	} {
		if externalOwnerAuthority(claims, target) {
			t.Errorf("%s must not be owner authority", name)
		}
	}
	if externalOwnerAuthority(&UserClaims{UserID: "owner-1", AccessToken: full()}, triggerTarget{Kind: triggerCallerWorkflow, CrewID: "crew-1"}) {
		t.Error("a workflow target is not a Crew")
	}
	// The Crew list can name specific Crews: the one named qualifies.
	if !externalOwnerAuthority(with(func(tk *accesstokens.Token) { tk.AllCrews, tk.CrewIDs = false, []string{"crew-1"} }), target) {
		t.Error("a token naming this Crew qualifies")
	}
}

// PLAT-746-804: a network reply from an own agent must not inherit another
// person's answer-only policy; labels and Run senders never prove ownership.
func TestNetworkReplyOwnAgentNeedsAdmittedSameOwnerAndNonRunEndpoints(t *testing.T) {
	dest := agentMessageEndpoint{UserID: "owner", Mode: "builder"}
	for _, tc := range []struct {
		name         string
		source, dest agentMessageEndpoint
		want         bool
	}{
		{"own agent", agentMessageEndpoint{UserID: "owner", Mode: "builder"}, dest, true},
		{"other agent with owner-like label", agentMessageEndpoint{UserID: "other", Label: "owner", OwnerAuthority: true}, dest, false},
		{"unknown sender", agentMessageEndpoint{}, dest, false},
		{"Run sender", agentMessageEndpoint{UserID: "owner", Mode: "run"}, dest, false},
		{"guest-driven sender", agentMessageEndpoint{UserID: "owner", Guest: "other"}, dest, false},
		{"Run recipient", agentMessageEndpoint{UserID: "owner"}, agentMessageEndpoint{UserID: "owner", Mode: "run"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crewNetworkReplyOwnAgent(tc.dest, tc.source); got != tc.want {
				t.Fatalf("own agent = %v, want %v", got, tc.want)
			}
		})
	}
}
