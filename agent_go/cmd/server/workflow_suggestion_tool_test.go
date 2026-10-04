package server

import (
	"context"
	"testing"
)

func TestWorkflowSuggestionReaderSubmissionAndOwnerReview(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_edit":true},{"id":"reader","username":"reader","can_edit":false},{"id":"other","username":"other","can_edit":true}]}`)
	server, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]}}`
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerWorkflowUIForCaller(reg, "workflow-builder", "chat", "Workflow/test", QueryRequest{}, true); err != nil {
		t.Fatal(err)
	}
	tool, found := reg.tools["submit_workflow_suggestion"]
	if !found {
		t.Fatal("Run mode must expose suggestion tool")
	}
	ctxFor := func(id string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: id})
	}
	for _, ctx := range []context.Context{context.Background(), ctxFor("other")} {
		if _, err := tool.exec(ctx, map[string]interface{}{"suggestion": "Improve report"}); err == nil {
			t.Fatal("unauthorized submission accepted")
		}
	}
	if _, err := tool.exec(ctxFor("reader"), map[string]interface{}{"suggestion": " "}); err == nil {
		t.Fatal("empty suggestion accepted")
	}
	if _, err := tool.exec(ctxFor("reader"), map[string]interface{}{"suggestion": "Improve report", "reason": "Include totals", "step_id": "report"}); err != nil {
		t.Fatal(err)
	}
	inputs, err := listReportHumanInputs(context.Background(), "Workflow/test", "pending", "user_suggestion")
	if err != nil || len(inputs) != 1 {
		t.Fatalf("durable queue: %v %v", inputs, err)
	}
	input := inputs[0]
	if input.CreatedBy != "reader" || input.ApplyContract.Mode != "no_change" || input.Evidence != "report" {
		t.Fatalf("unsafe suggestion: %+v", input)
	}
	answer := ReportHumanInputAnswerRequest{SelectedOptionID: "approve", AnsweredBy: "owner", AnsweredByKind: "human_ui"}
	if _, err := answerReportHumanInput(ctxFor("reader"), "Workflow/test", input.ID, answer); err == nil {
		t.Fatal("reader approved suggestion")
	}
	if _, err := dismissReportHumanInput(ctxFor("reader"), "Workflow/test", input.ID, ReportHumanInputAnswerRequest{}); err == nil {
		t.Fatal("reader dismissed suggestion")
	}
	if _, err := createReportHumanInput(ctxFor("reader"), "Workflow/test", ReportHumanInputCreateRequest{InputID: input.ID, Question: "Replace suggestion"}); err == nil {
		t.Fatal("suggestion overwritten")
	}
	approved, err := answerReportHumanInput(ctxFor("owner"), "Workflow/test", input.ID, answer)
	if err != nil || approved.Status != "answered" {
		t.Fatalf("owner review: %+v %v", approved, err)
	}
	if _, err := consumeReportHumanInput(ctxFor("reader"), "Workflow/test", input.ID, ReportHumanInputConsumeRequest{OutcomeSummary: "Implemented"}); err == nil {
		t.Fatal("reader consumed suggestion")
	}
	if mode := scheduledDecisionApplyMode(*approved); mode != "legacy_manual" {
		t.Fatalf("an accepted suggestion is review, not edit authority; apply mode = %q", mode)
	}
	again, err := listReportHumanInputs(context.Background(), "Workflow/test", "answered", "user_suggestion")
	if err != nil || len(again) != 1 || again[0].ApplyContract.Mode != "no_change" {
		t.Fatal("review not persisted safely", err)
	}
}

func TestWorkflowSuggestionBotRouteScope(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	server, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","created_by":"owner"}`
	docs.files["Workflow/other/workflow.json"] = `{"id":"wf_other","created_by":"owner"}`
	claims := &UserClaims{UserID: "bot", Provider: "bot_route", BotRouteWorkflowID: "wf_test", BotRouteWorkspacePath: "Workflow/test", BotRouteGrant: "run", ExecutionPrincipal: &ExecutionPrincipal{AuditActor: "slack-user"}}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerWorkflowUIForCaller(reg, "workflow-builder", "bot-chat", "Workflow/test", QueryRequest{BotPlatform: "slack"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["submit_workflow_suggestion"].exec(ctx, map[string]interface{}{"suggestion": "Include chart"}); err != nil {
		t.Fatal(err)
	}
	inputs, err := listReportHumanInputs(context.Background(), "Workflow/test", "pending", "user_suggestion")
	if err != nil || len(inputs) != 1 || inputs[0].CreatedBy != "slack-user" {
		t.Fatalf("bot attribution: %+v %v", inputs, err)
	}
	if err := requireSuggestionOwner(ctx, "Workflow/test", &inputs[0]); err == nil {
		t.Fatal("bot can approve")
	}
	other := &recordingRegistrar{}
	if err := api.registerWorkflowSuggestionTool(other, "bot-chat", "Workflow/other"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.tools["submit_workflow_suggestion"].exec(ctx, map[string]interface{}{"suggestion": "Other workflow"}); err == nil {
		t.Fatal("bot crossed scope")
	}
}
