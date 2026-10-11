package step_based_workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
)

func filedReviewConcern(t *testing.T, workspacePath, pulseRunID, module, text string) RunConcern {
	t.Helper()
	seedRunConcerns(t, workspacePath, pulseRunID, "", module, ConcernPhaseReview, text)
	concerns := activeRunConcernRows(t, workspacePath)
	if len(concerns) != 1 {
		t.Fatalf("concerns = %+v, want one", concerns)
	}
	return concerns[0]
}

func filedAdvisorConcern(t *testing.T, workspacePath, pulseRunID, module, text, route, nextCheck string) RunConcern {
	t.Helper()
	seedRunConcerns(t, workspacePath, pulseRunID, "", module, ConcernPhaseReview, text)
	concerns := activeRunConcernRows(t, workspacePath)
	if len(concerns) != 1 {
		t.Fatalf("load advisor concern: concerns=%+v", concerns)
	}
	// Advisor dispositions are checked against the stored route, so attach it
	// the way a filed advisor finding carries it.
	ctx := context.Background()
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open workflow db: %v", err)
	}
	defer db.Close()
	marker := pulseFindingDetailMarker{Concern: text, Module: module, PulseFindingDetails: PulseFindingDetails{
		IssueKind: IssueKindWorkflow, RecommendedRoute: route, NextCheck: nextCheck,
	}}
	if err := recordPulseFindingDetailAt(ctx, db, workspacePath, pulseRunID, module, marker,
		concerns[0].Fingerprint, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("attach advisor route: %v", err)
	}
	return concerns[0]
}

func recordFindingDispositions(t *testing.T, workspacePath, module, pulseRunID string, dispositions []PulseFindingDisposition) {
	t.Helper()
	ctx := context.Background()
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open lifecycle db: db=%v err=%v", db, err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin lifecycle transaction: %v", err)
	}
	defer tx.Rollback()
	if err := RecordPulseFindingDispositionsTx(ctx, tx, module, pulseRunID, dispositions, "2026-07-31T08:00:00Z"); err != nil {
		t.Fatalf("record dispositions: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit dispositions: %v", err)
	}
}

func TestPulseFindingLifecycleClosesOnlyWithVerifiedFixAndReopensOnRecurrence(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "bug_review"
	pulseRunID := "pulse-1"
	concernText := "stale selector repeats the same accounts"
	concern := filedReviewConcern(t, workspacePath, pulseRunID, module, concernText)

	recordFindingDispositions(t, workspacePath, module, pulseRunID, []PulseFindingDisposition{{
		Fingerprint:  concern.Fingerprint,
		FindingID:    "BUG-1",
		Disposition:  FindingDispositionFixedVerified,
		Summary:      "Selector now includes an exploration pool.",
		ChangedFiles: []string{"planning/step_config.json"},
		BeforeRefs:   []string{"selector:before"},
		AfterRefs:    []string{"selector:after"},
		Verification: []PulseFindingVerification{{
			Check:    "selector diversity test",
			Verdict:  VerificationPassed,
			Expected: "new candidates are eligible",
			Observed: "new candidates were selected",
			Evidence: []string{"go test ./... -run TestSelectorDiversity"},
		}},
	}})

	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("load lifecycle: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Status != ConcernStatusResolved {
		t.Fatalf("verified finding was not closed: %+v", lifecycles)
	}
	if len(lifecycles[0].Attempts) != 1 || lifecycles[0].Attempts[0].Status != "verified" {
		t.Fatalf("verified attempt missing: %+v", lifecycles[0].Attempts)
	}
	if len(lifecycles[0].Verification) != 1 || lifecycles[0].Verification[0].Verdict != VerificationPassed {
		t.Fatalf("verification evidence missing: %+v", lifecycles[0].Verification)
	}

	seedRunConcerns(t, workspacePath, "pulse-2", "", module, ConcernPhaseReview, concernText)
	lifecycles, err = LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("reload lifecycle: %v", err)
	}
	if lifecycles[0].Status != ConcernStatusOpen || lifecycles[0].SeenCount != 2 {
		t.Fatalf("recurrence did not reopen finding: %+v", lifecycles[0])
	}
	foundReopened := false
	for _, event := range lifecycles[0].Events {
		foundReopened = foundReopened || event.EventType == "reopened"
	}
	if !foundReopened {
		t.Fatalf("reopened event missing: %+v", lifecycles[0].Events)
	}
}

func TestPulseFindingLifecycleClosesAppliedChangeAndRecurrenceReopens(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "eval_health"
	pulseRunID := "pulse-1"
	concern := filedReviewConcern(t, workspacePath, pulseRunID, module, "evaluation uses a stale outcome")

	recordFindingDispositions(t, workspacePath, module, pulseRunID, []PulseFindingDisposition{{
		Fingerprint:  concern.Fingerprint,
		FindingID:    "EVAL-1",
		Disposition:  FindingDispositionChangedUnverified,
		Summary:      "Query changed; the next producing run is required for proof.",
		ChangedFiles: []string{"evaluation/plan.json"},
		NextCheck:    "next producing run",
		Verification: []PulseFindingVerification{{
			Check:   "new outcome appears in a producing run",
			Verdict: VerificationInconclusive,
		}},
	}})
	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("load awaiting lifecycle: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Status != ConcernStatusResolved {
		t.Fatalf("applied change did not close its issue: %+v", lifecycles)
	}

	seedRunConcerns(t, workspacePath, "normal-run-2", "", module, ConcernPhaseReview, "evaluation uses a stale outcome")
	lifecycles, err = LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("reload recurring lifecycle: %v", err)
	}
	if lifecycles[0].Status != ConcernStatusOpen || lifecycles[0].SeenCount != 2 {
		t.Fatalf("normal recurrence did not reopen applied fix: %+v", lifecycles[0])
	}
}

func TestPulseFindingIssueIDUpdatesOneRootCauseAndMergePreservesDuplicateHistory(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := pulsemodules.TechnicalReviewID
	first, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "collector silently drops failed rows", Module: module,
		PulseFindingDetails: PulseFindingDetails{
			// Existing databases can carry a historical finding_id. It must not
			// escape as the public identity after this migration.
			FindingID: "LEGACY-COLLECTOR-ROWS",
			IssueKind: "workflow_issue", Classification: "correctness_bug", Severity: "high",
			Summary: "Failed rows disappear.", Impact: "The workflow can report complete data when it is incomplete.",
			Evidence: []string{"runs/iteration-0/result.json"},
		},
	})
	if err != nil || first.IssueID == "" {
		t.Fatalf("record first finding: record=%+v err=%v", first, err)
	}
	if !strings.HasPrefix(first.IssueID, "PUL-") {
		t.Fatalf("public issue identity=%q, want PUL id", first.IssueID)
	}
	if first.IssueID == "PUL-"+strings.ToUpper(first.Fingerprint[:8]) {
		t.Fatalf("public issue identity must be stored, not derived from its legacy fingerprint: %+v", first)
	}
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		IssueID: first.IssueID, Concern: "row-level failures vanish before the summary is calculated", Module: module,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: "workflow_issue", Classification: "correctness_bug", Severity: "high",
			Summary: "The same row-loss defect has new evidence.", Impact: "Completeness reporting remains misleading.",
			Evidence: []string{"runs/iteration-1/result.json"},
		},
	}); err != nil {
		t.Fatalf("update existing root cause by PUL issue id: %v", err)
	}
	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, -1)
	if err != nil || len(findings) != 1 || findings[0].SeenCount != 2 {
		t.Fatalf("PUL update created a second identity: findings=%+v err=%v", findings, err)
	}

	second, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-3", "review-3", PulseReviewFindingInput{
		Concern: "summary hides the same failed collector rows", Module: module,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: "workflow_issue", Classification: "correctness_bug", Severity: "high",
			Summary: "A symptom was recorded separately for consolidation.", Impact: "It would otherwise duplicate repair work.",
			Evidence: []string{"runs/iteration-2/summary.json"},
		},
	})
	if err != nil {
		t.Fatalf("record duplicate candidate: %v", err)
	}
	if _, err := MergePulseFindingIssues(ctx, workspacePath, first.IssueID, []string{second.IssueID}, "Both records describe failed-row loss before summary calculation."); err != nil {
		t.Fatalf("merge semantic duplicate: %v", err)
	}
	findings, err = LoadPulseFindingLifecycles(ctx, workspacePath, module, -1)
	if err != nil || len(findings) != 2 {
		t.Fatalf("load merged lifecycle: findings=%+v err=%v", findings, err)
	}
	foundDuplicate := false
	for _, finding := range findings {
		if NewPulseIssue(finding).ID != second.IssueID {
			continue
		}
		if finding.Status != ConcernStatusResolved || finding.Details == nil || finding.Details.MergedIntoIssueID != first.IssueID {
			t.Fatalf("duplicate was not retired with its history linked: %+v", finding)
		}
		foundDuplicate = true
	}
	if !foundDuplicate {
		t.Fatalf("merged duplicate %s not found", second.IssueID)
	}

	seedRunConcerns(t, workspacePath, "pulse-4", "", module, ConcernPhaseReview, "summary hides the same failed collector rows")
	findings, err = LoadPulseFindingLifecycles(ctx, workspacePath, module, -1)
	if err != nil || len(findings) != 2 {
		t.Fatalf("reload alias recurrence: findings=%+v err=%v", findings, err)
	}
	for _, finding := range findings {
		switch NewPulseIssue(finding).ID {
		case first.IssueID:
			if finding.Status != ConcernStatusOpen || finding.SeenCount != 3 {
				t.Fatalf("alias recurrence did not reopen canonical issue: %+v", finding)
			}
		case second.IssueID:
			if finding.Status != ConcernStatusResolved {
				t.Fatalf("retired alias reopened instead of canonical issue: %+v", finding)
			}
		}
	}
}

