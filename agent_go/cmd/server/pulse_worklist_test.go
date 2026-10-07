package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/loopclosure"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulseintake"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

func TestTerminalPulseResultProjectsOneActivityItemPerRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/result-activity"
	pulseRunID := "pulse-result-activity"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview:    {Module: pulseModuleTechnicalReview, Due: true, Reason: "Execution evidence is ready."},
		pulseModuleArchitectureReview: {Module: pulseModuleArchitectureReview, Due: true, Reason: "Structural evidence is ready."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModuleTechnicalReview, pulseRunID, "done", "Checked execution and validation health; both are healthy, with no follow-up needed.", nil); err != nil {
		t.Fatalf("record terminal result: %v", err)
	}
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModuleArchitectureReview, pulseRunID, "done", "Checked workflow structure and boundaries; the current design remains appropriate.", nil); err != nil {
		t.Fatalf("record second terminal result: %v", err)
	}

	activity, err := services.ListOrgDashboardNotifications(ctx, workspacePath, 10)
	if err != nil {
		t.Fatalf("read projected Pulse Activity: %v", err)
	}
	if activity.PulseSummary == nil || len(activity.PulseSummary.Reviews) != 2 || len(activity.Recent) != 1 {
		t.Fatalf("terminal result was not projected into one Pulse Activity item: %#v", activity.PulseSummary)
	}
	reviewByModule := map[string]services.PulseReviewSummary{}
	for _, review := range activity.PulseSummary.Reviews {
		reviewByModule[review.Module] = review
	}
	if got := reviewByModule[pulseModuleTechnicalReview]; got.Summary != "Checked execution and validation health; both are healthy, with no follow-up needed." {
		t.Fatalf("Pulse Activity did not use the first stored review result: %#v", got)
	}
}

func TestPulseModuleSchemaMigratesLegacyAdvisorsToNewestStrategicState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/legacy-strategy"
	dbPath := filepath.Join(root, workspacePath, "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, pulseModuleStateSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, pulseModuleAuditSchema); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		module, runID, reason, at string
	}{
		{module: "strategy_auditor", runID: "pulse-old", reason: "older audit", at: "2026-08-17T01:00:00Z"},
		{module: pulseModuleStrategicReview, runID: "pulse-canonical-old", reason: "older canonical", at: "2026-08-17T02:00:00Z"},
		{module: "goal_advisor", runID: "pulse-new", reason: "newest opportunity", at: "2026-08-18T03:00:00Z"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_module_state
			(workspace_path,module,last_pulse_run_id,last_reason,updated_at) VALUES (?,?,?,?,?)`,
			workspacePath, row.module, row.runID, row.reason, row.at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_module_audit
			(workspace_path,module,pulse_run_id,result,reason,recorded_at) VALUES (?,?,?,?,?,?)`,
			workspacePath, row.module, row.runID, "done", row.reason, row.at); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	states, err := getPulseModuleStates(ctx, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	var strategic *PulseModuleState
	for i := range states {
		if states[i].Module == "strategy_auditor" || states[i].Module == "goal_advisor" {
			t.Fatalf("legacy live module survived migration: %#v", states[i])
		}
		if states[i].Module == pulseModuleStrategicReview {
			strategic = &states[i]
		}
	}
	if strategic == nil || strategic.LastPulseRunID != "pulse-new" || strategic.LastReason != "newest opportunity" {
		t.Fatalf("newest legacy state did not become canonical: %#v", strategic)
	}

	_, db, err = openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var legacyState, legacyAudit int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_module_state WHERE module IN ('strategy_auditor','goal_advisor')`).Scan(&legacyState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_module_audit WHERE module IN ('strategy_auditor','goal_advisor')`).Scan(&legacyAudit); err != nil {
		t.Fatal(err)
	}
	if legacyState != 0 || legacyAudit != 0 {
		t.Fatalf("legacy rows remain: state=%d audit=%d", legacyState, legacyAudit)
	}
	var canonicalAudits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_module_audit WHERE module=?`, pulseModuleStrategicReview).Scan(&canonicalAudits); err != nil {
		t.Fatal(err)
	}
	if canonicalAudits != 3 {
		t.Fatalf("canonical audit history=%d, want 3", canonicalAudits)
	}
}

func TestPulseModuleSchemaMergesLegacyEngineeringAndOpsIntoTechnicalReview(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/legacy-technical"
	dbPath := filepath.Join(root, workspacePath, "db", "db.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{pulseModuleStateSchema, pulseModuleAuditSchema, pulseReviewFocusStateSchema, pulseReviewFocusHistorySchema} {
		if _, err := db.ExecContext(ctx, schema); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		module, runID, reason, at string
	}{
		{module: pulseModuleWorkflowReview, runID: "pulse-shared", reason: "engineering evidence", at: "2026-08-17T01:00:00Z"},
		{module: pulseModuleTechnicalReview, runID: "pulse-technical-old", reason: "older canonical", at: "2026-08-17T02:00:00Z"},
		{module: pulseModuleLLMOpsReview, runID: "pulse-shared", reason: "newest runtime evidence", at: "2026-08-18T03:00:00Z"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_module_state
			(workspace_path,module,last_pulse_run_id,last_reason,updated_at) VALUES (?,?,?,?,?)`,
			workspacePath, row.module, row.runID, row.reason, row.at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_module_audit
			(workspace_path,module,pulse_run_id,result,reason,recorded_at) VALUES (?,?,?,?,?,?)`,
			workspacePath, row.module, row.runID, "done", row.reason, row.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_review_focus_state
		(workspace_path,module,focus_key,last_pulse_run_id,last_reviewed_at,last_verdict,last_selection_reason,updated_at)
		VALUES (?,?,?,?,?,?,?,?),(?,?,?,?,?,?,?,?)`,
		workspacePath, pulseModuleWorkflowReview, "execution_correctness", "pulse-shared", "2026-08-17T01:00:00Z", "finding", "engineering selected it", "2026-08-17T01:00:00Z",
		workspacePath, pulseModuleLLMOpsReview, "execution_correctness", "pulse-shared", "2026-08-18T03:00:00Z", "finding", "ops selected it later", "2026-08-18T03:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pulse_review_focus_history
		(workspace_path,module,pulse_run_id,focus_key,priority_class,selection_reason,recorded_at)
		VALUES (?,?,?,?,?,?,?),(?,?,?,?,?,?,?)`,
		workspacePath, pulseModuleWorkflowReview, "pulse-shared", "execution_correctness", "material_change", "engineering history", "2026-08-17T01:00:00Z",
		workspacePath, pulseModuleLLMOpsReview, "pulse-shared", "execution_efficiency", "overdue", "ops history", "2026-08-18T03:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	states, err := getPulseModuleStates(ctx, workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	var technical *PulseModuleState
	for i := range states {
		if states[i].Module == pulseModuleWorkflowReview || states[i].Module == pulseModuleLLMOpsReview {
			t.Fatalf("legacy technical module survived migration: %#v", states[i])
		}
		if states[i].Module == pulseModuleTechnicalReview {
			technical = &states[i]
		}
	}
	if technical == nil || technical.LastPulseRunID != "pulse-shared" || technical.LastReason != "newest runtime evidence" {
		t.Fatalf("newest legacy technical state did not become canonical: %#v", technical)
	}

	_, db, err = openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, want := range map[string]int{
		"pulse_module_state":         1,
		"pulse_module_audit":         2,
		"pulse_review_focus_state":   1,
		"pulse_review_focus_history": 2,
	} {
		var got int
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE module=?`, table)
		if err := db.QueryRowContext(ctx, query, pulseModuleTechnicalReview).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s canonical rows=%d, want %d", table, got, want)
		}
	}
	var focusRun, focusReason string
	if err := db.QueryRowContext(ctx, `SELECT last_pulse_run_id,last_selection_reason FROM pulse_review_focus_state
		WHERE workspace_path=? AND module=? AND focus_key='execution_health'`, workspacePath, pulseModuleTechnicalReview).Scan(&focusRun, &focusReason); err != nil {
		t.Fatal(err)
	}
	if focusRun != "pulse-shared" || focusReason != "ops selected it later" {
		t.Fatalf("newest focus state was not preserved: run=%q reason=%q", focusRun, focusReason)
	}
	var auditReason string
	if err := db.QueryRowContext(ctx, `SELECT reason FROM pulse_module_audit
		WHERE workspace_path=? AND module=? AND pulse_run_id='pulse-shared'`, workspacePath, pulseModuleTechnicalReview).Scan(&auditReason); err != nil {
		t.Fatal(err)
	}
	if auditReason != "newest runtime evidence" {
		t.Fatalf("newest same-run audit was not preserved: %q", auditReason)
	}
}

func TestValidateReviewerVerificationDispositionsRequiresLifecycleApplication(t *testing.T) {
	review := []step_based_workflow.PulseReviewVerificationResult{{
		FindingID: "ISS-9", Fingerprint: "fp-9", AttemptID: "fix-9",
		Verdict:  step_based_workflow.VerificationPassed,
		Expected: "new run contains value", Observed: "run-12 contains 42",
	}}
	if err := validateReviewerVerificationDispositions(review, nil); err == nil {
		t.Fatal("verification-only module must not become terminal without a disposition")
	}
	dispositions := []step_based_workflow.PulseFindingDisposition{{
		FindingID: "ISS-9", Fingerprint: "fp-9", AttemptID: "fix-9",
		Disposition: step_based_workflow.FindingDispositionFixedVerified,
		Verification: []step_based_workflow.PulseFindingVerification{{
			Verdict:  step_based_workflow.VerificationPassed,
			Expected: "new run contains value", Observed: "run-12 contains 42",
		}},
	}}
	if err := validateReviewerVerificationDispositions(review, dispositions); err != nil {
		t.Fatalf("matching disposition rejected: %v", err)
	}
}

// The three cross-checks (disposition value, verification proof, next_check
// boundary) are independent — nothing about the disposition value being wrong
// depends on whether the next_check text also mismatches — so all of them must
// be reported in one rejection.
//
// On 2026-08-04 finding PUL-70B1057E took three separate record_pulse_result
// rejections to clear this function alone, because it returned on the first
// mismatch and only revealed the next one after the caller fixed it: wrong
// disposition value, then a verification proof that didn't match the
// reviewer's evidence, then a next_check that didn't match the reviewer's
// boundary text.
func TestValidateReviewerVerificationDispositionsCombinesAllMismatches(t *testing.T) {
	review := []step_based_workflow.PulseReviewVerificationResult{{
		FindingID: "PUL-70B1057E", Fingerprint: "fp-70b1",
		Verdict:   step_based_workflow.VerificationInconclusive,
		Expected:  "widened selector pool applied",
		Observed:  "selector pool unchanged",
		NextCheck: "next default/reddit run after 2026-08-04T02:00Z",
	}}
	dispositions := []step_based_workflow.PulseFindingDisposition{{
		FindingID: "PUL-70B1057E", Fingerprint: "fp-70b1",
		// Wrong disposition value, a verification proof that doesn't match the
		// reviewer's evidence, and a next_check that doesn't match the
		// reviewer's boundary — three independent mismatches at once.
		Disposition: step_based_workflow.FindingDispositionAwaitingRun,
		Verification: []step_based_workflow.PulseFindingVerification{{
			Verdict:  step_based_workflow.VerificationInconclusive,
			Expected: "something else entirely",
			Observed: "something else entirely",
		}},
		NextCheck: "a different boundary",
	}}
	err := validateReviewerVerificationDispositions(review, dispositions)
	if err == nil {
		t.Fatal("mismatched disposition accepted")
	}
	for _, want := range []string{
		`requires disposition "changed_unverified", got "awaiting_run"`,
		"must carry the reviewer's structured inconclusive proof",
		"next_check must match the reviewer boundary",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection %q is missing %q", err.Error(), want)
		}
	}
}

