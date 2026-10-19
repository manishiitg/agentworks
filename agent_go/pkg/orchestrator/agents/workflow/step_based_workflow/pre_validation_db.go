package step_based_workflow

import (
	"context"
	"fmt"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// validateDBRule runs a DBValidationRule's read-only query against db/db.sqlite and
// evaluates its assertions, returning a FileCheckResult so it reuses the existing
// summary aggregation + corrective-feedback formatting (labeled "db: <name>").
func validateDBRule(ctx context.Context, rule DBValidationRule, baseOrchestrator *orchestrator.BaseOrchestrator) FileCheckResult {
	label := strings.TrimSpace(rule.Name)
	if label == "" {
		label = truncateForLabel(rule.SQL, 60)
	}
	fr := FileCheckResult{FileName: "db: " + label, Exists: true, IsJSON: true}

	if strings.TrimSpace(rule.SQL) == "" {
		fr.JSONChecks = append(fr.JSONChecks, JSONCheckResult{
			CheckType: "db_query", Passed: false,
			Expected: "a read-only SQL query", Actual: "(empty)",
			ErrorMsg: "db validation rule has no sql",
		})
		return fr
	}

	rows, err := virtualtools.QueryWorkflowDBForValidation(ctx, baseOrchestrator.WorkspaceClient, baseOrchestrator.GetWorkspacePath(), rule.SQL)
	if err != nil {
		fr.JSONChecks = append(fr.JSONChecks, JSONCheckResult{
			CheckType: "db_query", Passed: false,
			Expected: "query runs against db/db.sqlite", Actual: err.Error(),
			ErrorMsg: fmt.Sprintf("db query failed: %v", err),
		})
		return fr
	}
	fr.JSONChecks = evaluateDBRule(ctx, rule, rows)
	return fr
}

// evaluateDBRule applies a rule's row-count + first-row checks to query results.
// Pure (no db access) so it is unit-testable with synthetic rows.
func evaluateDBRule(ctx context.Context, rule DBValidationRule, rows []map[string]interface{}) []JSONCheckResult {
	out := make([]JSONCheckResult, 0, len(rule.Checks)+2)
	n := len(rows)

	if rule.MinRows != nil {
		out = append(out, boolCheckResult("min_rows", n >= *rule.MinRows,
			fmt.Sprintf(">= %d row(s)", *rule.MinRows), fmt.Sprintf("%d row(s)", n)))
	}
	if rule.MaxRows != nil {
		out = append(out, boolCheckResult("max_rows", n <= *rule.MaxRows,
			fmt.Sprintf("<= %d row(s)", *rule.MaxRows), fmt.Sprintf("%d row(s)", n)))
	}

	if len(rule.Checks) > 0 {
		if n == 0 {
			out = append(out, JSONCheckResult{
				Path: "(first row)", CheckType: "db_row", Passed: false,
				Expected: "at least one row to run checks on", Actual: "0 rows",
				ErrorMsg: "query returned no rows, so the row checks cannot be evaluated",
			})
		} else {
			// First row is an object keyed by column — reuse the JSON check evaluator.
			first := rows[0]
			for _, c := range rule.Checks {
				out = append(out, validateJSONCheck(ctx, c, first))
			}
		}
	}
	return out
}

// boolCheckResult builds a pass/fail JSONCheckResult for a simple boolean assertion.
func boolCheckResult(checkType string, passed bool, expected, actual string) JSONCheckResult {
	r := JSONCheckResult{CheckType: checkType, Passed: passed, Expected: expected, Actual: actual}
	if !passed {
		r.ErrorMsg = fmt.Sprintf("expected %s, got %s", expected, actual)
	}
	return r
}

// applyCheckedToSummary folds a checked result's JSONChecks into the run summary +
// OverallPass (mirrors the inline file aggregation; used for db rule results).
func applyCheckedToSummary(result *WorkspaceVerificationResult, fr FileCheckResult) {
	for _, jc := range fr.JSONChecks {
		result.Summary.TotalChecks++
		switch {
		case jc.SchemaError:
			result.Summary.SchemaErrors++
			result.Summary.SchemaWarnings = append(result.Summary.SchemaWarnings, checkToValidationError(fr.FileName, jc))
		case jc.Passed:
			result.Summary.PassedChecks++
		default:
			result.Summary.FailedChecks++
			result.OverallPass = false
			result.Summary.Errors = append(result.Summary.Errors, checkToValidationError(fr.FileName, jc))
		}
	}
}

func checkToValidationError(file string, jc JSONCheckResult) ValidationError {
	return ValidationError{
		File:      file,
		Path:      jc.Path,
		CheckType: jc.CheckType,
		Expected:  fmt.Sprintf("%v", jc.Expected),
		Actual:    fmt.Sprintf("%v", jc.Actual),
		Message:   jc.ErrorMsg,
	}
}

func truncateForLabel(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
