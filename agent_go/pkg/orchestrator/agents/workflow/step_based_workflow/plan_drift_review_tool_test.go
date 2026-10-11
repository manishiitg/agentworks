package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestValidateStepDriftChecksRejectsEmptySlice(t *testing.T) {
	if err := validateStepDriftChecks(nil); err == nil {
		t.Fatal("expected error for empty checks slice")
	}
}

func TestValidateStepDriftChecksRejectsMissingCheckID(t *testing.T) {
	err := validateStepDriftChecks([]StepDriftCheck{
		{CheckID: "", Status: "pass", Evidence: "a real evidence sentence here"},
	})
	if err == nil {
		t.Fatal("expected error for missing check_id")
	}
}

func TestValidateStepDriftChecksRejectsInvalidStatus(t *testing.T) {
	err := validateStepDriftChecks([]StepDriftCheck{
		{CheckID: "x", Status: "maybe", Evidence: "a real evidence sentence here"},
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestValidateStepDriftChecksRejectsPlaceholderEvidence(t *testing.T) {
	for _, placeholder := range []string{"", "ok", "fine", "n/a", "looks good"} {
		err := validateStepDriftChecks([]StepDriftCheck{
			{CheckID: "x", Status: "pass", Evidence: placeholder},
		})
		if err == nil {
			t.Fatalf("expected error for placeholder evidence %q", placeholder)
		}
	}
}

func TestValidateStepDriftChecksAcceptsRealEvidence(t *testing.T) {
	err := validateStepDriftChecks([]StepDriftCheck{
		{CheckID: "report_query_compatibility", Status: "pass", Evidence: "all 3 report queries ran cleanly against the current schema"},
		{CheckID: "step_description_accuracy", Status: "fail", Evidence: "description says it reads leads; step only reads campaigns", FindingID: "PUL-1234ABCD"},
	})
	if err != nil {
		t.Fatalf("expected valid checks to pass, got: %v", err)
	}
}

func TestValidateStepDriftChecksRejectsFailWithoutFindingID(t *testing.T) {
	err := validateStepDriftChecks([]StepDriftCheck{
		{CheckID: "step_description_accuracy", Status: "fail", Evidence: "description says it reads leads; step only reads campaigns"},
	})
	if err == nil {
		t.Fatal("expected an error for a fail-status check with no finding_id")
	}
	if !strings.Contains(err.Error(), "finding_id is required") {
		t.Fatalf("error %q does not mention the finding_id requirement", err.Error())
	}
}

func TestRecordPlanDriftReviewAllowsCompatibilityOnlyCheckSet(t *testing.T) {
	files := map[string]string{
		"planning/plan.json":        `{"steps":[{"id":"step-a","type":"regular"}]}`,
		"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`,
	}
	exec := newPlanDriftReviewTestExecutor(files)
	_, err := exec(context.Background(), map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{map[string]interface{}{
			"check_id": "title_reference_compatibility", "status": "pass",
			"evidence": "The title changed, and no dependent artifact references the display title.",
		}},
	})
	if err != nil {
		t.Fatalf("compatibility-only drift receipt should not require a general design audit: %v", err)
	}
}

func newPlanDriftReviewTestExecutor(files map[string]string) func(context.Context, map[string]interface{}) (string, error) {
	return newPlanDriftReviewTestExecutorForWorkspace("", files)
}

func newPlanDriftReviewTestExecutorForWorkspace(workspacePath string, files map[string]string) func(context.Context, map[string]interface{}) (string, error) {
	readFile := func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}
	return createRecordPlanDriftReviewExecutor(workspacePath, loggerv2.NewNoop(), readFile, writeFile)
}

// A fail-status check must reference a finding that genuinely exists — not
// merely a non-empty string. This is the concrete enforcement of the
// agreed corrective contract's atomicity requirement: record_pulse_finding
// must have already run and persisted before record_plan_drift_review will
// mark that check reviewed.
func TestRecordPlanDriftReviewExecutorRequiresRealFindingForFailStatus(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	finding := structuredFinding(pulsemodules.PlanDriftReviewID, "PLANDRIFT-TEST-1", "step-a's report query no longer resolves")
	finding.StepID = "step-a"
	recordTestReviewFinding(t, ws, "pulse-1", finding)
	findings, err := LoadPulseFindingLifecycles(ctx, ws, "", -1)
	if err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	realID := findings[0].IssueID

	files := map[string]string{"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`}
	executor := newPlanDriftReviewTestExecutorForWorkspace(ws, files)

	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "fail", "evidence": "the query against emails now errors: no such column", "finding_id": "PUL-FABRICATED"},
		},
	}); err == nil || !strings.Contains(err.Error(), "does not match any active Pulse finding") {
		t.Fatalf("expected rejection for a fabricated finding_id, got: %v", err)
	}

	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "fail", "evidence": "the query against emails now errors: no such column", "finding_id": realID},
		},
	}); err != nil {
		t.Fatalf("expected acceptance for a real finding_id %q, got: %v", realID, err)
	}
	var out StepConfigFile
	if err := json.Unmarshal([]byte(files[ws+"/planning/step_config.json"]), &out); err != nil {
		t.Fatalf("decode updated step config: %v", err)
	}
	if len(out.Steps) != 1 || out.Steps[0].AgentConfigs == nil || out.Steps[0].AgentConfigs.DriftReview == nil {
		t.Fatalf("missing persisted drift review: %+v", out.Steps)
	}
	if !out.Steps[0].AgentConfigs.DriftReview.NeedsReview {
		t.Fatal("an unresolved failed drift check must remain due so later reviewers cannot run against a stale plan")
	}
}