func TestPulseWorklistUsesWorkflowLocalDB(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	dbPath := filepath.Join(root, "Workflow", "example", "db", "db.sqlite")

	states, err := getPulseModuleStates(ctx, workspacePath)
	if err != nil {
		t.Fatalf("list before create: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("list before create returned %d states, want 0", len(states))
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("read-only list created db unexpectedly: stat err=%v", err)
	}

	recorded, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-1", completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {
			Module:       pulseModuleTechnicalReview,
			Due:          true,
			Reason:       "Latest run skipped a required step.",
			Evidence:     []string{"runs/iteration-0/logs/step-a"},
			CooldownRuns: 0,
		},
	}))
	if err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if len(recorded) != len(pulseModuleOrder) {
		t.Fatalf("recorded states = %d, want %d", len(recorded), len(pulseModuleOrder))
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected workflow-local db at %s: %v", dbPath, err)
	}

	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, "pulse-run-1")
	if err != nil {
		t.Fatalf("get worklist: %v", err)
	}
	if !ok {
		t.Fatal("get worklist ok=false, want true")
	}
	if got := worklist[pulseModuleTechnicalReview].LastDecision; got != "due" {
		t.Fatalf("workflow review decision = %q, want due", got)
	}

	updated, err := markPulseModuleResult(ctx, workspacePath, pulseModuleTechnicalReview, "pulse-run-1", "changed", "Bug Review fixed the skipped step.", []string{"builder/improve.html#decision"})
	if err != nil {
		t.Fatalf("mark result: %v", err)
	}
	if updated.LastDecision != "due" || updated.LastResult != "changed" || updated.LastRanAt == "" {
		t.Fatalf("updated state mismatch: %+v", updated)
	}

	timedOut, err := markPulseModuleResult(ctx, workspacePath, pulseModuleTechnicalReview, "pulse-run-1", "timed_out", "Bug Review exceeded the scheduler wait limit.", []string{"scheduler timeout"})
	if err != nil {
		t.Fatalf("mark timed-out result: %v", err)
	}
	if timedOut.LastResult != "timed_out" || timedOut.LastResultReason == "" {
		t.Fatalf("timed-out state mismatch: %+v", timedOut)
	}
}

func TestGetPulseReviewsAPIListsCompactReviewReceipts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	if err := step_based_workflow.CompletePulseReview(
		context.Background(), workspacePath, []string{pulseModuleWorkflowReview},
		"2026-07-31T08-00-00.000Z_pulse-1", "pulse-1", "A real issue was found.", "completed",
	); err != nil {
		t.Fatalf("record review: %v", err)
	}

	api := &StreamingAPI{}
	listRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/workflow/pulse-reviews?workspace_path=Workflow%2Fexample&module=workflow_review",
		nil,
	)
	listResponse := httptest.NewRecorder()
	api.handleGetPulseReviews(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listResponse.Code, listResponse.Body.String())
	}
	var listBody struct {
		Success bool `json:"success"`
		Reviews []struct {
			ID                int64  `json:"id"`
			Verdict           string `json:"verdict"`
			FindingCount      int    `json:"finding_count"`
			VerificationCount int    `json:"verification_count"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if !listBody.Success || len(listBody.Reviews) != 1 || listBody.Reviews[0].ID <= 0 {
		t.Fatalf("list response = %+v", listBody)
	}
	if got := listBody.Reviews[0].Verdict; got != "A real issue was found." {
		t.Fatalf("verdict = %q", got)
	}
	if listBody.Reviews[0].FindingCount != 0 || listBody.Reviews[0].VerificationCount != 0 {
		t.Fatalf("unexpected receipt counts: %+v", listBody.Reviews[0])
	}
}

func TestGetPulseAgentMetricsAPIExposesReviewersAndFixer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	ctx := context.Background()
	for _, metric := range []step_based_workflow.PulseAgentMetricRecord{
		{ExecutionID: "review-1-exec", PulseRunID: "pulse-1", ReviewRunID: "review-1", Module: "strategy_auditor", Role: "reviewer", Status: "completed", DurationMS: 1200},
		{ExecutionID: "fixer-1-exec", PulseRunID: "pulse-1", ReviewRunID: "review-1", Module: "pulse_fixer", Role: "fixer", Status: "completed", DurationMS: 2400},
	} {
		if err := step_based_workflow.RecordPulseAgentMetric(ctx, workspacePath, metric); err != nil {
			t.Fatalf("record metric: %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodGet,
		"/api/workflow/pulse-agent-metrics?workspace_path=Workflow%2Fexample&pulse_run_id=pulse-1", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetPulseAgentMetrics(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Success bool                                         `json:"success"`
		Metrics []step_based_workflow.PulseAgentMetricRecord `json:"metrics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || len(body.Metrics) != 2 {
		t.Fatalf("response = %#v", body)
	}
	if body.Metrics[0].Role != "fixer" || body.Metrics[1].Role != "reviewer" {
		t.Fatalf("newest metrics order/content = %#v", body.Metrics)
	}
}

func TestPulseWorklistRequiresCompleteModuleSet(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"

	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-1", []PulseWorklistDecision{
		{Module: pulseModuleWorkflowReview, Due: true, Reason: "A step failed."},
	}); err == nil {
		t.Fatal("recordPulseWorklist accepted a partial module list")
	}

	duplicates := completePulseWorklistDecisions(nil)
	duplicates[len(duplicates)-1].Module = pulseModuleWorkflowReview
	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-2", duplicates); err == nil {
		t.Fatal("recordPulseWorklist accepted duplicate modules")
	}
}

func TestPulseWorklistLetsGateSelectTheEvidenceJustifiedModules(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	decisions := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview:  {Module: pulseModuleWorkflowReview, Due: true, Reason: "Engineering evidence requires review."},
		pulseModuleLLMOpsReview:    {Module: pulseModuleLLMOpsReview, Due: true, Reason: "Operational evidence requires review."},
		pulseModuleStrategicReview: {Module: pulseModuleStrategicReview, Due: true, Reason: "Strategy evidence requires review."},
	})
	if _, err := recordPulseWorklist(ctx, "Workflow/example", "pulse-run-agentic-selection", decisions); err != nil {
		t.Fatalf("Gate's three evidence-justified modules were rejected: %v", err)
	}
}

func TestPulseWorklistRuntimeSignalsDoNotReserveReviewSlot(t *testing.T) {
	for _, fixture := range []struct{ name, status, timing, kind string }{
		{"child_error", "completed", `{"step_id":"step-a","tools":{"errored_count":1}}`, "completed_run_child_errors"},
		{"structured_failure", "completed", `{"step_id":"step-a","tools":{"calls":[{"tool_name":"shell","status":"success","result":{"exit_code":1}}]}}`, "tool_success_with_structured_failure"},
		{"known_failed_run", "failed", `{}`, "run_not_completed"},
	} {
		for _, selected := range []string{"observe", pulseModuleStrategicReview, pulseModuleTechnicalReview} {
			t.Run(fixture.name+"/"+selected, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("WORKSPACE_DOCS_PATH", root)
				workspacePath := "Workflow/example"
				runDir := filepath.Join(root, workspacePath, "runs", "iteration-0", "default")
				if err := os.MkdirAll(filepath.Join(runDir, "logs"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(runDir, "run_metadata.json"), []byte(`{"status":"`+fixture.status+`"}`), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(runDir, "logs", "execution-attempt-0-timing.json"), []byte(fixture.timing), 0o644); err != nil {
					t.Fatal(err)
				}
				intake := pulseintake.CheckRuntime(workspacePath, time.Now())
				if len(intake.Findings) != 1 || intake.Findings[0].Kind != fixture.kind {
					t.Fatalf("runtime evidence lost: %+v", intake)
				}
				decisions := completePulseWorklistDecisions(nil)
				for i := range decisions {
					decisions[i].Reason = "Reviewed prior disposition and required outputs; no new unresolved impact. Wait for comparable runs."
					decisions[i].Evidence = []string{"runs/iteration-0/default/run_metadata.json", "runs/iteration-0/default/logs/execution-attempt-0-timing.json"}
					if decisions[i].Module == selected {
						decisions[i].Due = true
						decisions[i].Reason = "New relevant evidence warrants this focused review."
					}
				}
				if _, err := recordPulseWorklist(context.Background(), workspacePath, "pulse-runtime-check", decisions); err != nil {
					t.Fatalf("Gate judgment rejected: %v", err)
				}
				worklist, ok, err := getPulseWorklistForRun(context.Background(), workspacePath, "pulse-runtime-check")
				if err != nil || !ok {
					t.Fatalf("read recorded worklist: %v", err)
				}
				for module, state := range worklist {
					if (state.LastDecision == "due") != (module == selected) {
						t.Fatalf("selection changed: %+v", state)
					}
				}
			})
		}
	}
}

func TestPulseSkippedChecksPreserveActualReviewBaseline(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	ws := "Workflow/evidence-window"
	for _, module := range []string{pulseModuleTechnicalReview, pulseModuleStrategicReview} {
		id := "review-" + module
		if _, err := recordPulseWorklist(ctx, ws, id, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
			module: {Module: module, Due: true, Reason: "Review current evidence."},
		})); err != nil {
			t.Fatal(err)
		}
		completed, err := markPulseModuleResult(ctx, ws, module, id, "done", "Need three more comparable growth runs.", []string{"runs/iteration-0/default"})
		if err != nil {
			t.Fatal(err)
		}
		for pass := 0; pass < 3; pass++ {
			decisions := completePulseWorklistDecisions(nil)
			for i := range decisions {
				decisions[i].Reason = "Only one additional comparable run; retain the existing assessment boundary."
				decisions[i].NextCheckAt = "2099-01-01"
			}
			if _, err := recordPulseWorklist(ctx, ws, fmt.Sprintf("wait-%s-%d", module, pass), decisions); err != nil {
				t.Fatal(err)
			}
			states, err := getPulseModuleStates(ctx, ws)
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range states {
				if state.Module == module && (state.LastRanAt != completed.LastRanAt || state.NextCheckAt != "2099-01-01") {
					t.Fatalf("Gate skip overwrote actual review baseline/boundary: %+v", state)
				}
			}
			receipts, err := latestPulseReviewReceipts(ctx, ws)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, receipt := range receipts {
				if receipt.Module == module {
					found = true
					if receipt.Reason != completed.LastResultReason || receipt.RecordedAt != completed.LastRanAt || receipt.PulseRunID != id {
						t.Fatalf("lost actual review receipt after skip: %+v", receipt)
					}
				}
			}
			if !found {
				t.Fatal("missing actual review receipt")
			}
		}
	}
}

