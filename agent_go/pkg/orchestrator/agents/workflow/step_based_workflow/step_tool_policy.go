package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A goal comes from soul, not from DB availability. Pulse configuration uses
// the same legacy schedule compatibility signals as WorkflowManifest.PulseEnabled.
func (hcpo *StepBasedWorkflowOrchestrator) refreshStepGoalPolicy(ctx context.Context, manifest string) {
	objective, _ := hcpo.ResolveWorkflowObjective(ctx)
	hasGoal := hcpo.platformStoresEnabled() && !hcpo.isRelayExecution() && strings.TrimSpace(objective) != ""
	hcpo.stepHasGoal.Store(hasGoal)
	var m struct {
		Pulse *struct {
			Enabled bool `json:"enabled"`
		} `json:"pulse"`
		Schedules []struct {
			Enabled    bool   `json:"enabled"`
			ReviewOnly bool   `json:"pulse_review_only"`
			Mode       string `json:"pulse_mode"`
		} `json:"schedules"`
	}
	enabled := false
	if json.Unmarshal([]byte(manifest), &m) == nil {
		enabled = m.Pulse != nil && m.Pulse.Enabled
		for _, schedule := range m.Schedules {
			enabled = enabled || (schedule.Enabled && (schedule.ReviewOnly || strings.EqualFold(strings.TrimSpace(schedule.Mode), "full")))
		}
	}
	hcpo.stepPulseEnabled.Store(hasGoal && enabled)
}

// Apply after category filtering: an old explicit list or wildcard cannot
// restore Builder-only or Crew function tools to an ordinary execution agent.
// This leaves the general step baseline and the owning pool's access ceiling intact.
func (hcpo *StepBasedWorkflowOrchestrator) filterOrdinaryStepTools(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	blocked := map[string]bool{
		"notify_user": true, "query_workflow_costs": true, "apply_workflow_db_migration": true,
		"list_functions": true, "call_function": true, "get_function_call": true, "reply_function_call": true,
		"get_goal_metrics": !hcpo.stepHasGoal.Load(), "record_goal_observations": !hcpo.stepHasGoal.Load(),
		"ask_pulse": !hcpo.stepPulseEnabled.Load(), "ask_goal_lead": !hcpo.stepPulseEnabled.Load(),
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
