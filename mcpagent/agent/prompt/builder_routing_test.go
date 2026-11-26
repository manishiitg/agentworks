package prompt

import (
	"strings"
	"testing"
)

func TestCodeExecutionTutorialDefersToRuntimeContract(t *testing.T) {
	text := GetCodeExecutionInstructions()
	if strings.Contains(text, "{{TOOL_STRUCTURE}}") || !strings.Contains(text, "runtime routing instructions") || !strings.Contains(text, "search_tools") || !strings.Contains(text, "get_api_spec") {
		t.Fatal("generic prompt must use discovery without an inventory placeholder")
	}
	for _, duplicate := range []string{"curl", "MCP_AUTH", "STEP_OUTPUT_DIR", "VAR_GROUP_NAME", "query_workflow_db"} {
		if strings.Contains(text, duplicate) {
			t.Fatalf("generic tutorial repeated runtime/product instruction %q", duplicate)
		}
	}
}

func TestDefaultPromptUsesDiscoveryWithoutToolInventory(t *testing.T) {
	text := BuildSystemPromptWithoutTools(true, false)
	for _, absent := range []string{"legacy_private_tool", "<available_tools>", "{{TOOL_STRUCTURE}}"} {
		if strings.Contains(text, absent) {
			t.Fatalf("default prompt embedded %q", absent)
		}
	}
	for _, want := range []string{"search_tools", "get_api_spec"} {
		if !strings.Contains(text, want) {
			t.Fatalf("default prompt lost %q", want)
		}
	}
}
