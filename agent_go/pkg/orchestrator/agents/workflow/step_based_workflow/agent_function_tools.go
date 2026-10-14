package step_based_workflow

import (
	"context"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AgentFunctionScope comes from the executing controller, never tool arguments.
type AgentFunctionScope struct {
	WorkspacePath, ExecutionID, StepID, Group, SessionID string
}

// AgentFunctionToolFactory binds server function services to one execution agent.
// Retained for server-side compatibility bindings; ordinary step factories no
// longer call appendAgentFunctionTools. Function-service access belongs to Crew.
type AgentFunctionToolFactory func(context.Context, AgentFunctionScope) ([]llmtypes.Tool, map[string]interface{}, error)

func bindAgentFunctionTools(controller *StepBasedWorkflowOrchestrator, factory AgentFunctionToolFactory) {
	if controller == nil || factory == nil {
		return
	}
	opts := controller.GetExecutionOptions()
	if opts == nil {
		opts = &ExecutionOptions{}
		controller.SetExecutionOptions(opts)
	}
	opts.AgentFunctionTools = factory
}

func (hcpo *StepBasedWorkflowOrchestrator) appendAgentFunctionTools(ctx context.Context, sessionID, stepID string, tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}, error) {
	opts := hcpo.GetExecutionOptions()
	if opts == nil || opts.AgentFunctionTools == nil {
		return tools, executors, nil
	}
	scope := AgentFunctionScope{WorkspacePath: hcpo.GetWorkspacePath(), ExecutionID: hcpo.bridgeExecutionID(), StepID: stepID, Group: hcpo.currentGroupName, SessionID: sessionID}
	if scope.ExecutionID == "" {
		scope.ExecutionID = opts.ExecutionID
	}
	functionTools, functionExecutors, err := opts.AgentFunctionTools(ctx, scope)
	if err != nil {
		return nil, nil, fmt.Errorf("bind agent function calls: %w", err)
	}
	if executors == nil {
		executors = map[string]interface{}{}
	}
	for _, tool := range functionTools {
		if tool.Function == nil {
			return nil, nil, fmt.Errorf("function service returned an unnamed tool")
		}
		name := tool.Function.Name
		if _, exists := executors[name]; exists {
			return nil, nil, fmt.Errorf("function tool %q collides with an execution tool", name)
		}
		execute, ok := functionExecutors[name].(func(context.Context, map[string]interface{}) (string, error))
		if !ok {
			return nil, nil, fmt.Errorf("function tool %q has no executor", name)
		}
		executors[name] = execute
		tools = append(tools, tool)
	}
	return tools, executors, nil
}