func TestPulseFindingIssueIDUpdateReloadsExistingStepFindingAcrossReviewerModule(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	const concern = "The evaluator replaces managed DB truth with a retired JSON fallback."
	seedRunConcerns(t, workspacePath, "execution-1", "", "eval-workflow-success", ConcernPhaseReview, concern)

	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load step finding: findings=%+v err=%v", findings, err)
	}
	issueID := NewPulseIssue(findings[0]).ID

	record, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		IssueID: issueID,
		Concern: "Current evidence confirms the same evaluator source-of-truth defect.",
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      "workflow_issue",
			Classification: "correctness_bug",
			Severity:       "high",
			Summary:        "Evaluator source-of-truth is inconsistent.",
			Impact:         "A successful run can receive a false zero score.",
			Evidence:       []string{"runs/iteration-0/default/logs/score-step/execution/execution-attempt-1-iteration-0.json"},
		},
	})
	if err != nil {
		t.Fatalf("update existing step finding through reviewer module: %v", err)
	}
	if record.IssueID != issueID {
		t.Fatalf("updated issue id=%q, want %q", record.IssueID, issueID)
	}

	findings, err = LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("reload updated step finding: findings=%+v err=%v", findings, err)
	}
	if findings[0].StepID != "eval-workflow-success" || findings[0].SeenCount != 2 {
		t.Fatalf("cross-module update changed identity or recurrence: %+v", findings[0])
	}
	if findings[0].Details == nil || findings[0].Details.Summary != "Evaluator source-of-truth is inconsistent." {
		t.Fatalf("typed evidence was not attached to existing step finding: %+v", findings[0])
	}
}

// TestRecordPulseReviewFindingUsesExplicitStepIDForNewFinding reproduces a
// real gap: a brand-new finding's StepID silently defaulted to the module
// name (e.g. "plan_drift_review") because no agent-facing input ever carried
// the actual plan step it was about, which meant a caller that requires real
// step attribution — plan_drift_review's own verifyStepDriftCheckFindingsExist,
// which checks a fail-status check's linked finding is filed against the
// exact step under review — could never match a legitimately filed finding.
func TestRecordPulseReviewFindingUsesExplicitStepIDForNewFinding(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)

	record, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "Report SQL references a column dropped from the live schema.",
		Module:  pulsemodules.PlanDriftReviewID,
		StepID:  "scan-linkedin-feed-for-job-posts",
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:        "workflow_issue",
			Classification:   "correctness_bug",
			Severity:         "medium",
			Summary:          "db/reports/index.html queries a column that no longer exists.",
			Impact:           "The report silently fails to render this section.",
			Evidence:         []string{"db/reports/index.html"},
			RecommendedRoute: "fixer_handoff",
		},
	})
	if err != nil {
		t.Fatalf("record new finding with explicit step_id: %v", err)
	}

	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load new finding: findings=%+v err=%v", findings, err)
	}
	if findings[0].StepID != "scan-linkedin-feed-for-job-posts" {
		t.Fatalf("new finding StepID = %q, want the explicit step_id, not the module name", findings[0].StepID)
	}
	if got := NewPulseIssue(findings[0]).ID; got != record.IssueID {
		t.Fatalf("issue id mismatch: got %q, record reported %q", got, record.IssueID)
	}
}

// TestRecordPulseReviewFindingFallsBackToModuleWhenStepIDOmitted preserves
// the pre-existing behavior for a genuinely module-wide finding that is not
// about one specific step.
func TestRecordPulseReviewFindingFallsBackToModuleWhenStepIDOmitted(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)

	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "Cost ledger totals diverge from the raw usage log workflow-wide.",
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      "workflow_issue",
			Classification: "correctness_bug",
			Severity:       "medium",
			Summary:        "Cost totals do not reconcile.",
			Impact:         "Cost reporting is unreliable workflow-wide.",
			Evidence:       []string{"db/db.sqlite"},
		},
	}); err != nil {
		t.Fatalf("record module-wide finding: %v", err)
	}

	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load module-wide finding: findings=%+v err=%v", findings, err)
	}
	if findings[0].StepID != pulsemodules.TechnicalReviewID {
		t.Fatalf("module-wide finding StepID = %q, want the module name as before", findings[0].StepID)
	}
}

// TestRecordPulseReviewFindingUpgradesLegacyModuleStepIDOnReuse reproduces a
// dead end the earlier step_id fix introduced: reusing an existing issue
// whose StepID is a canonical module name (the pre-step_id-argument fallback,
// or a genuinely module-wide finding) permanently preserved that placeholder
// even when a later call supplied a real step_id, because the SQL
// ON CONFLICT write path never applied the incoming step_id on a fingerprint
// match at all. That made a legacy or module-wide issue impossible to ever
// attribute to a real step through the normal "reuse the same issue_id for
// the same semantic root cause" path callers are told to follow — exactly
// the scenario plan_drift_review's own verifyStepDriftCheckFindingsExist
// needs to succeed for.
func TestRecordPulseReviewFindingUpgradesLegacyModuleStepIDOnReuse(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)

	// A finding filed with no step_id — StepID falls back to the module name,
	// simulating both a pre-fix legacy row and a genuinely module-wide finding.
	first, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "Report SQL references a column dropped from the live schema.",
		Module:  pulsemodules.PlanDriftReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      "workflow_issue",
			Classification: "correctness_bug",
			Severity:       "medium",
			Summary:        "db/reports/index.html queries a column that no longer exists.",
			Impact:         "The report silently fails to render this section.",
			Evidence:       []string{"db/reports/index.html"},
		},
	})
	if err != nil {
		t.Fatalf("record legacy finding: %v", err)
	}
	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load legacy finding: findings=%+v err=%v", findings, err)
	}
	if findings[0].StepID != pulsemodules.PlanDriftReviewID {
		t.Fatalf("legacy finding StepID = %q, want the module-name placeholder", findings[0].StepID)
	}

	// A later review reuses the same issue for the same root cause, now
	// supplying the real step_id per plan-drift-review.md's guidance.
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		IssueID: first.IssueID,
		StepID:  "scan-linkedin-feed-for-job-posts",
		Concern: "Report SQL still references the dropped column.",
		Module:  pulsemodules.PlanDriftReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      "workflow_issue",
			Classification: "correctness_bug",
			Severity:       "medium",
			Summary:        "db/reports/index.html queries a column that no longer exists.",
			Impact:         "The report silently fails to render this section.",
			Evidence:       []string{"db/reports/index.html"},
		},
	}); err != nil {
		t.Fatalf("reuse legacy finding with explicit step_id: %v", err)
	}

	findings, err = LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("reload upgraded finding: findings=%+v err=%v", findings, err)
	}
	if findings[0].StepID != "scan-linkedin-feed-for-job-posts" {
		t.Fatalf("upgraded finding StepID = %q, want the real step id from the reuse call", findings[0].StepID)
	}

	// A THIRD call, now omitting step_id again, must not regress the real
	// attribution back to a placeholder — a real step identity never moves.
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-3", "review-3", PulseReviewFindingInput{
		IssueID: first.IssueID,
		Concern: "Report SQL still references the dropped column, confirmed a third time.",
		Module:  pulsemodules.PlanDriftReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      "workflow_issue",
			Classification: "correctness_bug",
			Severity:       "medium",
			Summary:        "db/reports/index.html queries a column that no longer exists.",
			Impact:         "The report silently fails to render this section.",
			Evidence:       []string{"db/reports/index.html"},
		},
	}); err != nil {
		t.Fatalf("reconfirm finding without step_id: %v", err)
	}
	findings, err = LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("reload reconfirmed finding: findings=%+v err=%v", findings, err)
	}
	if findings[0].StepID != "scan-linkedin-feed-for-job-posts" {
		t.Fatalf("real step attribution regressed to %q after a step_id-omitting reuse", findings[0].StepID)
	}
}

