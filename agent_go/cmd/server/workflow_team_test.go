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
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const teamTestRoot = "Workflow/team-example"

type teamTestFixture struct {
	store *fakeWorkspaceDocumentStore
	api   *StreamingAPI
	root  string
}

func newTeamTestFixture(t *testing.T) *teamTestFixture {
	t.Helper()
	server, store := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	t.Setenv("WORKSPACE_FILE_STATE_DIR", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"leader","username":"leader","role":"creator"},{"id":"first","username":"first","role":"editor"},{"id":"second","username":"second","role":"editor"},{"id":"observer","username":"observer","role":"viewer"}]}`)
	if e := os.MkdirAll(filepath.Join(root, teamTestRoot), 0755); e != nil {
		t.Fatal(e)
	}
	raw := `{"steps":[{"type":"agent","id":"parent","title":"Coordinate","description":"Coordinate route contributions","items":[{"id":"coordinate","type":"user_message","message":"Coordinate contributions."}],"next_step_id":"end","predefined_routes":[{"route_id":"first-route","route_name":"First","sub_agent_step":{"type":"agent","id":"first-route","title":"First contribution","description":"Measure first contribution","items":[{"id":"measure","type":"user_message","message":"Measure contribution."}],"next_step_id":"end"}},{"route_id":"second-route","route_name":"Second","sub_agent_step":{"type":"agent","id":"second-route","title":"Second contribution","description":"Measure second contribution","items":[{"id":"measure","type":"user_message","message":"Measure contribution."}],"next_step_id":"end"}}]}],"orphan_steps":[]}`
	store.files[teamTestRoot+"/planning/plan.json"] = raw
	store.files[teamTestRoot+"/planning/step_config.json"] = `{"steps":[]}`
	f := &teamTestFixture{store: store, api: &StreamingAPI{}, root: root}
	f.manifest(t, &WorkflowManifest{SchemaVersion: 1, ID: "team-example", Label: "Team example", Access: &WorkflowAccess{Owners: []string{"leader"}}, Team: &WorkflowTeam{LeaderID: "leader", Revision: 1, Contributors: []WorkflowContributor{{UserID: "first", Run: true}, {UserID: "second", Run: true}, {UserID: "observer"}}, Routes: []WorkflowRouteAssignment{{StepID: "parent", RouteID: "first-route", ContributorID: "first"}, {StepID: "parent", RouteID: "second-route", ContributorID: "second"}}}})
	return f
}
func (f *teamTestFixture) manifest(t *testing.T, m *WorkflowManifest) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	f.store.mu.Lock()
	f.store.files[teamTestRoot+"/workflow.json"] = string(b)
	f.store.mu.Unlock()
	if e = os.WriteFile(filepath.Join(f.root, teamTestRoot, "workflow.json"), b, 0644); e != nil {
		t.Fatal(e)
	}
}
func (f *teamTestFixture) file(p string) string {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	return f.store.files[teamTestRoot+"/"+p]
}
func (f *teamTestFixture) hasHistory(tool, user string) bool {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	for path, raw := range f.store.files {
		if strings.HasPrefix(path, teamTestRoot+"/planning/changelog/") && strings.Contains(raw, `"user_id": "`+user+`"`) && strings.Contains(raw, tool) {
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
	if !f.hasHistory("edit_team_source", "first") {
		t.Fatal("source edit lost its authenticated change history")
	}
	if f.api.workflowBuilderHeldByOther("second-chat", "second", QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: teamTestRoot}) != "" || !workflowTeamAllowsConcurrent(teamTestRoot) {
		t.Fatal("team Builder still reserves whole workflow")
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
	sched := WorkflowSchedule{ID: "route-schedule", Name: "Route schedule", CronExpression: "0 9 * * *", Timezone: "UTC", GroupNames: []string{"default"}, RouteSelections: map[string]string{"parent": "first-route"}, TeamAuthorizedBy: "first", TeamPolicyRevision: 1, AfterRun: &ScheduleAfterRun{}}
	if e := teamScheduleAuthority(m.Team, "first", sched); e == nil {
		t.Fatal("editing implicitly granted schedule management")
	}
	m.Team.Contributors[0].ManageSchedules = true
	m.Schedules = []WorkflowSchedule{sched}
	f.manifest(t, m)
	if e := validateTeamScheduleFire(context.Background(), teamTestRoot, sched); e != nil {
		t.Fatal(e)
	}
	sched.AfterRun = &ScheduleAfterRun{Publish: true}
	if e := teamScheduleAuthority(m.Team, "first", sched); e == nil {
		t.Fatal("schedule grant implicitly granted publication")
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
	// Transfer changes one canonical owner and keeps history / outgoing member.
	m.Team.LeaderID = "second"
	m.Team.Contributors = []WorkflowContributor{{UserID: "leader"}, {UserID: "first"}}
	m.Team.Routes = []WorkflowRouteAssignment{{StepID: "parent", RouteID: "first-route", ContributorID: "first"}}
	if w := save("leader", m.Team, 1); w.Code != 200 {
		t.Fatalf("transfer: %d %s", w.Code, w.Body.String())
	}
	transferred, _, e := ReadWorkflowManifest(context.Background(), teamTestRoot)
	if e != nil || workflowExecutionOwnerUserID(transferred) != "second" || len(transferred.effectiveOwners()) != 1 {
		t.Fatalf("not one new owner: %+v %v", transferred, e)
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
func TestWorkflowTeamPulseTargetsContributorBuilderAndKeepsInboxPrivate(t *testing.T) {
	f := newTeamTestFixture(t)
	m, _, _ := ReadWorkflowManifest(context.Background(), teamTestRoot)
	setWorkflowPulseEnabled(m, true)
	f.manifest(t, m)
	ctx := teamTestContext("leader")
	conv, e := ensureGoalLeadConversation(ctx, teamTestRoot, m.ID, time.Now().UTC(), false, nil)
	if e != nil {
		t.Fatal(e)
	}
	oldFind, oldTurn := findPulseBuilderChat, agentMessageTurn
	findPulseBuilderChat = func(_ *StreamingAPI, _ context.Context, user string, _ *WorkflowManifest, _ string, _ string) (string, error) {
		if user != "first" && user != "second" {
			return "", fmt.Errorf("wrong private Builder recipient: %s", user)
		}
		return "team-" + user + "-builder", nil
	}
	delivered := make(chan error, 1)
	agentMessageTurn = func(_ *StreamingAPI, turnCtx context.Context, _ map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		release, e := acquireAgentMessageAuthority(turnCtx, session)
		if e != nil {
			delivered <- e
			return internalSessionTurnResult{}, e
		}
		defer release()
		if user != "first" || session != "team-first-builder" || teamActor(turnCtx) != "first" {
			delivered <- fmt.Errorf("wrong trusted receiving identity %s %s", user, teamActor(turnCtx))
		} else {
			delivered <- nil
		}
		return internalSessionTurnResult{FinalResponse: "Private chat text is not automatically forwarded"}, nil
	}
	t.Cleanup(func() { findPulseBuilderChat, agentMessageTurn = oldFind, oldTurn })
	out, e := f.api.askBuilder(ctx, pulseBuilderAskRequest{UserID: "leader", WorkspacePath: teamTestRoot, PulseSession: conv.SessionID, RouteStepID: "parent", RouteID: "first-route", Message: "Measure your route and send evidence when ready", SubmissionID: "team-route-message"})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-delivered:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("route Builder did not receive message")
	}
	inbox := fmt.Sprint(out["inbox_id"])
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: m.ID}, Path: teamTestRoot, Chat: &codeChat{Key: "session:team-first-builder", SessionID: "team-first-builder"}}
	if _, e = f.api.readAgentMessages(teamTestContext("first"), "first", caller, inbox, 0, 20, 0); e != nil {
		t.Fatal(e)
	}
	if _, e = f.api.readAgentMessages(teamTestContext("second"), "second", caller, inbox, 0, 20, 0); e == nil {
		t.Fatal("another Contributor read the private reply address")
	}
	// Reassignment does not remove the old recipient's membership. An old
	// address must still not redirect route work back to that person's chat.
	m.Team.Routes[0].ContributorID = "second"
	m.Team.Revision++
	f.manifest(t, m)
	_, e = f.api.askBuilder(ctx, pulseBuilderAskRequest{UserID: "leader", WorkspacePath: teamTestRoot, PulseSession: conv.SessionID, RouteStepID: "parent", RouteID: "first-route", Message: "Continue the route work", InboxID: inbox})
	if e == nil || !strings.Contains(e.Error(), "Builder changed") {
		t.Fatalf("old route inbox survived reassignment: %v", e)
	}
}
