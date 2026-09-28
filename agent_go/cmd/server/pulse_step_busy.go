package server

import (
	"context"
	"strings"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Pulse runs alongside the workflow's own schedules (its own lock since
// 9fccab474). It must not run a step the workflow is running at the same
// moment, or the step acts twice, e.g. sends the same emails again.

// isPulseScheduleSessionID reports a scheduled Pulse or Pulse fix-run session,
// from the schedule ID prefix newScheduleSessionID encodes.
func isPulseScheduleSessionID(sessionID string) bool {
	for _, id := range []string{manualWorkflowPulseScheduleID, pulseFixRunScheduleID} {
		prefix := id
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		if strings.HasPrefix(sessionID, "schedule-") && strings.Contains(sessionID, "--"+prefix+"_") {
			return true
		}
	}
	return false
}

// pulseStepBusyCheck is the StepBusyForPulse hook for the Pulse session
// pulseSession: a step is busy when a non-Pulse session is running it through
// execute_step, or a non-Pulse full-workflow run is on it now.
func (api *StreamingAPI) pulseStepBusyCheck(pulseSession string) func(context.Context, string, string) string {
	return func(_ context.Context, workspacePath, stepID string) string {
		for _, sid := range stepworkflow.RunningWorkflowStepSessions(workspacePath, stepID, pulseSession) {
			if sid != "" && !isPulseScheduleSessionID(sid) {
				return "session " + sid
			}
		}
		if api == nil {
			return ""
		}
		tracked := api.findRunningTrackedExecutionForWorkspaceWhere(workspacePath, func(exec *TrackedWorkflowExecution) bool {
			return strings.TrimSpace(exec.CurrentStepID) == strings.TrimSpace(stepID) &&
				exec.SessionID != pulseSession && !isPulseScheduleSessionID(exec.SessionID)
		})
		if tracked != nil {
			return "full workflow run in session " + tracked.SessionID
		}
		return ""
	}
}