// TestDifferentModulesSharingATargetKeyDoNotCollide reproduces Sales
// Outreach PUL-1E38F625: technical_review and strategic_review independently
// filed unrelated findings that happened to name the same target_key (a
// workflow-local location reference, not a deliberately shared identity).
// Before the fix, the second write silently reused the first module's
// fingerprint via record_pulse_finding's target_key-based dedup and
// overwrote its content -- the reload showed strategic_review's original
// finding replaced by technical_review's, with no error or warning.
func TestDifferentModulesSharingATargetKeyDoNotCollide(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	const sharedTargetKey = "planning/plan.json:step-prepare-linkedin-engagement"

	strategic, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "Engagement cadence escalates faster than the strategy calls for.",
		Module:  pulsemodules.StrategicReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:        IssueKindWorkflow,
			TargetKey:        sharedTargetKey,
			RecommendedRoute: pulseFindingRouteFixerHandoff,
			Classification:   "strategy_drift",
			Severity:         "medium",
			Summary:          "Cadence escalation outruns the approved strategy.",
			Impact:           "Outreach frequency drifts from the plan without a review checkpoint.",
			Evidence:         []string{"planning/plan.json"},
		},
	})
	if err != nil {
		t.Fatalf("file strategic_review finding: %v", err)
	}

	technical, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		Concern: "The step's tool allowlist is missing a masking capability it needs.",
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      IssueKindWorkflow,
			TargetKey:      sharedTargetKey,
			Classification: "correctness_bug",
			Severity:       "high",
			Summary:        "Masking-architecture gap in the engagement step.",
			Impact:         "PII can reach an unmasked tool call.",
			Evidence:       []string{"planning/step_config.json"},
		},
	})
	if err != nil {
		t.Fatalf("file technical_review finding: %v", err)
	}

	if strategic.IssueID == technical.IssueID {
		t.Fatalf("two unrelated findings from different modules collapsed onto one issue id %q", strategic.IssueID)
	}
	if strategic.Fingerprint == technical.Fingerprint {
		t.Fatalf("two unrelated findings from different modules collapsed onto one fingerprint %q", strategic.Fingerprint)
	}

	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 2 {
		t.Fatalf("load both findings: findings=%+v err=%v", findings, err)
	}
	byIssueID := map[string]PulseFindingLifecycle{}
	for _, finding := range findings {
		byIssueID[NewPulseIssue(finding).ID] = finding
	}
	strategicFinding, ok := byIssueID[strategic.IssueID]
	if !ok || strategicFinding.Details == nil || strategicFinding.Details.Summary != "Cadence escalation outruns the approved strategy." {
		t.Fatalf("strategic_review finding content missing or overwritten: %+v", strategicFinding)
	}
	if strategicFinding.StepID != pulsemodules.StrategicReviewID {
		t.Fatalf("strategic_review finding module attribution changed: %+v", strategicFinding)
	}
	technicalFinding, ok := byIssueID[technical.IssueID]
	if !ok || technicalFinding.Details == nil || technicalFinding.Details.Summary != "Masking-architecture gap in the engagement step." {
		t.Fatalf("technical_review finding content missing or overwritten: %+v", technicalFinding)
	}
	if technicalFinding.StepID != pulsemodules.TechnicalReviewID {
		t.Fatalf("technical_review finding module attribution changed: %+v", technicalFinding)
	}
}

// TestSameModuleTargetKeyStillDedupsAcrossPulseRuns proves the fix did not
// turn target_key dedup into "always new issue": the SAME module reporting
// the SAME target_key across two different Pulse runs must still converge on
// one issue, not create a duplicate per run.
func TestSameModuleTargetKeyStillDedupsAcrossPulseRuns(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	const targetKey = "planning/plan.json:step-collect-signals"

	first, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "The collect-signals step lacks a bounded retry budget.",
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: IssueKindWorkflow, TargetKey: targetKey,
			Classification: "reliability_bug", Severity: "medium",
			Summary: "Unbounded retry loop.", Impact: "A flaky source can stall the run indefinitely.",
			Evidence: []string{"planning/plan.json"},
		},
	})
	if err != nil {
		t.Fatalf("first technical_review finding: %v", err)
	}
	second, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		Concern: "Confirmed again: the collect-signals step still has no retry budget.",
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: IssueKindWorkflow, TargetKey: targetKey,
			Classification: "reliability_bug", Severity: "medium",
			Summary: "Unbounded retry loop, still present.", Impact: "A flaky source can stall the run indefinitely.",
			Evidence: []string{"planning/plan.json"},
		},
	})
	if err != nil {
		t.Fatalf("second technical_review finding: %v", err)
	}
	if first.IssueID != second.IssueID {
		t.Fatalf("same module/target_key across runs produced two issues: %q vs %q", first.IssueID, second.IssueID)
	}
}

// TestHarnessIssueTargetKeyStillDedupsAcrossModules protects the deliberately
// preserved module-agnostic behavior for harness_issue findings: this is a
// shared platform identity, and the module-scoping fix for PUL-1E38F625 must
// not narrow it.
func TestHarnessIssueTargetKeyStillDedupsAcrossModules(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	const targetKey = "harness:agent_browser:tab-acquisition"

	first, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "agent_browser network reads the wrong tab under shared CDP.",
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: IssueKindHarness, TargetKey: targetKey,
			Classification: "correctness_bug", Severity: "high",
			Summary: "Wrong tab under shared CDP.", Impact: "Network capture is attributed to the wrong tab.",
			Evidence:     []string{"agent_go/pkg/browser/browser_tools.go"},
			Reproduction: PulseFindingReproduction{Safe: true, Expected: "Correct tab", Observed: "Wrong tab"},
		},
	})
	if err != nil {
		t.Fatalf("first harness finding: %v", err)
	}
	second, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		Concern: "Independently confirmed: agent_browser network targets the wrong tab.",
		Module:  pulsemodules.StrategicReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: IssueKindHarness, TargetKey: targetKey,
			RecommendedRoute: pulseFindingRouteFixerHandoff,
			Classification:   "correctness_bug", Severity: "high",
			Summary: "Wrong tab under shared CDP, reconfirmed.", Impact: "Network capture is attributed to the wrong tab.",
			Evidence:     []string{"agent_go/pkg/browser/browser_tools.go"},
			Reproduction: PulseFindingReproduction{Safe: true, Expected: "Correct tab", Observed: "Wrong tab"},
		},
	})
	if err != nil {
		t.Fatalf("second harness finding: %v", err)
	}
	if first.IssueID != second.IssueID {
		t.Fatalf("harness_issue target_key dedup across modules regressed: %q vs %q", first.IssueID, second.IssueID)
	}
}

