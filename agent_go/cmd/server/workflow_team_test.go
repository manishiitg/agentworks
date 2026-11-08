package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

const teamTestRoot = "Workflow/team-example"

type teamTestFixture struct {
	api  *StreamingAPI
	root string
}

func newTeamTestFixture(t *testing.T) *teamTestFixture {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	t.Setenv("WORKSPACE_FILE_STATE_DIR", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"leader","username":"leader","email":"leader@example.com","role":"creator"},{"id":"first","username":"first","role":"editor"},{"id":"second","username":"second","email":"second@example.com","role":"editor"},{"id":"observer","username":"observer","role":"viewer"}]}`)
	if e := os.MkdirAll(filepath.Join(root, teamTestRoot, "planning"), 0755); e != nil {
		t.Fatal(e)
	}
	raw := `{"steps":[{"type":"agent","id":"parent","title":"Coordinate","description":"Coordinate route contributions","items":[{"id":"coordinate","type":"user_message","message":"Coordinate contributions."}],"next_step_id":"end","predefined_routes":[{"route_id":"first-route","route_name":"First","sub_agent_step":{"type":"agent","id":"first-route","title":"First contribution","description":"Measure first contribution","items":[{"id":"measure","type":"user_message","message":"Measure contribution."}],"next_step_id":"end"}},{"route_id":"second-route","route_name":"Second","sub_agent_step":{"type":"agent","id":"second-route","title":"Second contribution","description":"Measure second contribution","items":[{"id":"measure","type":"user_message","message":"Measure contribution."}],"next_step_id":"end"}}]}],"orphan_steps":[]}`
	if err := os.WriteFile(filepath.Join(root, teamTestRoot, "planning/plan.json"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, teamTestRoot, "planning/step_config.json"), []byte(`{"steps":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	f := &teamTestFixture{api: &StreamingAPI{}, root: root}
	f.manifest(t, &WorkflowManifest{SchemaVersion: 1, ID: "team-example", Label: "Team example", Access: &WorkflowAccess{Owners: []string{"leader"}}, Team: &WorkflowTeam{LeaderID: "leader", Revision: 1, Contributors: []WorkflowContributor{{UserID: "first", Run: true}, {UserID: "second", Run: true}, {UserID: "observer"}}, Routes: []WorkflowRouteAssignment{{StepID: "parent", RouteID: "first-route", ContributorID: "first"}, {StepID: "parent", RouteID: "second-route", ContributorID: "second"}}}})
	return f
}
func (f *teamTestFixture) manifest(t *testing.T, m *WorkflowManifest) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(f.root, teamTestRoot, "workflow.json"), b, 0644); e != nil {
		t.Fatal(e)
	}
}
func (f *teamTestFixture) file(p string) string {
	raw, _ := os.ReadFile(filepath.Join(f.root, teamTestRoot, p))
	return string(raw)
}
func (f *teamTestFixture) hasHistory(tool, user string) bool {
	paths, _ := filepath.Glob(filepath.Join(f.root, teamTestRoot, "planning/changelog/*.json"))
	for _, p := range paths {
		raw, _ := os.ReadFile(p)
		if strings.Contains(string(raw), `"user_id": "`+user+`"`) && strings.Contains(string(raw), tool) {
			return true
		}
	}
	return false
}
func (f *teamTestFixture) tools(t *testing.T, user string) *chatPolicyTestDefinition {
	t.Helper()
	reg := &chatPolicyTestDefinition{}
	wrapped := workflowTeamRegistrar{definitionRegistrar: reg, root: teamTestRoot, user: user}
	if e := f.api.registerWorkflowTeamTools(wrapped, teamTestRoot, user, false); e != nil {
		t.Fatal(e)
	}
	return reg
}
func teamTestContext(user string) context.Context {
	return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user})
}

