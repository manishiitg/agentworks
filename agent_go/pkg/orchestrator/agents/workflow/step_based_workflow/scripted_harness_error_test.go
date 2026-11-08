package step_based_workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Pin the production nonzero-exit branch, including both successful and failed
// pre-validation: neither may return success or persist a successful receipt.
func TestScriptedNonzeroExitCannotBePromotedByValidation(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "controller_scripted.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	ast.Inspect(f, func(n ast.Node) bool {
		branch, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		condition, ok := branch.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.LOR {
			return true
		}
		rhs, ok := condition.Y.(*ast.BinaryExpr)
		if !ok || rhs.Op != token.NEQ {
			return true
		}
		name, ok := rhs.X.(*ast.Ident)
		if !ok || name.Name != "exitCode" {
			return true
		}
		checked = true
		ast.Inspect(branch.Body, func(n ast.Node) bool {
			if field, ok := n.(*ast.KeyValueExpr); ok {
				key, _ := field.Key.(*ast.Ident)
				value, _ := field.Value.(*ast.Ident)
				if key != nil && key.Name == "Success" && value != nil && value.Name == "true" {
					t.Error("failed execution must not return Success: true")
				}
			}
			if call, ok := n.(*ast.CallExpr); ok {
				name, _ := call.Fun.(*ast.Ident)
				if name != nil && name.Name == "buildRunRecord" && len(call.Args) > 0 {
					value, _ := call.Args[0].(*ast.Ident)
					if value != nil && value.Name == "true" {
						t.Error("failed execution must not persist a success receipt")
					}
				}
			}
			return true
		})
		return false
	})
	if !checked {
		t.Fatal("nonzero-exit branch not found")
	}
}

// A request that never became a process must be reported as a harness rejection,
// whatever shape the failure arrives in.
func TestTransportAndAPIFailuresBothReadAsHarnessRejection(t *testing.T) {
	src := readSourceFile(t, "controller_scripted.go")
	fn := scriptedExecFunc(t, src)
	if strings.Count(fn, "ErrScriptedHarnessRejection") < 3 {
		t.Fatalf("missing client, transport/non-2xx, and API-error cases must all map to ErrScriptedHarnessRejection:\n%s", fn)
	}
	if !strings.Contains(fn, "if hcpo.WorkspaceClient == nil") {
		t.Fatal("a missing client is infrastructure, not a script fault")
	}
}

// scriptedExecFunc returns the body of execScriptedScript. These properties are
// about which code path is taken, which cannot be observed without a live
// workspace service.
func scriptedExecFunc(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "func (hcpo *StepBasedWorkflowOrchestrator) execScriptedScript(")
	if start < 0 {
		t.Fatal("execScriptedScript not found")
	}
	rel := strings.Index(src[start:], "\n}\n")
	if rel < 0 {
		t.Fatal("could not find the end of execScriptedScript")
	}
	// Strip comments: the prose legitimately names the headers and the old
	// dispatch, and matching those would defeat the point of the check.
	var code []string
	for _, line := range strings.Split(src[start:start+rel], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code = append(code, line)
	}
	return strings.Join(code, "\n")
}

// A rejected request means the script never ran, so it must not reach the
// relearn path. That path hands the agent the source plus the error and tells it
// to "fix the bug and rewrite it", then persists the rewrite — which is how two
// workflows replaced working scripts with workarounds for a bug in the harness.

// The ordinary failure path must keep working: a script that genuinely ran and
// failed is exactly what relearn exists for.

// The step must abort rather than fall through, and say plainly that the script
// is not at fault — this error is what the operator sees in the failure
// notification, and it is the only account of what happened.
func TestHarnessRejectionResultCarriesNoRunEvidence(t *testing.T) {
	src := readSourceFile(t, "controller_scripted.go")
	start := strings.Index(src, "func (hcpo *StepBasedWorkflowOrchestrator) tryRunSavedScriptedScript(")
	if start < 0 {
		t.Fatal("saved script runner is missing")
	}
	src = src[start:]
	idx := strings.Index(src, "if errors.Is(execErr, ErrScriptedHarnessRejection) {")
	if idx < 0 {
		t.Fatal("harness rejection must be detected in the fast path")
	}
	// Bound to this if-body only. Anything looser runs past the early return into
	// the ordinary failure path, which legitimately validates and records.
	rel := strings.Index(src[idx:], "\n\t}\n")
	if rel < 0 {
		t.Fatal("could not find the end of the rejection branch")
	}
	block := src[idx : idx+rel]
	if strings.Contains(block, "RunPreValidation") {
		t.Fatal("nothing ran, so there is no output to pre-validate")
	}
	if strings.Contains(block, "updateScriptedRunStats") {
		t.Fatal("a refused request must not be recorded against the script's run history")
	}
	if !strings.Contains(block, "HarnessFailure: true") || strings.Contains(block, "RanScript:") {
		t.Fatalf("result must report a non-run, not a failed run:\n%s", block)
	}
}
