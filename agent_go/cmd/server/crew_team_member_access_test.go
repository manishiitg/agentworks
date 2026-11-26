package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856: a member's Access page must never expose owner computer settings;
// participation changes affect only the caller's own agent and membership.
func TestCrewTeamMemberAccessChangesOnlyOwnParticipation(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"member","username":"member","products":["work"]},{"id":"outsider","username":"outsider","products":["work"]},{"id":"teammate","username":"teammate","products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "member-access", OwnerID: "owner", ProjectID: "member-access"}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: "member-access", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "member"}, {UserID: "teammate"}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	ws, documents := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	store := defaultProductConversationRegistryStore()
	key := "member-access:chat:own"
	memberMain := crewOwnerDefaultChatKey("member-access", "member")
	ownerMain := crewOwnerDefaultChatKey(team.ProjectID, team.OwnerID)
	documents.mu.Lock()
	documents.files["Crew/member-access/product.json"] = `{"schema_version":1,"product":"work","id":"member-access","main_chat_key":"` + ownerMain + `"}`
	documents.mu.Unlock()
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): {ProfileID: crewProfileID, ConversationKey: key, SessionID: "member-session"},
		productConversationRegistryEntryKey(crewProfileID, memberMain): {ProfileID: crewProfileID, ConversationKey: memberMain, SessionID: "member-main-session"}}}
	if err := store.writeDocument(t.Context(), productConversationRegistryPath("member"), doc); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"owner", "teammate"} {
		otherKey := "member-access:chat:" + who
		otherDoc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, otherKey): {ProfileID: crewProfileID, ConversationKey: otherKey, SessionID: who + "-session"}}}
		if who == "owner" {
			otherDoc.Entries[productConversationRegistryEntryKey(crewProfileID, ownerMain)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: ownerMain, SessionID: "owner-main-session", WorkspacePath: "Crew/member-access"}
		}
		if err := store.writeDocument(t.Context(), productConversationRegistryPath(who), otherDoc); err != nil {
			t.Fatal(err)
		}
	}
	// Settings admission uses the Team registry, not conversation discovery.
	// Seed the same owned agents before exercising visibility and its refusals.
	team.Agents = crewTeamAgentFixtures(team.ProjectID, map[string]map[string]bool{
		"member":   {key: true, memberMain: true},
		"owner":    {"member-access:chat:owner": true, ownerMain: true},
		"teammate": {"member-access:chat:teammate": true},
	})
	for id, agent := range team.Agents {
		if agent.OwnerID == "member" && agent.ChatKey == memberMain {
			agent.Name = "Main"
			team.Agents[id] = agent
		}
	}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	call := func(who, op, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/crew-teams/member-access/"+op, strings.NewReader(body))
		r.Header.Set(crewMacPairHeader, "pair") // the owner's calls here are the Mac's own
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	// A Team is a shared room (owner, 5 Nov): the owner (also from their Mac app through its sign-in) and the other
	// members watch a member's In team agent; nobody else does, and Only me (below) ends it. The owner's Mac read
	// a member's chat as 404 (an empty chat stuck on "Reconnecting…") before this rule.
	watching := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"member-session": {UserID: "member", Status: "completed"}}}
	watches := func(who string) bool {
		r := httptest.NewRequest("GET", "/api/sessions/member-session/events", nil)
		return watching.crewSessionTabView(r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who})), "member-session")
	}
	// Notifications follow the same rule from the same record (crewTeam.Agents, crew_team_in_team.go).
	notified := func(who string) bool {
		return watching.notifyCanSee(t.Context(), who, notifyChat{owner: "member", project: team.ProjectID, found: true}, "member-session")
	}
	if !watches("owner") || !watches("teammate") || watches("outsider") || !notified("teammate") || notified("outsider") {
		t.Fatalf("Team watch: owner=%t teammate=%t outsider=%t notified teammate=%t", watches("owner"), watches("teammate"), watches("outsider"), notified("teammate"))
	}
	// A function's own tab belongs to its owner: the Team never shares it, since a function is how others use a Crew.
	fnTab := team.ProjectID + codeChatSideMarker + crewFunctionTabPrefix + "plan-release"
	if team.agentInTeam("member", fnTab) || team.seesAgent("teammate", "member", fnTab) || !team.seesAgent("member", "member", fnTab) {
		t.Fatal("a function tab is shared with the Team, or hidden from its owner")
	}
	w := call("member", "agent-visibility", `{"agent_key":"`+key+`","in_team":false}`)
	if w.Code != 200 {
		t.Fatalf("own visibility: %d %s", w.Code, w.Body.String())
	}
	if saved, err := readCrewTeam(team.ProjectID); err != nil || saved.agentInTeam("member", key) {
		t.Fatalf("own agent not private: %v", err)
	}
	if watches("owner") || watches("teammate") || notified("owner") || notified("teammate") {
		t.Fatal("another person still watches, or is notified about, an Only me agent")
	}
	for _, who := range []string{"owner", "outsider"} {
		if w := call(who, "agent-visibility", `{"agent_key":"`+key+`","in_team":true}`); w.Code != 403 {
			t.Fatalf("%s changed another agent: %d", who, w.Code)
		}
		if w := call(who, "leave", `{}`); w.Code != 403 {
			t.Fatalf("%s left member Team: %d", who, w.Code)
		}
	}
	if w := call("member", "agent-visibility", `{"agent_key":"member-access:chat:someone-else","in_team":true}`); w.Code != 403 {
		t.Fatalf("member changed other agent: %d", w.Code)
	}
	for _, otherKey := range []string{"member-access:chat:owner", "member-access:chat:teammate"} {
		if w := call("member", "agent-visibility", `{"agent_key":"`+otherKey+`","in_team":false}`); w.Code != 403 {
			t.Fatalf("member changed registered other agent %s: %d", otherKey, w.Code)
		}
	}
	// The Team tab offers the member a choice (In team, purpose, AgentBridge) only for their own agent; everyone else's is theirs to change, and the
	// owner can never be removed.
	team, _ = readCrewTeam(team.ProjectID)
	tab := crewTeamTabFor(t.Context(), team, "member")
	own := 0
	for _, agent := range tab.Agents {
		if agent.WatchKey == key {
			own++
			if !agent.CanChange || agent.InTeam {
				t.Fatalf("own agent row: %+v", agent)
			}
		} else if agent.OwnerID != "member" && (agent.CanChange || agent.Fixed == "" || agent.AgentBridge != nil || agent.CanSetPurpose) {
			t.Fatalf("member can change another person's agent: %+v", agent)
		}
	}
	if own != 1 || !tab.CanLeave {
		t.Fatalf("wrong member tab: %+v", tab)
	}
	for _, person := range tab.People {
		if person.UserID == "owner" && person.CanRemove || person.UserID == "member" && person.Note != "you" {
			t.Fatalf("wrong person row: %+v", person)
		}
	}
	// When others ask (owner, 5 Nov): only the agent's own person sets its rule; others see it, read-only.
	if w := call("member", "agent-visibility", `{"agent_key":"`+key+`","in_team":true}`); w.Code != 200 {
		t.Fatalf("back in team: %d", w.Code)
	}
	// A tool names an agent by the name the Team tab shows ("Main"), among the caller's own agents only.
	if w := call("member", "agent-visibility", `{"agent_key":"Main","in_team":true}`); w.Code != 200 {
		t.Fatalf("own agent by name Main: %d %s", w.Code, w.Body.String())
	}
	if w := call("member", "agent-visibility", `{"agent_key":"`+crewOwnerDefaultChatKey("member-access", "owner")+`","in_team":true}`); w.Code != 403 {
		t.Fatalf("another person's Main: %d %s", w.Code, w.Body.String())
	}
	if w := call("member", "agent-rule", `{"agent_key":"`+key+`","when_others_ask":"Only answer about the website."}`); w.Code != 200 {
		t.Fatalf("own rule: %d %s", w.Code, w.Body.String())
	}
	for _, who := range []string{"owner", "teammate", "outsider"} {
		if w := call(who, "agent-rule", `{"agent_key":"`+key+`","when_others_ask":"Share everything."}`); w.Code != 403 {
			t.Fatalf("%s set another person's agent rule: %d", who, w.Code)
		}
	}
	team, _ = readCrewTeam(team.ProjectID)
	for viewer, editable := range map[string]bool{"member": true, "owner": false} {
		seen := false
		for _, agent := range crewTeamTabFor(t.Context(), team, viewer).Agents {
			if agent.WatchKey == key {
				seen = true
				if agent.WhenOthersAsk != "Only answer about the website." || !agent.HasRule || agent.CanEditRule != editable {
					t.Fatalf("%s sees rule row %+v", viewer, agent)
				}
			}
		}
		if !seen {
			t.Fatalf("%s does not see the member's agent", viewer)
		}
	}
	// Showcase, 6 Nov: on a Mac Crew's online copy every "When others ask" save was 403 agent_not_yours, the owner's
	// Mac agents and a member's Main alike, although the Team tab offered the edit. What the tab lets a person edit,
	// that person saves; everyone else's agents stay refused.
	if err := crewroot.WriteSync(team.ProjectID, crewroot.SyncBinding{Side: "server", Editing: "local", Mode: "relay", PairID: "pair", PeerProjectID: "mac-copy"}); err != nil {
		t.Fatal(err)
	}
	if err := writeMacSharedAgents(team.ProjectID, []macSharedAgent{{Tab: macMainTab, Name: "Main", Audience: "team"}, {Tab: "e139c703", Name: "Helper", Audience: "team"}}); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam(team.ProjectID)
	for _, who := range []string{"owner", "member"} {
		editable := 0
		for _, agent := range crewTeamTabFor(t.Context(), team, who).Agents {
			w := call(who, "agent-rule", `{"agent_key":"`+agent.Key+`","when_others_ask":"Ask me first."}`)
			if agent.CanEditRule {
				editable++
				if w.Code != 200 {
					t.Fatalf("%s could not save the rule the tab offers for %q: %d %s", who, agent.Key, w.Code, w.Body.String())
				}
			} else if w.Code != 403 {
				t.Fatalf("%s saved the rule of someone else's agent %q: %d", who, agent.Key, w.Code)
			}
		}
		if editable < 2 { // the owner's Mac Main and Helper; the member's Main and own agent
			t.Fatalf("%s edits %d of their agents", who, editable)
		}
	}
	// Native adapters and unique chat-tool names resolve only among the caller's agents.
	// The Team rows above exercise the server-issued IDs; retired Mac project aliases are gone.
	for _, name := range []string{"e139c703", "helper"} {
		if w := call("owner", "agent-rule", `{"agent_key":"`+name+`","when_others_ask":"Saved by `+name+`"}`); w.Code != 200 {
			t.Fatalf("owner could not save the rule of their own Mac agent by %q: %d %s", name, w.Code, w.Body.String())
		}
		if w := call("member", "agent-rule", `{"agent_key":"`+name+`","when_others_ask":"Share everything."}`); w.Code != 403 {
			t.Fatalf("member saved the owner's agent rule by %q: %d", name, w.Code)
		}
	}
	if saved, _ := readCrewTeam(team.ProjectID); saved.agentRule("owner", "e139c703").WhenOthersAsk != "Saved by helper" || saved.agentRule("owner", macMainTab).WhenOthersAsk != "Ask me first." {
		t.Fatalf("rules by Mac key or name not kept on the owner's agents: %+v", saved.Agents)
	}
	call("owner", "agent-rule", `{"agent_key":"Helper","when_others_ask":"Ask me first."}`)
	saved, _ := readCrewTeam(team.ProjectID)
	if saved.agentRule("owner", "e139c703").WhenOthersAsk != "Ask me first." || saved.agentRule("member", memberMain).WhenOthersAsk != "Ask me first." {
		t.Fatalf("rules not kept: %+v", saved.Agents)
	}
	// Public on AgentBridge from the website (owner, 6 Nov): anyone in the Team switches THEIR OWN agent, through their
	// own Mac; the call never reaches another person's Mac. A person whose Mac is off is told plainly (no Mac here).
	if w := call("owner", "agentbridge", `{"tab":"e139c703","public":true}`); w.Code != 409 || !strings.Contains(w.Body.String(), crewAgentBridgeMacOffline) {
		t.Fatalf("owner's AgentBridge with the Mac offline: %d %s", w.Code, w.Body.String())
	}
	if w := call("member", "agentbridge", `{"tab":"e139c703","public":true}`); w.Code != 409 || !strings.Contains(w.Body.String(), "mac_offline") {
		t.Fatalf("member without a paired Mac was not told it is offline: %d %s", w.Code, w.Body.String())
	}
	team, _ = readCrewTeam(team.ProjectID)
	team.People[0].Where, team.People[0].MacPair = "mac", "member-pair" // "member": their agents work on their own Mac
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	sent := map[string][]crewChatCall{}
	for _, who := range []string{"owner", "member"} {
		sock := &crewMacSocket{owner: who, device: who + "-mac", twins: map[string]string{team.ProjectID: map[string]string{"owner": "pair", "member": "member-pair"}[who]}, lastSeen: time.Now(),
			pending: map[string]chan crewChatResult{}, dispatched: map[string]chan struct{}{}}
		who := who
		sock.send = func(m crewRelayMessage) error {
			// Team changes also send notification frames without a command.
			if m.Call == nil {
				return nil
			}
			sent[who] = append(sent[who], *m.Call)
			sock.mu.Lock()
			sock.pending[m.Call.ID] <- crewChatResult{ID: m.Call.ID, Status: 200, Body: []byte(`{}`)}
			sock.mu.Unlock()
			return nil
		}
		crewMacSockets.Lock()
		crewMacSockets.byDevice[who+"-mac"] = sock
		crewMacSockets.Unlock()
		defer func(device string) {
			crewMacSockets.Lock()
			delete(crewMacSockets.byDevice, device)
			crewMacSockets.Unlock()
		}(who + "-mac")
	}
	if w := call("member", "agentbridge", `{"tab":"aaaa1111","public":true}`); w.Code != 200 || len(sent["member"]) != 1 || sent["member"][0].Operation != "agentbridge" || sent["member"][0].Action != "public" || len(sent["owner"]) != 0 {
		t.Fatalf("member's own agent was not made public through their own Mac: %d %s sent=%+v", w.Code, w.Body.String(), sent)
	}
	// Someone not in the Team, and a person invited but not yet joined, are refused by the Team rule (team_forbidden),
	// and nothing reaches any Mac.
	team, _ = readCrewTeam(team.ProjectID)
	team.People[1].Invited = true // "teammate"
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"outsider", "teammate"} {
		if w := call(who, "agentbridge", `{"tab":"aaaa1111","public":true}`); w.Code != 403 || !strings.Contains(w.Body.String(), "team_forbidden") {
			t.Fatalf("%s was not refused by the Team rule: %d %s", who, w.Code, w.Body.String())
		}
	}
	if len(sent["member"]) != 1 || len(sent["owner"]) != 0 {
		t.Fatalf("a refused call reached a Mac: %+v", sent)
	}
	team, _ = readCrewTeam(team.ProjectID)
	team.People[0].Where, team.People[0].MacPair, team.People[1].Invited = "", "", false
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if w := call("member", "leave", `{}`); w.Code != 200 {
		t.Fatalf("leave: %d %s", w.Code, w.Body.String())
	}
	saved, err := readCrewTeam(team.ProjectID)
	if err != nil || saved.member("member") || saved.OwnerID != "owner" {
		t.Fatalf("leave changed wrong membership: %+v %v", saved, err)
	}
}

