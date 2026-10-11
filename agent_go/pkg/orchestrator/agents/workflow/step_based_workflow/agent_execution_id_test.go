package step_based_workflow

import (
	"context"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

func TestAgentExecutionIDReusesStandaloneStepExecution(t *testing.T) {
	ctx := virtualtools.WithBackgroundAgentID(context.Background(), "exec-math-solver-123")
	if got := agentSequenceExecutionID(ctx, "math-solver"); got != "exec-math-solver-123" {
		t.Fatalf("agentSequenceExecutionID() = %q, want standalone step execution ID", got)
	}
}

func TestAgentExecutionIDDoesNotReuseWorkflowExecution(t *testing.T) {
	ctx := virtualtools.WithBackgroundAgentID(context.Background(), "workflow-full-123")
	if got := agentSequenceExecutionID(ctx, "math-solver"); got != "" {
		t.Fatalf("agentSequenceExecutionID() = %q, want empty for workflow-level execution", got)
	}
}