func TestWorkflowObservationBecomesIssueOnlyWhenReviewerPromotesIt(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	const concern = "The execution step emitted a broad shell scan without a bounded target."
	seedRunConcerns(t, workspacePath, "execution-1", "default", "collect-signals", ConcernPhaseExecution, concern)

	findings, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load workflow observation: findings=%+v err=%v", findings, err)
	}
	if findings[0].Kind != PulseFindingKindObservation || IsPulseIssue(findings[0]) {
		t.Fatalf("raw workflow evidence was projected as a canonical issue: %+v", findings[0])
	}
	issueID := findings[0].Issue.ID

	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-2", "review-2", PulseReviewFindingInput{
		IssueID: issueID,
		Concern: concern,
		Module:  pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind:      IssueKindWorkflow,
			Classification: "efficiency_bug",
			Severity:       "medium",
			Summary:        "Execution repeatedly performs an unbounded discovery scan.",
			Impact:         "The run spends most of its time rediscovering the same inputs.",
			Evidence:       []string{"runs/iteration-0/default/execution/collect-signals/tool_calls.json"},
		},
	}); err != nil {
		t.Fatalf("promote workflow observation: %v", err)
	}

	findings, err = LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("reload promoted issue: findings=%+v err=%v", findings, err)
	}
	if findings[0].Kind != PulseFindingKindIssue || !IsPulseIssue(findings[0]) {
		t.Fatalf("reviewer promotion did not enter canonical issue lifecycle: %+v", findings[0])
	}
	foundPromotion := false
	for _, event := range findings[0].Events {
		if event.EventType == "promoted_to_issue" {
			foundPromotion = true
		}
	}
	if !foundPromotion {
		t.Fatalf("promotion audit event missing: %+v", findings[0].Events)
	}
}

func TestPulseFindingLifecycleLoadsStructuredHarnessReproduction(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := pulsemodules.TechnicalReviewID
	concernText := "plan updater rejects the effective agent step because its saved type is legacy regular"
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-structured", "pulse-structured", PulseReviewFindingInput{
		Concern: concernText,
		Module:  module,
		PulseFindingDetails: PulseFindingDetails{
			FindingID:      "HARNESS-PLAN-EDIT-1",
			TargetKey:      "harness:plan-editor:legacy-agentic-regular",
			IssueKind:      IssueKindHarness,
			Classification: "correctness_bug",
			Severity:       "critical",
			Summary:        "Runtime and editing APIs disagree about the step type.",
			Impact:         "Pulse can diagnose the workflow defect but cannot apply its repair.",
			Workaround:     "Persist the step as message_sequence manually.",
			Evidence:       []string{"update_scripted_step rejected the agentic step", "update_agent_step rejected the saved regular type"},
			Reproduction: PulseFindingReproduction{
				Safe:        true,
				Setup:       "Use a copied plan with a regular step and declared_execution_mode=agentic.",
				Action:      "Call update_agent_step for that step.",
				Expected:    "The harness upgrades the type and applies the edit.",
				Observed:    "The updater rejects the saved regular type.",
				Limitations: "No production workflow execution is required.",
			},
		},
	}); err != nil {
		t.Fatalf("record structured harness finding: %v", err)
	}
	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("load structured harness finding: %v", err)
	}
	if len(lifecycles) != 1 {
		t.Fatalf("lifecycles = %+v, want one", lifecycles)
	}
	finding := lifecycles[0]
	if finding.FindingID != "HARNESS-PLAN-EDIT-1" || finding.Details == nil {
		t.Fatalf("structured identity missing: %+v", finding)
	}
	if finding.Details.IssueKind != "harness_issue" || finding.Details.TargetKey != "harness:plan-editor:legacy-agentic-regular" {
		t.Fatalf("harness classification missing: %+v", finding.Details)
	}
	if !finding.Details.Reproduction.Safe ||
		!strings.Contains(finding.Details.Reproduction.Expected, "upgrades the type") ||
		!strings.Contains(finding.Details.Reproduction.Observed, "rejects") {
		t.Fatalf("reproduction contract missing: %+v", finding.Details.Reproduction)
	}
	if len(finding.Details.Evidence) != 2 {
		t.Fatalf("evidence missing: %+v", finding.Details.Evidence)
	}
	if finding.Details.Platform == nil ||
		len(finding.Details.Platform.AffectedWorkflows) != 1 ||
		finding.Details.Platform.SeenCount != 1 {
		t.Fatalf("platform registry linkage missing: %+v", finding.Details.Platform)
	}
}