// PLAT-856-543: changing Team access cancels a removed member's existing turn,
// while the creator, other Crews and independent registered owners keep work.
func TestCrewTeamRevocationStopsOnlyLostBuilderAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","products":["work"]},{"id":"member","products":["work"]}]}`)
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "revoked", ProjectID: "revoked", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	macCrewOnlineCopy(t, "revoked")
	for _, action := range []string{"leave", "remove", "off"} {
		t.Run(action, func(t *testing.T) {
			team := crewTeam{ProjectID: "revoked", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "member"}}}
			if err := writeCrewTeam(team); err != nil {
				t.Fatal(err)
			}
			canceled := map[string]bool{}
			api := &StreamingAPI{stoppedSessions: map[string]bool{}, activeSessions: map[string]*ActiveSessionInfo{
				"member-turn":  {UserID: "member", WorkspacePath: "Crew/revoked", Status: "running"},
				"owner-turn":   {UserID: "owner", WorkspacePath: "Crew/revoked", Status: "running"},
				"another-crew": {UserID: "member", WorkspacePath: "Crew/another", Status: "running"},
			}, agentCancelFuncs: map[string]context.CancelFunc{}}
			for id := range api.activeSessions {
				api.agentCancelFuncs[id] = func() { canceled[id] = true }
			}
			route, method, who, body := "/api/crew-teams/revoked", "PUT", "owner", `{"name":"Example","on":true,"people":[]}`
			if action == "leave" {
				route += "/leave"
				method = "POST"
				who = "member"
				body = `{}`
			}
			if action == "off" {
				body = `{"name":"Example","on":false,"people":[{"user_id":"member"}]}`
			}
			r := httptest.NewRequest(method, route, strings.NewReader(body))
			r.Header.Set(crewMacPairHeader, testMacPair) // the owner's calls here are the Mac's own
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who}))
			router := mux.NewRouter()
			api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("revocation failed: %d %s", w.Code, w.Body.String())
			}
			if !canceled["member-turn"] || canceled["owner-turn"] || canceled["another-crew"] {
				t.Fatalf("wrong work canceled: %v", canceled)
			}
		})
	}
}

