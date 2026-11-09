package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// PLAT-746-748 / PR1730: Main publications have no AgentID, but their enabled
// and disabled actions are both immutable. The same Share response must retain
// the native last-shared-agent lock. Check the real Share HTTP path,
// including ordinary owner edits and an admitted teammate's owner-only refusal.
func TestCrewShareMainPublicationActionsStayFixed(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	router := mux.NewRouter()
	f.api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	call := func(user, method, suffix, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/crew-teams/team"+suffix, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	mustOK := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
	}
	mustOK(call("owner", "PUT", "", `{"name":"Example","on":true,"people":[{"user_id":"other"}],"folder":{"whole":true}}`))
	mustOK(call("other", "POST", "/invite", `{"accept":true}`))
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	// PR1730: one shared agent must keep the native lock and explanation;
	// with two, switching Main off is allowed and leaves the helper shared.
	main := crewOwnerDefaultChatKey("team", "owner")
	helper := "team" + codeChatSideMarker + "helper"
	saveAgents := func(keys ...string) {
		t.Helper()
		entries := map[string]ProductConversationRecord{}
		for _, key := range keys {
			entries[productConversationRegistryEntryKey(crewProfileID, key)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, SessionID: key + "-session"}
		}
		if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	mainAsk := func() crewShareAsk {
		t.Helper()
		w := call("owner", "GET", "/share", "")
		mustOK(w)
		var page crewSharePage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, agent := range page.Agents {
			if agent.Key == main && agent.Ask != nil {
				return *agent.Ask
			}
		}
		t.Fatal("Main agent missing from Share response")
		return crewShareAsk{}
	}
	saveAgents(main)
	if ask := mainAsk(); !ask.On || ask.CanChange || ask.Fixed != errCrewTeamLastShared.Error() {
		t.Fatalf("last shared agent lost its lock or explanation: %+v", ask)
	}
	if w := call("owner", "POST", "/agent-visibility", `{"agent_key":"main","in_team":false}`); w.Code != 400 || !strings.Contains(w.Body.String(), "last_shared_agent") {
		t.Fatalf("last shared agent was unshared: %d %s", w.Code, w.Body.String())
	}
	saveAgents(main, helper)
	if ask := mainAsk(); !ask.On || !ask.CanChange || ask.Fixed != "" {
		t.Fatalf("two shared agents still locked: %+v", ask)
	}
	mustOK(call("owner", "POST", "/agent-visibility", `{"agent_key":"main","in_team":false}`))
	if ask := mainAsk(); ask.On || !ask.CanChange {
		t.Fatalf("Main did not become private or cannot be shared again: %+v", ask)
	}
	// Restore Main for the unchanged published-action checks below.
	mustOK(call("owner", "POST", "/agent-visibility", `{"agent_key":"main","in_team":true}`))
	target := triggerTarget{Kind: triggerCallerCrew, Path: externalCommandCrewRoot, CrewID: "team", CrewOwner: "owner", CrewProfile: "work"}
	if err := writeCrewFunctions(ctx, target, []crewFunction{
		{Name: "enabled_action", Instructions: "Return the result."},
		{Name: "disabled_action", Instructions: "Return the result."},
	}); err != nil {
		t.Fatal(err)
	}
	mustOK(call("owner", "POST", "/action", `{"name":"disabled_action","on":false}`))
	readActions := func(user string) map[string]crewShareAction {
		t.Helper()
		w := call(user, "GET", "/share", "")
		mustOK(w)
		var page crewSharePage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		actions := map[string]crewShareAction{}
		for _, action := range page.ActionsGroup.Rows {
			actions[action.Name] = action
		}
		if len(actions) != 2 || !actions["enabled_action"].On || actions["disabled_action"].On {
			t.Fatalf("lost enabled or disabled Main actions: %+v", actions)
		}
		return actions
	}
	for _, action := range readActions("owner") {
		if !action.CanChange {
			t.Fatalf("ordinary owner cannot edit: %+v", action)
		}
	}
	// Admission to the Team succeeds, so this refusal must come from the
	// action's owner rule, rather than missing membership or account setup.
	mustOK(call("other", "GET", "/share", ""))
	w := call("other", "POST", "/action", `{"name":"enabled_action","on":false}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "owner_only") || !strings.Contains(w.Body.String(), "Ask the owner") {
		t.Fatalf("teammate changed an action or got no next step: %d %s", w.Code, w.Body.String())
	}

	record := &publishedProject{Root: externalCommandCrewRoot, WorkspaceID: "team", ProfileID: "work", OwnerID: "owner", PublisherID: "owner", ProjectID: "laptop", Revision: "main-publication", SourcePaths: []string{"functions.json"}}
	source, found, err := readFileFromWorkspace(ctx, crewFunctionRoot(ctx, target)+"/functions.json")
	if err != nil || !found {
		t.Fatalf("read saved actions: found=%v err=%v", found, err)
	}
	f.write(t, record.sourceRoot()+"/functions.json", source)
	// Publication is the source of truth, including disabled definitions;
	// the runtime Crew need not carry its own functions file.
	if err := writeFileToWorkspace(ctx, crewFunctionRoot(ctx, target)+"/functions.json", `{"functions":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"owner", "other"} {
		for _, action := range readActions(user) {
			if action.CanChange || !strings.Contains(action.Fixed, "publish again") {
				t.Fatalf("%s offered a published switch: %+v", user, action)
			}
		}
	}
	for _, body := range []string{
		`{"name":"enabled_action","on":false}`,
		`{"name":"disabled_action","on":true}`,
		`{"name":"enabled_action","on":true}`,
	} {
		w = call("owner", "POST", "/action", body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "publish again") {
			t.Fatalf("published action edit/no-op accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if got := f.read(t, record.sourceRoot()+"/functions.json"); got != source {
		t.Fatal("immutable publication source changed")
	}
}
