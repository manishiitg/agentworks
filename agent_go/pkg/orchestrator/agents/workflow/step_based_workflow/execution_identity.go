package step_based_workflow

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"

func (hcpo *StepBasedWorkflowOrchestrator) bridgeExecutionID() string {
	if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok && cab != nil {
		return cab.ExecutionID()
	}
	return ""
}