func TestPulseModuleViewExposesActualReviewAfterSkip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ctx := context.Background()
	ws := "Workflow/receipts"
	if receipts, err := latestPulseReviewReceipts(ctx, ws); err != nil || len(receipts) != 0 {
		t.Fatalf("empty history: %v %v", receipts, err)
	}
	if _, err := os.Stat(filepath.Join(root, ws, "db", "db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("history read created database: %v", err)
	}
	if _, err := markPulseModuleResult(ctx, ws, pulseModuleTechnicalReview, "review-old", "done", "Recovery verified; collect more growth outcomes.", []string{"runs/iteration-0/default/validation.json"}); err != nil {
		t.Fatal(err)
	}
	// A more recent scheduler skip must not masquerade as an actual review.
	if _, err := markPulseModuleResult(ctx, ws, pulseModuleTechnicalReview, "skip-new", "skipped", "Still waiting.", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := markPulseModuleResult(ctx, "Workflow/other", pulseModuleTechnicalReview, "other", "done", "Other workflow", nil); err != nil {
		t.Fatal(err)
	}
	view, err := readPulseModuleStateView(ctx, ws, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Receipts     []PulseModuleAudit `json:"last_review_receipts"`
		ReceiptError string             `json:"last_review_receipts_error"`
	}
	if err := json.Unmarshal([]byte(view), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Receipts) != 1 || payload.Receipts[0].PulseRunID != "review-old" || payload.Receipts[0].Reason != "Recovery verified; collect more growth outcomes." || len(payload.Receipts[0].Evidence) != 1 || payload.ReceiptError != "" {
		t.Fatalf("Gate missing isolated review baseline: %+v", payload)
	}
	_, db, err := openPulseModuleStateDB(ctx, ws, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `UPDATE pulse_module_audit SET evidence_json='not-json' WHERE pulse_run_id='review-old'`); err != nil {
		t.Fatal(err)
	}
	view, err = readPulseModuleStateView(ctx, ws, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(view), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ReceiptError == "" || len(payload.Receipts) != 0 {
		t.Fatalf("history read failure hidden: %+v", payload)
	}
}

func TestPulseWorklistDecisionRejectsReviewPlanFields(t *testing.T) {
	_, err := pulseWorklistDecisionsFromArgs([]interface{}{map[string]interface{}{
		"module":  pulseModuleTechnicalReview,
		"due":     true,
		"reason":  "Runtime evidence requires review.",
		"focuses": []interface{}{"execution_health"},
	}})
	if err == nil || !strings.Contains(err.Error(), `unknown field "focuses"`) {
		t.Fatalf("review-plan field must not be accepted by the small worklist receipt: %v", err)
	}
}

func TestTrustedPulseWorklistKeepsFirstCompleteGateDecision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--gate"
	sessionID := "schedule-cron--gate-recovery"
	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)

	first := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {Module: pulseModuleWorkflowReview, Due: true, Reason: "The run failed a required check."},
	})
	if _, err := recordPulseWorklistOnce(ctx, workspacePath, pulseRunID, first); err != nil {
		t.Fatalf("record first worklist: %v", err)
	}

	late := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {Module: pulseModuleWorkflowReview, Due: false, Reason: "Late stale decision.", CooldownRuns: 3},
	})
	states, err := recordPulseWorklistOnce(ctx, workspacePath, pulseRunID, late)
	if err != nil {
		t.Fatalf("record late worklist: %v", err)
	}
	for _, state := range states {
		if state.Module == pulseModuleWorkflowReview && state.LastDecision != "due" {
			t.Fatalf("late worklist replaced first Gate decision: %+v", state)
		}
	}
}

func TestPulseWorklistValidatesCadenceHints(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"

	missingCadence := completePulseWorklistDecisions(nil)
	missingCadence[0].CooldownRuns = 0
	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-missing-cadence", missingCadence); err == nil || !strings.Contains(err.Error(), "must include next_check_at") {
		t.Fatalf("missing skipped cadence error = %v", err)
	}

	invalidDate := completePulseWorklistDecisions(nil)
	invalidDate[0].NextCheckAt = "next Tuesday"
	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-invalid-date", invalidDate); err == nil || !strings.Contains(err.Error(), "must be RFC3339 or YYYY-MM-DD") {
		t.Fatalf("invalid next-check date error = %v", err)
	}

	negativeCooldown := completePulseWorklistDecisions(nil)
	negativeCooldown[0].CooldownRuns = -1
	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-negative-cooldown", negativeCooldown); err == nil || !strings.Contains(err.Error(), "must be non-negative") {
		t.Fatalf("negative cooldown error = %v", err)
	}

	dateOnly := completePulseWorklistDecisions(nil)
	dateOnly[0].CooldownRuns = 0
	dateOnly[0].NextCheckAt = "2026-07-12"
	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-date-only", dateOnly); err != nil {
		t.Fatalf("date-only cadence rejected: %v", err)
	}
}

func TestPulseWorklistSkipsOwnerDisabledReviewer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/selective-pulse"
	workflowDir := filepath.Join(root, workspacePath)
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Selective Pulse")
	manifest.Pulse = &WorkflowPulseConfig{Enabled: true, DisabledReviewModules: []string{pulseModuleStrategicReview}}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "workflow.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{manifestPath(workspacePath): string(manifestBytes)}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	pulseRunID := "pulse-owner-disabled"
	decisions := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleStrategicReview: {
			Module:   pulseModuleStrategicReview,
			Due:      true,
			Reason:   "Gate requested a strategy review.",
			Evidence: []string{"run:changed"},
		},
	})
	states, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, decisions)
	if err != nil {
		t.Fatalf("record Pulse worklist: %v", err)
	}
	var strategic *PulseModuleState
	for i := range states {
		if states[i].Module == pulseModuleStrategicReview {
			strategic = &states[i]
			break
		}
	}
	if strategic == nil {
		t.Fatal("strategic reviewer state was not recorded")
	}
	if strategic.LastDecision != "skipped" || !strings.Contains(strategic.LastReason, "workflow owner") {
		t.Fatalf("disabled strategy reviewer was not durably skipped: %+v", strategic)
	}
	if due, dueErr := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModuleStrategicReview); dueErr != nil {
		t.Fatalf("inspect strategy due state: %v", dueErr)
	} else if due {
		t.Fatal("disabled strategy reviewer must not be dispatched")
	}
}

func TestPulseWorklistToolArgumentsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want string
	}{
		{
			name: "missing due",
			item: map[string]interface{}{"module": pulseModuleWorkflowReview, "reason": "test"},
			want: ".due is required and must be boolean",
		},
		{
			name: "decision alias",
			item: map[string]interface{}{"module": pulseModuleWorkflowReview, "decision": "due", "reason": "test"},
			want: `unknown field "decision"`,
		},
		{
			name: "status alias",
			item: map[string]interface{}{"module": pulseModuleWorkflowReview, "status": "due", "reason": "test"},
			want: `unknown field "status"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pulseWorklistDecisionsFromArgs([]interface{}{tt.item})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parse error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRecordPulseWorklistToolRequiresACompleteTypedWorklist(t *testing.T) {
	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_worklist"].(func(context.Context, map[string]interface{}) (string, error))

	if _, err := execute(context.Background(), map[string]interface{}{"pulse_run_id": "probe"}); err == nil || !strings.Contains(err.Error(), "decisions must be an array") {
		t.Fatalf("incomplete worklist error = %v", err)
	}
}

func TestCurrentPulseRunUsesActiveSessionWithoutLease(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	sessionID := "workflow-builder-current-pulse"
	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)
	_, executors, _ := createPulseWorklistTools()
	record := executors["record_pulse_worklist"].(func(context.Context, map[string]interface{}) (string, error))
	decisions := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {Due: true, Reason: "Manual engineering review."},
	})
	decisionArgs := make([]interface{}, 0, len(decisions))
	for _, decision := range decisions {
		decisionArgs = append(decisionArgs, map[string]interface{}{
			"module": decision.Module, "due": decision.Due, "reason": decision.Reason,
			"cooldown_runs": decision.CooldownRuns,
		})
	}
	if _, err := record(ctx, map[string]interface{}{
		"workspace_path": workspacePath,
		"pulse_run_id":   "current",
		"mode":           pulseRunModeBacklogDrain,
		"mode_reason":    "A retained repair queue is ready to verify and drain.",
		"decisions":      decisionArgs,
	}); err != nil {
		t.Fatalf("record current-session worklist: %v", err)
	}
	if err := validatePulseToolRunID(ctx, "current"); err != nil {
		t.Fatalf("current session should authorize its own Pulse identity: %v", err)
	}
	states, err := getPulseModuleStates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("get module states: %v", err)
	}
	for _, state := range states {
		if state.LastPulseRunID != sessionID {
			t.Fatalf("current identity persisted as %q for module %s, want session %q", state.LastPulseRunID, state.Module, sessionID)
		}
	}
}

func TestPulseWorklistPersistsAgentSelectedModeAndKeepsFirstGateDecision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--backlog-drain"
	ctx := mcpexecutor.WithSessionID(context.Background(), pulseRunID)
	_, executors, _ := createPulseWorklistTools()
	record := executors["record_pulse_worklist"].(func(context.Context, map[string]interface{}) (string, error))
	decisions := pulseWorklistDecisionToolArgs(completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {Due: true, Reason: "Prior repairs now have run evidence."},
	}))
	args := map[string]interface{}{
		"workspace_path": workspacePath,
		"pulse_run_id":   pulseRunID,
		"mode":           pulseRunModeBacklogDrain,
		"mode_reason":    "Existing issues provide verification and repair work; broad discovery would be redundant.",
		"decisions":      decisions,
	}
	if _, err := record(ctx, args); err != nil {
		t.Fatalf("record backlog-drain worklist: %v", err)
	}
	mode, err := getPulseRunMode(context.Background(), workspacePath, pulseRunID)
	if err != nil || mode == nil {
		t.Fatalf("get persisted mode = %#v, %v", mode, err)
	}
	if mode.Mode != pulseRunModeBacklogDrain {
		t.Fatalf("mode = %q, want %q", mode.Mode, pulseRunModeBacklogDrain)
	}
	// A retry must preserve Gate's first decision rather than rewriting the
	// scheduled sequence after children may already have read it.
	args["mode"] = pulseRunModeDiscovery
	args["mode_reason"] = "A later retry should not replace the original Gate decision."
	if _, err := record(ctx, args); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	mode, err = getPulseRunMode(context.Background(), workspacePath, pulseRunID)
	if err != nil || mode == nil || mode.Mode != pulseRunModeBacklogDrain {
		t.Fatalf("retry rewrote persisted mode: %#v, %v", mode, err)
	}
	state, err := readPulseModuleStateView(context.Background(), workspacePath, pulseRunID)
	if err != nil || !strings.Contains(state, `"mode":"backlog_drain"`) {
		t.Fatalf("module state must expose persisted mode, state=%s err=%v", state, err)
	}
}

func TestMarkPulseModuleResultStoresMinimalDurableAudit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--audit"
	sessionID := "schedule-cron--audit-session"
	if _, err := recordPulseWorklist(context.Background(), workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "A verified repair is required."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}

	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)
	_, executors, _ := createPulseWorklistTools()
	recordTestReviewFinding(t, ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview, "stale run binding in planning/step_config.json")
	findings, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, pulseModuleTechnicalReview, 10)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load reviewer finding: findings=%+v err=%v", findings, err)
	}
	selected := findings[0]
	if selected.FindingID == "" || selected.FindingID != selected.Issue.ID || !strings.HasPrefix(selected.FindingID, "PUL-") {
		t.Fatalf("legacy finding did not receive compact issue identity: %+v", selected)
	}
	execute := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))
	_, err = execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath,
		"pulse_run_id":   pulseRunID,
		"module":         pulseModuleTechnicalReview,
		"result":         "changed",
		"reason":         "Fixed the stale run binding.",
		"evidence":       []string{"pulse/reviews/audit/workflow_review.md"},
		"changed_files":  []string{"planning/step_config.json"},
		"verification":   []string{"targeted binding test passed"},
		"before_refs":    []string{"step_config:sha256:before"},
		"after_refs":     []string{"step_config:sha256:after"},
		"finding_dispositions": []map[string]interface{}{{
			"issue_id":      selected.Issue.ID,
			"disposition":   "fixed_verified",
			"summary":       "The stale run binding was corrected and the targeted test passed.",
			"changed_files": []string{"planning/step_config.json"},
			"before_refs":   []string{"step_config:sha256:before"},
			"after_refs":    []string{"step_config:sha256:after"},
			"verification": []map[string]interface{}{{
				"check":    "targeted binding test",
				"verdict":  "passed",
				"expected": "the configured run binding resolves",
				"observed": "the configured run binding resolved",
				"evidence": []string{"go test ./cmd/server -run TestRunBinding"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("mark module result: %v", err)
	}

	_, db, err := openPulseModuleStateDB(context.Background(), workspacePath, false)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer db.Close()
	var result, reason, changedFilesJSON, verificationJSON, beforeRefsJSON, afterRefsJSON, recordedAt string
	err = db.QueryRow(`SELECT result, reason, changed_files_json, verification_json,
		before_refs_json, after_refs_json, recorded_at
		FROM pulse_module_audit WHERE workspace_path=? AND module=? AND pulse_run_id=?`,
		workspacePath, pulseModuleTechnicalReview, pulseRunID,
	).Scan(&result, &reason, &changedFilesJSON, &verificationJSON, &beforeRefsJSON, &afterRefsJSON, &recordedAt)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if result != "changed" || reason != "Fixed the stale run binding." || recordedAt == "" {
		t.Fatalf("audit identity mismatch: result=%q reason=%q recorded_at=%q", result, reason, recordedAt)
	}
	for name, tc := range map[string]struct {
		raw  string
		want string
	}{
		"changed_files": {changedFilesJSON, "planning/step_config.json"},
		"verification":  {verificationJSON, "targeted binding test passed"},
		"before_refs":   {beforeRefsJSON, "step_config:sha256:before"},
		"after_refs":    {afterRefsJSON, "step_config:sha256:after"},
	} {
		var values []string
		if err := json.Unmarshal([]byte(tc.raw), &values); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if len(values) != 1 || values[0] != tc.want {
			t.Fatalf("%s = %v, want [%q]", name, values, tc.want)
		}
	}
	lifecycles, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, pulseModuleWorkflowReview, 10)
	if err != nil {
		t.Fatalf("load finding lifecycles: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Status != step_based_workflow.ConcernStatusResolved {
		t.Fatalf("finding lifecycle not closed: %+v", lifecycles)
	}
	if len(lifecycles[0].Attempts) != 1 || len(lifecycles[0].Verification) != 1 ||
		lifecycles[0].Verification[0].Verdict != step_based_workflow.VerificationPassed {
		t.Fatalf("finding fix evidence missing: %+v", lifecycles[0])
	}
}

// PLAT-206: the split reviewer/Fixer contract legitimately produces two
// terminal-shaped calls to record_pulse_result for the SAME (module,
// pulse_run_id) -- the reviewer's own "done" (nothing more to review) and
// the Fixer's later "changed" (files were modified), dispatched as separate
// background agents per the pulse-review-fixer split. Before this fix, the
// retry path only accepted a second call whose result string matched the
// first exactly, so the Fixer's genuinely different "changed" call was
// rejected outright as "already terminal or belongs to another run" --
// confirmed live on confida-login: three real repairs stayed forever
// mislabelled queued_for_engineering because the Fixer had no channel to
// record their disposition and fell back to record_pulse_finding
// (evidence-only, no status mutation).
func TestRecordPulseResultAcceptsFixerSupplementalDispositionsAfterReviewerTerminal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--split-reviewer-fixer"
	sessionID := "schedule-cron--split-reviewer-fixer-session"
	if _, err := recordPulseWorklist(context.Background(), workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "A review is required."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}

	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)
	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))

	// The reviewer's own terminal write: nothing more to review this pass.
	if _, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "pulse_run_id": pulseRunID,
		"module": pulseModuleTechnicalReview, "result": "done",
		"reason": "Review complete, findings recorded for a linked decision.",
	}); err != nil {
		t.Fatalf("reviewer terminal write: %v", err)
	}

	// A finding the reviewer recorded, later repaired by a separately
	// dispatched Fixer background agent -- the split contract this reproduces.
	recordTestReviewFinding(t, ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview, "stale run binding in planning/step_config.json")
	findings, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, pulseModuleTechnicalReview, 10)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load reviewer finding: findings=%+v err=%v", findings, err)
	}
	selected := findings[0]

	// The Fixer's own later, separately-dispatched call: a genuinely
	// different result ("changed" vs. the reviewer's "done") for the SAME
	// pulse_run_id, carrying the repair's disposition.
	if _, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "pulse_run_id": pulseRunID,
		"module": pulseModuleTechnicalReview, "result": "changed",
		"reason":        "Fixed the stale run binding.",
		"changed_files": []string{"planning/step_config.json"},
		"verification":  []string{"targeted binding test passed"},
		"before_refs":   []string{"step_config:sha256:before"},
		"after_refs":    []string{"step_config:sha256:after"},
		"finding_dispositions": []map[string]interface{}{{
			"issue_id":      selected.Issue.ID,
			"disposition":   "fixed_verified",
			"summary":       "The stale run binding was corrected and the targeted test passed.",
			"changed_files": []string{"planning/step_config.json"},
			"before_refs":   []string{"step_config:sha256:before"},
			"after_refs":    []string{"step_config:sha256:after"},
			"verification": []map[string]interface{}{{
				"check":    "targeted binding test",
				"verdict":  "passed",
				"expected": "the configured run binding resolves",
				"observed": "the configured run binding resolved",
				"evidence": []string{"go test ./cmd/server -run TestRunBinding"},
			}},
		}},
	}); err != nil {
		t.Fatalf("Fixer supplemental write was rejected: %v", err)
	}

	// Live state and the audit must agree with the Fixer's evidenced result;
	// the original reviewer receipt is retained separately in history.
	states, err := getPulseModuleStates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("get states: %v", err)
	}
	found := false
	for _, state := range states {
		if state.Module == pulseModuleTechnicalReview {
			found = true
			if state.LastResult != "changed" {
				t.Fatalf("module result is stale: got %q, want changed", state.LastResult)
			}
		}
	}
	if !found {
		t.Fatalf("module state missing entirely: %+v", states)
	}

	// pulse_module_audit is keyed unique on (workspace_path, module,
	// pulse_run_id) -- it upserts, it is not an append log -- so the Fixer's
	// write becomes the one row's latest snapshot rather than a second row.
	// That is a pre-existing property of this table, not something this fix
	// changes; the load-bearing record for Gate's active queue is the
	// FINDING's own lifecycle/attempts below, which is append-only.
	_, db, err := openPulseModuleStateDB(context.Background(), workspacePath, false)
	if err != nil {
		t.Fatalf("open state db: %v", err)
	}
	defer db.Close()
	var auditCount int
	var latestResult string
	if err := db.QueryRow(`SELECT COUNT(*), MAX(result) FROM pulse_module_audit WHERE workspace_path=? AND module=? AND pulse_run_id=?`,
		workspacePath, pulseModuleTechnicalReview, pulseRunID,
	).Scan(&auditCount, &latestResult); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 1 || latestResult != "changed" {
		t.Fatalf("audit rows = %d (latest result %q), want 1 row upserted to the Fixer's most recent write", auditCount, latestResult)
	}

	// The repaired finding is no longer stuck at queued_for_engineering --
	// this is the exact confida-login symptom this fix closes. Its own
	// lifecycle (unlike the module audit row above) preserves both the
	// reviewer's original finding and the Fixer's resolving attempt.
	lifecycles, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, pulseModuleTechnicalReview, 10)
	if err != nil {
		t.Fatalf("load finding lifecycles: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Status != step_based_workflow.ConcernStatusResolved {
		t.Fatalf("Fixer's disposition did not close the finding: %+v", lifecycles)
	}
	if len(lifecycles[0].Attempts) != 1 || len(lifecycles[0].Verification) != 1 ||
		lifecycles[0].Verification[0].Verdict != step_based_workflow.VerificationPassed {
		t.Fatalf("finding fix evidence missing: %+v", lifecycles[0])
	}
}

// The relaxed retry path must not become a general-purpose way to overwrite
// an already-terminal module: a same-run call with a genuinely different
// result and NO dispositions carries no new evidence, so it must still be
// rejected exactly as before this fix.
func TestRecordPulseResultStillRejectsMismatchedResultWithNoDispositions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--mismatch-no-dispositions"
	sessionID := "schedule-cron--mismatch-no-dispositions-session"
	if _, err := recordPulseWorklist(context.Background(), workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "A review is required."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}

	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)
	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))

	if _, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "pulse_run_id": pulseRunID,
		"module": pulseModuleTechnicalReview, "result": "done",
		"reason": "Review complete.",
	}); err != nil {
		t.Fatalf("first terminal write: %v", err)
	}

	// "blocked" carries no changed_files/dispositions requirement, so this
	// exercises a genuinely different result with zero dispositions.
	_, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "pulse_run_id": pulseRunID,
		"module": pulseModuleTechnicalReview, "result": "blocked",
		"reason": "Trying to relitigate the module's own verdict with no new evidence.",
	})
	if err == nil || !strings.Contains(err.Error(), "already terminal or belongs to another run") {
		t.Fatalf("mismatched result with no dispositions should still be rejected, got: %v", err)
	}
}

func TestMarkPulseModuleChangedRequiresAuditProof(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--missing-audit"
	sessionID := "schedule-cron--missing-audit-session"
	if _, err := recordPulseWorklist(context.Background(), workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {Module: pulseModuleWorkflowReview, Due: true, Reason: "A verified repair is required."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}

	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)
	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))
	_, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath,
		"pulse_run_id":   pulseRunID,
		"module":         pulseModuleWorkflowReview,
		"result":         "changed",
		"reason":         "Claims a change without proof.",
	})
	if err == nil || !strings.Contains(err.Error(), "changed_files is required") {
		t.Fatalf("missing changed files error = %v", err)
	}
	states, err := getPulseModuleStates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("get states: %v", err)
	}
	for _, state := range states {
		if state.Module == pulseModuleWorkflowReview && state.LastResult != "" {
			t.Fatalf("invalid changed result was persisted: %+v", state)
		}
	}
}

func TestPulseAgentCannotOverwriteSchedulerTimeout(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--timeout"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {Module: pulseModuleWorkflowReview, Due: true, Reason: "Run requires review."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if _, err := markPulseModuleResult(ctx, workspacePath, pulseModuleWorkflowReview, pulseRunID, "timed_out", "Scheduler timeout", nil); err != nil {
		t.Fatalf("mark timeout: %v", err)
	}
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModuleWorkflowReview, pulseRunID, "changed", "Late reviewer result", nil); err == nil {
		t.Fatal("late agent result overwrote scheduler timeout")
	}
	state, err := getPulseModuleStates(ctx, workspacePath)
	if err != nil {
		t.Fatalf("get states: %v", err)
	}
	for _, module := range state {
		if module.Module == pulseModuleWorkflowReview && module.LastResult != "timed_out" {
			t.Fatalf("bug review result = %q, want timed_out", module.LastResult)
		}
	}
}

func TestValidatePulseGateCompletionRequiresOnlyCompleteDurableWorklist(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/gate-contract"
	pulseRunID := "schedule-manual--gate-contract"

	if err := validatePulseGateCompletion(ctx, workspacePath, pulseRunID); err == nil || !strings.Contains(err.Error(), "complete worklist") {
		t.Fatalf("missing worklist error = %v", err)
	}
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(nil)); err != nil {
		t.Fatalf("record complete worklist: %v", err)
	}
	if err := validatePulseGateCompletion(ctx, workspacePath, pulseRunID); err != nil {
		t.Fatalf("valid Gate completion rejected: %v", err)
	}
}

// The following test-only symbols keep the retired dashboard tests buildable
// while their bodies remain skipped until they are deleted with the next test
// file consolidation. Production Pulse no longer has these operations.
type pulseDashboardArtifactSnapshot struct{}

func validatePulseDashboardArtifact(context.Context, string, string, string, bool) error { return nil }
func capturePulseDashboardArtifacts(context.Context, string) ([]pulseDashboardArtifactSnapshot, error) {
	return nil, nil
}
func restorePulseDashboardArtifacts(context.Context, []pulseDashboardArtifactSnapshot) error {
	return nil
}

func TestValidatePulseDashboardArtifactRequiresFreshContractCompliantHTML(t *testing.T) {
	t.Skip("Pulse no longer writes or validates builder/improve.html")
	ctx := context.Background()
	workspacePath := "Workflow/dashboard-contract"
	pulseRunID := "schedule-manual--dashboard-contract"
	htmlPath := workspacePath + "/builder/improve.html"
	previousHTML := pulseImproveHTMLFixture(pulseRunID, "gate-only")
	workspaceState := &mockWorkspaceAPI{files: map[string]string{htmlPath: previousHTML}}
	workspace := httptest.NewServer(workspaceState)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("unchanged dashboard error = %v", err)
	}

	workspaceState.mu.Lock()
	workspaceState.files[htmlPath] = `<html><body><div>Issues &amp; fixes</div><div id="pulse-agent-handoff" data-pulse-run-id="` + pulseRunID + `"></div></body></html>`
	workspaceState.mu.Unlock()
	if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), "outdated") {
		t.Fatalf("retired dashboard format error = %v", err)
	}

	workspaceState.mu.Lock()
	workspaceState.files[htmlPath] = pulseImproveHTMLFixture(pulseRunID, "dashboard-updated")
	workspaceState.mu.Unlock()
	if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err != nil {
		t.Fatalf("valid dashboard artifact rejected: %v", err)
	}

	t.Run("rejects malformed technical nesting from production regression", func(t *testing.T) {
		malformed := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "malformed"),
			`<div class="brief"`,
			`</div><div class="brief"`,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = malformed
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil ||
			(!strings.Contains(err.Error(), "unexpected closing") && !strings.Contains(err.Error(), "mismatched closing")) {
			t.Fatalf("malformed technical nesting error = %v", err)
		}
	})

	t.Run("rejects retired reviewer coverage", func(t *testing.T) {
		withCoverage := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "coverage"),
			`<div id="pulse-agent-handoff"`,
			`<div class="coverage"><div class="covitem">Reviewer</div></div><div id="pulse-agent-handoff"`,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = withCoverage
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), ".coverage") {
			t.Fatalf("retired coverage error = %v", err)
		}
	})

	t.Run("rejects retired current work counts", func(t *testing.T) {
		withCounts := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "counts"),
			`<div id="pulse-agent-handoff"`,
			`<section class="worksummary"><div class="workstat">5</div></section><div id="pulse-agent-handoff"`,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = withCounts
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), ".worksummary") {
			t.Fatalf("retired Current work error = %v", err)
		}
	})

	t.Run("rejects missing outcome cell", func(t *testing.T) {
		missing := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "missing-cell"),
			`<div class="briefitem"><div class="k">Next</div><p>Later.</p></div>`,
			``,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = missing
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), "exactly 3 brief cells") {
			t.Fatalf("missing outcome cell error = %v", err)
		}
	})

	t.Run("rejects duplicated reviewer field dumps", func(t *testing.T) {

		withDump := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "reviewer-dump"),
			`<div id="pulse-agent-handoff"`,
			`<div class="modfields">raw reviewer fields</div><div id="pulse-agent-handoff"`,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = withDump
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), ".modfields") {
			t.Fatalf("reviewer dump error = %v", err)
		}
	})

	t.Run("allows material activity history beyond an arbitrary count", func(t *testing.T) {
		withExcessHistory := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "excess-history"),
			`<div id="pulse-agent-handoff"`,
			strings.Repeat(`<article class="entry resolved" data-date="2026-08-04"></article>`, 7)+`<div id="pulse-agent-handoff"`,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = withExcessHistory
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err != nil {
			t.Fatalf("material Activity history must not fail solely by count: %v", err)
		}
	})

	t.Run("requires canonical handoff attribute", func(t *testing.T) {
		wrongAttribute := strings.Replace(
			pulseImproveHTMLFixture(pulseRunID, "wrong-attribute"),
			`data-pulse-run-id="`+pulseRunID+`"`,
			`data-pulse-run="`+pulseRunID+`"`,
			1,
		)
		workspaceState.mu.Lock()
		workspaceState.files[htmlPath] = wrongAttribute
		workspaceState.mu.Unlock()
		if err := validatePulseDashboardArtifact(ctx, workspacePath, pulseRunID, previousHTML, true); err == nil || !strings.Contains(err.Error(), "handoff") {
			t.Fatalf("non-canonical handoff error = %v", err)
		}
	})
}

func pulseImproveHTMLFixture(pulseRunID, marker string) string {
	return `<html data-pulse-schema="5"><body>` +
		`<div class="brief"><div class="brief-h">Latest Pulse</div><div class="briefgrid">` +
		`<div class="briefitem"><div class="k">Outcome</div><p>Complete.</p></div>` +
		`<div class="briefitem"><div class="k">Goal movement</div><p>On track.</p></div>` +
		`<div class="briefitem"><div class="k">Next</div><p>Later.</p></div>` +
		`</div></div>` +
		`<div id="pulse-agent-handoff" data-pulse-run-id="` + pulseRunID + `" hidden>` + marker + `</div>` +
		`</body></html>`
}

func TestRestorePulseDashboardArtifactsRestoresBothFiles(t *testing.T) {
	t.Skip("Pulse no longer snapshots dashboard artifacts")
	ctx := context.Background()
	workspacePath := "Workflow/dashboard-rollback"
	improvePath := workspacePath + "/builder/improve.html"
	cardPath := workspacePath + "/builder/card.health.html"
	workspaceState := &mockWorkspaceAPI{files: map[string]string{
		improvePath: "previous improve",
		cardPath:    "previous card",
	}}
	workspace := httptest.NewServer(workspaceState)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	snapshots, err := capturePulseDashboardArtifacts(ctx, workspacePath)
	if err != nil {
		t.Fatalf("capture dashboard artifacts: %v", err)
	}
	workspaceState.mu.Lock()
	workspaceState.files[improvePath] = "partial improve"
	workspaceState.files[cardPath] = "partial card"
	workspaceState.mu.Unlock()

	if err := restorePulseDashboardArtifacts(ctx, snapshots); err != nil {
		t.Fatalf("restore dashboard artifacts: %v", err)
	}
	workspaceState.mu.Lock()
	defer workspaceState.mu.Unlock()
	if got := workspaceState.files[improvePath]; got != "previous improve" {
		t.Fatalf("improve after restore = %q", got)
	}
	if got := workspaceState.files[cardPath]; got != "previous card" {
		t.Fatalf("card after restore = %q", got)
	}
}

func TestRestorePulseDashboardArtifactsRemovesNewPartialFiles(t *testing.T) {
	t.Skip("Pulse no longer snapshots dashboard artifacts")
	ctx := context.Background()
	workspacePath := "Workflow/dashboard-new-rollback"
	improvePath := workspacePath + "/builder/improve.html"
	cardPath := workspacePath + "/builder/card.health.html"
	workspaceState := &mockWorkspaceAPI{files: map[string]string{}}
	workspace := httptest.NewServer(workspaceState)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	snapshots, err := capturePulseDashboardArtifacts(ctx, workspacePath)
	if err != nil {
		t.Fatalf("capture missing dashboard artifacts: %v", err)
	}
	workspaceState.mu.Lock()
	workspaceState.files[improvePath] = "partial improve"
	workspaceState.files[cardPath] = "partial card"
	workspaceState.mu.Unlock()

	if err := restorePulseDashboardArtifacts(ctx, snapshots); err != nil {
		t.Fatalf("restore dashboard artifacts: %v", err)
	}
	workspaceState.mu.Lock()
	defer workspaceState.mu.Unlock()
	if _, exists := workspaceState.files[improvePath]; exists {
		t.Fatal("new partial improve file was not removed")
	}
	if _, exists := workspaceState.files[cardPath]; exists {
		t.Fatal("new partial card file was not removed")
	}
}

func TestHandleGetPulseModuleState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"

	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-run-1", completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleStrategicReview: {
			Module: pulseModuleStrategicReview,
			Due:    true,
			Reason: "Goal trend is below target for two runs.",
		},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if err := initializePulseFinalCommandStates(ctx, workspacePath, "pulse-run-1"); err != nil {
		t.Fatalf("initialize final commands: %v", err)
	}
	if _, err := markPulseFinalCommandState(ctx, workspacePath, pulseFinalCommandBackup, "pulse-run-1", "done", "Backup completed"); err != nil {
		t.Fatalf("mark backup: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workflow/pulse-module-state?workspace_path=Workflow/example", nil)
	rec := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetPulseModuleState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success        bool                                   `json:"success"`
		Modules        []PulseModuleState                     `json:"modules"`
		Commands       []PulseFinalCommandState               `json:"commands"`
		GateMode       *PulseRunMode                          `json:"gate_mode"`
		ShadowCoverage map[string]string                      `json:"shadow_signal_coverage"`
		PlanDriftDue   bool                                   `json:"plan_drift_due"`
		PlanDriftItems []step_based_workflow.PlanDriftDueItem `json:"plan_drift_due_items"`
		PlanDriftError string                                 `json:"plan_drift_due_error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Success {
		t.Fatal("success=false")
	}
	if len(payload.Modules) != len(pulseModuleOrder) {
		t.Fatalf("modules = %d, want %d", len(payload.Modules), len(pulseModuleOrder))
	}
	if len(payload.Commands) != len(pulseFinalCommandOrder) {
		t.Fatalf("commands = %d, want %d", len(payload.Commands), len(pulseFinalCommandOrder))
	}
	if payload.GateMode == nil || payload.GateMode.Mode != pulseRunModeDiscovery {
		t.Fatalf("gate mode = %#v, want persisted default discovery", payload.GateMode)
	}
	if payload.Commands[0].Command != pulseFinalCommandBackup || payload.Commands[0].Status != "done" {
		t.Fatalf("backup command mismatch: %+v", payload.Commands[0])
	}
	if payload.ShadowCoverage["status"] != "not_instrumented" {
		t.Fatalf("shadow coverage = %+v, want not_instrumented", payload.ShadowCoverage)
	}
	if payload.PlanDriftDue || len(payload.PlanDriftItems) != 0 || payload.PlanDriftError != "" {
		t.Fatalf("unexpected Plan Drift due summary for workflow with no plan: due=%v items=%+v error=%q", payload.PlanDriftDue, payload.PlanDriftItems, payload.PlanDriftError)
	}
}