func TestHarnessFindingPlatformRegistryDeduplicatesAcrossWorkflows(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	issueKey := "harness:mcp-bridge:authorization-header"
	concernText := "MCP bridge rejects the valid bearer authorization header"
	for index, workspacePath := range []string{"Workflow/alpha", "Workflow/beta"} {
		runID := "pulse-" + string(rune('1'+index))
		if _, err := RecordPulseReviewFinding(ctx, workspacePath, runID, runID, PulseReviewFindingInput{
			Concern: concernText,
			Module:  pulsemodules.TechnicalReviewID,
			PulseFindingDetails: PulseFindingDetails{
				FindingID:      "HARNESS-MCP-1",
				TargetKey:      issueKey,
				IssueKind:      IssueKindHarness,
				Classification: "correctness_bug",
				Severity:       "high",
				Summary:        "The bridge rejects valid authorization.",
				Impact:         "Workflows cannot call MCP tools through the bridge.",
				Evidence:       []string{"bridge log: Invalid API token"},
				Reproduction: PulseFindingReproduction{
					Safe: true, Setup: "Use a local bridge fixture.", Action: "Send the documented header.",
					Expected: "Request is authorized.", Observed: "Invalid API token.",
				},
			},
		}); err != nil {
			t.Fatalf("record %s harness finding: %v", workspacePath, err)
		}
	}
	lifecycles, err := LoadPulseFindingLifecycles(ctx, "Workflow/alpha", pulsemodules.TechnicalReviewID, 10)
	if err != nil {
		t.Fatalf("load linked harness finding: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Details == nil || lifecycles[0].Details.Platform == nil {
		t.Fatalf("linked platform details missing: %+v", lifecycles)
	}
	platform := lifecycles[0].Details.Platform
	if platform.IssueKey != issueKey || platform.SeenCount != 2 {
		t.Fatalf("platform issue was not deduplicated: %+v", platform)
	}
	if got := strings.Join(platform.AffectedWorkflows, ","); got != "Workflow/alpha,Workflow/beta" {
		t.Fatalf("affected workflow linkage = %q", got)
	}
}

func TestFixedVerifiedRejectsInconclusiveEvidence(t *testing.T) {
	err := validateFindingDisposition(PulseFindingDisposition{
		Fingerprint:  "fp",
		FindingID:    "BUG-1",
		Disposition:  FindingDispositionFixedVerified,
		Summary:      "Claimed fixed.",
		ChangedFiles: []string{"workflow.json"},
		Verification: []PulseFindingVerification{{
			Check:   "runtime outcome",
			Verdict: VerificationInconclusive,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "no failed or inconclusive check") ||
		!strings.Contains(err.Error(), "inconclusive=1") {
		t.Fatalf("inconclusive fixed_verified evidence was accepted: %v", err)
	}
}

func TestFixedVerifiedRejectsUnpairedChangeReferences(t *testing.T) {
	err := validateFindingDisposition(PulseFindingDisposition{
		Fingerprint:  "fp",
		FindingID:    "BUG-REFS",
		Disposition:  FindingDispositionFixedVerified,
		Summary:      "Claimed fixed.",
		ChangedFiles: []string{"workflow.json"},
		BeforeRefs:   []string{"sha256:before"},
		Verification: []PulseFindingVerification{{
			Check:   "runtime outcome",
			Verdict: VerificationPassed,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "before_refs and after_refs as equal-length positional pairs") ||
		!strings.Contains(err.Error(), "before_refs=1, after_refs=0") {
		t.Fatalf("unpaired fixed_verified references were accepted: %v", err)
	}
}

func TestFixedVerifiedDispositionWhitespaceStillClosesFinding(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "bug_review"
	pulseRunID := "pulse-whitespace"
	concern := filedReviewConcern(t, workspacePath, pulseRunID, module, "whitespace routing regression")

	recordFindingDispositions(t, workspacePath, module, pulseRunID, []PulseFindingDisposition{{
		Fingerprint:  " " + concern.Fingerprint + " ",
		FindingID:    " BUG-SPACE ",
		Disposition:  FindingDispositionFixedVerified + " ",
		Summary:      " Lifecycle routing now uses canonical input. ",
		ChangedFiles: []string{" pulse_finding_lifecycle.go "},
		Verification: []PulseFindingVerification{{
			Check:   " lifecycle regression test ",
			Verdict: " " + VerificationPassed + " ",
		}},
	}})

	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("load lifecycle: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Status != ConcernStatusResolved {
		t.Fatalf("trimmed fixed_verified was not closed: %+v", lifecycles)
	}
	foundClosed := false
	for _, event := range lifecycles[0].Events {
		if event.EventType == "updated" {
			t.Fatalf("trimmed disposition was routed as an update: %+v", lifecycles[0].Events)
		}
		foundClosed = foundClosed || event.EventType == "closed"
	}
	if !foundClosed {
		t.Fatalf("trimmed disposition did not record a closed event: %+v", lifecycles[0].Events)
	}
}

func TestNoAttemptVerificationHistoryAccumulatesAcrossPulseRuns(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "bug_review"
	concern := filedReviewConcern(t, workspacePath, "pulse-1", module, "configuration is already correct")
	disposition := PulseFindingDisposition{
		Fingerprint: concern.Fingerprint,
		FindingID:   "BUG-NO-CHANGE",
		Disposition: FindingDispositionVerifiedNoChange,
		Summary:     "The suspected defect is not present.",
		Verification: []PulseFindingVerification{{
			Check:    "configuration contract",
			Verdict:  VerificationPassed,
			Expected: "setting is enabled",
			Observed: "setting is enabled",
		}},
	}
	recordFindingDispositions(t, workspacePath, module, "pulse-1", []PulseFindingDisposition{disposition})
	recordFindingDispositions(t, workspacePath, module, "pulse-2", []PulseFindingDisposition{disposition})

	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil {
		t.Fatalf("load lifecycle: %v", err)
	}
	if len(lifecycles) != 1 || len(lifecycles[0].Verification) != 2 {
		t.Fatalf("no-attempt verification history was overwritten: %+v", lifecycles)
	}
}

func TestExternalActionRequiredLeavesActiveQueueAndStaysSuppressedOnRecurrence(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "bug_review"
	concernText := "scheduler marks a truncated background run successful"
	concern := filedReviewConcern(t, workspacePath, "pulse-external-1", module, concernText)

	recordFindingDispositions(t, workspacePath, module, "pulse-external-1", []PulseFindingDisposition{{
		Fingerprint:     concern.Fingerprint,
		FindingID:       "HARNESS-SCHEDULER-1",
		Disposition:     FindingDispositionExternalAction,
		Summary:         "The defect is in the shared scheduler and has no workflow-level repair.",
		ExternalOwner:   "platform",
		ReasonCode:      "missing_platform_tool",
		ReopenCondition: "scheduler completion detection changes or a platform repair tool becomes available",
	}})

	active := activeRunConcernRows(t, workspacePath)
	if len(active) != 0 {
		t.Fatalf("externally owned finding remained active: %+v", active)
	}
	suppressed, err := LoadExternallyOwnedRunConcerns(ctx, workspacePath)
	if err != nil {
		t.Fatalf("load suppressed concerns: %v", err)
	}
	if len(suppressed) != 1 || suppressed[0].Fingerprint != concern.Fingerprint {
		t.Fatalf("external finding was not suppressed: %+v", suppressed)
	}

	seedRunConcerns(t, workspacePath, "pulse-external-2", "", module, ConcernPhaseReview, concernText)
	active = activeRunConcernRows(t, workspacePath)
	if len(active) != 0 {
		t.Fatalf("identical recurrence reopened external finding: %+v", active)
	}
	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, -1)
	if err != nil {
		t.Fatalf("load external lifecycle: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Status != ConcernStatusExternalActionRequired || lifecycles[0].SeenCount != 2 {
		t.Fatalf("external lifecycle lost recurrence audit: %+v", lifecycles)
	}
	if lifecycles[0].ExternalOwner != "platform" ||
		lifecycles[0].ReasonCode != "missing_platform_tool" ||
		!strings.Contains(lifecycles[0].ReopenCondition, "scheduler") {
		t.Fatalf("external ownership metadata missing: %+v", lifecycles[0])
	}
}

func TestExternalActionRequiredNeedsOwnershipAndReopenBoundary(t *testing.T) {
	err := validateFindingDisposition(PulseFindingDisposition{
		Fingerprint: "fp",
		FindingID:   "HARNESS-1",
		Disposition: FindingDispositionExternalAction,
		Summary:     "Outside workflow authority.",
	})
	if err == nil || !strings.Contains(err.Error(), "external_owner, reason_code, and reopen_condition") {
		t.Fatalf("incomplete external disposition was accepted: %v", err)
	}
}

// TestFindingBacklogLeadsWithTheLargestCluster covers the query the Fixer
// actually reads.
//
// get_pulse_state(view="backlog") resolves here, not through LoadOpenRunConcerns.
// Distinct behavioral findings on one step should remain adjacent so the
// reviewer can reason about their shared execution boundary. Prevalidation
// field failures are consolidated earlier into one step-level finding.
func TestFindingBacklogLeadsWithTheLargestCluster(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)

	file := func(runID, step, text string) {
		t.Helper()
		seedRunConcerns(t, workspacePath, runID, "", step, ConcernPhaseReview, text)
	}
	// Filed first, so recency ranks them last.
	for _, field := range []string{"wrong source", "stale selector", "missing retry"} {
		file("pulse-1", "execute-find-opportunities",
			"execution contract failure: "+field)
	}
	// Filed last, so recency would put this on top.
	file("pulse-2", "execute-digest", "digest was not delivered")

	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", 50)
	if err != nil {
		t.Fatalf("load backlog: %v", err)
	}
	if len(lifecycles) != 4 {
		t.Fatalf("expected 4 findings, got %d", len(lifecycles))
	}
	for i := 0; i < 3; i++ {
		if lifecycles[i].StepID != "execute-find-opportunities" {
			t.Fatalf("position %d is %q; the 3-concern cluster must lead and stay contiguous, "+
				"not be ranked below a single newer finding", i+1, lifecycles[i].StepID)
		}
	}
	if lifecycles[3].StepID != "execute-digest" {
		t.Fatalf("the isolated finding should follow the cluster, got %q", lifecycles[3].StepID)
	}
}

// TestAwaitingUserRequiresARealPendingQuestion closes the gap that left
// latency-report with five findings marked awaiting_user and zero pending
// questions: the operator was told five things needed their decision and had
// nothing to answer, with no way to discover why.
func TestAwaitingUserRequiresARealPendingQuestion(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "eval_health"
	pulseRunID := "pulse-1"
	concern := filedReviewConcern(t, workspacePath, pulseRunID, module,
		"eval steps declare no max_score scale")

	disposition := PulseFindingDisposition{
		Fingerprint: concern.Fingerprint,
		FindingID:   "EVAL-1",
		Disposition: FindingDispositionAwaitingUser,
		Summary:     "Needs a decision on the score scale.",
	}

	// No question at all: the label alone must not be accepted.
	if err := validateFindingDisposition(disposition); err == nil ||
		!strings.Contains(err.Error(), "requires human_input_id") {
		t.Fatalf("awaiting_user was accepted with no question to answer: %v", err)
	}

	// A question that was never created must not satisfy it either.
	disposition.HumanInputID = "invented-id"
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open workflow db: %v", err)
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS report_human_inputs (
		id TEXT PRIMARY KEY, workspace_path TEXT, source TEXT, priority TEXT,
		question TEXT, context TEXT, options_json TEXT, allow_free_text INTEGER,
		status TEXT, selected_option_id TEXT, note TEXT, run_id TEXT,
		consumed_by TEXT NOT NULL DEFAULT '', outcome_summary TEXT NOT NULL DEFAULT '',
		consumed_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create human inputs table: %v", err)
	}
	err = RecordPulseFindingDispositionsTx(ctx, db, module, pulseRunID,
		[]PulseFindingDisposition{disposition}, "")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("awaiting_user accepted a human input id that was never created: %v", err)
	}

	// An answered question does not keep a finding waiting either.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO report_human_inputs (id, status) VALUES ('answered-q', 'answered')`); err != nil {
		t.Fatalf("seed answered question: %v", err)
	}
	disposition.HumanInputID = "answered-q"
	if err := RecordPulseFindingDispositionsTx(ctx, db, module, pulseRunID,
		[]PulseFindingDisposition{disposition}, ""); err == nil ||
		!strings.Contains(err.Error(), "only wait on a pending decision") {
		t.Fatalf("a finding was parked on an already-answered question: %v", err)
	}

	// A real pending question is accepted.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO report_human_inputs (id, status) VALUES ('open-q', 'pending')`); err != nil {
		t.Fatalf("seed pending question: %v", err)
	}
	disposition.HumanInputID = "open-q"
	if err := RecordPulseFindingDispositionsTx(ctx, db, module, pulseRunID,
		[]PulseFindingDisposition{disposition}, ""); err != nil {
		t.Fatalf("a genuine pending decision was rejected: %v", err)
	}

	// Once the operator answers and the linked finding reaches a concrete
	// terminal outcome, the same atomic lifecycle write consumes the decision.
	// This prevents the next loop-closure pass from fabricating
	// answer_not_applied after the repair/rejection was already recorded.
	if _, err := db.ExecContext(ctx, `UPDATE report_human_inputs SET status='answered' WHERE id='open-q'`); err != nil {
		t.Fatalf("answer decision: %v", err)
	}
	resolved := disposition
	resolved.Disposition = FindingDispositionRejected
	resolved.HumanInputID = ""
	resolved.Summary = "The answered option retired this repair safely."
	if err := RecordPulseFindingDispositionsTx(ctx, db, module, "pulse-2",
		[]PulseFindingDisposition{resolved}, "2026-08-07T12:00:00Z"); err != nil {
		t.Fatalf("record linked decision outcome: %v", err)
	}
	var status, consumedBy, outcome string
	if err := db.QueryRowContext(ctx, `SELECT status, consumed_by, outcome_summary FROM report_human_inputs WHERE id='open-q'`).Scan(&status, &consumedBy, &outcome); err != nil {
		t.Fatal(err)
	}
	if status != "consumed" || consumedBy != "pulse" || outcome != resolved.Summary {
		t.Fatalf("linked decision not consumed: status=%q consumed_by=%q outcome=%q", status, consumedBy, outcome)
	}
}

func TestMigrateUnlinkedAwaitingUserFindingsQueuesLegacyDecisionForPulse(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	concern := filedReviewConcern(t, workspacePath, "pulse-legacy", "eval_health", "legacy score-scale decision")

	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open workflow db: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE report_human_inputs (
		id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		t.Fatalf("create human inputs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=? WHERE fingerprint=?`,
		ConcernStatusAcknowledged, concern.Fingerprint); err != nil {
		t.Fatalf("seed acknowledged finding: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE pulse_finding_events SET recorded_at='2026-07-31T00:00:00Z'
		WHERE fingerprint=? AND event_type='filed'`, concern.Fingerprint); err != nil {
		t.Fatalf("age initial filing event: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
		(fingerprint, finding_id, pulse_run_id, event_type, summary, metadata_json, recorded_at)
		VALUES (?, 'EVAL-LEGACY', 'pulse-legacy', 'awaiting_user', 'Needs a score-scale decision.', '{}', '2026-08-01T00:00:00Z')`, concern.Fingerprint); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}

	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		t.Fatalf("migrate legacy decision: %v", err)
	}
	var status, note string
	if err := db.QueryRowContext(ctx, `SELECT status, resolution_note FROM run_concerns WHERE fingerprint=?`, concern.Fingerprint).Scan(&status, &note); err != nil {
		t.Fatalf("load migrated finding: %v", err)
	}
	if status != ConcernStatusQueuedForEngineering || !strings.Contains(note, "Decision request missing") {
		t.Fatalf("legacy decision = status %q, note %q; want queued Pulse repair", status, note)
	}
	var migrationEvents int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_finding_events
		WHERE fingerprint=? AND event_type='decision_request_missing'`, concern.Fingerprint).Scan(&migrationEvents); err != nil {
		t.Fatalf("count migration events: %v", err)
	}
	if migrationEvents != 1 {
		t.Fatalf("migration events = %d, want 1", migrationEvents)
	}
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_finding_events
		WHERE fingerprint=? AND event_type='decision_request_missing'`, concern.Fingerprint).Scan(&migrationEvents); err != nil {
		t.Fatalf("count idempotent migration events: %v", err)
	}
	if migrationEvents != 1 {
		t.Fatalf("idempotent migration events = %d, want 1", migrationEvents)
	}
}

func TestMigrateUnlinkedAwaitingUserFindingsKeepsLinkedDecision(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	concern := filedReviewConcern(t, workspacePath, "pulse-linked", "eval_health", "linked score-scale decision")

	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open workflow db: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE report_human_inputs (
		id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		t.Fatalf("create human inputs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO report_human_inputs (id, status) VALUES ('technical-decision-score-scale', 'pending')`); err != nil {
		t.Fatalf("seed pending decision: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=? WHERE fingerprint=?`,
		ConcernStatusAcknowledged, concern.Fingerprint); err != nil {
		t.Fatalf("seed acknowledged finding: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE pulse_finding_events SET recorded_at='2026-07-31T00:00:00Z'
		WHERE fingerprint=? AND event_type='filed'`, concern.Fingerprint); err != nil {
		t.Fatalf("age initial filing event: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
		(fingerprint, finding_id, pulse_run_id, event_type, summary, metadata_json, recorded_at)
		VALUES (?, 'EVAL-LINKED', 'pulse-linked', 'awaiting_user', 'Needs a score-scale decision.',
		'{"human_input_id":"technical-decision-score-scale"}', '2026-08-01T00:00:00Z')`, concern.Fingerprint); err != nil {
		t.Fatalf("seed linked event: %v", err)
	}

	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		t.Fatalf("migrate linked decision: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM run_concerns WHERE fingerprint=?`, concern.Fingerprint).Scan(&status); err != nil {
		t.Fatalf("load linked finding: %v", err)
	}
	if status != ConcernStatusAcknowledged {
		t.Fatalf("linked decision status = %q, want %q", status, ConcernStatusAcknowledged)
	}
}

func TestParkingDispositionsAreRefused(t *testing.T) {
	for _, disposition := range []string{FindingDispositionProposalOnly, FindingDispositionQueuedForEngineering, FindingDispositionAwaitingRun, FindingDispositionBlocked} {
		err := validateFindingDisposition(PulseFindingDisposition{
			Fingerprint: "fp", FindingID: "PUL-1", Disposition: disposition,
			Summary: "Deferred to a later pass.", NextCheck: "next pass",
		})
		if err == nil || !strings.Contains(err.Error(), "retired") || !strings.Contains(err.Error(), "does not park issues") {
			t.Fatalf("%s must be refused as a parking disposition, got %v", disposition, err)
		}
	}
}

func TestAdvisorAwaitingUserRequiresOwnedDecision(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	pulseRunID := "pulse-goal-decision"
	concern := filedAdvisorConcern(t, workspacePath, pulseRunID, pulsemodules.LegacyGoalAdvisorID,
		"a new distribution channel could materially increase reach", pulseFindingRouteDecisionRequired, "")
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open workflow db: %v", err)
	}
	defer db.Close()
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS report_human_inputs (
		id TEXT PRIMARY KEY, workspace_path TEXT, source TEXT, priority TEXT,
		question TEXT, context TEXT, options_json TEXT, allow_free_text INTEGER,
		status TEXT, selected_option_id TEXT, note TEXT, run_id TEXT,
		consumed_by TEXT NOT NULL DEFAULT '', outcome_summary TEXT NOT NULL DEFAULT '',
		consumed_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("create human inputs table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO report_human_inputs (id, source, status) VALUES
		('strategic-proposal-wrong-owner', 'pulse', 'pending'),
		('plan-proposal-wrong-prefix', 'strategic_review', 'pending'),
		('strategic-proposal-owned-channel', 'strategic_review', 'pending')`); err != nil {
		t.Fatalf("seed decisions: %v", err)
	}

	disposition := PulseFindingDisposition{
		Fingerprint:  concern.Fingerprint,
		FindingID:    "GOAL-1",
		Disposition:  FindingDispositionAwaitingUser,
		Summary:      "Ask whether to test a new distribution channel.",
		HumanInputID: "strategic-proposal-wrong-owner",
	}
	if err := RecordPulseFindingDispositionsTx(ctx, db, pulsemodules.StrategicReviewID, pulseRunID,
		[]PulseFindingDisposition{disposition}, ""); err == nil || !strings.Contains(err.Error(), `source "pulse"`) {
		t.Fatalf("Strategic Review accepted another module's decision: %v", err)
	}

	disposition.HumanInputID = "plan-proposal-wrong-prefix"
	if err := RecordPulseFindingDispositionsTx(ctx, db, pulsemodules.StrategicReviewID, pulseRunID,
		[]PulseFindingDisposition{disposition}, ""); err == nil || !strings.Contains(err.Error(), `must start with "strategic-proposal-"`) {
		t.Fatalf("Strategic Review accepted the wrong decision id namespace: %v", err)
	}

	disposition.HumanInputID = "strategic-proposal-owned-channel"
	if err := RecordPulseFindingDispositionsTx(ctx, db, pulsemodules.StrategicReviewID, pulseRunID,
		[]PulseFindingDisposition{disposition}, ""); err != nil {
		t.Fatalf("Strategic Review's real pending decision was rejected: %v", err)
	}

	resolved := disposition
	resolved.Disposition = FindingDispositionRejected
	resolved.HumanInputID = ""
	resolved.Summary = "The answered decision rejected this experiment."
	if err := RecordPulseFindingDispositionsTx(ctx, db, pulsemodules.StrategicReviewID, "pulse-goal-decision-2",
		[]PulseFindingDisposition{resolved}, "2026-08-07T13:00:00Z"); err == nil || !strings.Contains(err.Error(), `status "pending"`) {
		t.Fatalf("Strategic Review closed a decision before it was answered: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE report_human_inputs SET status='answered' WHERE id='strategic-proposal-owned-channel'`); err != nil {
		t.Fatal(err)
	}
	if err := RecordPulseFindingDispositionsTx(ctx, db, pulsemodules.StrategicReviewID, "pulse-goal-decision-2",
		[]PulseFindingDisposition{resolved}, "2026-08-07T13:00:00Z"); err != nil {
		t.Fatalf("Strategic Review could not apply an answered decision: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM report_human_inputs WHERE id='strategic-proposal-owned-channel'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "consumed" {
		t.Fatalf("applied advisor decision status=%q, want consumed", status)
	}
}

// TestAwaitingRunSeparatesWaitingFromBlocked covers the distinction latency-report
// had no way to express.
//
// Four of its nine "blocked" findings were only waiting for data: security rows
// missing because those steps had not run, and an approved experiment unable to
// ship because the digest step had not executed since 2026-07-29. blocked
// absorbed them because changed_unverified demands a fix attempt with changed
// files, and nothing was fixed. Reading those as blockers points the operator at
// decisions that do not exist. The legacy disposition stays accepted for old
// review payloads, but it is returned to the active issue register.
// Old review payloads may still carry awaiting_run; reading them returns the
// issue to the active register rather than another waiting bucket.
func TestAwaitingRunCompatibilityMapsToActiveIssue(t *testing.T) {
	status, event, _ := lifecycleStatusForDisposition(FindingDispositionAwaitingRun)
	if status != ConcernStatusOpen {
		t.Fatalf("awaiting_run compatibility mapped to status %q, want %q", status, ConcernStatusOpen)
	}
	if event != "reopened_for_review" {
		t.Fatalf("awaiting_run event = %q", event)
	}
}

func TestAppliedFixNeedsNoLaterVerificationAttempt(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "bug_review"
	concern := filedReviewConcern(t, workspacePath, "pulse-1", module, "collector writes a null column")

	// Run 1 applies the fix; proof needs the next producing run. The backend
	// opens the attempt from this disposition.
	recordFindingDispositions(t, workspacePath, module, "pulse-1", []PulseFindingDisposition{{
		Fingerprint: concern.Fingerprint, FindingID: "BUG-1",
		Disposition: FindingDispositionChangedUnverified, Summary: "Applied; awaiting next valid run.",
		NextCheck:    "next scheduled dev collection run writes latency_daily_metrics",
		ChangedFiles: []string{"planning/plan.json"},
		BeforeRefs:   []string{"before"}, AfterRefs: []string{"after"},
		Verification: []PulseFindingVerification{{Check: "consumer read", Verdict: VerificationInconclusive}},
	}})
	opened, err := LoadPulseFindingLifecycles(ctx, workspacePath, module, 10)
	if err != nil || len(opened) != 1 || len(opened[0].Attempts) != 1 {
		t.Fatalf("changed_unverified did not open exactly one attempt: %+v err=%v", opened, err)
	}
	if opened[0].Status != ConcernStatusResolved || opened[0].Attempts[0].Status != "applied" {
		t.Fatalf("applied repair retained a verification backlog: %+v", opened[0])
	}
}

func TestLegacyAppliedFixVerificationBacklogMigratesClosed(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	module := "bug_review"
	concern := filedReviewConcern(t, workspacePath, "pulse-1", module, "collector writes a null column")
	recordFindingDispositions(t, workspacePath, module, "pulse-1", []PulseFindingDisposition{{
		Fingerprint: concern.Fingerprint, FindingID: "BUG-1",
		Disposition: FindingDispositionChangedUnverified, Summary: "Applied.",
		ChangedFiles: []string{"planning/plan.json"},
	}})

	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=? WHERE fingerprint=?`, ConcernStatusAwaitingRun, concern.Fingerprint); err != nil {
		t.Fatalf("restore legacy concern state: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE pulse_fix_attempts SET status=?`, ConcernStatusAwaitingVerification); err != nil {
		t.Fatalf("restore legacy attempt state: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db before explicit reconciliation: %v", err)
	}
	reconciled, err := ReconcilePulseFindingLifecycle(ctx, workspacePath)
	if err != nil {
		t.Fatalf("run explicit lifecycle reconciliation: %v", err)
	}
	if reconciled.AppliedClosures != 1 || reconciled.ClosedIssues == 0 {
		t.Fatalf("reconciliation result=%+v, want the legacy applied fix closed", reconciled)
	}
	db, err = openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("reopen reconciled db: %v", err)
	}
	defer db.Close()

	var concernStatus, attemptStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM run_concerns WHERE fingerprint=?`, concern.Fingerprint).Scan(&concernStatus); err != nil {
		t.Fatalf("read migrated concern: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM pulse_fix_attempts LIMIT 1`).Scan(&attemptStatus); err != nil {
		t.Fatalf("read migrated attempt: %v", err)
	}
	if concernStatus != ConcernStatusResolved || attemptStatus != "applied" {
		t.Fatalf("legacy applied fix remained active: concern=%q attempt=%q", concernStatus, attemptStatus)
	}
}

func TestLegacyUnfixedWaitReturnsToActiveRegister(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	concern := filedReviewConcern(t, workspacePath, "pulse-1", "bug_review", "collector output is incomplete")

	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=? WHERE fingerprint=?`, ConcernStatusAwaitingRun, concern.Fingerprint); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reconciled, err := ReconcilePulseFindingLifecycle(ctx, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.ReopenedWaitingIssues != 1 {
		t.Fatalf("reconciliation=%+v, want one unfixed wait reopened", reconciled)
	}
	db, err = openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM run_concerns WHERE fingerprint=?`, concern.Fingerprint).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != ConcernStatusOpen {
		t.Fatalf("unfixed wait status=%q, want %q", status, ConcernStatusOpen)
	}
}

func TestActionableBacklogReconciliationRetiresUntypedAndHandsOffPlatform(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	seedRunConcerns(t, workspacePath, "run-1", "", "collect", ConcernPhaseExecution, "historical collector note without reviewer promotion")
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "workflow validation rejects valid data", Module: pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: IssueKindWorkflow, Classification: "correctness_bug", Severity: "high",
			Summary: "A workflow validation boundary rejects valid data.", Impact: "The workflow cannot complete valid runs.",
			Evidence: []string{"runs/iteration-1/result.json"},
		},
	}); err != nil {
		t.Fatalf("record workflow finding: %v", err)
	}
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "review-1", PulseReviewFindingInput{
		Concern: "shared tool permission is unavailable", Module: pulsemodules.TechnicalReviewID,
		PulseFindingDetails: PulseFindingDetails{
			IssueKind: IssueKindHarness, TargetKey: "platform:tool-permission", Classification: "platform_permission", Severity: "high",
			Summary: "The shared runtime blocks a required tool.", Impact: "No workflow plan edit can grant the permission.",
			Evidence:     []string{"runs/iteration-1/result.json"},
			Reproduction: PulseFindingReproduction{Safe: true, Expected: "tool is available", Observed: "permission denied"},
		},
	}); err != nil {
		t.Fatalf("record harness finding: %v", err)
	}

	result, err := ReconcilePulseActionableBacklog(ctx, workspacePath)
	if err != nil {
		t.Fatalf("reconcile actionable backlog: %v", err)
	}
	if result.RetiredLegacyObservations != 1 || result.PlatformHandoffs != 1 || result.ActionableWorkflowIssues != 1 {
		t.Fatalf("unexpected reconciliation result: %+v", result)
	}
	if count, err := CountPulseActionableWorkflowIssues(ctx, workspacePath); err != nil || count != 1 {
		t.Fatalf("actionable workflow count=%d err=%v, want 1", count, err)
	}
	lifecycles, err := LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil {
		t.Fatalf("load reconciled lifecycles: %v", err)
	}
	var retiredLegacy, platformHandoff bool
	for _, finding := range lifecycles {
		if strings.Contains(finding.Text, "historical collector") {
			retiredLegacy = finding.Status == ConcernStatusRejected
		}
		if finding.Details != nil && finding.Details.IssueKind == IssueKindHarness {
			platformHandoff = finding.Status == ConcernStatusExternalActionRequired
		}
	}
	if !retiredLegacy || !platformHandoff {
		t.Fatalf("legacy/platform records were not projected out of repair debt: %+v", lifecycles)
	}
}

