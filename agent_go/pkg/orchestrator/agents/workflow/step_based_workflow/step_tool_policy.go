package step_based_workflow

import (
	"context"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (hcpo *StepBasedWorkflowOrchestrator) refreshStepGoalPolicy(ctx context.Context, manifest string) {
	objective, _ := hcpo.ResolveWorkflowObjective(ctx)
	hasGoal := hcpo.platformStoresEnabled() && !hcpo.isRelayExecution() && strings.TrimSpace(objective) != ""
	hcpo.stepHasGoal.Store(hasGoal)

}

// Apply after category filtering: an old explicit list or wildcard cannot
// restore Builder-only or Crew function tools to an ordinary execution agent.
// This leaves the general step baseline and the owning pool's access ceiling intact.
func (hcpo *StepBasedWorkflowOrchestrator) filterOrdinaryStepTools(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	blocked := map[string]bool{
		"query_workflow_costs": true, "apply_workflow_db_migration": true,
		"list_functions": true, "call_function": true, "get_function_call": true, "reply_function_call": true,
		"get_goal_metrics": !hcpo.stepHasGoal.Load(), "record_goal_observations": !hcpo.stepHasGoal.Load(),
	}
	result := make([]llmtypes.Tool, 0, len(tools))
	resultExecutors := make(map[string]interface{}, len(executors))
	for _, tool := range tools {
		if tool.Function == nil || blocked[tool.Function.Name] {
			continue
		}
		result = append(result, tool)
		if executor, ok := executors[tool.Function.Name]; ok {
			resultExecutors[tool.Function.Name] = executor
		}
	}
	return result, resultExecutors
}

// Managers use the same baseline and can only narrow it. No capability tools
// are appended after filtering, so removed Builder tools cannot leak back in.
func (hcpo *StepBasedWorkflowOrchestrator) prepareOrchestratorCustomTools(cfg *AgentConfigs, dbAccess string) ([]llmtypes.Tool, map[string]interface{}) {
	tools, executors := hcpo.prepareCustomTools(cfg)
	result := make([]llmtypes.Tool, 0, len(tools))
	for _, tool := range tools {
		name := tool.Function.Name
		if (dbAccess == DBAccessNone && name == "query_workflow_db") || (dbAccess != DBAccessReadWrite && name == "mutate_workflow_db") {
			delete(executors, name)
			continue
		}
		result = append(result, tool)
	}
	return result, executors
}