func TestHandleGetPulseFindingsReturnsFiledLifecycle(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/example"
	recordTestReviewFinding(t, ctx, workspacePath, "pulse-run-1", pulseModuleTechnicalReview, "selector keeps targeting the same accounts")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/workflow/pulse-findings?workspace_path=Workflow/example&module=technical_review",
		nil,
	)
	rec := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetPulseFindings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Success  bool                                        `json:"success"`
		Findings []step_based_workflow.PulseFindingLifecycle `json:"findings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Success || len(payload.Findings) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	finding := payload.Findings[0]
	if finding.Status != step_based_workflow.ConcernStatusOpen || finding.Module != pulseModuleTechnicalReview {
		t.Fatalf("finding identity/status mismatch: %+v", finding)
	}
	if finding.Kind != step_based_workflow.PulseFindingKindIssue {
		t.Fatalf("reviewer finding kind=%q, want canonical issue", finding.Kind)
	}
	if finding.Issue.ID == "" || finding.Issue.Title != "selector keeps targeting the same accounts" ||
		finding.Issue.Status != "backlog" || finding.Issue.Priority != "medium" {
		t.Fatalf("compact issue projection missing: %+v", finding.Issue)
	}
	if finding.FindingID != finding.Issue.ID || !strings.HasPrefix(finding.FindingID, "PUL-") {
		t.Fatalf("fixer-compatible finding id missing: finding_id=%q issue=%+v", finding.FindingID, finding.Issue)
	}
	if len(finding.Events) != 1 || finding.Events[0].EventType != "filed" {
		t.Fatalf("filed lifecycle event missing: %+v", finding.Events)
	}
}

func TestPulseBacklogSummaryDoesNotCountObservationsAsIssues(t *testing.T) {
	issues := []step_based_workflow.PulseFindingLifecycle{
		{Status: step_based_workflow.ConcernStatusOpen},
		{Status: step_based_workflow.ConcernStatusResolved},
	}
	observations := []step_based_workflow.PulseFindingLifecycle{
		{Status: step_based_workflow.ConcernStatusOpen},
		{Status: step_based_workflow.ConcernStatusOpen},
		{Status: step_based_workflow.ConcernStatusRejected},
	}
	summary := pulseBacklogSummary(issues, observations)
	if got := summary["active_count"]; got != 1 {
		t.Fatalf("active canonical issues=%v, want 1", got)
	}
	if got := summary["active_observation_count"]; got != 2 {
		t.Fatalf("active observations=%v, want 2", got)
	}
}

func TestPulseBacklogViewKeepsWorkflowObservationsOutOfFixerFeed(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/example"
	recordTestReviewFinding(t, ctx, workspacePath, "review-1", pulseModuleWorkflowReview, "collector silently drops failed records")
	seedLegacyStepObservation(t, workspacePath, "execution-1", "default", "collect", "broad collector scan repeats on every run")

	raw, err := readPulseBacklogView(ctx, workspacePath, "")
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	var payload struct {
		Issues           []map[string]interface{} `json:"issues"`
		Observations     []map[string]interface{} `json:"observations"`
		IssueTotal       int                      `json:"issue_total"`
		ObservationTotal int                      `json:"observation_total"`
		Detail           string                   `json:"detail"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode backlog: %v", err)
	}
	if payload.Detail != "compact" || payload.IssueTotal != 1 || len(payload.Issues) != 1 {
		t.Fatalf("Fixer feed should contain exactly the canonical issue: %+v", payload)
	}
	if payload.ObservationTotal != 1 || len(payload.Observations) != 0 {
		t.Fatalf("workflow observations must remain counted for audit but stay out of the reviewer/fixer feed: %+v", payload)
	}
	if _, leaked := payload.Issues[0]["fingerprint"]; leaked {
		t.Fatalf("internal fingerprint leaked into Fixer feed: %+v", payload.Issues[0])
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
		t.Fatalf("decode compact map: %v", err)
	}
	if _, duplicated := rawMap["findings"]; duplicated {
		t.Fatal("compact backlog duplicated canonical issues under legacy findings alias")
	}
}

