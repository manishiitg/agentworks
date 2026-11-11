package server

import (
	"context"
	"io"
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

// PLAT-746-820: imported Main was named correctly but allocated a second Team ID.
func TestCrewTeamImportedMainKeepsIdentityAndRejectsDuplicateBindings(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.Background()
	key := "team:chat:imported-example"
	f.write(t, externalCommandCrewState+"/product.json", `{"schema_version":1,"product":"work","id":"team","main_chat_key":"`+key+`"}`)
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, WorkspacePath: externalCommandCrewRoot, SessionID: "imported-session"}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): record}}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{RegistryVersion: 1, ProjectID: "team", OwnerID: "owner", On: true}
	discovered := memberTeamAgents(ctx, team, "owner", "owner")
	if len(discovered) != 1 || discovered[0].Tab != macMainTab || discovered[0].ChatKey != key {
		t.Fatalf("manifest Main not used for identity: %+v", discovered)
	}
	mainID := crewTeamAgentIDPrefix + uuid.NewString()
	main := crewTeamRegisteredAgent{ID: mainID, OwnerID: "owner", Tab: macMainTab, ChatKey: key, Place: "server", InTeam: false, Rule: crewTeamAgentRule{ChatShare: crewChatShareAgent}}
	oldBinding := main
	oldBinding.Tab = "imported-example"
	team.Agents = map[string]crewTeamRegisteredAgent{mainID: oldBinding}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	people := []crewTeamAgentsPerson{{UserID: "owner", Agents: discovered}}
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatal(err)
	}
	if people[0].Agents[0].ID != mainID || people[0].Agents[0].InTeam {
		t.Fatal("discovery replaced Main identity or privacy")
	}
	// A second native tab label for this same owner/chat must reuse the existing ID.
	people[0].Agents[0].Tab = "imported-example"
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatal(err)
	}
	if people[0].Agents[0].ID != mainID {
		t.Fatal("same chat received another identity")
	}
	saved, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Agents[mainID].Tab != macMainTab {
		t.Fatal("matching the chat must update its tab without replacing its ID")
	}
	dupID := crewTeamAgentIDPrefix + uuid.NewString()
	duplicate := saved.Agents[mainID]
	duplicate.ID, duplicate.Tab = dupID, "imported-example"
	saved.Agents[dupID] = duplicate
	dir, err := crewTeamDir("team")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "team.json")
	if err := fsutil.WriteJSONAtomic(file, saved, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkCrewTeamRegistries(io.Discard); err == nil {
		t.Fatal("checker accepted duplicate owner/chat binding")
	}
	if err := assignCrewTeamAgentIDs(saved, people); err != nil {
		t.Fatal(err)
	}
	repaired, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired.Agents) != 1 || repaired.Agents[mainID].ID != mainID || repaired.Agents[mainID].InTeam || repaired.Agents[mainID].Rule != main.Rule {
		t.Fatal("repair did not preserve converted Main and its settings")
	}
	if err := checkCrewTeamRegistries(io.Discard); err != nil {
		t.Fatal(err)
	}
	// An ambiguous privacy conflict must never silently widen access.
	duplicate.InTeam = true
	repaired.Agents[dupID] = duplicate
	if err := fsutil.WriteJSONAtomic(file, repaired, 0600); err != nil {
		t.Fatal(err)
	}
	if err := assignCrewTeamAgentIDs(repaired, people); err == nil {
		t.Fatal("conflicting duplicate privacy accepted")
	}
	unchanged, err := readCrewTeam("team")
	if err != nil || len(unchanged.Agents) != 2 {
		t.Fatal("refused repair changed saved registry")
	}
}