func TestChangedUnverifiedClosesWithoutSeparateVerificationBoundary(t *testing.T) {
	base := PulseFindingDisposition{
		Fingerprint: "fp", FindingID: "BUG-1",
		Disposition:  FindingDispositionChangedUnverified,
		Summary:      "Collector now writes the column.",
		ChangedFiles: []string{"planning/plan.json"},
		Verification: []PulseFindingVerification{{Check: "consumer read", Verdict: VerificationInconclusive}},
	}

	if err := validateFindingDisposition(base); err != nil {
		t.Fatalf("an applied fix was incorrectly forced into a verification queue: %v", err)
	}
}

// The consolidated Fixer is instructed to pass module="pulse_fixer", meaning
// every due module. This read path treats module as a plain step_id filter, so
// the sentinel matched nothing: the backlog view returned 0 rows for
// the one value the Fixer's own contract tells it to send, against 149 for an
// omitted filter on social-media. It only did any work by falling back to
// omitting the module.

// Live on social-media 2026-09-23: 87 rows filed by steps through the retired
// record_run_concern tool (typed, never routed) kept Technical failing every
// pass. The cleanup retires them; reviewer-filed and prevalidation rows stay.
func TestReconcileRetiresUnroutedStepFiledTypedRows(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	stepRow := func(step, phase, text string) {
		t.Helper()
		seedRunConcerns(t, workspacePath, "run-1", "", step, phase, text)
		db, err := openRunConcernsDB(ctx, workspacePath, false)
		if err != nil || db == nil {
			t.Fatalf("open db: %v", err)
		}
		defer db.Close()
		fp := concernFingerprint(step, text)
		marker := pulseFindingDetailMarker{Concern: text, Module: step, PulseFindingDetails: PulseFindingDetails{IssueKind: IssueKindWorkflow, Summary: text}}
		if err := recordPulseFindingDetailAt(ctx, db, workspacePath, "run-1", step, marker, fp, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("attach details: %v", err)
		}
	}
	stepRow("execute-remediate", ConcernPhaseExecution, "step draft ignores learnings")
	stepRow("sequence-a", ConcernPhaseAgent, "sequence skipped a target")
	recordTestReviewFinding(t, workspacePath, "pulse-1", testReviewFinding(pulsemodules.TechnicalReviewID, "reviewer-filed defect stays"))

	result, err := ReconcilePulseActionableBacklog(ctx, workspacePath)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RetiredLegacyObservations != 2 {
		t.Fatalf("retired = %d, want the 2 step-filed rows", result.RetiredLegacyObservations)
	}
	active := activeRunConcernRows(t, workspacePath)
	if len(active) != 1 || active[0].Text != "reviewer-filed defect stays" {
		t.Fatalf("active after reconcile = %+v", active)
	}
}