func TestPulseBacklogCompactIncludesClosedIssueTextForSemanticReuse(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/example"
	const concern = "collector silently drops failed records"
	recordTestReviewFinding(t, ctx, workspacePath, "review-1", pulseModuleTechnicalReview, concern)
	findings, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)
	if err != nil || len(findings) != 1 {
		t.Fatalf("load issue: findings=%+v err=%v", findings, err)
	}
	if err := step_based_workflow.ResolveRunConcern(ctx, workspacePath, findings[0].Fingerprint,
		step_based_workflow.ConcernStatusResolved, "test", "Fix applied."); err != nil {
		t.Fatalf("close issue: %v", err)
	}

	raw, err := readPulseBacklogView(ctx, workspacePath, "")
	if err != nil {
		t.Fatalf("read compact register: %v", err)
	}
	var payload struct {
		Issues       []map[string]interface{} `json:"issues"`
		ClosedIssues []map[string]interface{} `json:"closed_issues"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode compact register: %v", err)
	}
	if len(payload.Issues) != 0 || len(payload.ClosedIssues) != 1 {
		t.Fatalf("closed semantic candidate missing: %+v", payload)
	}
	if payload.ClosedIssues[0]["summary"] != concern || payload.ClosedIssues[0]["issue_id"] == "" {
		t.Fatalf("closed candidate lacks issue identity/text: %+v", payload.ClosedIssues[0])
	}
}

func TestPulseBacklogFullDetailRequiresAndFiltersPublicIDs(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/example"
	largeEvidence := "unique-large-evidence-marker-" + strings.Repeat("evidence ", 40000)
	recordTestReviewFindingWithEvidence(t, ctx, workspacePath, "review-large", pulseModuleWorkflowReview,
		"unique-large-evidence-marker collector drops failed records", []string{largeEvidence})
	recordTestReviewFinding(t, ctx, workspacePath, "review-other", pulseModuleWorkflowReview, "unrelated second issue")

	compact, err := readPulseBacklogViewWithOptions(ctx, workspacePath, "", "compact", nil)
	if err != nil {
		t.Fatalf("read compact backlog: %v", err)
	}
	if strings.Contains(compact, largeEvidence) || len(compact) > 20_000 {
		t.Fatalf("compact backlog leaked full lifecycle text: bytes=%d", len(compact))
	}
	var index struct {
		Issues []struct {
			IssueID string `json:"issue_id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(compact), &index); err != nil || len(index.Issues) != 2 {
		t.Fatalf("decode compact issue index: issues=%d err=%v", len(index.Issues), err)
	}
	largeIssueID := ""
	var compactPayload struct {
		Issues []map[string]interface{} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(compact), &compactPayload); err != nil {
		t.Fatalf("decode compact cards: %v", err)
	}
	for _, card := range compactPayload.Issues {
		if strings.Contains(fmt.Sprint(card["summary"]), "unique-large-evidence-marker") {
			largeIssueID = fmt.Sprint(card["issue_id"])
			break
		}
	}
	if largeIssueID == "" {
		t.Fatalf("large issue missing from compact index: %+v", compactPayload.Issues)
	}

	if _, err := readPulseBacklogViewWithOptions(ctx, workspacePath, "", "full", nil); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("broad full-history read should be rejected, got %v", err)
	}
	full, err := readPulseBacklogViewWithOptions(ctx, workspacePath, "", "full", []string{largeIssueID})
	if err != nil {
		t.Fatalf("read targeted full detail: %v", err)
	}
	var detail struct {
		Detail  string                                      `json:"detail"`
		Records []step_based_workflow.PulseFindingLifecycle `json:"records"`
	}
	if err := json.Unmarshal([]byte(full), &detail); err != nil {
		t.Fatalf("decode targeted detail: %v", err)
	}
	if detail.Detail != "full" || len(detail.Records) != 1 {
		t.Fatalf("targeted response = %+v, want one full record", detail)
	}
	if !strings.Contains(detail.Records[0].Text, "unique-large-evidence-marker") {
		t.Fatalf("targeted record omitted requested evidence: %+v", detail.Records[0])
	}
	if detail.Records[0].Fingerprint != "" {
		t.Fatalf("targeted response leaked internal fingerprint: %+v", detail.Records[0])
	}
}

func TestPulseBacklogTargetedDetailRejectsAmbiguousPublicID(t *testing.T) {
	findings := []step_based_workflow.PulseFindingLifecycle{
		{Fingerprint: "abcdef12-first-full-fingerprint"},
		{Fingerprint: "abcdef12-second-full-fingerprint"},
	}
	selected, missing, ambiguous := selectPulseLifecyclesByIssueID(findings, []string{"PUL-ABCDEF12"})
	if len(selected) != 0 || len(missing) != 0 || !reflect.DeepEqual(ambiguous, []string{"PUL-ABCDEF12"}) {
		t.Fatalf("selected=%+v missing=%+v ambiguous=%+v", selected, missing, ambiguous)
	}
}

