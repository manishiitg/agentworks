package server

import (
	"context"
	"testing"
)

// TestConsumedHumanInputRejectsLateAnswer pins the PLAT-073 cluster I fix
// (cf457bdd/7602e2ac): answerReportHumanInput's UPDATE previously had no
// status guard, so a late/duplicate answer call arriving after the request
// was already consumed would silently revert status back to 'answered' while
// leaving the prior consumed_at/outcome_summary in place — a row that reads
// as simultaneously answered and consumed, which is exactly what loop_closure
// observed live. The in-process mutex only serializes goroutines in this
// server; it does not protect against a second process (the documented
// chat/schedule concurrency contract) racing the same SQLite row.
func TestConsumedHumanInputRejectsLateAnswer(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/concurrent-writer"

	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID: "decision-1", Question: "Proceed?", AllowFreeText: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := answerReportHumanInput(ctx, workspacePath, "decision-1", ReportHumanInputAnswerRequest{
		Note: "yes", AnsweredBy: "operator-1",
	}); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	consumed, err := consumeReportHumanInput(ctx, workspacePath, "decision-1", ReportHumanInputConsumeRequest{
		OutcomeSummary: "Applied to the plan.", OutcomeEvidence: "Checked the saved fixture plan.", ConsumedBy: "pulse-fixer",
	})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if consumed.Status != "consumed" || consumed.ConsumedAt == "" || consumed.OutcomeSummary == "" {
		t.Fatalf("expected a fully consumed row, got %+v", consumed)
	}

	// A late/duplicate answer arriving after consumption must be rejected,
	// not silently accepted and reverted back to 'answered'.
	if _, err := answerReportHumanInput(ctx, workspacePath, "decision-1", ReportHumanInputAnswerRequest{
		Note: "changed my mind", AnsweredBy: "operator-2",
	}); err == nil {
		t.Fatal("expected the late answer to be rejected once the row was already consumed")
	}

	// The row must still read as consumed — the exact invariant loop_closure
	// depends on (a status='answered' row must never also carry consumed_at).
	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, false)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	after, err := getReportHumanInputByID(ctx, db, normalized, "decision-1")
	if err != nil {
		t.Fatalf("reload after rejected late answer: %v", err)
	}
	if after.Status != "consumed" || after.ConsumedAt == "" || after.OutcomeSummary == "" {
		t.Fatalf("consumed row must not have been reverted by the rejected late answer: %+v", after)
	}
}

// TestConsumedHumanInputRejectsLateDismiss mirrors the above for dismiss.
func TestConsumedHumanInputRejectsLateDismiss(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/concurrent-writer-dismiss"

	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID: "decision-2", Question: "Proceed?", AllowFreeText: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := answerReportHumanInput(ctx, workspacePath, "decision-2", ReportHumanInputAnswerRequest{
		Note: "yes", AnsweredBy: "operator-1",
	}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if _, err := consumeReportHumanInput(ctx, workspacePath, "decision-2", ReportHumanInputConsumeRequest{
		OutcomeSummary: "Applied.", OutcomeEvidence: "Checked the fixture result.", ConsumedBy: "pulse-fixer",
	}); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if _, err := dismissReportHumanInput(ctx, workspacePath, "decision-2", ReportHumanInputAnswerRequest{
		AnsweredBy: "operator-2",
	}); err == nil {
		t.Fatal("expected the late dismiss to be rejected once the row was already consumed")
	}
}

// TestDoubleConsumeRejectsSecondCall proves the pre-existing WHERE guard in
// consumeReportHumanInput (which never checked RowsAffected) now surfaces a
// concurrent-writer error instead of silently reporting success for a write
// that changed nothing.
func TestDoubleConsumeRejectsSecondCall(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/double-consume"

	if _, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		InputID: "decision-3", Question: "Proceed?", AllowFreeText: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := answerReportHumanInput(ctx, workspacePath, "decision-3", ReportHumanInputAnswerRequest{
		Note: "yes", AnsweredBy: "operator-1",
	}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if _, err := consumeReportHumanInput(ctx, workspacePath, "decision-3", ReportHumanInputConsumeRequest{
		OutcomeSummary: "First consumption.", OutcomeEvidence: "Checked the fixture result.", ConsumedBy: "pulse-fixer-1",
	}); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := consumeReportHumanInput(ctx, workspacePath, "decision-3", ReportHumanInputConsumeRequest{
		OutcomeSummary: "Second consumption.", OutcomeEvidence: "Checked the fixture result.", ConsumedBy: "pulse-fixer-2",
	}); err == nil {
		t.Fatal("expected the second consume call to be rejected rather than silently reporting success")
	}
}
