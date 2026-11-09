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
// and disabled actions are both immutable. Check the real Share HTTP path,
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
		for _, agent := range page.Agents {
			for _, action := range agent.Actions {
				actions[action.Name] = action
			}
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
	source := f.read(t, crewFunctionRoot(ctx, target)+"/functions.json")
	f.write(t, record.sourceRoot()+"/functions.json", source)
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	// Publication is the source of truth, including disabled definitions;
	// the runtime Crew need not carry its own functions file.
	f.write(t, crewFunctionRoot(ctx, target)+"/functions.json", `{"functions":[]}`)
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
