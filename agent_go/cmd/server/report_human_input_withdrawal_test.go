package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/mcpagent/executor"
)

// PLAT-814: drive the real Builder tools, SQLite upgrade, completed-work record
// and owner list handler. Withdrawal retires a unique proposal, not an answer.
func TestBuilderWithdrawsObsoleteProposalWithoutOwnerAnswer(t *testing.T) {
	ctx := humanAnswerFixture(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	const ws = "Workflow/a"
	_, executors, categories := createReportHumanInputTools()
	call := func(name string, args map[string]interface{}) (string, error) {
		return executors[name].(func(context.Context, map[string]interface{}) (string, error))(ctx, args)
	}
	create := func(id string) {
		t.Helper()
		if _, err := call("create_human_input_request", map[string]interface{}{
			"workspace_path": ws, "input_id": id, "source": "strategic_review", "question": "Apply the measurement repair?",
			"options":        []interface{}{map[string]interface{}{"id": "approve", "title": "Approve"}},
			"apply_contract": map[string]interface{}{"mode": "targeted_fixer", "approved_scope": "Repair the measurement recording fields."},
		}); err != nil {
			t.Fatal(err)
		}
	}
	create("obsolete-repair")
	_, db, err := openReportHumanInputDB(ctx, ws, false)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-change schema with the real request/history already in it.
	if _, err := db.Exec(`ALTER TABLE report_human_inputs DROP COLUMN withdrawal_json`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := listReportHumanInputs(ctx, ws, "pending", "")
	if err != nil || len(before) != 1 || before[0].Withdrawal != nil {
		t.Fatalf("existing request lost in schema upgrade: %+v %v", before, err)
	}
	work, err := recordPulseGoalWork(ctx, ws, PulseGoalWorkItem{Title: "Repair measurement recording", Status: "done", ActionTaken: "Builder repaired the recording fields under current authority.", Effect: "unclear", DecisionID: "obsolete-repair"}, "pulse-check")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordPulseRecommendationFromToolArgs(ctx, map[string]interface{}{"workspace_path": ws, "input_id": "obsolete-repair", "option_id": "approve", "why": "The repair restores meaningful measurements."}); err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{"workspace_path": ws, "input_id": "obsolete-repair", "reason": "The bounded repair is complete under current authority; goal impact is still unclear.", "evidence": []interface{}{work.ID, "workflow.json pulse.autonomy.change", "pulse/work/repair-receipt.json"}}
	withdraw := executors["withdraw_human_input_request"].(func(context.Context, map[string]interface{}) (string, error))
	if categories["withdraw_human_input_request"] != "human_tools" || !agentworksproduct.ChatAllowsTool("builder", "withdraw_human_input_request") || agentworksproduct.ChatAllowsTool("run", "withdraw_human_input_request") {
		t.Fatal("withdrawal must be admitted only to Builder")
	}
	for _, session := range []string{"", "tool-a", "sched_a_1", "unknown-chat"} {
		if _, err := withdraw(executor.WithSessionID(context.Background(), session), args); err == nil {
			t.Fatalf("non-Builder session %q withdrew a request", session)
		}
	}
	other := map[string]interface{}{"workspace_path": "Workflow/b", "input_id": "obsolete-repair", "reason": args["reason"], "evidence": args["evidence"]}
	if _, err := withdraw(ctx, other); err == nil {
		t.Fatal("withdrew another workflow's request")
	}
	noProof := map[string]interface{}{"workspace_path": ws, "input_id": "obsolete-repair", "reason": args["reason"]}
	if _, err := withdraw(ctx, noProof); err == nil {
		t.Fatal("accepted withdrawal without evidence")
	}
	result, err := withdraw(ctx, args)
	if err != nil || !strings.Contains(result, `"status":"withdrawn"`) {
		t.Fatalf("Builder could not retire the unique obsolete proposal: %s %v", result, err)
	}
	_, db, err = openReportHumanInputDB(ctx, ws, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := getReportHumanInputByID(ctx, db, ws, "obsolete-repair")
	if err != nil || got == nil || got.Withdrawal == nil || got.Withdrawal.Reason != args["reason"] || got.Withdrawal.ActorID == "" || got.Withdrawal.SessionID != executor.SessionIDFromContext(ctx) || got.Withdrawal.WithdrawnAt == "" {
		t.Fatalf("missing withdrawal audit: %+v %v", got, err)
	}
	if got.SelectedOptionID != "" || got.AnsweredBy != "" || got.AnsweredAt != "" || got.ConsumedAt != "" || got.DismissedAt != "" || got.Note != "" || got.OutcomeSummary != "" || got.Question != before[0].Question || !reflect.DeepEqual(got.Options, before[0].Options) || !reflect.DeepEqual(got.ApplyContract, before[0].ApplyContract) {
		t.Fatalf("fabricated an owner answer or changed proposal semantics: %+v", got)
	}
	var audit string
	if err := db.QueryRow(`SELECT details FROM report_human_input_events WHERE input_id='obsolete-repair' AND event_type='withdrawn'`).Scan(&audit); err != nil || !strings.Contains(audit, work.ID) {
		t.Fatalf("missing immutable evidence event: %s %v", audit, err)
	}
	log, err := listPulseDecisionLog(ctx, ws, 10)
	if err != nil || len(log) != 1 || log[0].DecisionStatus != "withdrawn" || log[0].OwnerResponse != "" || log[0].OwnerAnswer != "" {
		t.Fatalf("recommendation history fabricated owner approval: %+v %v", log, err)
	}
	for _, status := range []string{"pending", "answered", ""} {
		rec := httptest.NewRecorder()
		pulsePlatformAPI.handleListReportHumanInputs(rec, httptest.NewRequest("GET", "/?workspace_path="+ws+"&status="+status, nil).WithContext(ctx))
		var body struct {
			Inputs []ReportHumanInput `json:"inputs"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || (status == "" && (len(body.Inputs) != 1 || body.Inputs[0].Status != "withdrawn")) || (status != "" && len(body.Inputs) != 0) {
			t.Fatalf("owner list lost history or still needs an answer: %s %v", rec.Body.String(), err)
		}
	}
	if _, err := withdraw(ctx, args); err == nil {
		t.Fatal("repeated withdrawal should not create another audit event")
	}
	if _, err := answerReportHumanInput(ctx, ws, got.ID, ReportHumanInputAnswerRequest{SelectedOptionID: "approve"}); err == nil {
		t.Fatal("an answer resurrected a withdrawn request")
	}
	if _, err := dismissReportHumanInput(ctx, ws, got.ID, ReportHumanInputAnswerRequest{}); err == nil {
		t.Fatal("dismissal overwrote withdrawal provenance")
	}
	if _, err := consumeReportHumanInput(ctx, ws, got.ID, ReportHumanInputConsumeRequest{OutcomeSummary: "Applied"}); err == nil {
		t.Fatal("withdrawal was treated as approval to apply")
	}
	create("answered-request")
	if _, err := answerReportHumanInput(ctx, ws, "answered-request", ReportHumanInputAnswerRequest{SelectedOptionID: "approve"}); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []ReportHumanInputCreateRequest{
		{InputID: "human-request", Source: "pulse", Question: "A human question", CreatedByKind: "human_ui"},
		{InputID: "unknown-origin", Source: "pulse", Question: "A legacy question"},
		{InputID: "suggestion", Source: "user_suggestion", Question: "An owner suggestion", CreatedVia: "suggestion_tool", CreatedByKind: "human_ui"},
	} {
		if _, err := createReportHumanInput(ctx, ws, fixture); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"answered-request", "human-request", "unknown-origin", "suggestion"} {
		args["input_id"] = id
		if _, err := withdraw(ctx, args); err == nil {
			t.Fatalf("withdrew protected request %s", id)
		}
	}
}