// PLAT-746-1024: an old read-copy folder and marker must not redirect
// normal file reads away from the Crew's current authoritative content.
func TestCrewDashboardIgnoresRetiredMacSnapshot(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	key := externalCommandCrewRoot + "/db/reports/index.html"
	canonical, bound, err := crewroot.Resolve(f.docs, key)
	if err != nil || !bound {
		t.Fatalf("canonical Crew resource path: %t %v", bound, err)
	}
	if err = os.MkdirAll(filepath.Dir(canonical), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(canonical, []byte("<html>current authoritative page</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	f.write(t, externalCommandCrewRoot+"/mac-dashboards/db/reports/index.html", "<html>retired snapshot</html>")
	manifest, err := crewSyncManifestPath("team")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(manifest), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest+".dashboards-updated", []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "pair", Side: "server", Editing: "local", PeerProjectID: "mac"}); err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	mapped := publishedReadPath(ctx, key)
	if mapped != key {
		t.Fatal("read redirected to the retired snapshot")
	}
	data, err := f.api.reportPreviewWorkspaceClient(ctx, claims, externalCommandCrewRoot).DownloadFile(ctx, mapped)
	if err != nil || !strings.Contains(string(data), "current authoritative page") || strings.Contains(string(data), "retired snapshot") {
		t.Fatalf("canonical workspace read: %q %v", data, err)
	}
}
