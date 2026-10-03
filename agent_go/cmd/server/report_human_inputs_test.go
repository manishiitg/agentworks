package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestNormalizeReportHumanInputSourceMergesLegacyAdvisorIdentity(t *testing.T) {
	for input, want := range map[string]string{
		"engineering-review": "technical_review",
		"Operations Review":  "technical_review",
		"strategic-review":   "strategic_review",
		"strategy-auditor":   "strategic_review",
		"Strategy Auditor":   "strategic_review",
		"goal-advisor":       "strategic_review",
		"unknown":            "pulse",
	} {
		if got := normalizeReportHumanInputSource(input); got != want {
			t.Fatalf("normalizeReportHumanInputSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBackgroundWorkflowChildCanCreateOnlyOwnHumanInputRequest(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	tools, executors, _ := createReportHumanInputTools()
	var createFound bool
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == "create_human_input_request" {
			createFound = true
		}
	}
	if !createFound {
		t.Fatal("create_human_input_request is not defined")
	}
	create, ok := executors["create_human_input_request"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("create_human_input_request executor has the wrong type")
	}

	childCreate := scopeDelegatedHumanInputExecutor("Workflow/owned", create)
	if _, err := childCreate(context.Background(), map[string]interface{}{
		"workspace_path": "Workflow/owned",
		"input_id":       "strategy-proposal-own-scope",
		"source":         "strategy_auditor",
		"question":       "Approve the scoped proposal?",
	}); err != nil {
		t.Fatalf("background child could not create its own decision: %v", err)
	}
	inputs, err := listReportHumanInputs(context.Background(), "Workflow/owned", "pending", "")
	if err != nil || len(inputs) != 1 {
		t.Fatalf("own workflow decision = %d inputs, err=%v; want one", len(inputs), err)
	}
	if _, err := childCreate(context.Background(), map[string]interface{}{
		"workspace_path": "Workflow/other",
		"question":       "This must not escape the child workflow.",
	}); err == nil || !strings.Contains(err.Error(), "only for Workflow/owned") {
		t.Fatalf("cross-workflow decision error = %v, want scope rejection", err)
	}
}

func TestReportHumanInputsUseWorkflowLocalDB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	dbPath := filepath.Join(root, "Workflow", "example", "db", "db.sqlite")

	inputs, err := listReportHumanInputs(ctx, workspacePath, "", "")
	if err != nil {
		t.Fatalf("list before create: %v", err)
	}
	if len(inputs) != 0 {
		t.Fatalf("list before create returned %d inputs, want 0", len(inputs))
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("read-only list created db unexpectedly: stat err=%v", err)
	}

	created, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID:       "choose-cadence",
		Source:        "strategic_review",
		Priority:      "high",
		Question:      "Should Goal Advisor run daily until recovery?",
		Context:       "The workflow missed the goal three times.",
		Evidence:      "builder/improve.html#latest",
		CreatedBy:     "test",
		AllowFreeText: true,
		Options: []ReportHumanInputOption{
			{ID: "daily", Title: "Run daily", Description: "Escalate until two clean runs."},
			{ID: "weekly", Title: "Keep weekly", Description: "Avoid extra cost for now."},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.WorkspacePath != workspacePath || created.Source != "strategic_review" || created.Status != "pending" {
		t.Fatalf("created input mismatch: %+v", created)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected workflow-local db at %s: %v", dbPath, err)
	}

	pending, err := listReportHumanInputs(ctx, workspacePath, "pending", "")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "choose-cadence" {
		t.Fatalf("pending mismatch: %+v", pending)
	}

	answered, err := answerReportHumanInput(ctx, workspacePath, "choose-cadence", ReportHumanInputAnswerRequest{
		SelectedOptionID: "daily",
		Note:             "Escalate for this week.",
		AnsweredBy:       "user",
	})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if answered.Status != "answered" || answered.SelectedOptionID != "daily" || !strings.Contains(answered.Note, "Escalate") {
		t.Fatalf("answered mismatch: %+v", answered)
	}

	contextBlock := formatAnsweredReportHumanInputsForAgent(ctx, workspacePath)
	if !strings.Contains(contextBlock, "choose-cadence") || !strings.Contains(contextBlock, "option=daily") || !strings.Contains(contextBlock, "The workflow missed the goal three times.") {
		t.Fatalf("answered context missing answer details:\n%s", contextBlock)
	}

	consumed, err := consumeReportHumanInput(ctx, workspacePath, "choose-cadence", ReportHumanInputConsumeRequest{
		OutcomeSummary: "Goal Advisor cadence kept daily until recovery.",
		ConsumedBy:     "pulse",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if consumed.Status != "consumed" || !strings.Contains(consumed.OutcomeSummary, "daily") {
		t.Fatalf("consumed mismatch: %+v", consumed)
	}
	if block := formatAnsweredReportHumanInputsForAgent(ctx, workspacePath); block != "" {
		t.Fatalf("consumed answer should not be re-injected, got:\n%s", block)
	}
}

// TestOpenReportHumanInputDBUsesWALMode locks in the fix for a real incident:
// get_pulse_state(view=module) hit persistent SQLITE_BUSY under concurrent
// load because this DB was opened in SQLite's default rollback-journal mode,
// where a writer's transaction exclusively locks the whole file against every
// concurrent reader. get_pulse_state(view=module) alone opens this DB 5
// separate times sequentially for one call (pulse_worklist.go), multiplying
// the collision window. WAL mode lets readers proceed without blocking on a
// writer. Also locks in that busy_timeout is DSN-embedded, not a one-time
// runtime PRAGMA, since this *sql.DB has no SetMaxOpenConns cap and a
// runtime PRAGMA on one pooled connection would silently not apply to a
// second connection the pool opens for a concurrent query.
func TestOpenReportHumanInputDBUsesWALMode(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())

	_, db, err := openReportHumanInputDB(ctx, "Workflow/wal-mode-check", true)
	if err != nil {
		t.Fatalf("openReportHumanInputDB: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want \"wal\" -- a reader/writer collision on this db.sqlite will hit SQLITE_BUSY under concurrent load without WAL mode", journalMode)
	}

	var busyTimeoutMs int
	if err := db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeoutMs); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeoutMs <= 0 {
		t.Fatalf("busy_timeout = %d, want > 0 -- a concurrent writer would return SQLITE_BUSY immediately instead of retrying", busyTimeoutMs)
	}
}

func TestReportHumanInputPersistsStructuredApplyContract(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	created, err := createReportHumanInput(ctx, "Workflow/decision-contract", ReportHumanInputCreateRequest{
		InputID: "technical-decision-prompt-contract", Source: "technical_review", Question: "Approve prompt cleanup?",
		ApplyContract: ReportHumanInputApplyContract{Mode: "targeted_fixer", IssueID: "PUL-55BE3473", ApprovedScope: "Extract one contract at a time.", PreRunChecks: []string{"validate_plan_change", "validate_plan_change"}, PostRunProof: "one producing run"},
	})
	if err != nil {
		t.Fatalf("create decision contract: %v", err)
	}
	if got := created.ApplyContract; got.Mode != "targeted_fixer" || got.IssueID != "PUL-55BE3473" || len(got.PreRunChecks) != 1 || got.FailurePolicy != "continue_unchanged" {
		t.Fatalf("created contract = %+v", got)
	}
	inputs, err := listReportHumanInputs(ctx, "Workflow/decision-contract", "pending", "")
	if err != nil || len(inputs) != 1 || inputs[0].ApplyContract.ApprovedScope != "Extract one contract at a time." {
		t.Fatalf("stored contract = %+v, err=%v", inputs, err)
	}
	if _, err := createReportHumanInput(ctx, "Workflow/decision-contract", ReportHumanInputCreateRequest{
		InputID: "bad-contract", Question: "Bad?", ApplyContract: ReportHumanInputApplyContract{Mode: "targeted_fixer"},
	}); err == nil || !strings.Contains(err.Error(), "approved_scope") {
		t.Fatalf("targeted fixer without scope error = %v", err)
	}
}

func TestReportHumanInputToolReportsExactInvalidApplyContractField(t *testing.T) {
	for _, tc := range []struct {
		name          string
		applyContract interface{}
		want          string
	}{
		{name: "whole contract is string", applyContract: `{}`, want: "apply_contract must be an object"},
		{name: "scope is object", applyContract: map[string]interface{}{"mode": "targeted_fixer", "approved_scope": map[string]interface{}{"objective": "repair route"}}, want: "apply_contract.approved_scope must be a string"},
		{name: "proof is array", applyContract: map[string]interface{}{"mode": "targeted_fixer", "approved_scope": "repair route", "post_run_proof": []interface{}{"fixture passes"}}, want: "apply_contract.post_run_proof must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{
				"workspace_path": "Workflow/upwork",
				"question":       "Apply the bounded plan repair?",
				"apply_contract": tc.applyContract,
			}
			_, err := reportHumanInputCreateRequestFromToolArgs(args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// Background Pulse fix runs never apply answered decisions (owner decision,
// PLAT-381): the tool that handed them approved decisions is gone.
func TestFixRunsCannotFetchApprovedDecisions(t *testing.T) {
	tools, executors, categories := createReportHumanInputTools()
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == "list_approved_fixer_decisions" {
			t.Fatal("list_approved_fixer_decisions is still offered")
		}
	}
	if _, ok := executors["list_approved_fixer_decisions"]; ok {
		t.Fatal("list_approved_fixer_decisions still has an executor")
	}
	if _, ok := categories["list_approved_fixer_decisions"]; ok {
		t.Fatal("list_approved_fixer_decisions still has a category")
	}
}

func TestPromptContractDecisionMigrationGetsTargetedFixerContract(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/migrated-prompt-contract"
	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID: "technical-decision-prompt-contract-consolidation-example", Source: "technical_review", Question: "Approve cleanup?",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "Workflow", "migrated-prompt-contract", "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE report_human_inputs SET apply_contract_json='{}'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	inputs, err := listReportHumanInputs(ctx, workspacePath, "pending", "")
	if err != nil || len(inputs) != 1 {
		t.Fatalf("migrated inputs=%+v err=%v", inputs, err)
	}
	if got := inputs[0].ApplyContract; got.Mode != "targeted_fixer" || got.FailurePolicy != "continue_unchanged" || !strings.Contains(got.ApprovedScope, "one versioned") {
		t.Fatalf("prompt-contract migration = %+v", got)
	}
}

func TestAnswerHumanInputRequestToolUsesValidatedDecisionLifecycle(t *testing.T) {
	ctx := humanAnswerFixture(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/a"
	inputID := "quality-scorecard-status"

	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID:  inputID,
		Source:   "pulse",
		Question: "Should the quality scorecard be turned back on?",
		Options: []ReportHumanInputOption{
			{ID: "turn_on", Title: "Turn it back on"},
			{ID: "keep_off", Title: "Keep it off"},
		},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tools, executors, categories := createReportHumanInputTools()
	found := false
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == "answer_human_input_request" {
			found = true
			break
		}
	}
	if !found || categories["answer_human_input_request"] != "human_tools" {
		t.Fatalf("answer tool is not registered in human_tools: found=%v category=%q", found, categories["answer_human_input_request"])
	}
	executor, ok := executors["answer_human_input_request"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("answer_human_input_request executor is missing or has the wrong type")
	}

	if _, err := executor(ctx, map[string]interface{}{
		"workspace_path":     workspacePath,
		"input_id":           inputID,
		"selected_option_id": "invented_option",
	}); err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Fatalf("invalid option error = %v, want validation failure", err)
	}
	pending, err := listReportHumanInputs(ctx, workspacePath, "pending", "")
	if err != nil || len(pending) != 1 {
		t.Fatalf("invalid tool call changed the pending decision: inputs=%+v err=%v", pending, err)
	}

	result, err := executor(ctx, map[string]interface{}{
		"workspace_path":     workspacePath,
		"input_id":           inputID,
		"selected_option_id": "turn_on",
	})
	if err != nil {
		t.Fatalf("answer tool: %v", err)
	}
	if !strings.Contains(result, `"status":"answered"`) || !strings.Contains(result, `"selected_option_id":"turn_on"`) {
		t.Fatalf("answer tool result does not report the transition: %s", result)
	}
	answered, err := listReportHumanInputs(ctx, workspacePath, "answered", "")
	if err != nil || len(answered) != 1 || answered[0].SelectedOptionID != "turn_on" {
		t.Fatalf("answered decision mismatch: inputs=%+v err=%v", answered, err)
	}
}

func TestGetHumanInputRequestToolReadsCanonicalDecision(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/read-decision"
	inputID := "strategy-proposal-read-me"
	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID:  inputID,
		Source:   "strategy_auditor",
		Question: "Apply the proposed strategy?",
		Context:  "Proposal: use the verified strategy on the next run.",
		Options:  []ReportHumanInputOption{{ID: "approve", Title: "Approve"}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := answerReportHumanInput(ctx, workspacePath, inputID, ReportHumanInputAnswerRequest{
		SelectedOptionID: "approve",
		AnsweredBy:       "user",
	}); err != nil {
		t.Fatalf("answer: %v", err)
	}

	tools, executors, categories := createReportHumanInputTools()
	found := false
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == "get_human_input_request" {
			found = true
			break
		}
	}
	if !found || categories["get_human_input_request"] != "human_tools" {
		t.Fatalf("get tool is not registered in human_tools: found=%v category=%q", found, categories["get_human_input_request"])
	}
	get, ok := executors["get_human_input_request"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("get_human_input_request executor is missing or has the wrong type")
	}
	result, err := get(ctx, map[string]interface{}{"workspace_path": workspacePath, "input_id": inputID})
	if err != nil {
		t.Fatalf("get tool: %v", err)
	}
	for _, want := range []string{`"status":"read"`, `"selected_option_id":"approve"`, "use the verified strategy"} {
		if !strings.Contains(result, want) {
			t.Fatalf("get tool result missing %q: %s", want, result)
		}
	}
}

func TestAnsweredGoalAdvisorPlanProposalCarriesContext(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/proposal"
	proposalContext := "Proposal: add a validation step before delivery. Exact edits: add regular step validate-offer after draft-offer; update delivery dependency. Rationale: two clean runs still sent weak offers. Expected impact: fewer off-goal deliveries. Risk: extra runtime. Evidence: runs/iteration-0/group-1/logs/deliver-offer/execution/execution-attempt-1-iteration-0.json"

	_, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID:  "plan-proposal-validate-offer",
		Source:   "goal_advisor",
		Priority: "high",
		Question: "Approve adding an offer-validation step?",
		Context:  proposalContext,
		Evidence: "runs/iteration-0/group-1/logs/deliver-offer/execution/execution-attempt-1-iteration-0.json",
		Options: []ReportHumanInputOption{
			{ID: "approve", Title: "Approve", Description: "Apply this plan change in the next Pulse pass."},
			{ID: "reject", Title: "Reject", Description: "Keep the current plan."},
			{ID: "defer", Title: "Defer", Description: "Wait for more evidence."},
		},
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	_, err = answerReportHumanInput(ctx, workspacePath, "plan-proposal-validate-offer", ReportHumanInputAnswerRequest{
		SelectedOptionID: "approve",
		AnsweredBy:       "user",
	})
	if err != nil {
		t.Fatalf("answer proposal: %v", err)
	}

	contextBlock := formatAnsweredReportHumanInputsForAgent(ctx, workspacePath)
	for _, want := range []string{
		"plan-proposal-validate-offer",
		"option=approve",
		"add regular step validate-offer",
		"apply it only with normal plan modification/config/measurement/report tools",
		"mark_human_input_consumed",
	} {
		if !strings.Contains(contextBlock, want) {
			t.Fatalf("answered proposal context missing %q:\n%s", want, contextBlock)
		}
	}
}

func TestReportHumanInputAllowsFreeTextInsteadOfOption(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/free-text-choice"

	_, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID:       "custom-answer",
		Source:        "pulse",
		Question:      "Which backup policy should be used?",
		AllowFreeText: true,
		Options: []ReportHumanInputOption{
			{ID: "strict", Title: "Strict", Description: "Require secret migration first."},
			{ID: "partial", Title: "Partial", Description: "Continue excluding the config file."},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := answerReportHumanInput(ctx, workspacePath, "custom-answer", ReportHumanInputAnswerRequest{}); err == nil {
		t.Fatal("empty answer should be rejected")
	}

	answered, err := answerReportHumanInput(ctx, workspacePath, "custom-answer", ReportHumanInputAnswerRequest{
		Note:       "Keep the full backup and do not treat the portal password as a workflow secret.",
		AnsweredBy: "user",
	})
	if err != nil {
		t.Fatalf("answer with free text: %v", err)
	}
	if answered.Status != "answered" || answered.SelectedOptionID != "" || !strings.Contains(answered.Note, "full backup") {
		t.Fatalf("note-only answer mismatch: %+v", answered)
	}
}

func TestReportHumanInputRejectsEscapingWorkspacePath(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	if _, err := createReportHumanInput(context.Background(), "../outside", ReportHumanInputCreateRequest{
		Question: "Should this be rejected?",
	}); err == nil {
		t.Fatal("expected path traversal workspace_path to be rejected")
	}
}

func TestReportHumanInputAnswerPersistsAttributionAndEvent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/attribution"
	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID: "approve", Question: "Approve?", AllowFreeText: true,
		CreatedBy: "pulse", CreatedByKind: "agent", CreatedVia: "agent_tool", SessionID: "pulse-run-1",
	}); err != nil {
		t.Fatal(err)
	}
	answered, err := answerReportHumanInput(ctx, workspacePath, "approve", ReportHumanInputAnswerRequest{
		Note: "yes", AnsweredBy: "operator-1", AnsweredByKind: "human_ui", AnsweredVia: "report_ui", SessionID: "chat-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if answered.AnsweredBy != "operator-1" || answered.AnsweredByKind != "human_ui" || answered.AnsweredVia != "report_ui" || answered.AnsweredSessionID != "chat-1" {
		t.Fatalf("answer attribution mismatch: %+v", answered)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "Workflow", "attribution", "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var eventType, actorID, actorKind, channel, sessionID, details string
	if err := db.QueryRow(`SELECT event_type, actor_id, actor_kind, channel, session_id, details
		FROM report_human_input_events WHERE input_id='approve' ORDER BY id DESC LIMIT 1`).Scan(
		&eventType, &actorID, &actorKind, &channel, &sessionID, &details); err != nil {
		t.Fatal(err)
	}
	if eventType != "answered" || actorID != "operator-1" || actorKind != "human_ui" || channel != "report_ui" || sessionID != "chat-1" {
		t.Fatalf("event attribution = %q %q %q %q %q", eventType, actorID, actorKind, channel, sessionID)
	}
	if strings.Contains(details, "yes") || !strings.Contains(details, `"note_present":true`) {
		t.Fatalf("event must record answer shape without duplicating free text: %s", details)
	}
}

func TestReportHumanInputHTTPAnswerIgnoresForgedActor(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/http-attribution"
	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID: "approve", Question: "Approve?", AllowFreeText: true,
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ReportHumanInputAnswerRequest{
		WorkspacePath: workspacePath, Note: "yes", AnsweredBy: "forged-user",
	})
	req := httptest.NewRequest("POST", "/api/report-human-inputs/approve/answer", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentWorks-Session-ID", "ui-session-1")
	req = mux.SetURLVars(req, map[string]string{"input_id": "approve"})
	recorder := httptest.NewRecorder()
	api := &StreamingAPI{}
	api.handleAnswerReportHumanInput(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("answer status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	inputs, err := listReportHumanInputs(ctx, workspacePath, "answered", "")
	if err != nil || len(inputs) != 1 {
		t.Fatalf("answered inputs=%+v err=%v", inputs, err)
	}
	if inputs[0].AnsweredBy == "forged-user" || inputs[0].AnsweredByKind != "human_ui" || inputs[0].AnsweredVia != "report_ui" || inputs[0].AnsweredSessionID != "ui-session-1" {
		t.Fatalf("HTTP attribution was not server-derived: %+v", inputs[0])
	}
}
