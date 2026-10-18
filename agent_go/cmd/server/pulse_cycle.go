package server

import (
	"context"
	"errors"
	"strings"
)

// Every active check, failed-run follow-up and manual cycle uses the same
// continuing Pulse. OFF changes its task and held permissions to a weekly
// report; housekeeping remains in the ordinary run's separate short turn.
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
	saved, _, err := latestPulseGoalCheck(context.WithoutCancel(ctx), sctx.WorkspacePath)
	if err != nil || saved == nil {
		s.sessionLogf(sctx, runSessionID, "[PULSE] turn finished without its saved goal report; cycle is partial (read error=%v)", err)
		return pulseLifecyclePartial
	}
	s.sessionLogf(sctx, runSessionID, "[PULSE] saved goal report check=%s report_only=%t next=%s", saved.CheckID, saved.ReportOnly, saved.NextCheckAt)
	return pulseLifecycleCompleted
}
