package server

import (
	"context"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"strings"
	"testing"
	"time"
)

func TestPulseImprovementBoundaryCannotBePushedForwardByOtherReviews(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	for _, module := range []string{pulseModuleArchitectureReview, pulseModuleStrategicReview} {
		previous := PulseModuleState{Module: module, LastPulseRunID: "old", LastDecision: "skipped", NextCheckAt: "2026-09-11"}
		decision := PulseWorklistDecision{Module: module, Reason: "Technical has work", NextCheckAt: "2026-09-20"}
		got, err := preservePulseImprovementBoundary(previous, decision, "new", now)
		if err != nil || got.Due || got.NextCheckAt != previous.NextCheckAt {
			t.Fatalf("boundary moved: %+v %v", got, err)
		}
		got, err = preservePulseImprovementBoundary(previous, decision, "new", now.AddDate(0, 0, 2))
		if err != nil || !got.Due {
			t.Fatalf("matured boundary skipped: %+v %v", got, err)
		}
		decision.DeferReason = "Awaiting customer outcome window"
		got, err = preservePulseImprovementBoundary(previous, decision, "new", now.AddDate(0, 0, 2))
		if err != nil || got.Due || !strings.Contains(got.Reason, decision.DeferReason) {
			t.Fatalf("explicit deferral lost: %+v %v", got, err)
		}
		decision.NextCheckAt = "2026-09-01"
		if _, err := preservePulseImprovementBoundary(previous, decision, "new", now); err == nil {
			t.Fatal("past deferral accepted")
		}
	}
}

func TestPulseFourModulesPersistAndCompleteIndependently(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ws, run := "Workflow/independent", "pulse-four"
	decisions := completePulseWorklistDecisions(nil)
	for i := range decisions {
		decisions[i].Due = decisions[i].Module != pulseModulePlanDriftReview
		if !decisions[i].Due {
			decisions[i].CooldownRuns = 1
		}
	}
	if _, err := recordPulseWorklist(ctx, ws, run, decisions); err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{pulseModuleTechnicalReview, pulseModuleArchitectureReview, pulseModuleStrategicReview} {
		stage := pulseLifecycleModuleReviewStep(run, module)
		if !strings.Contains(stage.query, "module="+`"`+module+`"`) {
			t.Fatalf("role missing: %s", stage.query)
		}
		if err := beginDuePulseReviewRecoveries(ctx, ws, run, module); err != nil {
			t.Fatal(err)
		}
		result := "done"
		if module == pulseModuleTechnicalReview {
			result = "failed"
		}
		if _, err := markPulseModuleResultFromAgent(ctx, ws, module, run, result, "Recorded evidence.", []string{"runs/proof"}); err != nil {
			t.Fatal(err)
		}
		if err := validatePulseDueModuleResultsFor(ctx, ws, run, module); err != nil && result == "done" {
			t.Fatal(err)
		}
	}
	state, _, err := getPulseWorklistForRun(ctx, ws, run)
	if err != nil || state[pulseModuleStrategicReview].LastResult != "done" || state[pulseModuleArchitectureReview].LastResult != "done" {
		t.Fatalf("independent completion missing: %+v %v", state, err)
	}
}

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
	if _, err = consumeReportHumanInput(ctx, ws, decision.ID, ReportHumanInputConsumeRequest{OutcomeSummary: "Applied code/format/main.py; fixture outputs equal; plan revision after"}); err != nil {
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

func TestPulseProtectedDateSurvivesActualWorklistPersistence(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ws := "Workflow/protected-date"
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	initial := completePulseWorklistDecisions(map[string]PulseWorklistDecision{pulseModuleArchitectureReview: {Reason: "Review after outcome window", NextCheckAt: past}})
	if _, err := recordPulseWorklist(ctx, ws, "first", initial); err != nil {
		t.Fatal(err)
	}
	next := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview:    {Due: true, Reason: "QA has a regression"},
		pulseModuleArchitectureReview: {Reason: "Technical was selected", NextCheckAt: future},
	})
	states, err := recordPulseWorklist(ctx, ws, "second", next)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.Module == pulseModuleArchitectureReview && state.LastDecision != "due" {
			t.Fatalf("research starved: %+v", state)
		}
	}
	parsed, err := pulseWorklistDecisionsFromArgs([]interface{}{map[string]interface{}{"module": pulseModuleArchitectureReview, "due": false, "reason": "Waiting on customers", "next_check_at": future, "defer_reason": "Outcome source is not published yet"}})
	if err != nil || parsed[0].DeferReason == "" {
		t.Fatalf("typed deferral unavailable: %+v %v", parsed, err)
	}
}

func TestArchitectureFocusHasSeparateDurableCoverage(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ws := "Workflow/coverage"
	if _, err := recordPulseReviewFocus(ctx, ws, "pulse-architecture", pulseModuleArchitectureReview, "learning_quality", "route-a", "changed_focus", "New conflicting learning", "Proposed scoped cleanup", "", "", []string{"learnings/route-a.md"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	coverage, err := getPulseReviewFocusStates(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, focus := range coverage {
		if focus.Module == pulseModuleArchitectureReview && focus.FocusKey == "learning_quality" && focus.LastReviewedAt != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("architecture coverage missing: %+v", coverage)
	}
}
