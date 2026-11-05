package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
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
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): {ProfileID: crewProfileID, ConversationKey: key, SessionID: "member-session"}}}
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
	w := call("member", "agent-visibility", `{"agent_key":"`+key+`","in_team":false}`)
	if w.Code != 200 {
		t.Fatalf("own visibility: %d %s", w.Code, w.Body.String())
	}
	identities, err := store.agentIdentities(t.Context(), "member", crewProfileID, team.ProjectID)
	if err != nil || !identities[key].OnlyMe {
		t.Fatalf("own agent not private: %v", err)
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
	view := api.crewTeamMemberAccess(t.Context(), team, "member")
	if len(view.OwnAgents) != 1 || view.OwnAgents[0].InTeam {
		t.Fatalf("wrong member view: %+v", view)
	}
	if w := call("member", "leave", `{}`); w.Code != 200 {
		t.Fatalf("leave: %d %s", w.Code, w.Body.String())
	}
	saved, err := readCrewTeam(team.ProjectID)
	if err != nil || saved.member("member") || saved.OwnerID != "owner" {
		t.Fatalf("leave changed wrong membership: %+v %v", saved, err)
	}
}
