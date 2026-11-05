package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

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
	ws, _ := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	store := defaultProductConversationRegistryStore()
	key := "member-access:chat:own"
	memberMain := crewOwnerDefaultChatKey("member-access", "member")
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): {ProfileID: crewProfileID, ConversationKey: key, SessionID: "member-session"},
		productConversationRegistryEntryKey(crewProfileID, memberMain): {ProfileID: crewProfileID, ConversationKey: memberMain, SessionID: "member-main-session"}}}
	if err := store.writeDocument(t.Context(), productConversationRegistryPath("member"), doc); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"owner", "teammate"} {
		otherKey := "member-access:chat:" + who
		otherDoc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, otherKey): {ProfileID: crewProfileID, ConversationKey: otherKey, SessionID: who + "-session"}}}
		if err := store.writeDocument(t.Context(), productConversationRegistryPath(who), otherDoc); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	call := func(who, op, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/crew-teams/member-access/"+op, strings.NewReader(body))
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
	// Notifications follow the same rule from the same record (crewTeam.AgentChoices, crew_team_in_team.go).
	notified := func(who string) bool {
		return watching.notifyCanSee(t.Context(), who, notifyChat{owner: "member", project: team.ProjectID, found: true}, "member-session")
	}
	if !watches("owner") || !watches("teammate") || watches("outsider") || !notified("teammate") || notified("outsider") {
		t.Fatalf("Team watch: owner=%t teammate=%t outsider=%t notified teammate=%t", watches("owner"), watches("teammate"), watches("outsider"), notified("teammate"))
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
		if agent.Key == key {
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
			if agent.Key == key {
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
	saved, _ := readCrewTeam(team.ProjectID)
	if saved.agentRule("owner", "e139c703").WhenOthersAsk != "Ask me first." || saved.agentRule("member", memberMain).WhenOthersAsk != "Ask me first." {
		t.Fatalf("rules not kept: %+v", saved.AgentRules)
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
	for _, action := range []string{"leave", "remove", "off", "registered-owner"} {
		t.Run(action, func(t *testing.T) {
			team := crewTeam{ProjectID: "revoked", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "member"}}}
			if err := writeCrewTeam(team); err != nil {
				t.Fatal(err)
			}
			if action == "registered-owner" {
				if _, err := registry.AddCoOwner("work", "revoked", "member"); err != nil {
					t.Fatal(err)
				}
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
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who}))
			router := mux.NewRouter()
			api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("revocation failed: %d %s", w.Code, w.Body.String())
			}
			if canceled["member-turn"] != (action != "registered-owner") || canceled["owner-turn"] || canceled["another-crew"] {
				t.Fatalf("wrong work canceled: %v", canceled)
			}
		})
	}
}
