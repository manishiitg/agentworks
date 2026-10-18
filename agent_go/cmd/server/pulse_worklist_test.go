package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestGetPulseReviewsAPIListsCompactReviewReceipts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/example"
	_, db, err := openPulseStoreDB(context.Background(), workspacePath, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE pulse_review_log (_id INTEGER PRIMARY KEY,module TEXT,review_run_id TEXT,pulse_run_id TEXT,verdict TEXT,status TEXT,finding_count INTEGER,verification_count INTEGER,verifications_json TEXT,recorded_at TEXT); INSERT INTO pulse_review_log VALUES(1,'technical_review','2026-07-31T08-00-00.000Z_pulse-1','pulse-1','A real issue was found.','completed',0,0,'[]','2026-07-31T08:00:00Z')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
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

// TestCreatePulseWorklistToolsEveryToolHasACategory reproduces a real production
// failure: resolve_run_concern was added to the returned tool list without a
// matching entry in the categories map, so any orchestrator whose tool set
// included it failed at agent-creation time with "tool resolve_run_concern not
// found in ToolCategories map - category is REQUIRED" — before the orchestrator
// could even start (social-media's "[Route] Execution" todo_task, 2026-07-29).
// The categories map is hand-maintained separately from the tool slice, so
// nothing else catches a new tool silently missing its entry.
func TestCreatePulseWorklistToolsEveryToolHasACategory(t *testing.T) {
	tools, _, categories := createPulseTools()
	for _, tool := range tools {
		if tool.Function == nil {
			t.Fatalf("tool with nil Function: %+v", tool)
		}
		name := tool.Function.Name
		category, ok := categories[name]
		if !ok {
			t.Fatalf("tool %q has no entry in the categories map returned by createPulseTools", name)
		}
		if category == "" {
			t.Fatalf("tool %q has an empty category", name)
		}
	}
}

func TestResolveRunConcernToolCannotCloseWithoutVerification(t *testing.T) {
	_, executors, _ := createPulseTools()
	execute := executors["resolve_run_concern"].(func(context.Context, map[string]interface{}) (string, error))
	_, err := execute(context.Background(), map[string]interface{}{
		"workspace_path": "Workflow/example",
		"fingerprint":    "finding-fingerprint",
		"status":         "resolved",
		"note":           "claimed fixed without proof",
	})
	if err == nil || !strings.Contains(err.Error(), "Builder") {
		t.Fatalf("unverified close error = %v", err)
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
