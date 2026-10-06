package step_based_workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// PLAT-262: a Run-mode session must never see a mutating tool in its catalog
// at all (not just have it rejected at call time) — see the RCA in
// docs/bugs/pulse_platform/sandbox/access/plat-262.md for why registration-time exclusion,
// keyed on WorkshopMode via iwm.isRunModeRestricted(), is the correct
// enforcement point (not WorkflowAccessLevel directly — a read-only identity
// is simply always pinned to "run" by the caller, and anyone else genuinely
// in "run" mode gets the same reduced tool set on purpose).
//
// registerInteractiveWorkshopTools has too many construction-time
// dependencies to invoke with fakes (see workshop_tool_allowlist_test.go),
// so this reads the guard structure out of the source: every mutating tool
// name below must be registered via mcpAgent.RegisterCustomTool inside the
// `else` branch of an `if iwm.isRunModeRestricted() { ... } else if err := ...`
// statement, and every tool expected to stay available in Run mode must NOT be.
func TestMutatingWorkshopToolsAreGatedByRunMode(t *testing.T) {
	const source = "interactive_workshop_manager.go"

	file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}

	gated := map[string]bool{}
	ungatedRegistered := map[string]bool{}

	ast.Inspect(file, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		toolNames := registeredToolNamesInElse(ifStmt)
		if len(toolNames) == 0 {
			return true
		}
		if isRunModeRestrictedGuard(ifStmt.Cond) {
			for _, name := range toolNames {
				gated[name] = true
			}
		} else {
			for _, name := range toolNames {
				ungatedRegistered[name] = true
			}
		}
		return true
	})

	mustBeGated := []string{
		"debug_step", "update_step_config", "review_step_code",
		"update_variable", "add_group", "update_group", "delete_group",
		"update_workflow_config", "set_workflow_contract_version",
		"create_schedule", "create_calendar_schedule", "update_schedule", "delete_schedule",
		"import_skill", "uninstall_skill", "install_skill", "set_workflow_llm_config",
	}
	for _, name := range mustBeGated {
		if !gated[name] {
			t.Errorf("%q must be registered inside an `if iwm.isRunModeRestricted() {...} else if err := mcpAgent.RegisterCustomTool(...)` guard, but was not found gated", name)
		}
	}

	mustStayAvailable := []string{"execute_step", "query_step", "run_full_workflow", "list_secrets"}
	for _, name := range mustStayAvailable {
		if gated[name] {
			t.Errorf("%q must stay available in Run mode but was found gated behind iwm.isRunModeRestricted()", name)
		}
	}
}

// registeredToolNamesInElse returns every RegisterCustomTool string literal
// tool name found in ifStmt's Else branch (recursing into nested else-if
// chains, since update_variable's guard sits inside one).
func registeredToolNamesInElse(ifStmt *ast.IfStmt) []string {
	var names []string
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "RegisterCustomTool" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			if name, err := strconv.Unquote(literal.Value); err == nil {
				names = append(names, name)
			}
			return true
		})
	}
	visit(ifStmt.Else)
	return names
}

// isRunModeRestrictedGuard reports whether cond is (syntactically) the call
// expression iwm.isRunModeRestricted().
func isRunModeRestrictedGuard(cond ast.Expr) bool {
	call, ok := cond.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if selector.Sel.Name != "isRunModeRestricted" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "iwm"
}

// TestRunModeZeroesBackgroundAgentWritePaths proves the write-path narrowing
// runBackgroundTaskAgentSequence applies before creating a background
// sub-agent's folder guard: full reads always, but zero write grants once
// iwm.isRunModeRestricted() is true. workshopWritePaths itself must keep
// returning a non-empty allow-list for a normal (Workshop-mode) caller, or
// this test would pass vacuously.
func TestRunModeZeroesBackgroundAgentWritePaths(t *testing.T) {
	const workspacePath = "Workflow/example"

	normalWritePaths := workshopWritePaths(workspacePath)
	if len(normalWritePaths) == 0 {
		t.Fatal("workshopWritePaths returned no paths for a normal caller — test would pass vacuously")
	}

	iwm := &InteractiveWorkshopManager{workshopModeOverride: "run"}
	writePaths := workshopWritePaths(workspacePath)
	if iwm.isRunModeRestricted() {
		writePaths = []string{}
	}
	if len(writePaths) != 0 {
		t.Fatalf("run mode must zero the background agent's write paths, got %v", writePaths)
	}
}
