package server

import (
	"context"
	"errors"
	"strings"
)

// Every active check, failed-run follow-up and manual cycle uses the same
// continuing Pulse. OFF starts no turn; housekeeping remains independent
// in the ordinary run's separate short turn.
func (s *SchedulerService) runSinglePulseCycle(ctx context.Context, sctx *ScheduleContext, runSessionID string) pulseLifecycleResult {
	pulseRunID := strings.TrimSpace(runSessionID)
	if pulseRunID == "" {
		pulseRunID = s.newScheduleSessionID(sctx)
	}
	step := pulseLifecycleGoalCheckStep(ctx, sctx.WorkspacePath, pulseRunID, notificationInstructionsFromCapabilities(sctx.Capabilities))
	step.goalLead = true
	result := s.runGoalLeadPassStep(ctx, sctx, step)
	if result.outcome == pulseLifecycleStepInterrupted || errors.Is(result.err, context.Canceled) {
		return pulseLifecycleStopped
	}
	if result.outcome != pulseLifecycleStepCompleted {
		return pulseLifecyclePartial
	}
	s.sessionLogf(sctx, runSessionID, "[PULSE] goal check saved and turn completed")
	return pulseLifecycleCompleted
}
