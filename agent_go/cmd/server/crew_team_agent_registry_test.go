package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	people[0].Agents[0] = crewTeamAgent{Tab: macMainTab, ChatKey: crewOwnerDefaultChatKey(team.ProjectID, team.OwnerID), Place: "server"}
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
	key := "team:chat:root-example"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","main_chat_key":"`+key+`"}`)
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
	oldBinding.Tab = "root-example"
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
	people[0].Agents[0].Tab = "root-example"
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
	// A member's default Main and two root chats remain three independent agents.
	// The owner's manifest Main is itself a root chat; that role is owner-only.
	saved.People = []crewTeamPerson{{UserID: "member"}}
	if err := writeCrewTeam(saved); err != nil {
		t.Fatal(err)
	}
	memberKeys := []string{crewOwnerDefaultChatKey("team", "member"), "team:chat:root-member-one", "team:chat:root-member-two"}
	entries := map[string]ProductConversationRecord{}
	for _, chat := range memberKeys {
		entries[productConversationRegistryEntryKey(crewProfileID, chat)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: chat, WorkspacePath: externalCommandCrewRoot, SessionID: "session-" + chat}
	}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("member"), productConversationRegistryDocument{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	memberRows := memberTeamAgents(ctx, saved, "member", "member")
	if len(memberRows) != 3 {
		t.Fatalf("member discovery lost chats: %+v", memberRows)
	}
	for _, row := range memberRows {
		wantMain := row.ChatKey == memberKeys[0]
		if (row.Tab == macMainTab) != wantMain || crewTeamMainAgent(saved, "member", row.ChatKey) != wantMain {
			t.Fatalf("member root chat received Main role: %+v", row)
		}
	}
	people = []crewTeamAgentsPerson{{UserID: "owner", Agents: discovered}, {UserID: "member", Agents: memberRows}}
	if err := assignCrewTeamAgentIDs(saved, people); err != nil {
		t.Fatal(err)
	}
	registered, err := readCrewTeam("team")
	if err != nil || len(registered.Agents) != 4 {
		t.Fatalf("expected four independent identities: %v %+v", err, registered.Agents)
	}
	for _, chat := range memberKeys {
		a, ok := registered.registeredAgent("member", chat)
		if !ok || a.ChatKey != chat || a.ID == mainID {
			t.Fatalf("member chat lost its own identity: %s %+v", chat, a)
		}
	}
	// A conflicting discovery row must not erase unrelated people or change
	// either binding. Exercise the real bySource/byChat collision from the incident.
	conflict := crewTeamAgent{Tab: macMainTab, ChatKey: memberKeys[1]}
	people[1].Agents = append([]crewTeamAgent{conflict}, memberRows...)
	if err := assignCrewTeamAgentIDs(registered, people); err != nil {
		t.Fatal(err)
	}
	if len(people[0].Agents) != 1 || people[0].Agents[0].ID != mainID || len(people[1].Agents) != 3 {
		t.Fatalf("one conflict hid healthy agents: %+v", people)
	}
	afterConflict, err := readCrewTeam("team")
	if err != nil || len(afterConflict.Agents) != len(registered.Agents) {
		t.Fatalf("conflict changed registry size: %v", err)
	}
	for id, before := range registered.Agents {
		if afterConflict.Agents[id] != before {
			t.Fatalf("conflict changed identity or settings: %s", id)
		}
	}
	if err := checkCrewTeamRegistries(io.Discard); err != nil {
		t.Fatalf("healthy registry failed team-check: %v", err)
	}
	// Main is a role, not identity: moving it to a private registered chat
	// must retain both IDs and each chat's independent permissions.
	secondKey := "team:chat:second"
	secondID := crewTeamAgentIDPrefix + uuid.NewString()
	second := crewTeamRegisteredAgent{ID: secondID, OwnerID: "owner", Tab: "second", ChatKey: secondKey, Place: "server", InTeam: false, Rule: crewTeamAgentRule{ChatShare: crewChatShareAgent, WhenOthersAsk: "second rule"}}
	old := afterConflict.Agents[mainID]
	old.InTeam, old.Rule.WhenOthersAsk = true, "first rule"
	afterConflict.Agents[mainID], afterConflict.Agents[secondID] = old, second
	if err := writeCrewTeamAgentFixture(afterConflict); err != nil {
		t.Fatal(err)
	}
	ownerEntries := map[string]ProductConversationRecord{}
	for _, chat := range []string{key, secondKey, "team"} {
		ownerEntries[productConversationRegistryEntryKey(crewProfileID, chat)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: chat, WorkspacePath: externalCommandCrewRoot, SessionID: "session-" + chat}
	}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: ownerEntries}); err != nil {
		t.Fatal(err)
	}
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","main_chat_key":"`+secondKey+`"}`)
	if crewTeamOwnerMainKey(ctx, afterConflict) != secondKey || crewTeamMainAgent(afterConflict, "owner", key) || !crewTeamMainAgent(afterConflict, "owner", secondKey) {
		t.Fatal("Main did not follow its manifest pointer")
	}
	rows, err := defaultProductConversationRegistryStore().projectChats(ctx, "owner", crewProfileID, "team")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.IsMainChat != (row.ConversationKey == secondKey) {
			t.Fatal("bare root overrode the manifest Main")
		}
	}
	if agent, ok := afterConflict.registeredAgent("owner", secondKey); !ok || agent.ID != secondID || agent.InTeam {
		t.Fatal("stale Main role overrode exact private chat identity")
	}
	// B arrives first, without A in this discovery round: role movement cannot
	// depend on map order or on the old chat still being discoverable.
	moved := []crewTeamAgentsPerson{{UserID: "owner", Agents: []crewTeamAgent{{Tab: macMainTab, ChatKey: secondKey, Place: "server"}}}}
	if err := assignCrewTeamAgentIDs(afterConflict, moved); err != nil {
		t.Fatal(err)
	}
	if len(moved[0].Agents) != 1 || moved[0].Agents[0].ID != secondID || moved[0].Agents[0].InTeam {
		t.Fatal("Main reassignment replaced identity or shared a private chat")
	}
	rebound, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	old.Tab, second.Tab = "root-example", macMainTab
	if rebound.Agents[mainID] != old || rebound.Agents[secondID] != second {
		t.Fatalf("Main movement changed more than role: %+v", rebound.Agents)
	}
	view := crewTeamTabFor(ctx, rebound, "owner")
	if !strings.Contains(view.Intro, "Main is not shared") {
		t.Fatal("Team tab did not explain its private Main")
	}
	for _, person := range teamAgentsByPerson(ctx, rebound, "member") {
		for _, agent := range person.Agents {
			if agent.ID == secondID {
				t.Fatal("private Main became visible to a teammate")
			}
		}
	}
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","main_chat_key":"`+key+`"}`)
	// Retain the separate malformed-persisted-registry refusal below.
	if err := writeCrewTeamAgentFixture(saved); err != nil {
		t.Fatal(err)
	}
	dupID := crewTeamAgentIDPrefix + uuid.NewString()
	duplicate := saved.Agents[mainID]
	duplicate.ID, duplicate.Tab = dupID, "root-example"
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
	if err := assignCrewTeamAgentIDs(saved, people); err == nil {
		t.Fatal("registry accepted duplicate owner/chat binding")
	}
	unchanged, err := readCrewTeam("team")
	if err != nil || len(unchanged.Agents) != 2 || unchanged.Agents[mainID] != saved.Agents[mainID] || unchanged.Agents[dupID] != duplicate {
		t.Fatal("refused duplicate registry write changed identities or settings")
	}
}

