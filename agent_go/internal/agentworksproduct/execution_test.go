package agentworksproduct

import (
	"slices"
	"testing"
)

func TestExecutionFunctionToolsAreProductOwned(t *testing.T) {
	def := ExecutionFunctionTools()
	if def.Category == "" || !slices.Equal(def.Tools, []string{"list_functions", "call_function", "get_function_call", "reply_function_call"}) {
		t.Fatalf("unexpected execution function surface: %+v", def)
	}
	def.Tools[0] = "changed"
	if ExecutionFunctionTools().Tools[0] == "changed" {
		t.Fatal("caller mutated the embedded product declaration")
	}
}
