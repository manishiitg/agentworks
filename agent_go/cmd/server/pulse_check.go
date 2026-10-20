package server

import (
	"context"
	"fmt"
	"time"
)

// A periodic Pulse turn uses the same conversation as messages and failures.
func (s *SchedulerService) launchDuePulseChecks(ctx context.Context) {
	if paused, _, err := s.IsProductPaused(ctx, "agentworks"); err != nil || paused {
		return
	}
	workflows, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		scheduleLogf("[PULSE] cannot scan workflows: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, item := range workflows {
		if item.Manifest == nil || item.Manifest.Kind == "relay" || !item.Manifest.PulseEnabled() || !workflowHasSoul(ctx, item.WorkspacePath) {
			continue
		}
		state, err := readPulseScheduleState(ctx, item.WorkspacePath)
		if err != nil {
			continue
		}
		if state != nil && now.Before(state.LastStartedAt.Add(pulseCheckDefaultGap)) {
			continue
		}
		if s.runningPulseRuns() >= maxConcurrentPulseRuns {
			return
		}
		runID, err := s.triggerPulseRun(item.WorkspacePath, "cron")
		if err == nil {
			scheduleLogf("[PULSE] periodic turn started workflow=%s run=%s", item.WorkspacePath, runID)
		}
	}
}

func pulseLifecycleCheckStep(ctx context.Context, workspacePath, pulseRunID string, _ workflowNotificationContentInstructions) pulseLifecycleStep {
	perms, permissions := pulsePermissionsForWorkflow(ctx, workspacePath)
	return pulseLifecycleStep{label: "pulse", goalWork: &perms, query: fmt.Sprintf("PULSE TURN: check the goal and actual results for this workflow. Read the evidence you need, discuss the most useful next action with Builder, and follow up any promised outcome. Use the existing conversation and ordinary workflow records; no separate report or work ledger is required. Run: %s.\n\n%s", pulseRunID, permissions)}
}
