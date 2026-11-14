package server

import (
	"testing"
	"time"
)

// PLAT-746-971: a transfer is not general Team access to somebody's private
// conversation; only the named recipient may download it, while still joined.
func TestCrewAgentTransferOnlyNamedCurrentRecipient(t *testing.T) {
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{ProjectID: "room", OwnerID: "maker", On: true,
		People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentTransfer(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("incomplete uploads must not be downloadable")
	}
	offer.Ready = true
	if err := offer.admit(team, "recipient", true, now); err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"maker", "other", ""} {
		if err := offer.admit(team, person, true, now); err == nil {
			t.Fatalf("private bundle admitted %q", person)
		}
	}
	if _, err := newCrewAgentTransfer(team, "other", "recipient", agent.ID, now); err == nil {
		t.Fatal("foreign chat transfer admitted")
	}
	if err := offer.admit(team, "recipient", true, offer.Expires); err == nil {
		t.Fatal("expired offer admitted")
	}
	team.People[0].Invited = true
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("removed recipient admitted")
	}
	team.People[0].Invited = false
	changed := agent
	changed.OwnerID = "other"
	team.Agents[agent.ID] = changed
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("stale owner admitted")
	}
}
