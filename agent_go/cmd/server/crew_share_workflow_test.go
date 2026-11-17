package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type shareWorkflowFixture struct {
	f      *externalToolsFixture
	ctx    context.Context
	router *mux.Router
}

func newShareWorkflowFixture(t *testing.T) *shareWorkflowFixture {
	t.Helper()
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	x := &shareWorkflowFixture{f: f, ctx: context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}), router: mux.NewRouter()}
	f.api.registerCrewTeamRoutes(x.router.PathPrefix("/api").Subrouter())
	return x
}

func (x *shareWorkflowFixture) call(method, suffix, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, "/api/crew-teams/team"+suffix, strings.NewReader(body)).WithContext(x.ctx)
	x.router.ServeHTTP(w, r)
	return w
}

func (x *shareWorkflowFixture) seed(t *testing.T, id string, records []projectFunctionDefinition) crewExecutableBinding {
	t.Helper()
	binding := crewExecutableBinding{ID: id, CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/" + id, Kind: "script", RuntimeRoot: "workflows/" + id}
	seedSavedCrewExecutableForTest(t, binding)
	if err := initializeCrewScriptSource(x.ctx, binding, "Example Python workflow", ""); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectFunctionDefinitions(x.ctx, binding.Root, records); err != nil {
		t.Fatal(err)
	}
	return binding
}

func namedShareAction(t *testing.T) projectFunctionDefinition {
	t.Helper()
	record, err := workflowFunctionDefinition(WorkflowSchedule{ID: uuid.NewString(), Enabled: true, Function: &WorkflowFunctionSpec{Name: "review_pr", AllowedCallers: []triggerCaller{{Type: "crew", ID: "source", ProfileID: "work"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// PLAT-746 / PR1730: allowed callers plus no optional description must not
// hide a named function. A native InputMapping binding stays a connection.
func TestShareNamedActionWithCallersStaysEditable(t *testing.T) {
	x := newShareWorkflowFixture(t)
	caller := triggerCaller{Type: "crew", ID: "source", ProfileID: "work"}
	connection, err := workflowBindingDefinition(WorkflowSchedule{ID: uuid.NewString(), Enabled: true, Caller: &caller})
	if err != nil {
		t.Fatal(err)
	}
	binding := x.seed(t, "named-action-workflow", []projectFunctionDefinition{namedShareAction(t), connection})
	w := x.call("GET", "/share", "")
	var page crewSharePage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.ActionsGroup.Rows) != 1 {
		t.Fatalf("named action missing or generated connection shown: %d %s", w.Code, w.Body.String())
	}
	action := page.ActionsGroup.Rows[0]
	if action.Name != "review_pr" || !action.On || !action.CanChange || action.Target != binding.ID {
		t.Fatalf("named action lost its switch or target: %+v", action)
	}
	w = x.call("POST", "/action", `{"target":"`+binding.ID+`","name":"review_pr","on":false}`)
	if w.Code != 200 {
		t.Fatalf("owner cannot disable displayed named action: %d %s", w.Code, w.Body.String())
	}
	records, err := readProjectFunctionDefinitions(x.ctx, binding.Root)
	if err != nil || len(records) != 2 {
		t.Fatalf("saved actions: %+v %v", records, err)
	}
	for _, record := range records {
		if record.Name == "review_pr" && record.Enabled || record.ID == connection.ID && !record.Enabled {
			t.Fatalf("wrong action changed: %+v", record)
		}
	}
}

// A Crew can hold several workflows. The menu and Share list each one, and a submitted target is authorized
// against its own Crew binding.
func TestShareAndMenuListEveryWorkflowAndValidateActualTarget(t *testing.T) {
	x := newShareWorkflowFixture(t)
	z := x.seed(t, "z-workflow", []projectFunctionDefinition{namedShareAction(t)})
	a := x.seed(t, "a-workflow", []projectFunctionDefinition{namedShareAction(t)})
	for i := 0; i < 16; i++ {
		listed := crewWorkflowsOf(externalCommandCrewRoot)
		if len(listed) != 2 || listed[0].ID != a.ID || listed[1].ID != z.ID {
			t.Fatalf("unstable workflow order: %+v", listed)
		}
	}
	menu, err := crewPanelWorkflows(x.ctx, externalCommandCrewRoot)
	if err != nil || len(menu.Rows) != 2 {
		t.Fatalf("menu must list both workflows: %+v %v", menu, err)
	}
	ids := map[string]bool{}
	for _, row := range menu.Rows {
		ids[row.ID] = true
	}
	if !ids["workflow:"+a.ID] || !ids["workflow:"+z.ID] {
		t.Fatalf("menu rows do not match the workflows: %+v", menu.Rows)
	}
	w := x.call("GET", "/share", "")
	var page crewSharePage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.ActionsGroup.Rows) != 2 {
		t.Fatalf("Share must list both workflows' actions: %d %s", w.Code, w.Body.String())
	}
	targets := map[string]bool{}
	for _, row := range page.ActionsGroup.Rows {
		targets[row.Target] = true
	}
	if !targets[a.ID] || !targets[z.ID] {
		t.Fatalf("Share rows do not match the workflows: %+v", page.ActionsGroup.Rows)
	}
	w = x.call("POST", "/action", `{"target":"`+z.ID+`","name":"review_pr","on":false}`)
	if w.Code != 200 {
		t.Fatalf("a current same-Crew target was refused: %d %s", w.Code, w.Body.String())
	}
	foreign := crewExecutableBinding{ID: "foreign-workflow", CrewID: "another", CrewRoot: "Crew/another", Root: "Workflow/foreign-workflow", Kind: "script"}
	if err := registerCrewExecutable(foreign); err != nil {
		t.Fatal(err)
	}
	w = x.call("POST", "/action", `{"target":"`+foreign.ID+`","name":"review_pr","on":false}`)
	if w.Code != 404 {
		t.Fatalf("another Crew's target was accepted: %d %s", w.Code, w.Body.String())
	}
}
