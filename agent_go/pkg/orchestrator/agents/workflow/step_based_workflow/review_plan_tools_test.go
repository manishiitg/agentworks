package step_based_workflow

import (
	"context"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestEveryReadOnlyBackgroundReviewerGetsQueryButNotMutation(t *testing.T) {
	tool := func(name string) llmtypes.Tool {
		return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}}
	}
	noop := func(context.Context, map[string]interface{}) (string, error) { return "", nil }
	base := &orchestrator.BaseOrchestrator{
		WorkspaceTools: []llmtypes.Tool{
			tool("execute_shell_command"),
			tool("human_feedback"),
			tool("record_summary"),
			tool("query_workflow_db"),
			tool("mutate_workflow_db"),
		},
		WorkspaceToolExecutors: map[string]interface{}{
			"execute_shell_command": noop,
			"human_feedback":        noop,
			"record_summary":        noop,
			"query_workflow_db":     noop,
			"mutate_workflow_db":    noop,
		},
	}

	tools, executors := prepareReadOnlyBackgroundAgentTools(base)
	names := make([]string, 0, len(tools))
	for _, definition := range tools {
		if definition.Function != nil {
			names = append(names, definition.Function.Name)
		}
	}
	if !slices.Contains(names, "query_workflow_db") || executors["query_workflow_db"] == nil {
		t.Fatalf("read-only background reviewer missing query_workflow_db: tools=%v executors=%v", names, executors)
	}
	if slices.Contains(names, "mutate_workflow_db") || executors["mutate_workflow_db"] != nil {
		t.Fatalf("read-only background reviewer received mutation authority: tools=%v executors=%v", names, executors)
	}
	if slices.Contains(names, "human_feedback") || executors["human_feedback"] != nil {
		t.Fatalf("Builder reviewer received run-only human_feedback: tools=%v executors=%v", names, executors)
	}
	if !slices.Contains(names, "record_summary") || executors["record_summary"] == nil {
		t.Fatalf("Builder reviewer missing product-declared record_summary: tools=%v executors=%v", names, executors)
	}
}
