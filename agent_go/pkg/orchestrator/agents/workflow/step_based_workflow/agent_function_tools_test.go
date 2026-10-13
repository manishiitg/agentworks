package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAgentFunctionToolsScopeAndTestModeBoundary(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/sample")
	c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, currentGroupName: "default", selectedRunFolder: "iteration-12-sched/default"}
	c.SetExecutionOptions(&ExecutionOptions{ExecutionID: "execution-one"})
	executionID := c.bridgeExecutionID()
	if executionID == "" {
		executionID = "execution-one"
	}
	called := false
	bindAgentFunctionTools(c, func(_ context.Context, scope AgentFunctionScope) ([]llmtypes.Tool, map[string]interface{}, error) {
		if scope.WorkspacePath != "Workflow/sample" || scope.SessionID != "step-session" || scope.StepID != "review" || scope.Group != "default" || scope.ExecutionID != executionID || scope.RunFolder != c.selectedRunFolder {
			t.Fatalf("untrusted or missing execution scope: %+v", scope)
		}
		var tools []llmtypes.Tool
		executors := map[string]interface{}{}
		for _, name := range []string{"call_function", "get_function_call", "reply_function_call"} {
			tools = append(tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}})
			executors[name] = func(context.Context, map[string]interface{}) (string, error) { called = true; return "ok", nil }
		}
		return tools, executors, nil
	})
	_, executors, err := c.appendAgentFunctionTools(context.Background(), "step-session", "review", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.testRun.Store(&testmode.Run{ID: "test-run"})
	for _, name := range []string{"call_function", "reply_function_call", "get_function_call"} {
		called = false
		_, err := executors[name].(func(context.Context, map[string]interface{}) (string, error))(context.Background(), nil)
		if name == "get_function_call" {
			if err != nil || !called {
				t.Fatal("test mode blocked a status read")
			}
		} else if err == nil || called || !strings.Contains(err.Error(), "test mode") {
			t.Fatalf("test mode allowed an external function effect: %s %v", name, err)
		}
	}
}
