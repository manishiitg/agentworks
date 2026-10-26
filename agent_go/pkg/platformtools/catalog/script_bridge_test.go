package catalog

import (
	"context"
	"testing"
)

// PLAT-825: the built-in DB helper calls database action=mutate, but a script step's bridge held only the member
// names, so every scripted DB write failed with tool_not_allowed.
func TestScriptFamiliesReachOnlyTheStepsOwnMembers(t *testing.T) {
	got := ""
	families := ScriptFamilies(map[string]ScriptExecutor{
		"mutate_workflow_db": func(_ context.Context, args map[string]interface{}) (string, error) {
			if _, leaked := args["action"]; leaked {
				t.Fatal("action reached the member tool")
			}
			got = args["sql"].(string)
			return "ok", nil
		},
	})
	database, ok := families["database"]
	if !ok {
		t.Fatal("database family missing for a step that can mutate")
	}
	if _, err := database(context.Background(), map[string]interface{}{"action": "mutate", "sql": "insert"}); err != nil || got != "insert" {
		t.Fatalf("mutate did not reach the member: %v %q", err, got)
	}
	if _, err := database(context.Background(), map[string]interface{}{"action": "query", "sql": "select 1"}); err == nil {
		t.Fatal("query ran although the step has no query member")
	}
}
