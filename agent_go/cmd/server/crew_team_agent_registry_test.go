package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// PLAT-746-820: a Main agent had different addresses on Mac and web, while
// agents owned by different members could have the same native tab name.
func TestCrewTeamAgentIdentitySurvivesDefaultKeyChangeAndSeparatesOwners(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	team := crewTeam{ProjectID: "identity-room", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}}
	people := []crewTeamAgentsPerson{
		{UserID: "owner", Agents: []crewTeamAgent{{Tab: macDefaultTab, ChatKey: team.ProjectID}}},
		{UserID: "member", Agents: []crewTeamAgent{{Tab: macDefaultTab, ChatKey: "member-main"}}},
	}
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatal(err)
	}
	ownerID, memberID := people[0].Agents[0].ID, people[1].Agents[0].ID
	if ownerID == "" || memberID == "" || ownerID == memberID {
		t.Fatal("agents of different owners must have different IDs")
	}
	// Owner, 26 Nov: a discovered agent starts Only me; nobody shares it but its person.
	fresh, err := readCrewTeam(team.ProjectID)
	if err != nil || fresh.Agents[ownerID].InTeam || fresh.seesAgent("member", "owner", ownerID) {
		t.Fatalf("a new agent was shared by default: %v", err)
	}
	// This identity fixture has no account/project-owner grant. Seed the saved
	// choice directly, as the rest of this fixture does; do not weaken admission.
	live := fresh.Agents[ownerID]
	live.Rule.ChatShare = crewChatShareAgent
	fresh.Agents[ownerID] = live
	fixtureDir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(fixtureDir, "team.json"), fresh, 0600); err != nil {
		t.Fatal(err)
	}

	// A member's Main without a chat yet must not be saved: such a row made the whole
	// registry refuse every later read (server B, 13-14 Nov).
	unready := []crewTeamAgentsPerson{{UserID: "member", Agents: []crewTeamAgent{{Tab: "other", ChatKey: ""}}}}
	if err := assignCrewTeamAgentIDs(team, unready); err != nil || len(unready[0].Agents) != 0 {
		t.Fatalf("agent without a chat was registered: err=%v agents=%d", err, len(unready[0].Agents))
	}
	if err := assignCrewTeamAgentIDs(team, people); err != nil {
		t.Fatalf("registry unreadable after an agent without a chat: %v", err)
	}
	savedLive, err := readCrewTeam(team.ProjectID)
	if err != nil || savedLive.Agents[ownerID].Rule.ChatShare != crewChatShareAgent {
		t.Fatal("discovery replaced saved Hidden choice", err)
	}
	people[0].Agents[0] = crewTeamAgent{Tab: macDefaultTab, ChatKey: crewOwnerDefaultChatKey(team.ProjectID, team.OwnerID), Place: "server"}
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
	// PLAT-746-1030: a terminal row must not wedge discovery (986), rotate
	// identity on rejoin, overwrite privacy choices or masquerade as another chat.
	current, err := readCrewTeam(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	named := namedMessageAgent{ID: "named-terminal", UserID: "member", Name: "research", Label: "Research", Attachment: &namedAgentAttachment{ID: "lease-one", Provider: "claude", SessionID: "native-one"}}
	terminal, err := upsertCrewTeamTerminal(&current, "member", named, "mac-one")
	if err != nil {
		t.Fatal(err)
	}
	terminal.InTeam = true
	terminal.Rule.WhenOthersAsk = "Keep it short."
	current.Agents[terminal.ID] = terminal
	named.Attachment.ID, named.Attachment.SessionID = "lease-two", "native-two"
	named.Label = "Research follow-up"
	rotated, err := upsertCrewTeamTerminal(&current, "member", named, "mac-two")
	if err != nil || rotated.ID != terminal.ID || rotated.Tab != terminal.Tab || !rotated.InTeam || rotated.Rule.WhenOthersAsk != "Keep it short." || rotated.ChatKey != "native-two" || rotated.Terminal.AttachmentID != "lease-two" || rotated.Terminal.MacPair != "mac-two" || rotated.Name != named.Label {
		t.Fatalf("terminal rejoin lost identity, privacy or route: %+v %v", rotated, err)
	}
	count := len(current.Agents)
	named.Attachment.SessionID = ""
	if _, err := upsertCrewTeamTerminal(&current, "member", named, "mac-two"); err == nil || len(current.Agents) != count {
		t.Fatal("empty session must never enter the registry")
	}
	named.Attachment.SessionID = people[1].Agents[0].ChatKey
	if _, err := upsertCrewTeamTerminal(&current, "member", named, "mac-two"); err == nil {
		t.Fatal("a terminal cannot reuse an app agent's owner/chat binding")
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	if err := assignCrewTeamAgentIDsWithDefault(current, people, crewOwnerDefaultAgentKey(team.ProjectID)); err != nil {
		t.Fatal(err)
	}
	unchanged, err := readCrewTeam(team.ProjectID)
	if err != nil || !reflect.DeepEqual(unchanged.Agents[rotated.ID], rotated) {
		t.Fatalf("discovery changed terminal identity, route or privacy: %v", err)
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
				tab = macDefaultTab
			}
			id := crewTeamAgentIDPrefix + uuid.NewSHA1(uuid.NameSpaceOID, []byte(project+"/"+person+"/"+tab)).String()
			chat := key
			if tab == macDefaultTab {
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
	return nil
}

// PLAT-746-820: imported Main was named correctly but allocated a second Team ID.
func TestCrewTeamImportedDefaultAgentKeepsIdentityAndRejectsDuplicateBindings(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.Background()
	key := "team:chat:root-example"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","default_agent_key":"`+key+`"}`)
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, WorkspacePath: externalCommandCrewRoot, SessionID: "imported-session"}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): record}}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{RegistryVersion: 1, ProjectID: "team", OwnerID: "owner", On: true}
	discovered := memberTeamAgents(ctx, team, "owner", "owner")
	if len(discovered) != 1 || discovered[0].Tab != macDefaultTab || discovered[0].ChatKey != key {
		t.Fatalf("manifest Main not used for identity: %+v", discovered)
	}
	mainID := crewTeamAgentIDPrefix + uuid.NewString()
	main := crewTeamRegisteredAgent{ID: mainID, OwnerID: "owner", Tab: macDefaultTab, ChatKey: key, Place: "server", InTeam: false, Rule: crewTeamAgentRule{ChatShare: crewChatShareAgent}}
	oldBinding := main
	oldBinding.Tab = "root-example"
	team.Agents = map[string]crewTeamRegisteredAgent{mainID: oldBinding}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	// PLAT-746-1035: even a discovered "shared" choice may not overwrite saved Only me.
	shared := true
	discovered[0].initialInTeam = &shared
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
	if saved.Agents[mainID].Tab != macDefaultTab {
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
		if (row.Tab == macDefaultTab) != wantMain || crewTeamDefaultAgent(saved, "member", row.ChatKey) {
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
	conflict := crewTeamAgent{Tab: macDefaultTab, ChatKey: memberKeys[1]}
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
		if !reflect.DeepEqual(afterConflict.Agents[id], before) {
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
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","default_agent_key":"`+secondKey+`"}`)
	if crewTeamOwnerDefaultKey(ctx, afterConflict) != secondKey || crewTeamDefaultAgent(afterConflict, "owner", key) || !crewTeamDefaultAgent(afterConflict, "owner", secondKey) {
		t.Fatal("Main did not follow its manifest pointer")
	}
	rows, err := defaultProductConversationRegistryStore().projectChats(ctx, "owner", crewProfileID, "team")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.IsDefaultAgent != (row.ConversationKey == secondKey) {
			t.Fatal("bare root overrode the manifest Main")
		}
	}
	if agent, ok := afterConflict.registeredAgent("owner", secondKey); !ok || agent.ID != secondID || agent.InTeam {
		t.Fatal("stale Main role overrode exact private chat identity")
	}
	// B arrives first, without A in this discovery round: role movement cannot
	// depend on map order or on the old chat still being discoverable.
	moved := []crewTeamAgentsPerson{{UserID: "owner", Agents: []crewTeamAgent{{Tab: macDefaultTab, ChatKey: secondKey, Place: "server"}}}}
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
	old.Tab, second.Tab = "root-example", macDefaultTab
	if !reflect.DeepEqual(rebound.Agents[mainID], old) || !reflect.DeepEqual(rebound.Agents[secondID], second) {
		t.Fatalf("Main movement changed more than role: %+v", rebound.Agents)
	}
	// Main is an agent like any other (#4046): no extra line about it on the Team tab.
	if view := crewTeamTabFor(ctx, rebound, "owner"); strings.Contains(view.Intro, "Main is not shared") {
		t.Fatal("Team tab still singles out Main")
	}
	for _, person := range teamAgentsByPerson(ctx, rebound, "member") {
		for _, agent := range person.Agents {
			if agent.ID == secondID {
				t.Fatal("private Main became visible to a teammate")
			}
		}
	}
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","default_agent_key":"`+key+`"}`)
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
	if err != nil || len(unchanged.Agents) != 2 || !reflect.DeepEqual(unchanged.Agents[mainID], saved.Agents[mainID]) || !reflect.DeepEqual(unchanged.Agents[dupID], duplicate) {
		t.Fatal("refused duplicate registry write changed identities or settings")
	}
}

// PLAT-746-901: opening Team must not expand schedules/functions/webhooks just
// to identify Main. Invalid unrelated action files previously hid Main too.
func TestCrewTeamDefaultAgentLookupSkipsRuntimeActions(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.Background()
	key := "team:chat:root-example"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","default_agent_key":"`+key+`"}`)
	for _, name := range []string{"workflow.json", "schedules.json", "functions.json", "webhooks.json"} {
		f.write(t, externalCommandCrewSettings+"/"+name, "invalid unrelated runtime data")
	}
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, WorkspacePath: externalCommandCrewRoot, SessionID: "main-session"}
	store := defaultProductConversationRegistryStore()
	if err := store.writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): record}}); err != nil {
		t.Fatal(err)
	}
	main := crewOwnerDefaultAgentKey("team")
	if main != key {
		t.Fatalf("unrelated runtime actions blocked the saved Main: %q", main)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true}
	rows := memberTeamAgentsWithDefault(ctx, team, "owner", "owner", main)
	if len(rows) != 1 || rows[0].ChatKey != key || rows[0].Tab != macDefaultTab {
		t.Fatalf("Team discovery did not use the resolved Main: %+v", rows)
	}
	// Keep validating the owner registry rather than trusting the manifest alone.
	if err := store.writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{}}); err != nil {
		t.Fatal(err)
	}
	if crewOwnerDefaultAgentKey("team") != "" {
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
	rows := crewTeamReachableIn(t.Context(), team, "owner")
	if len(rows) != 1 || rows[0].Address != crewTeamAgentAddress("team", shared) {
		t.Fatalf("registered target missing or private row exposed: %+v", rows)
	}
	agent, owner, err := crewTeamAgentByID(t.Context(), "team", "owner", shared)
	if err != nil || owner != "other" || agent.ID != shared {
		t.Fatalf("listed registry target refused by admission: %+v %s %v", agent, owner, err)
	}
	// Chosen people (PLAT-746-1034): a teammate left off the list cannot reach the agent; putting them on restores it.
	if _, err := setCrewTeamAgentAudience(t.Context(), "team", "other", shared, crewTeamAudienceChosen, []string{}, true); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam("team")
	if rows := crewTeamReachableIn(t.Context(), team, "owner"); len(rows) != 0 || crewTeamReachRow(team, "owner", team.Agents[shared]) {
		t.Fatalf("agent reachable by a teammate who is not chosen: %+v", rows)
	}
	// Source-row admission for offline copies: the agent's own person reaches it under any audience, Team off
	// included (only_me); a teammate does not.
	off := team
	off.On = false
	if a := off.Agents[shared]; crewTeamRowAudience(off, a) != "only_me" || !crewTeamSourceRowAdmits(off, "other", a) || crewTeamSourceRowAdmits(off, "owner", a) || !crewTeamSourceRowAdmits(team, "other", a) {
		t.Fatal("source-row admission must admit only the agent's own person while only_me or not chosen")
	}
	removed := off
	removed.People = nil
	if a := removed.Agents[shared]; a.OwnerID != removed.OwnerID && crewTeamSourceRowAdmits(removed, a.OwnerID, a) {
		t.Fatal("a maker removed from the Team must lose their own row's copy")
	}
	// The maker's own source address is admitted (Only me and Team off included); a coworker who is not chosen and a
	// removed maker are not, and no rows of another person's agents come back.
	has := func(rows []crewTeamReachableAgent, id string) bool {
		return slices.ContainsFunc(rows, func(r crewTeamReachableAgent) bool { return r.ID == id })
	}
	if rows := crewTeamOwnSourceRows(t.Context(), off, "other"); !has(rows, shared) || len(crewTeamOwnSourceRows(t.Context(), off, "owner")) != 0 || len(crewTeamOwnSourceRows(t.Context(), removed, "other")) != 0 {
		t.Fatalf("own source rows must admit only the saved maker: %+v", rows)
	}
	if _, _, err := crewTeamAdmitAddress(t.Context(), "other", crewTeamAgentBridgeAddress("other", shared)); err != nil {
		t.Fatalf("maker's own source address refused: %v", err)
	}
	if _, _, err := crewTeamAdmitAddress(t.Context(), "owner", crewTeamAgentBridgeAddress("other", shared)); err == nil {
		t.Fatal("a teammate who is not chosen reached the maker's source address")
	}
	if _, err := setCrewTeamAgentAudience(t.Context(), "team", "other", shared, "", []string{"owner", "not-a-member"}, true); !errors.Is(err, errCrewTeamAudiencePerson) {
		t.Fatalf("chosen list accepted a non-member: %v", err)
	}
	if _, err := setCrewTeamAgentAudience(t.Context(), "team", "other", shared, "", []string{"owner"}, true); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam("team")
	if rows := crewTeamReachableIn(t.Context(), team, "owner"); len(rows) != 1 {
		t.Fatalf("chosen teammate cannot reach the agent: %+v", rows)
	}
	// A disabled teammate is refused by the canonical gate (crewTeamReachOpen), not a per-path copy: the offline
	// copy's source-row admission delegates to it for everyone but the maker.
	withMember := team
	withMember.People = append(slices.Clone(team.People), crewTeamPerson{UserID: "member"})
	whole := withMember.Agents[shared]
	whole.Rule.Audience, whole.Rule.People = "", nil
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]},{"id":"member","role":"editor","products":["work"]}]}`)
	if !crewTeamSourceRowAdmits(withMember, "member", whole) {
		t.Fatal("an active teammate must reach a whole-Team agent")
	}
	*directory = strings.Replace(*directory, `{"id":"member","role":"editor"`, `{"id":"member","role":"editor","disabled":true`, 1)
	invalidateUserDirectoryCache()
	if crewTeamSourceRowAdmits(withMember, "member", whole) || crewTeamReachRow(withMember, "member", whole) {
		t.Fatal("a disabled teammate reached a Team agent")
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]}]}`)
	if err := removeCrewAgentListing("team", "other", "team:chat:helper"); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam("team")
	rows = crewTeamReachableIn(t.Context(), team, "owner")
	if len(rows) != 0 {
		t.Fatal("deleted registry identity remained listed")
	}
}