func TestPulseFinalCommandStatesRemainWaitingUntilAgentRecordsOutcomes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "pulse-final-1"

	if err := initializePulseFinalCommandStates(ctx, workspacePath, pulseRunID); err != nil {
		t.Fatalf("initialize final commands: %v", err)
	}
	states, err := getPulseFinalCommandStates(ctx, workspacePath)
	if err != nil {
		t.Fatalf("get final commands: %v", err)
	}
	if len(states) != len(pulseFinalCommandOrder) {
		t.Fatalf("states = %d, want %d", len(states), len(pulseFinalCommandOrder))
	}
	for _, state := range states {
		if state.Status != "waiting" || state.PulseRunID != pulseRunID {
			t.Fatalf("unexpected initialized state: %+v", state)
		}
	}

	running, err := markPulseFinalCommandState(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "running", "Starting backup")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if running.StartedAt == "" || running.FinishedAt != "" {
		t.Fatalf("running timestamps mismatch: %+v", running)
	}
	done, err := markPulseFinalCommandState(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "done", "Backup completed")
	if err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if done.FinishedAt == "" {
		t.Fatalf("done state missing finished_at: %+v", done)
	}

	states, err = getPulseFinalCommandStates(ctx, workspacePath)
	if err != nil {
		t.Fatalf("get command states: %v", err)
	}
	for _, state := range states {
		if state.Command == pulseFinalCommandBackup {
			if state.Status != "done" {
				t.Fatalf("completed backup was overwritten: %+v", state)
			}
			continue
		}
		if state.Status != "waiting" {
			t.Fatalf("Go inferred an outcome the agent did not record: %+v", state)
		}
	}
}

func TestSuccessfulFinalCommandClosesOnlyStageOwnershipFindings(t *testing.T) {
	t.Skip("dashboard-stage ownership ended with the retired improve.html stage")
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/finalizer-reconciliation"
	pulseRunID := "pulse-finalizer-reconciliation"
	if err := initializePulseFinalCommandStates(ctx, workspacePath, pulseRunID); err != nil {
		t.Fatal(err)
	}
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE run_concerns (fingerprint TEXT PRIMARY KEY, status TEXT NOT NULL, resolved_at TEXT NOT NULL DEFAULT '', resolved_by TEXT NOT NULL DEFAULT '', resolution_note TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE pulse_finding_events (_id INTEGER PRIMARY KEY AUTOINCREMENT, fingerprint TEXT NOT NULL, finding_id TEXT NOT NULL DEFAULT '', pulse_run_id TEXT NOT NULL DEFAULT '', attempt_id TEXT NOT NULL DEFAULT '', event_type TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', recorded_at TEXT NOT NULL, UNIQUE(fingerprint,pulse_run_id,attempt_id,event_type))`,
		`INSERT INTO run_concerns(fingerprint,status) VALUES ('dashboard-owned','external_action_required'),('real-platform-bug','external_action_required')`,
		`INSERT INTO pulse_finding_events(fingerprint,finding_id,pulse_run_id,event_type,metadata_json,recorded_at) VALUES
			('dashboard-owned','PUL-DASH','review-1','external_action_required','{"reason_code":"dashboard_stage_owned"}','2026-08-07T00:00:00Z'),
			('real-platform-bug','PUL-REAL','review-1','external_action_required','{"reason_code":"missing_platform_tool"}','2026-08-07T00:00:00Z')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := markPulseFinalCommandStateFromAgent(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "running", "Backing up"); err != nil {
		t.Fatal(err)
	}
	if _, err := markPulseFinalCommandStateFromAgent(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "done", "Backed up"); err != nil {
		t.Fatal(err)
	}
	for fingerprint, want := range map[string]string{"dashboard-owned": "resolved", "real-platform-bug": "external_action_required"} {
		var status string
		if err := db.QueryRowContext(ctx, `SELECT status FROM run_concerns WHERE fingerprint=?`, fingerprint).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("%s status=%q, want %q", fingerprint, status, want)
		}
	}
	var closed int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_finding_events WHERE fingerprint='dashboard-owned' AND event_type='closed' AND pulse_run_id=?`, pulseRunID).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("closed events=%d, want 1", closed)
	}
}

func TestPulseFinalCommandAgentWritesAreOrderedAndMonotonic(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--final"
	if err := initializePulseFinalCommandStates(ctx, workspacePath, pulseRunID); err != nil {
		t.Fatalf("initialize final commands: %v", err)
	}
	if _, err := markPulseFinalCommandStateFromAgent(ctx, workspacePath, "dashboard", pulseRunID, "running", "Removed dashboard"); err == nil || !strings.Contains(err.Error(), "not a valid") {
		t.Fatalf("removed dashboard was accepted: %v", err)
	}
	if _, err := markPulseFinalCommandStateFromAgent(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "done", "Skipped running"); err == nil || !strings.Contains(err.Error(), "marked running") {
		t.Fatalf("direct-done error = %v", err)
	}
	if _, err := markPulseFinalCommandStateFromAgent(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "running", "Backing up"); err != nil {
		t.Fatalf("mark backup running: %v", err)
	}
	if _, err := markPulseFinalCommandStateFromAgent(ctx, workspacePath, pulseFinalCommandBackup, pulseRunID, "done", "Backed up"); err != nil {
		t.Fatalf("mark backup done: %v", err)
	}
}

// TestCreatePulseWorklistToolsEveryToolHasACategory reproduces a real production
// failure: resolve_run_concern was added to the returned tool list without a
// matching entry in the categories map, so any orchestrator whose tool set
// included it failed at agent-creation time with "tool resolve_run_concern not
// found in ToolCategories map - category is REQUIRED" — before the orchestrator
// could even start (social-media's "[Route] Execution" todo_task, 2026-07-29).
// The categories map is hand-maintained separately from the tool slice, so
// nothing else catches a new tool silently missing its entry.
func TestCreatePulseWorklistToolsEveryToolHasACategory(t *testing.T) {
	tools, _, categories := createPulseWorklistTools()
	for _, tool := range tools {
		if tool.Function == nil {
			t.Fatalf("tool with nil Function: %+v", tool)
		}
		name := tool.Function.Name
		category, ok := categories[name]
		if !ok {
			t.Fatalf("tool %q has no entry in the categories map returned by createPulseWorklistTools", name)
		}
		if category == "" {
			t.Fatalf("tool %q has an empty category", name)
		}
	}
}

func TestResolveRunConcernToolCannotCloseWithoutVerification(t *testing.T) {
	_, executors, _ := createPulseWorklistTools()
	execute := executors["resolve_run_concern"].(func(context.Context, map[string]interface{}) (string, error))
	_, err := execute(context.Background(), map[string]interface{}{
		"workspace_path": "Workflow/example",
		"fingerprint":    "finding-fingerprint",
		"status":         "resolved",
		"note":           "claimed fixed without proof",
	})
	if err == nil || !strings.Contains(err.Error(), "verified finding_dispositions") {
		t.Fatalf("unverified close error = %v", err)
	}
}

func TestGetPulseModuleStateExposesLoopClosureButNotShadowHistory(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	_, executors, _ := createPulseWorklistTools()
	execute := executors["get_pulse_state"].(func(context.Context, map[string]interface{}) (string, error))

	raw, err := execute(context.Background(), map[string]interface{}{"workspace_path": "Workflow/example", "view": "module"})
	if err != nil {
		t.Fatalf("get_pulse_state(view=module): %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode Gate payload: %v", err)
	}
	loopClosure, exists := payload["loop_closure"].(map[string]interface{})
	if !exists {
		t.Fatalf("Gate payload is missing loop_closure evidence: %s", raw)
	}
	if got := loopClosure["coverage_status"]; got != loopclosure.CoverageNotInstrumented {
		t.Fatalf("loop_closure coverage = %#v, want %q", got, loopclosure.CoverageNotInstrumented)
	}
	intake, exists := payload["deterministic_intake"].(map[string]interface{})
	if !exists {
		t.Fatalf("Gate payload is missing deterministic intake evidence: %s", raw)
	}
	runtime, exists := intake["runtime"].(map[string]interface{})
	if !exists || runtime["coverage_status"] != "not_instrumented" {
		t.Fatalf("runtime intake = %#v, want not-instrumented evidence", intake["runtime"])
	}
	planDependencies, exists := intake["plan_change_dependencies"].(map[string]interface{})
	if !exists || planDependencies["coverage_status"] != "verified" || planDependencies["failed"] != false {
		t.Fatalf("plan dependency intake = %#v, want verified clean evidence", intake["plan_change_dependencies"])
	}
	if note, _ := payload["deterministic_intake_note"].(string); !strings.Contains(note, "not automatic Pulse issues") {
		t.Fatalf("deterministic_intake_note = %q", note)
	}
	note, _ := payload["loop_closure_note"].(string)
	for _, required := range []string{"do not mandate a module", "authorize mutation", "coverage_status"} {
		if !strings.Contains(note, required) {
			t.Fatalf("loop_closure_note missing %q: %q", required, note)
		}
	}
	for _, forbidden := range []string{"stalled_loops", "shadow_signals", "shadow_signal_observations"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("Gate payload exposes internal shadow field %q: %s", forbidden, raw)
		}
	}
}

