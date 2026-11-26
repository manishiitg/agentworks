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

// A Crew can hold several workflows: the menu lists each one, but Share never offers a workflow to others; a
// Crew's workflows stay private to its owner (owner, 23 Nov, #3605).
func TestShareNeverOffersWorkflowsWhileMenuListsThem(t *testing.T) {
	x := newShareWorkflowFixture(t)
	z := x.seed(t, "z-workflow", []projectFunctionDefinition{namedShareAction(t)})
	a := x.seed(t, "a-workflow", []projectFunctionDefinition{namedShareAction(t)})
	listed := crewWorkflowsOf(externalCommandCrewRoot)
	if len(listed) != 2 || listed[0].ID != a.ID || listed[1].ID != z.ID {
		t.Fatalf("unstable workflow order: %+v", listed)
	}
	menu, err := crewPanelWorkflows(x.ctx, externalCommandCrewRoot)
	if err != nil || len(menu.Rows) != 2 {
		t.Fatalf("menu must list both workflows: %+v %v", menu, err)
	}
	w := x.call("GET", "/share", "")
	var page crewSharePage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatalf("share page: %d %s", w.Code, w.Body.String())
	}
	for _, row := range page.ActionsGroup.Rows {
		if row.Name == "review_pr" {
			t.Fatalf("Share offered a workflow's function: %+v", row)
		}
	}
	w = x.call("POST", "/action", `{"name":"review_pr","on":false}`)
	records, err := readProjectFunctionDefinitions(x.ctx, z.Root)
	if err != nil || len(records) != 1 || !records[0].Enabled {
		t.Fatalf("the action switch changed a workflow's function (%d): %+v %v", w.Code, records, err)
	}
}

// On a Mac, a Crew's workflow tools are the owner's only (#3605): every kind of turn answering someone else loses
// them; the owner's own turn and any server turn keep them.
func TestMacCrewWorkflowToolsOwnerOnly(t *testing.T) {
	owner := &UserClaims{UserID: "owner"}
	cases := []struct {
		name  string
		local bool
		req   QueryRequest
		deny  bool
	}{
		{"owner's own turn", true, QueryRequest{}, false},
		{"owner asked", true, QueryRequest{AskedBy: "owner"}, false},
		{"teammate asks the shared agent", true, QueryRequest{SharedAgentSender: "teammate"}, true},
		{"another person's agent message", true, QueryRequest{AgentMessageFromOtherPerson: true}, true},
		{"guest caller", true, QueryRequest{CrewGuestCaller: "guest"}, true},
		{"pinned Run mode", true, QueryRequest{PinRunMode: true}, true},
		{"asked by someone else", true, QueryRequest{AskedBy: "teammate"}, true},
		{"server turn", false, QueryRequest{SharedAgentSender: "teammate"}, false},
	}
	for _, c := range cases {
		mode := "server"
		if c.local {
			mode = "local"
		}
		t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", mode)
		if got := macCrewWorkflowToolsDenied(c.req, owner); got != c.deny {
			t.Errorf("%s: denied=%v, want %v", c.name, got, c.deny)
		}
	}
}