// A finding_id that exists but is already resolved must be rejected — a
// closed-out finding does not prove anything is currently being tracked, so
// referencing one is exactly as fraudulent as referencing an unrelated one.
func TestRecordPlanDriftReviewExecutorRejectsResolvedFinding(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	finding := structuredFinding(pulsemodules.PlanDriftReviewID, "PLANDRIFT-RESOLVED-1", "step-a's report query no longer resolves")
	finding.StepID = "step-a"
	recordTestReviewFinding(t, ws, "pulse-1", finding)
	findings, err := LoadPulseFindingLifecycles(ctx, ws, "", -1)
	if err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	realID := findings[0].IssueID
	fingerprint := findings[0].Fingerprint

	db, err := openRunConcernsDB(ctx, ws, false)
	if err != nil || db == nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status=? WHERE fingerprint=?`, ConcernStatusResolved, fingerprint); err != nil {
		db.Close()
		t.Fatalf("mark resolved: %v", err)
	}
	db.Close()

	files := map[string]string{"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`}
	executor := newPlanDriftReviewTestExecutorForWorkspace(ws, files)
	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "fail", "evidence": "the query against emails now errors: no such column", "finding_id": realID},
		},
	}); err == nil || !strings.Contains(err.Error(), "does not match any active Pulse finding") {
		t.Fatalf("expected rejection for a resolved finding_id, got: %v", err)
	}
}

// A finding_id that exists and is active, but was filed against a DIFFERENT
// step, must be rejected — a real active finding does not prove it belongs
// to THIS step's drift failure.
func TestRecordPlanDriftReviewExecutorRejectsFindingForDifferentStep(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	finding := structuredFinding(pulsemodules.PlanDriftReviewID, "PLANDRIFT-OTHERSTEP-1", "step-b's own unrelated issue")
	finding.StepID = "step-b"
	recordTestReviewFinding(t, ws, "pulse-1", finding)
	findings, err := LoadPulseFindingLifecycles(ctx, ws, "", -1)
	if err != nil {
		t.Fatalf("load findings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	realIDForOtherStep := findings[0].IssueID

	files := map[string]string{"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`}
	executor := newPlanDriftReviewTestExecutorForWorkspace(ws, files)
	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "fail", "evidence": "the query against emails now errors: no such column", "finding_id": realIDForOtherStep},
		},
	}); err == nil || !strings.Contains(err.Error(), "does not match any active Pulse finding") {
		t.Fatalf("expected rejection for a finding filed against a different step, got: %v", err)
	}
}

