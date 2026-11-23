package mcpagent

import (
	"context"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Real failure mode: a part counted twice or missed, so the parts no longer add up to what is sent.
func TestPromptPartSizesAddUpToTheOutgoingPrompt(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("operator policy")
	a.appendedSystemPrompts = []string{"extra rule"}
	a.filteredTools = []llmtypes.Tool{{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "do_it"}}}

	got := a.PromptPartSizes(context.Background())
	if got.Instructions != len("operator policy") || got.Added != len("extra rule") {
		t.Fatalf("instructions/added = %d/%d", got.Instructions, got.Added)
	}
	if got.Tools == 0 {
		t.Fatal("tool definitions were not measured")
	}
	if sum := got.Instructions + got.Added + got.Skills + got.Runtime; sum != len(a.outgoingSystemPromptForContext(context.Background())) {
		t.Fatalf("parts sum to %d, outgoing prompt is %d", sum, len(a.outgoingSystemPromptForContext(context.Background())))
	}
}
