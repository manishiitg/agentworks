package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// suggest_crew_change: another user of a Crew suggests a change over MCP; it
// lands in the owner's Suggestions view. The owner cannot suggest to their
// own Crew, and the tool needs crews:run.
func TestExternalSuggestCrewChangeReachesTheOwner(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	runner := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:run"}, AllCrews: true}}

	code, out := externalCrewRequest(t, env, runner, "suggest_crew_change", map[string]any{"crew_id": "gamma", "suggestion": "Also post the summary to #qa", "about": "daily_report"})
	if code != 200 || out["status"] != "submitted_for_owner_review" {
		t.Fatalf("suggest to another user's Crew = %d %v", code, out)
	}
	otherCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	inputs, err := listReportHumanInputs(otherCtx, linkGammaPath, "pending", "user_suggestion")
	if err != nil || len(inputs) != 1 || inputs[0].CreatedBy != "owner" || inputs[0].Evidence != "daily_report" {
		t.Fatalf("owner's Suggestions view: %+v %v", inputs, err)
	}

	if code, _ := externalCrewRequest(t, env, runner, "suggest_crew_change", map[string]any{"crew_id": "beta", "suggestion": "x"}); code != 400 {
		t.Fatalf("the owner must change their own Crew directly, got %d", code)
	}
	readOnly := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if externalTokenAllows(readOnly, externalTool{Name: "suggest_crew_change"}) {
		t.Fatal("suggest_crew_change needs crews:run")
	}
	if !externalTokenAllows(runner, externalTool{Name: "suggest_crew_change"}) {
		t.Fatal("crews:run allows suggest_crew_change")
	}
}

// suggest_workflow_change goes through the same checks as the Run-mode
// tool: workflow access required, stored for the owner's review.
func TestSubmitWorkflowSuggestionForExternalCallers(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true},{"id":"reader","username":"reader","can_edit":false},{"id":"stranger","username":"stranger","can_edit":true}]}`)
	server, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]}}`
	ctx := context.Background()
	if _, err := submitWorkflowSuggestion(ctx, &UserClaims{UserID: "stranger"}, "Workflow/test", "", "x", "", ""); err == nil {
		t.Fatal("a user without access suggested")
	}
	if _, err := submitWorkflowSuggestion(ctx, &UserClaims{UserID: "reader"}, "Workflow/test", "", "Add weekly totals", "finance asks", "report"); err != nil {
		t.Fatal(err)
	}
	inputs, err := listReportHumanInputs(ctx, "Workflow/test", "pending", "user_suggestion")
	if err != nil || len(inputs) != 1 || inputs[0].CreatedBy != "reader" || inputs[0].ApplyContract.Mode != "no_change" {
		t.Fatalf("stored suggestion: %+v %v", inputs, err)
	}
	if externalTokenAllows(&UserClaims{UserID: "reader", AccessToken: &accesstokens.Token{Scopes: []string{"workflows:read"}}}, externalTool{Name: "suggest_workflow_change"}) == false {
		t.Fatal("a read token may suggest (suggestions are open to read-only users)")
	}
}
