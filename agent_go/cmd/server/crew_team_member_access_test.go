package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
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
	// Showcase, 6 Nov: the owner's Main, asked to set the rule of "testing xxx" from chat, named it by its tab, its Mac
	// chat key and its name, with the Mac off; each was refused. Each now saves, for the caller's own agents only.
	for _, name := range []string{"e139c703", "mac-copy:chat:e139c703", "helper", "mac-copy"} {
		if w := call("owner", "agent-rule", `{"agent_key":"`+name+`","when_others_ask":"Saved by `+name+`"}`); w.Code != 200 {
			t.Fatalf("owner could not save the rule of their own Mac agent by %q: %d %s", name, w.Code, w.Body.String())
		}
		if w := call("member", "agent-rule", `{"agent_key":"`+name+`","when_others_ask":"Share everything."}`); w.Code != 403 {
			t.Fatalf("member saved the owner's agent rule by %q: %d", name, w.Code)
		}
	}
	if saved, _ := readCrewTeam(team.ProjectID); saved.agentRule("owner", "e139c703").WhenOthersAsk != "Saved by helper" || saved.agentRule("owner", macMainTab).WhenOthersAsk != "Saved by mac-copy" {
		t.Fatalf("rules by Mac key or name not kept on the owner's agents: %+v", saved.AgentRules["owner"])
	}
	call("owner", "agent-rule", `{"agent_key":"Helper","when_others_ask":"Ask me first."}`)
	saved, _ := readCrewTeam(team.ProjectID)
	if saved.agentRule("owner", "e139c703").WhenOthersAsk != "Ask me first." || saved.agentRule("member", memberMain).WhenOthersAsk != "Ask me first." {
		t.Fatalf("rules not kept: %+v", saved.AgentRules)
	}
	// Public on AgentBridge from the website (owner, 6 Nov): only the Crew's owner switches it, through their Mac; a
	// Team member is refused by the owner rule, and the owner is told plainly while the Mac is offline (no Mac here).
	if w := call("member", "agentbridge", `{"tab":"e139c703","public":true}`); w.Code != 403 || !strings.Contains(w.Body.String(), "agentbridge_owner_only") {
		t.Fatalf("member switched the owner's AgentBridge: %d %s", w.Code, w.Body.String())
	}
	if w := call("owner", "agentbridge", `{"tab":"e139c703","public":true}`); w.Code != 409 || !strings.Contains(w.Body.String(), crewAgentBridgeMacOffline) {
		t.Fatalf("owner's AgentBridge with the Mac offline: %d %s", w.Code, w.Body.String())
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

// PLAT-856-509 (owner, 6 Nov): a teammate opens a Mac Crew's dashboard from the server's read copy, through the real
// dashboard reader and workspace, while the Crew's own db/ on the server is left alone.
func TestCrewMacDashboardsAreReadFromTheCopy(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]}]}`)
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(f.docs, externalCommandCrewRoot)
	own := filepath.Join(root, "db", "reports")
	if err := os.MkdirAll(own, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "index.html"), []byte("<html><title>Server page</title></html>"), 0o660); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	page := "<html><head><title>Mac page</title></head><body>from the Mac</body></html>"
	version, err := chunks.Split(strings.NewReader(page), int64(len(page)), false, func(id string, data []byte) error { return putChunk(store, id, data) })
	if err != nil {
		t.Fatal(err)
	}
	if err = swapCrewMacDashboards(root, store, map[string]chunks.File{"db/reports/index.html": version}); err != nil {
		t.Fatal(err)
	}
	manifest, err := crewSyncManifestPath("team")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(manifest), 0o700)
	}
	if err == nil {
		err = os.WriteFile(manifest+".dashboards-updated", []byte("{}"), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "other", Username: "other"}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	// A Crew that lives on the server reads its own db/, even with such a folder present.
	for _, p := range []string{"/db/reports/index.html", "/db/db.sqlite"} {
		if got := publishedReadPath(ctx, externalCommandCrewRoot+p); got != externalCommandCrewRoot+p {
			t.Fatalf("a server Crew was redirected to %s", got)
		}
	}
	if err = crewroot.WriteSync("team", crewroot.SyncBinding{PairID: "pair", Side: "server", Editing: "local", PeerProjectID: "mac"}); err != nil {
		t.Fatal(err)
	}
	mapped := publishedReadPath(ctx, externalCommandCrewRoot+"/db/reports/index.html")
	if mapped != externalCommandCrewRoot+"/"+crewroot.MacDashboardsFolder+"/db/reports/index.html" {
		t.Fatalf("read goes to %s", mapped)
	}
	data, err := f.api.reportPreviewWorkspaceClient(ctx, claims, externalCommandCrewRoot).DownloadFile(ctx, mapped)
	if err != nil || !strings.Contains(string(data), "from the Mac") {
		t.Fatalf("the workspace read of the copy: %q %v", data, err)
	}
	if got := crewMacDashboardsRead(ctx, externalCommandCrewRoot+"/db/db.sqlite"); got != externalCommandCrewRoot+"/"+crewroot.MacDashboardsFolder+"/db/db.sqlite" {
		t.Fatalf("database query goes to %s", got)
	}
	listing := httptest.NewRecorder()
	f.api.handleDashboards(listing, adminRequest(http.MethodPost, "/api/dashboards", `{"workspace":"`+externalCommandCrewRoot+`","action":"list"}`, claims, nil))
	if listing.Code != 200 || !strings.Contains(listing.Body.String(), "Mac page") || strings.Contains(listing.Body.String(), "Server page") {
		t.Fatalf("listing: %d %s", listing.Code, listing.Body.String())
	}
	tab := httptest.NewRecorder()
	f.api.handleCrewTeamDashboardsTab(tab, adminRequest(http.MethodGet, "/api/crew-teams/team/dashboards-tab", "", claims, map[string]string{"project": "team"}))
	if tab.Code != 200 || !strings.Contains(tab.Body.String(), "copy on the server") || !strings.Contains(tab.Body.String(), "work on the owner's Mac") {
		t.Fatalf("tab: %d %s", tab.Code, tab.Body.String())
	}
}

// PLAT-856-509 (owner, 6 Nov): with Crew co-owners gone, a Crew's builders are added from its Team; over MCP too. The
// owner or a joined member may look at the directory and add a person (invited until they accept); an outsider may do neither.
func TestCrewTeamPeopleOverMCPFollowTheTeamRules(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]},{"id":"outsider","role":"editor","products":["work"]},{"id":"newbie","role":"editor","products":["work"]}]}`)
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}}); err != nil {
		t.Fatal(err)
	}
	call := func(user string, args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		args["crew_id"] = "team"
		r := adminRequest(http.MethodPost, "/api/external/v1/call", "", &UserClaims{UserID: user, Username: user}, nil)
		w := httptest.NewRecorder()
		f.api.externalCrewTeamSyncCall(w, r, args)
		return w
	}
	for _, user := range []string{"owner", "other"} {
		if w := call(user, map[string]any{"action": "directory"}); w.Code != 200 || !strings.Contains(w.Body.String(), `"user_id":"newbie"`) {
			t.Fatalf("%s directory: %d %s", user, w.Code, w.Body.String())
		}
	}
	if w := call("outsider", map[string]any{"action": "directory"}); w.Code != http.StatusForbidden {
		t.Fatalf("an outsider saw the directory: %d %s", w.Code, w.Body.String())
	}
	if w := call("outsider", map[string]any{"action": "people", "person": "newbie", "level": "works", "confirm": true}); w.Code < 400 {
		t.Fatalf("an outsider added someone: %d %s", w.Code, w.Body.String())
	}
	if w := call("other", map[string]any{"action": "people", "person": "newbie", "level": "works", "confirm": true}); w.Code != 200 {
		t.Fatalf("a joined member could not add: %d %s", w.Code, w.Body.String())
	}
	team, err := readCrewTeam("team")
	if err != nil || !slices.ContainsFunc(team.People, func(p crewTeamPerson) bool { return p.UserID == "newbie" && p.Invited }) {
		t.Fatalf("newbie is not invited: %+v %v", team.People, err)
	}
	// The Team's settings over MCP: the Team switch is the main owner's alone; an outsider changes nothing.
	for _, refused := range []struct {
		user string
		args map[string]any
	}{{"other", map[string]any{"action": "settings", "on": false}}, {"outsider", map[string]any{"action": "settings", "on": false}}} {
		if w := call(refused.user, refused.args); w.Code < 400 {
			t.Fatalf("%s changed the Team's settings: %d %s", refused.user, w.Code, w.Body.String())
		}
	}
	if team, _ = readCrewTeam("team"); !team.On {
		t.Fatal("a refused settings call turned the Team off")
	}
	if w := call("owner", map[string]any{"action": "settings", "on": false}); w.Code != 200 {
		t.Fatalf("owner turn off: %d %s", w.Code, w.Body.String())
	}
	if team, _ = readCrewTeam("team"); team.On || len(team.People) != 2 {
		t.Fatalf("settings did not keep the people while turning off: %+v", team)
	}
	if w := call("owner", map[string]any{"action": "settings", "on": true}); w.Code != 200 {
		t.Fatalf("owner turn on: %d %s", w.Code, w.Body.String())
	}
	if team, _ = readCrewTeam("team"); !team.On {
		t.Fatalf("the owner could not turn the Team on: %+v", team)
	}
	if w := call("owner", map[string]any{"action": "settings"}); w.Code != http.StatusBadRequest {
		t.Fatalf("an empty settings call: %d %s", w.Code, w.Body.String())
	}
}
