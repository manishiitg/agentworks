package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
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
	client := workspacepkg.NewClient(getWorkspaceAPIURL(), workspacepkg.WithUserID(user))
	read := func(ctx context.Context, p string) (string, error) {
		result, e := client.ReadWorkspaceFile(ctx, workspacepkg.ReadWorkspaceFileParams{Filepath: p})
		return result.Content, e
	}
	write := func(ctx context.Context, p, s string) error {
		_, e := client.UpdateWorkspaceFile(ctx, workspacepkg.UpdateWorkspaceFileParams{Filepath: p, Content: s})
		return e
	}
	if e := stepworkflow.RegisterPlanModificationTools(wrapped, teamTestRoot, loggerv2.NewNoop(), read, write, nil, "workflow-builder"); e != nil {
		t.Fatal(e)
	}
	if e := f.api.registerWorkflowTeamTools(wrapped, teamTestRoot, user, false); e != nil {
		t.Fatal(e)
	}
	return reg
}
func teamTestContext(user string) context.Context {
	return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user})
}

// Real native tools and workspace HTTP round trips pin the exclusive editor,
// concurrent chat, revision and retained-grant boundaries together.
func TestWorkflowTeamNativeEditsRejectOtherEditorsAndStaleRevisions(t *testing.T) {
	f := newTeamTestFixture(t)
	first, second, leader := f.tools(t, "first"), f.tools(t, "second"), f.tools(t, "leader")
	initial := f.file("planning/plan.json")
	revision := teamRevision(initial)
	change := func(reg *chatPolicyTestDefinition, id, title, rev string) error {
		_, e := reg.tools["update_step"].exec(context.Background(), map[string]interface{}{"step_id": id, "changes": map[string]interface{}{"title": title, "reason": "Clarify contribution"}, "expected_plan_revision": rev})
		return e
	}
	if e := change(leader, "first-route", "Leader overwrite", revision); e == nil {
		t.Fatal("Leader edited an assigned Contributor route")
	}
	if e := change(second, "first-route", "Other overwrite", revision); e == nil {
		t.Fatal("another Contributor edited the assigned route")
	}
	if f.file("planning/plan.json") != initial {
		t.Fatal("refused native edits changed the saved plan")
	}
	if e := change(first, "first-route", "Measured first contribution", revision); e != nil {
		t.Fatal(e)
	}
	saved := f.file("planning/plan.json")
	if !strings.Contains(saved, "Measured first contribution") {
		t.Fatal("native scoped update was not saved")
	}
	if e := change(second, "second-route", "Second contribution revised", revision); e == nil || !strings.Contains(e.Error(), "conflict") {
		t.Fatalf("stale edit must conflict: %v", e)
	}
	if f.file("planning/plan.json") != saved {
		t.Fatal("stale edit overwrote saved work")
	}
	if e := change(second, "second-route", "Measured second contribution", teamRevision(saved)); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(f.file("planning/plan.json"), "Measured first contribution") || !strings.Contains(f.file("planning/plan.json"), "Measured second contribution") {
		t.Fatal("independent edits lost a contribution")
	}
	// Raw clients and the direct browser proxy cannot bypass native authority.
	client := workspacepkg.NewClient(getWorkspaceAPIURL(), workspacepkg.WithUserID("leader"))
	if _, e := client.UpdateWorkspaceFile(teamTestContext("leader"), workspacepkg.UpdateWorkspaceFileParams{Filepath: teamTestRoot + "/code/first-route/main.py", Content: "overwrite"}); e == nil {
		t.Fatal("raw source write bypassed assignment")
	}
	request := httptest.NewRequest(http.MethodPut, "/api/workspace/documents/"+teamTestRoot+"/code/first-route/main.py", nil).WithContext(teamTestContext("leader"))
	if newWorkspaceProxyPolicy(request, "leader").denies("path", teamTestRoot+"/code/first-route/main.py") == "" {
		t.Fatal("browser proxy bypassed assignment")
	}
	if _, e := leader.tools["edit_team_source"].exec(context.Background(), map[string]interface{}{"path": "code/first-route/main.py", "content": "overwrite", "expected_revision": "missing"}); e == nil {
		t.Fatal("Leader source tool bypassed assignment")
	}
	if _, e := first.tools["edit_team_source"].exec(context.Background(), map[string]interface{}{"path": "code/first-route/main.py", "content": "print('measure')", "expected_revision": "missing"}); e != nil {
		t.Fatal(e)
	}
	// The local commit preserves native source capacity and file mode, and
	// still writes the same canonical changelog without re-entering HTTP locks.
	source := filepath.Join(f.root, teamTestRoot, "code/first-route/main.py")
	if e := os.Chmod(source, 0755); e != nil {
		t.Fatal(e)
	}
	large := "#" + strings.Repeat("x", 3<<20) + "\nprint('measure')"
	if _, e := first.tools["edit_team_source"].exec(context.Background(), map[string]interface{}{"path": "code/first-route/main.py", "content": large, "expected_revision": teamRevision("print('measure')")}); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(source)
	if e != nil || info.Mode().Perm() != 0755 || f.file("code/first-route/main.py") != large {
		t.Fatal("native source replacement lost its content or mode", e)
	}
	if !f.hasHistory("edit_team_source", "first") {
		t.Fatal("source edit lost its authenticated change history")
	}
	m, _, _ := ReadWorkflowManifest(context.Background(), teamTestRoot)
	m.Team.Routes[0].ContributorID = "second"
	m.Team.Revision++
	f.manifest(t, m)
	if e := change(first, "first-route", "Retained permission overwrite", teamRevision(f.file("planning/plan.json"))); e == nil {
		t.Fatal("retained tool kept revoked route authority")
	}
	if e := change(second, "first-route", "Reassigned contribution", teamRevision(f.file("planning/plan.json"))); e != nil {
		t.Fatal(e)
	}
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
	sched := WorkflowSchedule{ID: "route-schedule", Name: "Route schedule", CronExpression: "0 9 * * *", Timezone: "UTC", GroupNames: []string{"default"}, RouteSelections: map[string]string{"parent": "first-route"}, TeamAuthorizedBy: "first", TeamPolicyRevision: 1}
	if e := teamScheduleAuthority(m.Team, "first", sched); e == nil {
		t.Fatal("editing implicitly granted schedule management")
	}
	m.Team.Contributors[0].ManageSchedules = true
	m.Schedules = []WorkflowSchedule{sched}
	f.manifest(t, m)
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

func TestWorkflowTeamFailedPreparationDoesNotCommitPlan(t *testing.T) {
	f := newTeamTestFixture(t)
	reg := &chatPolicyTestDefinition{}
	wrapped := workflowTeamRegistrar{definitionRegistrar: reg, root: teamTestRoot, user: "first"}
	initial := f.file("planning/plan.json")
	e := wrapped.RegisterCustomTool("update_step", "Prepare a change", map[string]interface{}{"properties": map[string]interface{}{}}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		raw, _, e := readFileFromWorkspace(ctx, teamTestRoot+"/planning/plan.json")
		if e != nil {
			return "", e
		}
		raw = strings.Replace(raw, "First contribution", "Changed contribution", 1)
		if e = writeFileToWorkspace(ctx, teamTestRoot+"/planning/plan.json", raw); e != nil {
			return "", e
		}
		return "", fmt.Errorf("dependent configuration failed")
	}, "workflow")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reg.tools["update_step"].exec(context.Background(), map[string]interface{}{"expected_plan_revision": teamRevision(initial)}); e == nil {
		t.Fatal("preparation unexpectedly succeeded")
	}
	if f.file("planning/plan.json") != initial {
		t.Fatal("failed preparation left a partial plan")
	}
}

