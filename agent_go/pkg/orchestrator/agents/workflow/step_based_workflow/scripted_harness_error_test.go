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
func TestHarnessRejectionAbortsInsteadOfRelearning(t *testing.T) {
	d := decideScriptedFastPath(&ScriptedFastPathResult{
		HarnessFailure: true,
		HarnessError:   "workspace execution authorization required",
		ExistingScript: "print('working code')",
	})
	if !d.HarnessFailure {
		t.Fatal("a harness rejection must be surfaced as such, not as a script failure")
	}
	if d.PriorError != "" || d.PriorScript != "" {
		t.Fatalf("nothing may be handed to the relearn path: %#v", d)
	}
	if d.FastPathDone {
		t.Fatal("a script that never ran cannot have completed the step")
	}
	if !strings.Contains(d.HarnessError, "authorization required") {
		t.Fatalf("the underlying error must survive for the operator: %q", d.HarnessError)
	}
}

func TestHarnessTimeoutAbortsWithoutPretendingTheScriptNeverStarted(t *testing.T) {
	d := decideScriptedFastPath(&ScriptedFastPathResult{
		RanScript:      true,
		HarnessTimeout: true,
		TimeoutError:   "script exceeded the workspace harness timeout",
		Output:         "CASE login: starting",
		ExistingScript: "print('working code')",
	})
	if !d.HarnessTimeout || d.HarnessFailure {
		t.Fatalf("timeout must remain distinct from startup refusal: %#v", d)
	}
	if d.PriorError != "" || d.PriorScript != "" {
		t.Fatalf("harness timeout must not enter the LLM repair path: %#v", d)
	}

	src := readSourceFile(t, "controller_execution.go")
	idx := strings.Index(src, "if scriptedDecision.HarnessTimeout {")
	refusal := strings.Index(src, "if scriptedDecision.HarnessFailure {")
	if idx < 0 || refusal < 0 || idx > refusal {
		t.Fatal("timeout must be handled before the startup-refusal branch")
	}
	block := src[idx:refusal]
	for _, want := range []string{"started main.py", "partial output was preserved", "code-repair loop was not started"} {
		if !strings.Contains(block, want) {
			t.Fatalf("timeout message missing %q:\n%s", want, block)
		}
	}
}

// The ordinary failure path must keep working: a script that genuinely ran and
// failed is exactly what relearn exists for.
func TestGenuineScriptFailureStillRelearns(t *testing.T) {
	d := decideScriptedFastPath(&ScriptedFastPathResult{
		RanScript:      true,
		Success:        false,
		Error:          "KeyError: 'ticker'",
		ExistingScript: "print('broken')",
	})
	if d.HarnessFailure {
		t.Fatal("a real script failure must not be mistaken for a harness failure")
	}
	if d.PriorError == "" || d.PriorScript == "" {
		t.Fatalf("relearn context must still be handed to the LLM: %#v", d)
	}
}

// The step must abort rather than fall through, and say plainly that the script
// is not at fault — this error is what the operator sees in the failure
// notification, and it is the only account of what happened.
func TestHarnessRejectionStepErrorBlamesTheHarness(t *testing.T) {
	src := readSourceFile(t, "controller_execution.go")
	idx := strings.Index(src, "if scriptedDecision.HarnessFailure {")
	if idx < 0 {
		t.Fatal("the harness-failure branch must abort the step before the relearn switch")
	}
	relearn := strings.Index(src, "learnCodePriorError = scriptedDecision.PriorError")
	if relearn < idx {
		t.Fatal("the abort must come before relearn context is assigned")
	}
	block := src[idx:relearn]
	if !strings.Contains(block, "return \"\", updatedContextFiles, fmt.Errorf(") {
		t.Fatalf("the branch must return an error, not fall through:\n%s", block)
	}
	for _, want := range []string{"not a fault in the script", "do not modify main.py", "produced no output"} {
		if !strings.Contains(block, want) {
			t.Fatalf("step error must state %q:\n%s", want, block)
		}
	}
}

