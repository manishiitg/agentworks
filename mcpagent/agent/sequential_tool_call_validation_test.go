package mcpagent

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestRequireFunctionCallRejectsNilFunctionCall(t *testing.T) {
	functionCall, err := requireFunctionCall(llmtypes.ToolCall{ID: "malformed-call"})
	if err == nil {
		t.Fatal("tool call with no FunctionCall should be rejected")
	}
	if functionCall != nil {
		t.Fatal("malformed tool call should not return a FunctionCall")
	}
	if !strings.Contains(err.Error(), "nil function call") {
		t.Fatalf("unexpected error: %v", err)
	}
}