// PLAT-746-901: opening Team must not expand schedules/functions/webhooks just
// to identify Main. Invalid unrelated action files previously hid Main too.
func TestCrewTeamMainLookupSkipsRuntimeActions(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.Background()
	key := "team:chat:root-example"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","main_chat_key":"`+key+`"}`)
	for _, name := range []string{"workflow.json", "schedules.json", "functions.json", "webhooks.json"} {
		f.write(t, externalCommandCrewSettings+"/"+name, "invalid unrelated runtime data")
	}
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, WorkspacePath: externalCommandCrewRoot, SessionID: "main-session"}
	store := defaultProductConversationRegistryStore()
	if err := store.writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): record}}); err != nil {
		t.Fatal(err)
	}
	main := crewOwnerMainChatKey("team")
	if main != key {
		t.Fatalf("unrelated runtime actions blocked the saved Main: %q", main)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true}
	rows := memberTeamAgentsWithMain(ctx, team, "owner", "owner", main)
	if len(rows) != 1 || rows[0].ChatKey != key || rows[0].Tab != macMainTab {
		t.Fatalf("Team discovery did not use the resolved Main: %+v", rows)
	}
	// Keep validating the owner registry rather than trusting the manifest alone.
	if err := store.writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{}}); err != nil {
		t.Fatal(err)
	}
	if crewOwnerMainChatKey("team") != "" {
		t.Fatal("an unregistered Main must stay unavailable")
	}
}

// PLAT-746-951: a cold/offline Mac metadata read must not hide an admitted
// registry target. Listing and send admission use the same registry identity.
func TestCrewTeamRegisteredTargetsSurviveMissingMacMetadata(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "owner-mac"}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other", Where: "mac", MacPair: "member-pair"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"other": {"helper": true, "private": false}})}
	var shared string
	for id, a := range team.Agents {
		a.Place = "mac"
		a.Name = a.Tab
		team.Agents[id] = a
		if a.InTeam {
			shared = id
		}
	}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	crewMacTeamTabsCache.Store("team\x00other", crewMacTeamTabsRead{at: time.Now(), tabs: []crewMacTeamTab{}})
	t.Cleanup(func() { crewMacTeamTabsCache.Delete("team\x00other") })
	rows := teamMemberAgentRows(t.Context(), "owner", []map[string]interface{}{{"id": "team", "name": "Example"}})
	if len(rows) != 1 || rows[0]["id"] != crewTeamAgentAddress("team", shared) {
		t.Fatalf("registered target missing or private row exposed: %+v", rows)
	}
	agent, owner, err := crewTeamAgentByID(t.Context(), "team", "owner", shared)
	if err != nil || owner != "other" || agent.ID != shared {
		t.Fatalf("listed registry target refused by admission: %+v %s %v", agent, owner, err)
	}
	if err := removeCrewAgentListing("team", "other", "team:chat:helper"); err != nil {
		t.Fatal(err)
	}
	rows = teamMemberAgentRows(t.Context(), "owner", []map[string]interface{}{{"id": "team", "name": "Example"}})
	if len(rows) != 0 {
		t.Fatal("deleted registry identity remained listed")
	}
}
