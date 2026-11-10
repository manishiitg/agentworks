package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// PLAT-746-820: a Main agent had different addresses on Mac and web, while
// agents owned by different members could have the same native tab name.
func TestCrewTeamAgentIdentitySurvivesMainKeyChangeAndSeparatesOwners(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	team := crewTeam{ProjectID: "identity-room", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}}
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
	// A discovery round started before removal must not register the removed person's agents.
	saved, err := readCrewTeam(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	saved.People = nil
	if err := writeCrewTeam(saved); err != nil {
		t.Fatal(err)
	}
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatal(err)
	}
	if len(people[1].Agents) != 0 {
		t.Fatal("stale discovery exposed a removed member")
	}
	if err := os.WriteFile(filepath.Join(dir, "team.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := assignCrewTeamAgentIDs(team, people); err == nil {
		t.Fatal("corrupt registry must fail, not silently issue replacement IDs")
	}
}

// Seed registry settings for access tests; production edits use the owner-checked API.
func crewTeamAgentFixtures(project string, choices map[string]map[string]bool) map[string]crewTeamRegisteredAgent {
	out := map[string]crewTeamRegisteredAgent{}
	for person, tabs := range choices {
		for key, inTeam := range tabs {
			tab := strings.TrimPrefix(key, project+codeChatSideMarker)
			if key == crewOwnerDefaultChatKey(project, person) {
				tab = macMainTab
			}
			id := crewTeamAgentIDPrefix + uuid.NewSHA1(uuid.NameSpaceOID, []byte(project+"/"+person+"/"+tab)).String()
			chat := key
			if tab == macMainTab {
				chat = crewOwnerDefaultChatKey(project, person)
			} else if !strings.Contains(key, codeChatSideMarker) {
				chat = project + codeChatSideMarker + tab
			}
			out[id] = crewTeamRegisteredAgent{ID: id, OwnerID: person, Tab: tab, ChatKey: chat, InTeam: inTeam}
		}
	}
	return out
}

func writeCrewTeamAgentFixture(team crewTeam) error {
	if err := writeCrewTeam(team); err != nil {
		return err
	}
	saved, err := readCrewTeam(team.ProjectID)
	if err != nil {
		return err
	}
	fixtures := make(map[string]crewTeamRegisteredAgent, len(team.Agents))
	for _, a := range team.Agents {
		if old, ok := saved.registeredAgent(a.OwnerID, a.Tab); ok {
			a.ID = old.ID
		}
		fixtures[a.ID] = a
	}
	saved.Agents = fixtures
	dir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		return err
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), saved, 0600); err != nil {
		return err
	}
	pruneCrewStandby(team.ProjectID)
	return nil
}