// A pass is done only when nothing it owns is left open: every open workflow
// issue counts, however old, except one waiting on the user's decision.
// Parked states from retired dispositions are reopened first.
func TestPassMustCloseEveryOpenWorkflowIssue(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	recordTestReviewFinding(t, workspacePath, "pulse-old", testReviewFinding(pulsemodules.TechnicalReviewID, "old unrouted defect"))
	handoff := testReviewFinding(pulsemodules.TechnicalReviewID, "routed to the fixer")
	handoff.RecommendedRoute = pulseFindingRouteFixerHandoff
	recordTestReviewFinding(t, workspacePath, "pulse-old", handoff)
	queued := recordTestReviewFinding(t, workspacePath, "pulse-old", testReviewFinding(pulsemodules.TechnicalReviewID, "queued for a later pass"))
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=? WHERE fingerprint=?`, ConcernStatusQueuedForEngineering, queued.Fingerprint); err != nil {
		t.Fatal(err)
	}
	db.Close()

	result, err := ReconcilePulseActionableBacklog(ctx, workspacePath)
	if err != nil || result.ReopenedParkedIssues != 1 {
		t.Fatalf("reconcile = %+v (%v), want the queued issue reopened", result, err)
	}
	future := time.Now().UTC().Add(time.Hour)
	if n, err := CountPulseActionableWorkflowIssuesForPass(ctx, workspacePath, future); err != nil || n != 3 {
		t.Fatalf("open issues the pass must close = %d (%v), want all 3", n, err)
	}
}

// Pulse finds an issue and closes it by fixing it; it never waits for
// evidence. New waits are refused, a legacy wait whose review found no
// problem closes, and every other legacy wait goes back to the fixer.
func TestEvidenceWaitsAreRefusedAndRetired(t *testing.T) {
	ctx := context.Background()
	workspacePath := concernsWorkspace(t)
	wait := testReviewFinding(pulsemodules.TechnicalReviewID, "parked until a connection request is accepted")
	wait.RecommendedRoute = pulseFindingRouteEvidenceWait
	wait.NextCheck = "after the request is accepted"
	if _, err := RecordPulseReviewFinding(ctx, workspacePath, "pulse-1", "pulse-1", wait); err == nil || !strings.Contains(err.Error(), "does not wait for evidence") {
		t.Fatalf("filing an evidence wait should be refused, got %v", err)
	}

	noProblem := recordTestReviewFinding(t, workspacePath, "pulse-1", testReviewFinding(pulsemodules.TechnicalReviewID, "cadence works; nothing wrong"))
	realGap := recordTestReviewFinding(t, workspacePath, "pulse-1", testReviewFinding(pulsemodules.TechnicalReviewID, "goal metric never recorded"))
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		t.Fatalf("open db: %v", err)
	}
	park := func(fingerprint, classification string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE pulse_finding_details SET detail_json=json_set(detail_json, '$.recommended_route', 'evidence_wait', '$.classification', ?) WHERE fingerprint=?`, classification, fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status='acknowledged' WHERE fingerprint=?`, fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	park(noProblem.Fingerprint, "no_material_problem")
	park(realGap.Fingerprint, "measurement_gap")
	db.Close()

	result, err := ReconcilePulseActionableBacklog(ctx, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if result.ClosedEvidenceWaits != 1 || result.ReroutedEvidenceWaits != 1 || result.EvidenceWaits != 0 {
		t.Fatalf("reconciliation = %+v, want one closed, one rerouted, none left waiting", result)
	}
	db, _ = openRunConcernsDB(ctx, workspacePath, true)
	defer db.Close()
	var status, route string
	if err := db.QueryRowContext(ctx, `SELECT status FROM run_concerns WHERE fingerprint=?`, noProblem.Fingerprint).Scan(&status); err != nil || status != ConcernStatusResolved {
		t.Fatalf("no-problem wait status = %q (%v), want resolved", status, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT c.status, json_extract(d.detail_json, '$.recommended_route') FROM run_concerns c JOIN pulse_finding_details d ON d.fingerprint=c.fingerprint WHERE c.fingerprint=?`, realGap.Fingerprint).Scan(&status, &route); err != nil || status != ConcernStatusOpen || route != pulseFindingRouteFixerHandoff {
		t.Fatalf("real wait = %q/%q (%v), want open and routed to the fixer", status, route, err)
	}
}