func writePlanDriftDependencyReceiptFixture(t *testing.T, root, workspacePath, artifactReview string) {
	t.Helper()
	dir := filepath.Join(root, workspacePath, "planning", "changelog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"entries":[{"change_id":"change-plan-drift-1","timestamp":"2026-09-17T06:00:00Z","tool":"update_regular_step","reason":"change the output contract","step_ids":["step-a"]%s}]}`, artifactReview)
	if err := os.WriteFile(filepath.Join(dir, "changelog-2026-09-17-06-00-00.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Gate is allowed to select Plan Drift instead of Technical Review for an
// incomplete PLAT-197 receipt. The selected module must close that exact
// receipt before claiming success, or the next Gate deterministically creates
// the receipt-only Technical pass this bridge is intended to eliminate.
func TestPlanDriftResultRejectsOpenDependencyReceiptsWhenTechnicalIsNotDue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/plan-drift-receipt"
	writePlanDriftDependencyReceiptFixture(t, root, workspacePath, "")

	err := validatePlanDriftDependencyClosure(context.Background(), workspacePath, "pulse-1", pulseModulePlanDriftReview, "done")
	if err == nil {
		t.Fatal("successful Plan Drift result was allowed to leave an incomplete dependency receipt")
	}
	for _, want := range []string{"change-plan-drift-1", "mark_changelog_artifact_reviewed", "six-surface"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("closure error missing %q: %v", want, err)
		}
	}
}

func TestPlanDriftResultAllowsReceiptOwnedByTechnicalInSameRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/plan-drift-technical-owner"
	writePlanDriftDependencyReceiptFixture(t, root, workspacePath, "")
	if err := writePulseModuleDueRow(ctx, workspacePath, "pulse-1", pulseModuleTechnicalReview, "technical review owns the receipt"); err != nil {
		t.Fatal(err)
	}

	if err := validatePlanDriftDependencyClosure(ctx, workspacePath, "pulse-1", pulseModulePlanDriftReview, "done"); err != nil {
		t.Fatalf("Plan Drift should allow the same-run Technical owner to close the receipt: %v", err)
	}
}

func TestPlanDriftResultAcceptsCompleteDependencyReceipt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/plan-drift-closed-receipt"
	surfaces := `,"artifact_review":{"done":true,"surfaces":{` +
		`"downstream_steps":{"disposition":"already_compatible","evidence":["consumer contract checked"]},` +
		`"validation":{"disposition":"already_compatible","evidence":["validation contract checked"]},` +
		`"evaluation":{"disposition":"not_applicable","evidence":["no evaluation consumes this field"]},` +
		`"reporting":{"disposition":"already_compatible","evidence":["report query checked"]},` +
		`"database":{"disposition":"already_compatible","evidence":["database contract checked"]},` +
		`"learnings_and_knowledge":{"disposition":"not_applicable","evidence":["no guidance describes this field"]}}}`
	writePlanDriftDependencyReceiptFixture(t, root, workspacePath, surfaces)

	if err := validatePlanDriftDependencyClosure(context.Background(), workspacePath, "pulse-1", pulseModulePlanDriftReview, "done"); err != nil {
		t.Fatalf("complete dependency receipt should allow Plan Drift success: %v", err)
	}
}

func TestRecordPulseWorklistPersistsShadowObservationAfterDecision(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--shadow"
	sessionID := "schedule-cron--shadow-session"
	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)

	decisions := completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleWorkflowReview: {
			Module:   pulseModuleWorkflowReview,
			Due:      true,
			Reason:   "Gate independently found a failed step.",
			Evidence: []string{"run_metadata.json"},
		},
	})
	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_worklist"].(func(context.Context, map[string]interface{}) (string, error))
	args := map[string]interface{}{
		"workspace_path": workspacePath,
		"pulse_run_id":   pulseRunID,
		"mode":           pulseRunModeDiscovery,
		"mode_reason":    "The Gate is recording a normal review pass for this test.",
		"decisions":      pulseWorklistDecisionToolArgs(decisions),
	}
	if _, err := execute(ctx, args); err != nil {
		t.Fatalf("record_pulse_worklist: %v", err)
	}
	// An idempotent retry happens after the current Gate state exists. It must
	// not overwrite the pre-decision shadow snapshot with a later observation.
	if _, err := execute(ctx, args); err != nil {
		t.Fatalf("idempotent record_pulse_worklist retry: %v", err)
	}

	observations, err := getPulseShadowSignalObservations(context.Background(), workspacePath, 10)
	if err != nil {
		t.Fatalf("get shadow observations: %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %d, want 1: %+v", len(observations), observations)
	}
	got := observations[0]
	if got.PulseRunID != pulseRunID || got.DetectorVersion != loopclosure.DetectorVersion {
		t.Fatalf("observation identity mismatch: %+v", got)
	}
	if got.CoverageStatus != loopclosure.CoverageNotInstrumented {
		t.Fatalf("coverage = %q, want %q", got.CoverageStatus, loopclosure.CoverageNotInstrumented)
	}
	if len(got.GateDecisions) != len(pulseModuleOrder) {
		t.Fatalf("Gate decisions = %d, want %d", len(got.GateDecisions), len(pulseModuleOrder))
	}
	if len(got.Signals) != 0 {
		t.Fatalf("new workflow should have no shadow findings: %+v", got.Signals)
	}
}

func pulseWorklistDecisionToolArgs(decisions []PulseWorklistDecision) []interface{} {
	out := make([]interface{}, 0, len(decisions))
	for _, decision := range decisions {
		item := map[string]interface{}{
			"module":        decision.Module,
			"due":           decision.Due,
			"reason":        decision.Reason,
			"cooldown_runs": decision.CooldownRuns,
		}
		if len(decision.Evidence) > 0 {
			evidence := make([]interface{}, 0, len(decision.Evidence))
			for _, value := range decision.Evidence {
				evidence = append(evidence, value)
			}
			item["evidence"] = evidence
		}
		if decision.NextCheckAt != "" {
			item["next_check_at"] = decision.NextCheckAt
		}
		if decision.NextCheckAfterRunID != "" {
			item["next_check_after_run_id"] = decision.NextCheckAfterRunID
		}
		out = append(out, item)
	}
	return out
}

func completePulseWorklistDecisions(overrides map[string]PulseWorklistDecision) []PulseWorklistDecision {
	out := make([]PulseWorklistDecision, 0, len(pulseModuleOrder))
	for _, module := range pulseModuleOrder {
		decision := PulseWorklistDecision{
			Module:       module,
			Due:          false,
			Reason:       "No evidence requires this module this run.",
			CooldownRuns: 1,
		}
		if overrides != nil {
			if override, ok := overrides[module]; ok {
				if override.Module == "" {
					override.Module = module
				}
				decision = override
			}
		}
		out = append(out, decision)
	}
	return out
}

// recordPulseModuleDueForManualReview is the fix for a manual, non-Gate
// invocation (e.g. /review-artifact-drift's Part 1) needing to persist a
// record_pulse_result -- that write is due-gated on a Gate-recorded worklist
// row for the exact pulse_run_id, which a manual invocation never has.
func TestRecordPulseModuleDueForManualReviewSucceedsWithNoPriorState(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/manual-review-due"
	pulseRunID := "manual-session-1"

	if err := recordPulseModuleDueForManualReview(ctx, workspacePath, pulseRunID, pulseModulePlanDriftReview, "manual /review-artifact-drift invocation"); err != nil {
		t.Fatalf("recordPulseModuleDueForManualReview: %v", err)
	}
	if due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModulePlanDriftReview); err != nil {
		t.Fatalf("inspect due-ness: %v", err)
	} else if !due {
		t.Fatal("plan_drift_review should be due for the manual session after recordPulseModuleDueForManualReview")
	}
	// The whole point: record_pulse_result must now accept a terminal write
	// for this exact pulse_run_id.
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModulePlanDriftReview, pulseRunID, "done", "Manual artifact-drift audit complete.", nil); err != nil {
		t.Fatalf("record_pulse_result rejected the manual invocation's own due claim: %v", err)
	}
}

func TestRecordPulseModuleDueForManualReviewRefusesWhenAnotherRunIsMidFlight(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/manual-review-collision"

	// A real scheduled Pulse pass: Gate recorded plan_drift_review due for
	// pulse-scheduled-1, and its reviewer turn has not yet persisted a result.
	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-scheduled-1", completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModulePlanDriftReview: {Module: pulseModulePlanDriftReview, Due: true, Reason: "A step's drift_review record is null."},
	})); err != nil {
		t.Fatalf("record scheduled worklist: %v", err)
	}

	// A manual /review-artifact-drift invocation, running concurrently under a
	// completely different pulse_run_id, must not be able to silently steal
	// the module's single shared row out from under the in-flight scheduled
	// pass -- that would corrupt the scheduled pass's own later receipt
	// validation.
	err := recordPulseModuleDueForManualReview(ctx, workspacePath, "manual-session-concurrent", pulseModulePlanDriftReview, "manual /review-artifact-drift invocation")
	if err == nil || !strings.Contains(err.Error(), "pulse-scheduled-1") {
		t.Fatalf("expected refusal naming the in-flight scheduled run, got: %v", err)
	}

	// The scheduled pass's own claim must remain completely intact.
	if due, dueErr := pulseWorklistModulesDue(ctx, workspacePath, "pulse-scheduled-1", pulseModulePlanDriftReview); dueErr != nil {
		t.Fatalf("inspect scheduled due-ness: %v", dueErr)
	} else if !due {
		t.Fatal("the refused manual declaration must not have disturbed the scheduled pass's own due claim")
	}
}

func TestRecordPulseModuleDueForManualReviewAllowedAfterScheduledPassResolves(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/manual-review-after-resolved"

	if _, err := recordPulseWorklist(ctx, workspacePath, "pulse-scheduled-2", completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModulePlanDriftReview: {Module: pulseModulePlanDriftReview, Due: true, Reason: "A step's drift_review record is null."},
	})); err != nil {
		t.Fatalf("record scheduled worklist: %v", err)
	}
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModulePlanDriftReview, "pulse-scheduled-2", "done", "Scheduled pass finished.", nil); err != nil {
		t.Fatalf("resolve scheduled pass: %v", err)
	}

	// The scheduled pass already recorded its own terminal result -- it is no
	// longer mid-flight, so a later manual invocation is safe to claim the row.
	if err := recordPulseModuleDueForManualReview(ctx, workspacePath, "manual-session-after", pulseModulePlanDriftReview, "manual /review-artifact-drift invocation"); err != nil {
		t.Fatalf("recordPulseModuleDueForManualReview should succeed once the prior pass has a terminal result: %v", err)
	}
}

func TestPulseReviewFocusCatalogUsesValidationContractHealthWithoutSafety(t *testing.T) {
	want := []string{
		"execution_health",
		"validation_contract_health",
		"store_integrity",
		"report_quality_truth",
		"measurement_quality_truth",
	}
	if got := pulseReviewFocusCatalog[pulseModuleTechnicalReview]; !slices.Equal(got, want) {
		t.Fatalf("technical focus catalog = %v, want %v", got, want)
	}
	if slices.Contains(want, "safety_permissions") {
		t.Fatal("safety_permissions must not be an active selectable focus")
	}
}

// A Pulse run has two names (the session-style ID final commands are stored
// under, and the fix-run record's UUID). The run's own session may write under
// either; anyone else naming the wrong run is refused with the ID to use.
func TestPulseFinalCommandAcceptsTheRunsOtherNameFromItsOwnSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	const session, uuid = "schedule-cron--pulse-fi_1", "85d08c43-b8c4-4ec8-81bc-0f3c0b5538bf"
	if err := initializePulseFinalCommandStates(context.Background(), workspacePath, session); err != nil {
		t.Fatalf("initialize final commands: %v", err)
	}
	other := mcpexecutor.WithSessionID(context.Background(), "some-other-session")
	_, err := markPulseFinalCommandStateFromAgent(other, workspacePath, pulseFinalCommandBackup, uuid, "running", "Backing up")
	if err == nil || !strings.Contains(err.Error(), "belongs to Pulse run") || !strings.Contains(err.Error(), `pulse_run_id="`+session+`"`) {
		t.Fatalf("a foreign session must be refused with the ID to use, got: %v", err)
	}
	own := mcpexecutor.WithSessionID(context.Background(), session)
	if _, err := markPulseFinalCommandStateFromAgent(own, workspacePath, pulseFinalCommandBackup, uuid, "running", "Backing up"); err != nil {
		t.Fatalf("the run's own session under its other name must be accepted: %v", err)
	}
}
