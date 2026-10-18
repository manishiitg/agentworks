package step_based_workflow

import (
	"context"
	"strings"
	"testing"
)

func TestTypedReviewDecisionRequiredCreatesNoOrphanFinding(t *testing.T) {
	ws := concernsWorkspace(t)
	ctx := context.Background()
	input := PulseReviewFindingInput{
		Concern: "the fixed pipeline has no runtime orchestration choice", Module: "llm_ops_review",
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: "workflow_issue", Classification: "step_type_fitness", Severity: "medium",
			Summary: "A fixed sequence uses an unnecessary orchestrator.", Impact: "It adds cost and hides lifecycle state.",
			Evidence: []string{"planning/plan.json"}, RecommendedRoute: pulseFindingRouteDecisionRequired,
		},
	}
	if _, err := RecordPulseReviewFinding(ctx, ws, "pulse-1", "review-1", input); err == nil || !strings.Contains(err.Error(), "requires human_input_id") {
		t.Fatalf("decision_required finding was accepted without a decision: %v", err)
	}
	db, err := openRunConcernsDB(ctx, ws, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS report_human_inputs (id TEXT PRIMARY KEY, source TEXT, status TEXT)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO report_human_inputs (id, source, status) VALUES
		('ops-decision-fixed-pipeline-wrong-source', 'pulse', 'pending'),
		('ops-decision-fixed-pipeline', 'ops_review', 'pending')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	input.HumanInputID = "ops-decision-fixed-pipeline-wrong-source"
	if _, err := RecordPulseReviewFinding(ctx, ws, "pulse-1", "review-1", input); err == nil || !strings.Contains(err.Error(), `source "pulse"`) {
		t.Fatalf("Ops Review accepted a generic Pulse decision: %v", err)
	}
	input.HumanInputID = "ops-decision-fixed-pipeline"
	record, err := RecordPulseReviewFinding(ctx, ws, "pulse-1", "review-1", input)
	if err != nil {
		t.Fatalf("record linked decision finding: %v", err)
	}
	if record.Status != ConcernStatusAcknowledged {
		t.Fatalf("linked decision status = %q, want %q", record.Status, ConcernStatusAcknowledged)
	}
	db, err = openRunConcernsDB(ctx, ws, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var linkedID string
	if err := db.QueryRowContext(ctx, `SELECT json_extract(metadata_json, '$.human_input_id')
		FROM pulse_finding_events WHERE fingerprint=? AND event_type='awaiting_user'`, record.Fingerprint).Scan(&linkedID); err != nil {
		t.Fatalf("load awaiting_user link: %v", err)
	}
	if linkedID != input.HumanInputID {
		t.Fatalf("linked decision id = %q, want %q", linkedID, input.HumanInputID)
	}
}

func TestTypedReviewRequiresCompleteFindingRecord(t *testing.T) {
	ws := concernsWorkspace(t)
	_, err := RecordPulseReviewFinding(context.Background(), ws, "pulse-1", "review-1", PulseReviewFindingInput{Concern: "missing detail", Module: "workflow_review", PulseFindingDetails: PulseFindingDetails{IssueKind: "workflow_issue"}})
	if err == nil || !strings.Contains(err.Error(), "classification") {
		t.Fatalf("expected incomplete finding rejection, got %v", err)
	}
}
