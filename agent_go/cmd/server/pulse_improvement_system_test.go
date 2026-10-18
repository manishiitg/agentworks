package server

import (
	"context"
	"testing"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestArchitectureImprovementDecisionApplicationAndOutcome(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ws := "Workflow/improvement-loop"
	decision, err := createReportHumanInput(ctx, ws, ReportHumanInputCreateRequest{InputID: "architecture-proposal-script", Source: "architecture_review", Question: "Use a script for deterministic formatting?", ApplyContract: ReportHumanInputApplyContract{Mode: "targeted_fixer", ApprovedScope: "Replace deterministic formatting with code/format/main.py", PreRunChecks: []string{"Fixture outputs must match"}, PostRunProof: "Compare format latency and equal outputs on the next comparable run", FailurePolicy: "continue_unchanged"}, Options: []ReportHumanInputOption{{ID: "approve", Title: "Approve"}, {ID: "reject", Title: "Reject"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := step.RecordPulseReviewFinding(ctx, ws, "pulse-architecture", "review-architecture", step.PulseReviewFindingInput{
		Module: pulseModuleArchitectureReview, Concern: "Deterministic formatting can avoid repeated model calls", HumanInputID: decision.ID,
		PulseFindingDetails: step.PulseFindingDetails{IssueKind: step.IssueKindWorkflow, RecommendedRoute: "decision_required", Classification: "efficiency_or_coaching", Severity: "medium", Summary: "Script deterministic formatting", Impact: "Reduce latency with equal outputs", Evidence: []string{"run-before/timing.json"}},
	}); err != nil {
		t.Fatal(err)
	}
	improvement := step.PulseIntervention{InterventionID: "int-architecture", Title: "Script deterministic formatting", Kind: "architecture_improvement", CriterionID: "sc-1", Metric: "format_latency", ImpactType: "reliability", ExpectedDirection: "decrease", BaselineWindow: "run-before", Checkpoint: "next comparable normal run", Guardrails: []string{"same output"}, RollbackCondition: "output regression", InterferenceDomains: []string{"control:format"}, HumanInputID: decision.ID, Status: "proposed"}
	ledger, err := step.RecordPulseImpactUpdate(ctx, ws, step.PulseImpactUpdate{Interventions: []step.PulseIntervention{improvement}})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Interventions[0].Status != "proposed" {
		t.Fatal("proposal not retained")
	}
	if _, err = answerReportHumanInput(ctx, ws, decision.ID, ReportHumanInputAnswerRequest{SelectedOptionID: "approve"}); err != nil {
		t.Fatal(err)
	}
	ledger, err = step.LoadPulseImpactLedger(ctx, ws, 20)
	if err != nil || ledger.Interventions[0].Status != "approved" {
		t.Fatalf("approval falsely applied or lost: %+v %v", ledger, err)
	}
	improvement.Status = "running"
	if _, err = step.RecordPulseImpactUpdate(ctx, ws, step.PulseImpactUpdate{Interventions: []step.PulseIntervention{improvement}}); err == nil {
		t.Fatal("application accepted without consumed receipt")
	}
	if _, err = consumeReportHumanInput(ctx, ws, decision.ID, ReportHumanInputConsumeRequest{OutcomeSummary: "Applied code/format/main.py; fixture outputs equal; plan revision after", OutcomeEvidence: "Checked code/format/main.py and matching fixture outputs at plan revision after"}); err != nil {
		t.Fatal(err)
	}
	ledger, err = step.LoadPulseImpactLedger(ctx, ws, 20)
	if err != nil || ledger.Interventions[0].Status != "running" {
		t.Fatalf("application not tracked: %+v %v", ledger, err)
	}
	improvement = ledger.Interventions[0]
	improvement.Status = "adopted"
	improvement.TerminalOutcome = "Reduced latency with matching outputs"
	if _, err = step.RecordPulseImpactUpdate(ctx, ws, step.PulseImpactUpdate{Interventions: []step.PulseIntervention{improvement}}); err == nil {
		t.Fatal("adoption accepted without outcome evidence")
	}
	before, after := 10.0, 3.0
	_, err = step.RecordPulseImpactUpdate(ctx, ws, step.PulseImpactUpdate{Assessments: []step.PulseImpactAssessment{{InterventionID: improvement.InterventionID, Verdict: "improved", BeforeWindow: "run-before", AfterWindow: "run-after", BeforeValue: &before, AfterValue: &after, Confidence: "medium", AssessedAt: "2026-09-10T00:00:00Z", Evidence: []string{"run-before/timing.json", "run-after/timing.json", "fixture-equality.json"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err = step.RecordPulseImpactUpdate(ctx, ws, step.PulseImpactUpdate{Interventions: []step.PulseIntervention{improvement}})
	if err != nil || ledger.Interventions[0].Status != "adopted" || len(ledger.Assessments) != 1 {
		t.Fatalf("outcome loop incomplete: %+v %v", ledger, err)
	}
}