func TestWorkflowTeamPolicySchedulesAndDecisionDelegation(t *testing.T) {
	f := newTeamTestFixture(t)
	m, _, _ := ReadWorkflowManifest(context.Background(), teamTestRoot)
	save := func(user string, team *WorkflowTeam, revision int) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]interface{}{"workspace_path": teamTestRoot, "team": team, "expected_revision": revision})
		r := httptest.NewRequest(http.MethodPut, "/workflow/team", strings.NewReader(string(b))).WithContext(teamTestContext(user))
		w := httptest.NewRecorder()
		f.api.handleWorkflowTeam(w, r)
		return w
	}
	if w := save("first", m.Team, 1); w.Code != 403 {
		t.Fatalf("Contributor changed membership: %d %s", w.Code, w.Body.String())
	}
	duplicate := *m.Team
	duplicate.Contributors = append(append([]WorkflowContributor{}, m.Team.Contributors...), WorkflowContributor{UserID: "leader"})
	if w := save("leader", &duplicate, 1); w.Code != 400 {
		t.Fatalf("two roles accepted: %d %s", w.Code, w.Body.String())
	}
	if w := save("leader", m.Team, 0); w.Code != 409 {
		t.Fatal("stale team policy accepted")
	}
	sched := WorkflowSchedule{ID: "route-schedule", Name: "Route schedule", CronExpression: "0 9 * * *", Timezone: "UTC", InputPreset: "default", ChatID: "main", RouteSelections: map[string]string{"parent": "first-route"}, TeamAuthorizedBy: "first", TeamPolicyRevision: 1}
	if e := teamScheduleAuthority(m.Team, "first", sched); e == nil {
		t.Fatal("editing implicitly granted schedule management")
	}
	m.Team.Contributors[0].ManageSchedules = true
	m.Schedules = []WorkflowSchedule{sched}
	f.manifest(t, m)
	// PLAT-746-265: timers live in schedules.json, not the retired manifest
	// array. Test the saved projection that the scheduler actually fires.
	if err := saveWorkflowTimerActions(teamTestContext("leader"), teamTestRoot, m); err != nil {
		t.Fatal(err)
	}
	m, _, _ = ReadWorkflowManifest(context.Background(), teamTestRoot)
	sched = m.Schedules[0]
	if e := validateTeamScheduleFire(context.Background(), teamTestRoot, sched); e != nil {
		t.Fatal(e)
	}
	m.Team.Contributors[0].ManageSchedules = false
	f.manifest(t, m)
	if e := validateTeamScheduleFire(context.Background(), teamTestRoot, m.Schedules[0]); e == nil {
		t.Fatal("schedule retained revoked grant")
	}
	if e := validateTeamQuery(teamTestContext("first"), QueryRequest{AgentMode: "workflow", SelectedFolder: teamTestRoot}); e == nil {
		t.Fatal("Contributor started an unrestricted workflow run")
	}
	ctx := teamTestContext("first")
	if _, e := createReportHumanInput(ctx, teamTestRoot, ReportHumanInputCreateRequest{InputID: "approve-route", Question: "Approve contribution?", AllowFreeText: true, CreatedBy: "first"}); e != nil {
		t.Fatal(e)
	}
	answer := ReportHumanInputAnswerRequest{Note: "approved", AnsweredBy: "first"}
	if _, e := answerReportHumanInput(ctx, teamTestRoot, "approve-route", answer); e == nil {
		t.Fatal("edit rights granted approval")
	}
	m.Team.Contributors[0].Approve = true
	m.Team.Contributors[0].ApprovalIDs = []string{"different-decision"}
	f.manifest(t, m)
	if _, e := answerReportHumanInput(ctx, teamTestRoot, "approve-route", answer); e == nil {
		t.Fatal("approval scope was ignored")
	}
	m.Team.Contributors[0].ApprovalIDs = []string{"approve-route"}
	f.manifest(t, m)
	if _, e := answerReportHumanInput(ctx, teamTestRoot, "approve-route", answer); e != nil {
		t.Fatal(e)
	}
	if _, e := answerReportHumanInput(teamTestContext("leader"), teamTestRoot, "approve-route", ReportHumanInputAnswerRequest{Note: "replace accepted answer"}); e == nil {
		t.Fatal("competing answer silently replaced approval")
	}
	// Transfer changes the execution leader; existing project owners retain access.
	m.Team.LeaderID = "second"
	m.Team.Contributors = []WorkflowContributor{{UserID: "leader"}, {UserID: "first"}}
	m.Team.Routes = []WorkflowRouteAssignment{{StepID: "parent", RouteID: "first-route", ContributorID: "first"}}
	if w := save("leader", m.Team, 1); w.Code != 200 {
		t.Fatalf("transfer: %d %s", w.Code, w.Body.String())
	}
	transferred, _, e := ReadWorkflowManifest(context.Background(), teamTestRoot)
	if e != nil || workflowExecutionOwnerUserID(transferred) != "second" || len(transferred.effectiveOwners()) != 2 || workflowAccessForManifest(&UserClaims{UserID: "leader"}, transferred) != WorkflowAccessOwner {
		t.Fatalf("execution identity or co-owner access lost: %+v %v", transferred, e)
	}
	if !f.hasHistory("write_workflow_manifest", "leader") {
		t.Fatal("ownership transfer lost its authenticated change history")
	}
	if _, e := listReportHumanInputs(context.Background(), teamTestRoot, "answered", ""); e != nil {
		t.Fatal(e)
	}
}

// Exercise actual durable messaging with only the receiving model replaced.
// The receiving identity is the route's Contributor, not the Pulse Leader.

func TestWorkflowTeamOwnerTabsKeepExecutionAndSharedGrants(t *testing.T) {
	f := newTeamTestFixture(t)
	m, _, err := ReadWorkflowManifest(context.Background(), teamTestRoot)
	if err != nil {
		t.Fatal(err)
	}
	m.Access.AllowedKBWriters = []string{"research"}
	m.Access.RunEveryone = true
	f.manifest(t, m)
	if _, err := f.api.setProjectAccessLists(teamTestContext("leader"), &UserClaims{UserID: "leader"}, teamTestRoot, m, []string{"leader", "second"}, nil, true, false); err != nil {
		t.Fatalf("add project owner: %v", err)
	}
	saved, _, err := ReadWorkflowManifest(context.Background(), teamTestRoot)
	if err != nil || workflowAccessForManifest(&UserClaims{UserID: "second"}, saved) != WorkflowAccessOwner || workflowExecutionOwnerUserID(saved) != "leader" || !saved.Access.RunEveryone || !containsID(saved.Access.AllowedKBWriters, "research") {
		t.Fatalf("owner update changed execution or grants: %+v %v", saved, err)
	}
	if err := validateTeamQuery(teamTestContext("second"), QueryRequest{AgentMode: "workflow", SelectedFolder: teamTestRoot}); err != nil {
		t.Fatal("project owner lost shared execution access", err)
	}
}
