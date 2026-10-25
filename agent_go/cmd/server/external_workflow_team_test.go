package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Drive the real MCP transport, account grants, website team handler and
// native disk writer. Team administration must never become a model turn.
func TestExternalWorkflowTeamManagementOverMCP(t *testing.T) {
	f := newBuilderOperationFixture(t)
	t.Setenv("WORKSPACE_FILE_STATE_DIR", t.TempDir())
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"id":"parent","type":"agent","predefined_routes":[{"route_id":"first","route_name":"First","sub_agent_step":{"id":"first-step","type":"scripted"}},{"route_id":"second","route_name":"Second","sub_agent_step":{"id":"second-step","type":"scripted"}}]}]}`)
	m := NewWorkflowManifest("Team example")
	m.ID, m.CreatedBy = "invoices", "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Editors: []string{"outsider"}, Readers: []string{"reader"}}
	m.Schedules = []WorkflowSchedule{{ID: "route-clock", Name: "Reviewed clock", CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: false,
		GroupNames: []string{"default"}, RouteSelections: map[string]string{"parent": "first"}}}
	b, _ := json.Marshal(m)
	f.write(t, "Workflow/invoices/workflow.json", string(b))
	ownerToken, owner := f.issue(t, "owner")
	contributorToken, contributor := f.issue(t, "outsider")
	claims, err := accessTokenClaims(ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	server := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, t.Context(), server.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	spec := callRemoteTool(t, t.Context(), cli, "get_api_spec", map[string]any{"names": []any{"workflow_team"}})
	requireRemoteSuccess(t, spec, "team discovery")
	get := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": "workflow_team", "arguments": map[string]any{"workflow_id": "invoices", "action": "get"}})
	requireRemoteSuccess(t, get, "team read")
	if text := marshalStructured(t, get); !strings.Contains(text, "eligible_members") || !strings.Contains(text, "first") {
		t.Fatalf("missing member/route discovery: %s", text)
	}
	policy := &WorkflowTeam{LeaderID: "owner", Contributors: []WorkflowContributor{
		{UserID: "outsider", Run: true, ManageSchedules: true, Approve: true, ApprovalIDs: []string{"decision-1"}, Backup: true, Notify: true}, {UserID: "reader"}},
		Routes: []WorkflowRouteAssignment{{StepID: "parent", RouteID: "first", ContributorID: "outsider"}}, ReadOnlyStepIDs: []string{"second-step"}}
	update := func(token string, team *WorkflowTeam, revision, want int) {
		t.Helper()
		f.call(t, token, "workflow_team", map[string]any{"action": "update", "team": team, "expected_revision": revision, "confirm": "invoices"}, want)
	}
	update(contributor, policy, 0, 403) // An editor cannot enable team mode.
	update(owner, policy, 1, 409)       // Initial enablement requires revision zero.
	// Without confirm, adding members and naming a Leader changes nothing and says exactly what would change.
	pending := externalTestBody(t, f.call(t, owner, "workflow_team", map[string]any{"action": "update", "team": policy, "expected_revision": 0}, 200), 200)
	changes, _ := pending["team_changes"].(map[string]any)
	if pending["confirmation_required"] != true || pending["applied"] != false || changes == nil || len(changes["added"].([]any)) != 2 || changes["leader_changed"] != true {
		t.Fatalf("unconfirmed enablement was not held back with a precise list: %#v", pending)
	}
	var untouched WorkflowManifest
	if err := json.Unmarshal([]byte(f.read(t, "Workflow/invoices/workflow.json")), &untouched); err != nil || untouched.Team != nil {
		t.Fatalf("an unconfirmed update changed the team: %v %+v", err, untouched.Team)
	}
	result := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": "workflow_team", "arguments": map[string]any{
		"workflow_id": "invoices", "action": "update", "team": policy, "expected_revision": 0, "confirm": "invoices"}})
	requireRemoteSuccess(t, result, "team enablement")
	read := func() *WorkflowManifest {
		t.Helper()
		var saved WorkflowManifest
		if err := json.Unmarshal([]byte(f.read(t, "Workflow/invoices/workflow.json")), &saved); err != nil {
			t.Fatal(err)
		}
		return &saved
	}
	saved := read()
	if saved.Team == nil || saved.Team.Revision != 1 || saved.Team.LeaderID != "owner" || !saved.Team.Contributors[0].ManageSchedules ||
		saved.Team.Contributors[0].Publish || len(saved.Team.Contributors[0].ApprovalIDs) != 1 || len(saved.Team.Routes) != 1 || len(saved.Team.ReadOnlyStepIDs) != 1 {
		t.Fatalf("team policy was not saved exactly: %+v", saved.Team)
	}
	if err := validateTeamScheduleFire(t.Context(), "Workflow/invoices", saved.Schedules[0]); err == nil {
		t.Fatal("team enablement silently authorized old schedules")
	}
	reauthorize := func(token string, revision, want int) {
		body := externalTestBody(t, f.call(t, token, "workflow_team", map[string]any{"action": "get"}, 200), 200)
		snapshots, ok := body["schedule_revisions"].(map[string]any)
		if !ok || snapshots["route-clock"] == nil {
			t.Fatalf("missing schedule revision: %#v", body)
		}
		f.call(t, token, "workflow_team", map[string]any{"action": "reauthorize_schedule", "schedule_id": "route-clock", "expected_revision": revision, "expected_schedule_revision": snapshots["route-clock"]}, want)
	}
	reauthorize(contributor, 0, 409)
	f.call(t, contributor, "workflow_team", map[string]any{"action": "reauthorize_schedule", "schedule_id": "route-clock", "expected_revision": 1, "expected_schedule_revision": "stale"}, 409)
	reauthorize(contributor, 1, 200)
	saved = read()
	if saved.Schedules[0].TeamAuthorizedBy != "outsider" || saved.Schedules[0].TeamPolicyRevision != 1 || saved.Schedules[0].Enabled || saved.Schedules[0].CronExpression != "0 9 * * *" {
		t.Fatalf("reauthorization changed the schedule: %+v", saved.Schedules[0])
	}
	if err := validateTeamScheduleFire(t.Context(), "Workflow/invoices", saved.Schedules[0]); err != nil {
		t.Fatal(err)
	}
	update(contributor, policy, 1, 403) // Route editor cannot grant themselves more rights.
	update(owner, policy, 0, 409)
	bad := *policy
	bad.Routes = []WorkflowRouteAssignment{{StepID: "parent", RouteID: "unknown", ContributorID: "outsider"}}
	update(owner, &bad, 1, 400)
	bad = *policy
	bad.Contributors = append(append([]WorkflowContributor{}, policy.Contributors...), WorkflowContributor{UserID: "owner"})
	update(owner, &bad, 1, 400)
	// A Run person sees the policy, not the management member directory.
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	issue := func(user string, scopes []string, ids []string, guard *wf.FolderGuard) string {
		t.Helper()
		_, raw, err := store.Issue(t.Context(), accesstokens.Token{Name: "Team connection", UserID: user, Scopes: scopes, WorkflowIDs: ids, FileGuard: guard, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	readOnly := issue("reader", []string{"workflows:read"}, []string{"invoices"}, nil)
	w := f.call(t, readOnly, "workflow_team", map[string]any{"action": "get"}, 200)
	if _, exposed := externalTestBody(t, w, 200)["eligible_members"]; exposed {
		t.Fatal("read-only consent exposed the management directory")
	}
	update(readOnly, policy, 1, 403)
	// Personal MCP follows the person's current website role. Old picked
	// scopes, folder guards and workflow lists do not narrow an owner; scoped
	// machine/project connections have their own contract regressions.
	guarded := issue("owner", builderConsentScopes, []string{"invoices"}, &wf.FolderGuard{ReadPaths: []string{"code"}, WritePaths: []string{"code"}})
	f.call(t, guarded, "workflow_team", map[string]any{"action": "get"}, 200)
	foreign := issue("owner", builderConsentScopes, []string{"secret"}, nil)
	f.call(t, foreign, "workflow_team", map[string]any{"action": "get"}, 200)
	// PLAT-825-112: the 23 October co-owner model (61ce3d2351) keeps
	// existing owners when the team Leader changes; it is not an ownership transfer.
	// Legacy connection issuance replaces the owner's earlier grant; renew it
	// after the current-role connection checks above.
	_, owner = f.issue(t, "owner")
	transfer := *policy
	transfer.LeaderID = "outsider"
	transfer.Contributors = []WorkflowContributor{{UserID: "owner"}, {UserID: "reader"}}
	transfer.Routes = []WorkflowRouteAssignment{}
	update(owner, &transfer, 1, 200)
	update(owner, &transfer, 2, 200)
	update(contributor, &transfer, 3, 200)
	if saved := read(); saved.Team.LeaderID != "outsider" || saved.Team.Revision != 4 || len(saved.Access.Owners) != 2 || !containsID(saved.Access.Owners, "owner") || !containsID(saved.Access.Owners, "outsider") {
		t.Fatalf("co-owners were lost while changing the Leader: %+v", saved)
	}
	// Removing a listed owner is different from changing the team Leader.
	// Retain them as a Contributor so the refusal must be management authority,
	// rather than losing discovery of the workflow altogether.
	removed := read()
	removed.Access.Owners = []string{"outsider"}
	body, err := json.Marshal(removed)
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, "Workflow/invoices/workflow.json", string(body))
	invalidateWorkflowManifestIndex()
	update(owner, &transfer, 4, 403)
	update(contributor, &transfer, 4, 200)
	if err := store.Revoke(t.Context(), contributorToken.ID, "outsider", time.Now()); err != nil {
		t.Fatal(err)
	}
	update(contributor, &transfer, 5, 401)
}
