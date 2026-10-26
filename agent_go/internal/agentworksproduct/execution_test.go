package agentworksproduct

import (
	"slices"
	"testing"
)

func TestExecutionFunctionToolsAreProductOwned(t *testing.T) {
	def := ExecutionFunctionTools()
	if def.Category == "" || !slices.Equal(def.Tools, []string{"project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply"}) {
		t.Fatalf("unexpected execution function surface: %+v", def)
	}
	def.Tools[0] = "changed"
	if ExecutionFunctionTools().Tools[0] == "changed" {
		t.Fatal("caller mutated the embedded product declaration")
	}
}
