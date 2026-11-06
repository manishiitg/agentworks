package server

import (
	"context"
	"strings"
	"testing"
)

// When others ask (owner, 5 Nov): a teammate's ask of the owner's agent gets the fixed note and the owner's own rule
// for that turn (resolveAgentProfileForQuery puts it in AgentProfileContext.OthersAsking); the owner's own turn gets
// nothing.
func TestWhenOthersAskNoteOnlyForAnotherPerson(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"teammate","username":"teammate","products":["work"]}]}`)
	key := "wo-ask:chat:helper"
	team := crewTeam{ProjectID: "wo-ask", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "teammate"}},
		AgentRules: map[string]map[string]crewTeamAgentRule{"owner": {key: {WhenOthersAsk: "Only answer about the website project."}}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, req := range map[string]QueryRequest{
		"guest call":          {CrewGuestCaller: "teammate", PinRunMode: true},
		"agent message":       {AskedBy: "teammate", AgentMessageFromOtherPerson: true},
		"shared Mac agent":    {SharedAgentSender: "teammate"},
		"own turn in my Crew": {},
	} {
		userID := "owner"
		if name == "own turn in my Crew" {
			userID = "teammate" // a person's own turn in the owner's Crew is still another person asking
		}
		note := whenOthersAskForTurn(ctx, &req, userID, "owner", key)
		if !strings.Contains(note, "This request is from teammate, not owner.") || !strings.Contains(note, "Do not use owner's personal accounts") || !strings.Contains(note, "Only answer about the website project.") {
			t.Fatalf("%s: note %q", name, note)
		}
	}
	// Answer only is the default and Make changes the owner's choice; a server agent's turn says which (6 Nov: one row
	// for every agent, so the setting must hold for a server agent too).
	req0 := QueryRequest{AskedBy: "teammate"}
	if note := whenOthersAskForTurn(ctx, &req0, "owner", "owner", key); !strings.Contains(note, "answer questions only") {
		t.Fatalf("default is answer only: %q", note)
	}
	team.AgentRules["owner"][key] = crewTeamAgentRule{WhenOthersAsk: "Only answer about the website project.", CanMakeChanges: true}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if note := whenOthersAskForTurn(ctx, &req0, "owner", "owner", key); !strings.Contains(note, "lets you make changes") || strings.Contains(note, "answer questions only") {
		t.Fatalf("Make changes is said: %q", note)
	}
	// The owner's own turns (typed, schedules, their user-view test chat) get nothing.
	for _, req := range []QueryRequest{{}, {CrewGuestCaller: "owner", PinRunMode: true}} {
		if note := whenOthersAskForTurn(ctx, &req, "owner", "owner", key); note != "" {
			t.Fatalf("owner's own turn got %q", note)
		}
	}
	// Without a rule only the fixed line is added.
	req := QueryRequest{AskedBy: "teammate"}
	if note := whenOthersAskForTurn(ctx, &req, "owner", "owner", "wo-ask:chat:other"); !strings.Contains(note, "not owner") || strings.Contains(note, "rule for when others ask") {
		t.Fatalf("default note %q", note)
	}
}
