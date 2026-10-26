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
	for _, action := range []string{"query", "describe"} {
		if _, err := database(context.Background(), map[string]interface{}{"action": action, "sql": "select 1"}); err == nil {
			t.Fatalf("%s ran although the step has no query source", action)
		}
	}

	// The real database read source also handles describe; scripts must reach
	// both without gaining mutate when their step only admits that source.
	families = ScriptFamilies(map[string]ScriptExecutor{
		"query_workflow_db": func(_ context.Context, args map[string]interface{}) (string, error) {
			got = args["sql"].(string)
			if action, _ := args["action"].(string); action != "" {
				got = action + ":" + got
			}
			return "ok", nil
		},
	})
	database = families["database"]
	if database == nil {
		t.Fatal("database family missing for an admitted query source")
	}
	if _, err := database(context.Background(), map[string]interface{}{"action": "query", "sql": "select 1"}); err != nil || got != "select 1" {
		t.Fatalf("query did not reach its admitted member: %v %q", err, got)
	}
	if _, err := database(context.Background(), map[string]interface{}{"action": "describe", "sql": "schema"}); err != nil || got != "describe:schema" {
		t.Fatalf("describe did not select the admitted source operation: %v %q", err, got)
	}
	if _, err := database(context.Background(), map[string]interface{}{"action": "mutate", "sql": "insert"}); err == nil {
		t.Fatal("mutate ran although the step only admits the query source")
	}
}
