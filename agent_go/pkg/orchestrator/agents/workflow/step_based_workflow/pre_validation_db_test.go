package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func TestDBValidatorUsesOnlyItsRegisteredManagedDatabase(t *testing.T) {
	const own = "Workflow/validation-example"
	session := "managed-validator-" + t.Name()
	common.SetSessionFolderGuard(session, []string{own}, nil)
	common.SetSessionFolderGuardBlockedPaths(session, []string{own + "/db/db.sqlite"})
	common.SetSessionShellEnv(session, map[string]string{"WORKFLOW_DB_ACCESS": "read"})
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var query workspace.QueryWorkflowDBParams
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/query" || query.DBPath != own+"/db/db.sqlite" || query.SQL != "SELECT 1 AS valid" {
			t.Errorf("unexpected managed validation request: %s %+v", r.URL.Path, query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"rows":[{"valid":1}]}}`))
	}))
	defer server.Close()
	client := workspace.NewClient(server.URL)
	bo := &orchestrator.BaseOrchestrator{WorkspaceClient: client}
	bo.SetWorkspacePath(own)
	rule := DBValidationRule{SQL: "SELECT 1 AS valid", MinRows: intp(1)}
	if _, fail := passed(validateDBRule(ctx, rule, bo).JSONChecks); fail != 0 {
		t.Fatal("native validation should query its own managed DB despite the raw-file block")
	}
	if _, err := client.QueryWorkflowDB(ctx, workspace.QueryWorkflowDBParams{DBPath: own + "/db/db.sqlite", SQL: rule.SQL}); err == nil {
		t.Fatal("raw database access must remain blocked")
	}
	bo.SetWorkspacePath("Workflow/another-example")
	if _, fail := passed(validateDBRule(ctx, rule, bo).JSONChecks); fail == 0 {
		t.Fatal("a validator must not query another workflow's database")
	}
	bo.SetWorkspacePath(own)
	common.SetSessionShellEnv(session, map[string]string{"WORKFLOW_DB_ACCESS": "none"})
	if _, fail := passed(validateDBRule(ctx, rule, bo).JSONChecks); fail == 0 {
		t.Fatal("disabled database access must remain disabled")
	}
	if requests != 1 {
		t.Fatalf("denied calls reached the workspace service: %d requests", requests)
	}
}

func intp(n int) *int { return &n }

func passed(checks []JSONCheckResult) (pass, fail int) {
	for _, c := range checks {
		if c.Passed {
			pass++
		} else {
			fail++
		}
	}
	return
}

func TestEvaluateDBRule(t *testing.T) {
	ctx := context.Background()
	rows3 := []map[string]interface{}{
		{"status": "PASS"}, {"status": "PASS"}, {"status": "PASS"},
	}

	// min_rows satisfied
	if _, fail := passed(evaluateDBRule(ctx, DBValidationRule{MinRows: intp(1)}, rows3)); fail != 0 {
		t.Errorf("min_rows>=1 on 3 rows should pass, got %d failures", fail)
	}
	// min_rows violated
	if _, fail := passed(evaluateDBRule(ctx, DBValidationRule{MinRows: intp(5)}, rows3)); fail != 1 {
		t.Errorf("min_rows>=5 on 3 rows should fail once, got %d", fail)
	}
	// max_rows violated (e.g. "no failures" with rows present)
	if _, fail := passed(evaluateDBRule(ctx, DBValidationRule{MaxRows: intp(0)}, rows3)); fail != 1 {
		t.Errorf("max_rows<=0 on 3 rows should fail once, got %d", fail)
	}
	// max_rows satisfied on empty result
	if _, fail := passed(evaluateDBRule(ctx, DBValidationRule{MaxRows: intp(0)}, nil)); fail != 0 {
		t.Errorf("max_rows<=0 on 0 rows should pass, got %d failures", fail)
	}

	// first-row check passes
	rule := DBValidationRule{Checks: []JSONValidationCheck{{Path: "$.status", MustExist: true, Pattern: "^PASS$"}}}
	if _, fail := passed(evaluateDBRule(ctx, rule, rows3)); fail != 0 {
		t.Errorf("first-row status==PASS should pass, got %d failures", fail)
	}
	// first-row check fails on wrong value
	badRows := []map[string]interface{}{{"status": "FAIL"}}
	if _, fail := passed(evaluateDBRule(ctx, rule, badRows)); fail != 1 {
		t.Errorf("first-row status==PASS should fail on FAIL, got %d", fail)
	}
	// row checks but no rows -> a failure (can't evaluate)
	if _, fail := passed(evaluateDBRule(ctx, rule, nil)); fail != 1 {
		t.Errorf("row checks with 0 rows should fail once, got %d", fail)
	}
}

func TestApplyCheckedToSummaryDBFailureFailsOverall(t *testing.T) {
	result := &WorkspaceVerificationResult{OverallPass: true}
	fr := validateDBRuleResultStub("db: no failures", false)
	applyCheckedToSummary(result, fr)
	if result.OverallPass {
		t.Error("a failed db check should flip OverallPass to false")
	}
	if result.Summary.FailedChecks != 1 || len(result.Summary.Errors) != 1 {
		t.Errorf("expected 1 failed check + 1 error, got failed=%d errors=%d", result.Summary.FailedChecks, len(result.Summary.Errors))
	}
}

// validateDBRuleResultStub builds a FileCheckResult with one pass/fail check.
func validateDBRuleResultStub(name string, pass bool) FileCheckResult {
	return FileCheckResult{FileName: name, Exists: true, IsJSON: true, JSONChecks: []JSONCheckResult{
		{CheckType: "max_rows", Passed: pass, Expected: "<= 0 row(s)", Actual: "2 row(s)", ErrorMsg: "expected <= 0 row(s), got 2 row(s)"},
	}}
}