// Owner, 20 Nov: agents a teammate deleted on their Mac stayed in the Team menu. A complete answer from that Mac
// prunes its deleted app agents and keeps the ones it still lists, terminal chats and other people's agents.
func TestPruneDeletedCrewMacAgentsKeepsListedAndOthers(t *testing.T) {
	newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member", Where: "mac", MacPair: "pair"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{
		"member": {"kept": true, "deleted": true},
		"owner":  {"owner-tab": true},
	})}
	for id, a := range team.Agents {
		a.Place = "mac"
		team.Agents[id] = a
	}
	terminal := crewTeamRegisteredAgent{ID: crewTeamAgentIDPrefix + "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", OwnerID: "member", Tab: "terminal-x", ChatKey: "11111111-2222-4333-8444-555555555555", Name: "Terminal", Place: "mac", Kind: "terminal", InTeam: true}
	team.Agents[terminal.ID] = terminal
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	pruneDeletedCrewMacAgents("team", "member", map[string]bool{macDefaultTab: true, "kept": true})
	saved, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	tabs := map[string]bool{}
	for _, a := range saved.Agents {
		tabs[a.OwnerID+"/"+a.Tab] = true
	}
	if tabs["member/deleted"] || !tabs["member/kept"] || !tabs["owner/owner-tab"] || !tabs["member/terminal-x"] {
		t.Fatalf("wrong agents after prune: %v", tabs)
	}
}