// A refused request must not count against the script's record. These counters
// drive needs_review and the argument for unlocking a locked script; charging
// them for runs that never happened would condemn working code.
func TestHarnessRejectionResultCarriesNoRunEvidence(t *testing.T) {
	src := readSourceFile(t, "controller_scripted.go")
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

// A script that deliberately exits ScriptedTerminalRefusalExitCode (a
// fail-closed data-safety guard refusing to proceed) must abort the step, not
// reach the relearn path. Live incident on HDFC-Personal-Accounts: a BR5-01
// guard correctly refused to overwrite bank balance history it could not
// verify was current, and the (then-undifferentiated) relearn fallback read
// that refusal, agreed it was correct, and performed the exact write and
// database mutation the script had refused to do.
func TestTerminalRefusalAbortsInsteadOfRelearning(t *testing.T) {
	d := decideScriptedFastPath(&ScriptedFastPathResult{
		RanScript:             true,
		ExitCode:              ScriptedTerminalRefusalExitCode,
		TerminalRefusal:       true,
		TerminalRefusalReason: "ABORT: failed to read existing rows; refusing to overwrite and risk wiping history",
		ExistingScript:        "print('working code')",
	})
	if !d.TerminalRefusal {
		t.Fatal("a terminal refusal must be surfaced as such, not as a script failure")
	}
	if d.PriorError != "" || d.PriorScript != "" {
		t.Fatalf("nothing may be handed to the relearn path: %#v", d)
	}
	if d.FastPathDone {
		t.Fatal("a deliberate refusal did not complete the step")
	}
	if !strings.Contains(d.TerminalRefusalReason, "refusing to overwrite") {
		t.Fatalf("the refusal detail must survive for the operator: %q", d.TerminalRefusalReason)
	}
}

// The reserved exit code is an opt-in signal, not a guess from exit code
// alone: any other non-zero exit (including the still-conventional 1) must
// keep relearning exactly as before this existed.
func TestOrdinaryNonZeroExitStillRelearnsNotJustReservedCode(t *testing.T) {
	d := decideScriptedFastPath(&ScriptedFastPathResult{
		RanScript:      true,
		Success:        false,
		ExitCode:       1,
		Error:          "KeyError: 'ticker'",
		ExistingScript: "print('broken')",
	})
	if d.TerminalRefusal {
		t.Fatal("an ordinary exit code 1 failure must not be mistaken for a deliberate refusal")
	}
	if d.PriorError == "" || d.PriorScript == "" {
		t.Fatalf("relearn context must still be handed to the LLM: %#v", d)
	}
}

// Both execution paths must use the evidence-based message and stop before repair.
func TestTerminalRefusalStepErrorStopsBeforeRelearning(t *testing.T) {
	src := readSourceFile(t, "controller_execution.go")
	idx := strings.Index(src, "if scriptedDecision.TerminalRefusal {")
	relearn := strings.Index(src, "learnCodePriorError = scriptedDecision.PriorError")
	if idx < 0 || relearn < idx {
		t.Fatal("terminal stop must precede relearn context")
	}
	if !strings.Contains(src[idx:relearn], "return \"\", updatedContextFiles, scriptedTerminalStopError(") {
		t.Fatal("terminal stop must return the evidence-based error")
	}
	if !strings.Contains(src, "scriptedTerminalStopError(step.GetID(), canonicalResult.TerminalRefusalReason)") {
		t.Fatal("canonical repair execution must use the same terminal stop message")
	}
}

// A deliberate refusal must not count against the script's lock-code record,
// the same way a harness rejection does not — it is evidence the guard is
// working, not that the script is broken.
func TestTerminalRefusalResultCarriesNoRunEvidence(t *testing.T) {
	src := readSourceFile(t, "controller_scripted.go")
	idx := strings.Index(src, "if execErr == nil && exitCode == ScriptedTerminalRefusalExitCode {")
	if idx < 0 {
		t.Fatal("terminal refusal must be detected in the fast path")
	}
	rel := strings.Index(src[idx:], "\n\t}\n")
	if rel < 0 {
		t.Fatal("could not find the end of the terminal-refusal branch")
	}
	block := src[idx : idx+rel]
	if strings.Contains(block, "RunPreValidation") {
		t.Fatal("a deliberate refusal has nothing valid to pre-validate")
	}
	if strings.Contains(block, "updateScriptedRunStats") {
		t.Fatal("a deliberate refusal must not be recorded against the script's run history")
	}
	if !strings.Contains(block, "TerminalRefusal:") {
		t.Fatalf("result must report the refusal:\n%s", block)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
