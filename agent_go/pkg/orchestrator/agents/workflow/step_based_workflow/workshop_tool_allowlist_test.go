package step_based_workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// Registering a workshop tool is only half of shipping one: it also has to be
// listed in GetToolsForWorkshopMode, or the session refuses it at call time
// with
//
//	tool_not_allowed: "<name>" is registered but is not in this session's
//	allowed tool set.
//
// Nothing catches that at build time, and the refusal tells the agent the grant
// is fixed and not worth retrying — so a misconfigured tool reads as one
// deliberately withheld. get_contract_upgrades shipped that way and failed on
// its first real call.
//
// registerInteractiveWorkshopTools has too many construction-time dependencies
// to invoke with fakes, so this reads the declarations out of the source.
func TestEveryRegisteredWorkshopToolIsAllowedInSomeMode(t *testing.T) {
	const source = "interactive_workshop_manager.go"

	file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}

	registered := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "RegisterCustomTool", "RegisterCustomToolWithTimeout":
		default:
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			// A computed name cannot be checked from here; its allowlist entry
			// has to be reviewed by hand.
			return true
		}
		if name, err := strconv.Unquote(literal.Value); err == nil {
			registered[ConsolidatedPlanToolName(name)] = true
		}
		return true
	})

	if len(registered) < 20 {
		t.Fatalf("only found %d registered tools in %s; the scan is not finding them", len(registered), source)
	}

	// Tools registered through CreateWorkflowDBToolRegistry / CreateWorkflowCostsToolRegistry
	// (tool_setup.go) never appear as a RegisterCustomTool call in this file at
	// all -- the AST scan above cannot see them (PLAT-221). Check them from their own
	// canonical name lists instead of trying to widen the AST scan to a
	// registration path it was never written to see.
	for name := range virtualtools.WorkflowDBToolNames() {
		registered[ConsolidatedPlanToolName(name)] = true
	}
	for name := range virtualtools.WorkflowCostsToolNames() {
		registered[ConsolidatedPlanToolName(name)] = true
	}

	allowed := map[string]bool{}
	for _, mode := range []string{"builder", "run", "pulse"} {
		for _, name := range GetToolsForWorkshopMode(mode) {
			allowed[name] = true
		}
	}

	var missing []string
	for name := range registered {
		if !allowed[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("registered but in no workshop mode's allowed set, so unusable at runtime: %v", missing)
	}
}
