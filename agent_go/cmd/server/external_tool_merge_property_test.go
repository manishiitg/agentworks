package server

import "testing"

// create_crew and update_crew both take "functions" in different shapes; the
// merged crew tool must show both, not only the first (reported by a tester
// whose update call failed with "got array, want object").
func TestMergedToolKeepsDifferentShapesOfTheSameArgument(t *testing.T) {
	props := map[string]any{}
	array := map[string]any{"type": "array"}
	object := map[string]any{"type": "object"}
	externalMergeProperty(props, "functions", array)
	externalMergeProperty(props, "functions", object)
	externalMergeProperty(props, "functions", object)
	union, _ := props["functions"].(map[string]any)["anyOf"].([]any)
	if len(union) != 2 {
		t.Fatalf("expected both shapes once, got %+v", props["functions"])
	}
}
