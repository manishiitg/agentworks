package server

import (
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-820: a Main agent had different addresses on Mac and web, while
// agents owned by different members could have the same native tab name.
func TestCrewTeamAgentIdentitySurvivesMainKeyChangeAndSeparatesOwners(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	team := crewTeam{ProjectID: "identity-room", OwnerID: "owner"}
	people := []crewTeamAgentsPerson{
		{UserID: "owner", Agents: []crewTeamAgent{{Tab: macMainTab, ChatKey: team.ProjectID}}},
		{UserID: "member", Agents: []crewTeamAgent{{Tab: macMainTab, ChatKey: "member-main"}}},
	}
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatal(err)
	}
	ownerID, memberID := people[0].Agents[0].ID, people[1].Agents[0].ID
	if ownerID == "" || memberID == "" || ownerID == memberID {
		t.Fatal("agents of different owners must have different IDs")
	}
	people[0].Agents[0] = crewTeamAgent{Tab: "owner-tab", ChatKey: crewOwnerDefaultChatKey(team.ProjectID, team.OwnerID), Place: "server"}
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatal(err)
	}
	if people[0].Agents[0].ID != ownerID || people[1].Agents[0].ID != memberID {
		t.Fatal("changing place or a Main chat key must preserve identity")
	}
	dir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-identities.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := assignCrewTeamAgentIDs(team, people); err == nil {
		t.Fatal("corrupt registry must fail, not silently issue replacement IDs")
	}
}