func TestRecordPlanDriftReviewExecutorWritesNewRecord(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`,
	}
	executor := newPlanDriftReviewTestExecutor(files)

	result, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "pass", "evidence": "all report queries ran cleanly against the current schema"},
		},
	})
	if err != nil {
		t.Fatalf("executor returned error: %v", err)
	}
	if !strings.Contains(result, "step-a") {
		t.Fatalf("result = %q, want it to mention the step id", result)
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("updated step_config.json is invalid JSON: %v", err)
	}
	if len(out.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(out.Steps))
	}
	dr := out.Steps[0].AgentConfigs.DriftReview
	if dr == nil {
		t.Fatal("expected drift_review to be set")
	}
	if len(dr.Checks) != 1 || dr.Checks[0].CheckID != "report_query_compatibility" || dr.Checks[0].Status != "pass" {
		t.Fatalf("drift_review.checks = %+v, unexpected", dr.Checks)
	}
	if dr.ReviewedAt == "" || dr.ReviewedBy == "" {
		t.Fatalf("expected reviewed_at/reviewed_by to be populated: %+v", dr)
	}
	if dr.NeedsReview {
		t.Fatalf("a completed review must clear needs_review, got true: %+v", dr)
	}
	// PLAT-259 second independent review: a completed review must stamp the
	// current contract version, or CollectPlanDriftCandidates would treat
	// this brand-new review as already stale.
	if dr.ContractVersion != planDriftReviewContractVersion {
		t.Fatalf("drift_review.contract_version = %d, want %d (the current contract version)", dr.ContractVersion, planDriftReviewContractVersion)
	}
}

func TestRecordPlanDriftReviewExecutorAllowsCompatibilityOnlyAgentReceipt(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/plan.json":        `{"steps":[{"id":"sequence-a","type":"agent","title":"Sequence","description":"Complete the outcome.","items":[{"id":"verify","type":"user_message","message":"Verify the outcome."}]}]}`,
		"planning/step_config.json": `{"steps":[{"id":"sequence-a"}]}`,
	}
	executor := newPlanDriftReviewTestExecutor(files)

	baseCheck := map[string]interface{}{"check_id": "step_description_accuracy", "status": "pass", "evidence": "the description matches the current configured behavior"}
	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "sequence-a",
		"checks":  []interface{}{baseCheck},
	}); err != nil {
		t.Fatalf("expected the executor to accept a targeted compatibility receipt without a general structure audit, got %v", err)
	}

	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "sequence-a",
		"checks": []interface{}{
			baseCheck,
			map[string]interface{}{
				"check_id": agentSequenceBestPracticesDriftCheckID,
				"status":   "pass",
				"evidence": "the sequence owns one coherent outcome, verifies authoritative evidence, repairs gaps, and uses the automatic final gate",
			},
		},
	}); err != nil {
		t.Fatalf("expected the executor to accept an explicitly relevant agent check, got %v", err)
	}
}

// A completed review persists reviewed_through_change_id when the caller
// supplied one, so a later review knows exactly which changelog entries it
// still needs to read.
func TestRecordPlanDriftReviewExecutorPersistsReviewedThroughChangeID(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`,
	}
	executor := newPlanDriftReviewTestExecutor(files)

	if _, err := executor(ctx, map[string]interface{}{
		"step_id":                    "step-a",
		"reviewed_through_change_id": "change-abc123",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "pass", "evidence": "all report queries ran cleanly against the current schema"},
		},
	}); err != nil {
		t.Fatalf("executor returned error: %v", err)
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("updated step_config.json is invalid JSON: %v", err)
	}
	dr := out.Steps[0].AgentConfigs.DriftReview
	if dr == nil || dr.ReviewedThroughChangeID != "change-abc123" {
		t.Fatalf("expected reviewed_through_change_id = %q, got %+v", "change-abc123", dr)
	}
}