// Exercise actual durable messaging with only the receiving model replaced.
// The receiving identity is the route's Contributor, not the Pulse Leader.

// Repeated message item IDs never confer route ownership. Drive native edits
// through the workspace boundary, including a collision with a real step ID.
func TestWorkflowTeamItemIDsDoNotGrantStepAuthority(t *testing.T) {
	f := newTeamTestFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, teamTestRoot, "planning/plan.json"), []byte(strings.ReplaceAll(f.file("planning/plan.json"), `"id":"coordinate"`, `"id":"measure"`)), 0644); err != nil {
		t.Fatal(err)
	}
	first := f.tools(t, "first")
	change := func(id, itemID, message string) error {
		_, err := first.tools["update_step"].exec(context.Background(), map[string]interface{}{
			"step_id": id, "changes": map[string]interface{}{
				"items":  []interface{}{map[string]interface{}{"id": itemID, "type": "user_message", "message": message}},
				"reason": "Clarify measurement instructions",
			}, "expected_plan_revision": teamRevision(f.file("planning/plan.json")),
		})
		return err
	}
	if err := change("first-route", "measure", "Measure the assigned contribution."); err != nil {
		t.Fatalf("repeated item ID in another route blocked an authorized edit: %v", err)
	}
	if err := change("parent", "measure", "Overwrite shared coordination."); err == nil {
		t.Fatal("repeated item ID granted authority over a shared step's item")
	}
	// An item named like an actual shared step must not erase that step from
	// the protected projection or overwrite its index entry.
	if err := change("first-route", "parent", "Measure the assigned contribution."); err != nil {
		t.Fatal(err)
	}
	before := f.file("planning/plan.json")
	if err := change("parent", "parent", "Overwrite shared coordination."); err == nil {
		t.Fatal("item ID collision granted authority over the shared step")
	}
	if f.file("planning/plan.json") != before {
		t.Fatal("rejected shared edit changed the saved plan")
	}
	if err := change("second-route", "measure", "Overwrite another contribution."); err == nil {
		t.Fatal("item ID granted authority over another Contributor's route")
	}
}

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