func TestRecordPlanDriftReviewExecutorCreatesStepConfigEntryWhenMissing(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[]}`,
	}
	executor := newPlanDriftReviewTestExecutor(files)

	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "brand-new-step",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "orphaned_tables", "status": "pass", "evidence": "all live tables have at least one known reference"},
		},
	}); err != nil {
		t.Fatalf("executor returned error: %v", err)
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("updated step_config.json is invalid JSON: %v", err)
	}
	if len(out.Steps) != 1 || out.Steps[0].ID != "brand-new-step" {
		t.Fatalf("expected a new step_config entry for brand-new-step, got %+v", out.Steps)
	}
	if out.Steps[0].AgentConfigs == nil || out.Steps[0].AgentConfigs.DriftReview == nil {
		t.Fatal("expected drift_review to be set on the newly created entry")
	}
}

func TestRecordPlanDriftReviewExecutorOverwritesPriorRecord(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2020-01-01T00:00:00Z","reviewed_by":"old","checks":[{"check_id":"old_check","status":"fail","evidence":"stale evidence from a prior review pass"}]}}}]}`,
	}
	executor := newPlanDriftReviewTestExecutor(files)

	if _, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "report_query_compatibility", "status": "pass", "evidence": "all report queries ran cleanly against the current schema"},
		},
		"reviewed_by": "pulse:plan_drift_review",
	}); err != nil {
		t.Fatalf("executor returned error: %v", err)
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("updated step_config.json is invalid JSON: %v", err)
	}
	dr := out.Steps[0].AgentConfigs.DriftReview
	if len(dr.Checks) != 1 || dr.Checks[0].CheckID != "report_query_compatibility" {
		t.Fatalf("expected the old record to be fully replaced, got %+v", dr.Checks)
	}
	if dr.ReviewedBy != "pulse:plan_drift_review" {
		t.Fatalf("reviewed_by = %q, want explicit override to be honored", dr.ReviewedBy)
	}
}

func TestRecordPlanDriftReviewExecutorRejectsInvalidChecksWithoutWriting(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a"}]}`,
	}
	original := files["planning/step_config.json"]
	executor := newPlanDriftReviewTestExecutor(files)

	_, err := executor(ctx, map[string]interface{}{
		"step_id": "step-a",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "x", "status": "pass", "evidence": "ok"},
		},
	})
	if err == nil {
		t.Fatal("expected error for placeholder evidence")
	}
	if files["planning/step_config.json"] != original {
		t.Fatal("step_config.json must not be written when validation fails")
	}
}

func TestRecordPlanDriftReviewExecutorRequiresStepID(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"planning/step_config.json": `{"steps":[]}`}
	executor := newPlanDriftReviewTestExecutor(files)

	_, err := executor(ctx, map[string]interface{}{
		"checks": []interface{}{
			map[string]interface{}{"check_id": "x", "status": "pass", "evidence": "a real evidence sentence here"},
		},
	})
	if err == nil {
		t.Fatal("expected error for missing step_id")
	}
}

// A prompt repair is a current-contract check, not a request to execute the
// workflow again. Its evidence and closed stale flag survive persistence even
// when no run artifacts exist.
func TestRecordPlanDriftReviewPersistsPromptRepairWithoutProducingRun(t *testing.T) {
	files := map[string]string{
		"planning/plan.json":        `{"steps":[{"id":"sequence-a","type":"agent"}]}`,
		"planning/step_config.json": `{"steps":[{"id":"sequence-a","agent_configs":{"drift_review":{"needs_review":true}}}]}`,
	}
	executor := newPlanDriftReviewTestExecutor(files)
	_, err := executor(context.Background(), map[string]interface{}{
		"step_id":                    "sequence-a",
		"reviewed_through_change_id": "change-prompt-1",
		"checks": []interface{}{
			map[string]interface{}{"check_id": "agent_best_practices", "status": "pass", "evidence": "Current sequence preserves declared inputs, outcome, and approval ordering."},
			map[string]interface{}{"check_id": "step_prompt_quality", "status": "fixed", "evidence": "Applied step-description.md: removed contradictory field list, retained the supplied schema and approval boundary. No producing run was performed."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatal(err)
	}
	dr := out.Steps[0].AgentConfigs.DriftReview
	if dr == nil || dr.NeedsReview || dr.ReviewedThroughChangeID != "change-prompt-1" {
		t.Fatalf("repair remained pending: %+v", dr)
	}
	if len(dr.Checks) != 2 || dr.Checks[1].CheckID != "step_prompt_quality" || dr.Checks[1].Status != "fixed" {
		t.Fatalf("prompt evidence was not persisted: %+v", dr.Checks)
	}
}
