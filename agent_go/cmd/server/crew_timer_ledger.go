package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Crew timers share the existing durable scheduler ledger. The dispatcher has
// already claimed the conversation; recording evidence never claims it again.
// Functions, webhooks, watchers and built-in product messages keep their own
// execution records and do not become timers as a side effect.
func (s *ProjectActionDispatcher) recordCrewTimerAdmission(ctx context.Context, job productScheduleJob, runID, trigger string, occurrence, started time.Time) (*SchedulerService, error) {
	if job.Profile.ID != "work" || job.ProjectID == "" || job.AutomationKind != "" || job.Schedule.Condition != nil {
		return nil, nil
	}
	if s.api == nil || s.api.scheduler == nil {
		return nil, fmt.Errorf("Crew timer run ledger is unavailable")
	}
	scheduler := s.api.scheduler
	scheduler.stateStoreMu.RLock()
	defer scheduler.stateStoreMu.RUnlock()
	if scheduler.stateStore == nil {
		return nil, fmt.Errorf("Crew timer run ledger is unavailable")
	}
	if err := scheduler.stateStore.RecordAdmittedRun(ctx, schedulerstate.Run{
		RunID: runID, ScopeType: "crew", ScopeID: job.ProjectID,
		ScheduleID: job.Schedule.ID, TriggerSource: trigger,
		ScheduledFor: occurrence, StartedAt: started,
	}); err != nil {
		return nil, fmt.Errorf("record Crew timer admission: %w", err)
	}
	scheduleLogf("[CREW_TIMER] admitted crew=%s schedule=%s run=%s trigger=%s", job.ProjectID, job.Schedule.ID, runID, trigger)
	return scheduler, nil
}

func finishCrewTimerEvidence(ctx context.Context, scheduler *SchedulerService, job productScheduleJob, runID, sessionID string, runErr error) error {
	if scheduler == nil {
		return nil
	}
	terminal := schedulerstate.StateCompleted
	reason := "Crew timer completed"
	errText := ""
	if runErr != nil {
		terminal = schedulerstate.StateFailed
		errText = runErr.Error()
		reason = "Crew timer failed"
		if errors.Is(runErr, context.Canceled) {
			terminal = schedulerstate.StateStopped
			reason = "Crew timer stopped"
		}
	} else {
		scheduler.transitionScheduleRun(ctx, nil, schedulerstate.Transition{RunID: runID, To: schedulerstate.StateWorkflowFinished, SessionID: sessionID, Reason: "Crew timer turn finished"})
	}
	scheduler.transitionScheduleRun(ctx, nil, schedulerstate.Transition{
		RunID: runID, To: terminal, SessionID: strings.TrimSpace(sessionID),
		Reason: reason, ErrorMessage: errText,
	})
	scheduler.stateStoreMu.RLock()
	saved, err := scheduler.stateStore.GetRun(context.WithoutCancel(ctx), runID)
	scheduler.stateStoreMu.RUnlock()
	if err != nil || saved.State != terminal {
		scheduleLogf("[CREW_TIMER] terminal_persistence_failed crew=%s schedule=%s run=%s expected=%s error=%v", job.ProjectID, job.Schedule.ID, runID, terminal, err)
		return fmt.Errorf("Crew timer terminal evidence was not saved: expected %s, found %s: %v", terminal, saved.State, err)
	}
	scheduleLogf("[CREW_TIMER] finished crew=%s schedule=%s run=%s outcome=%s", job.ProjectID, job.Schedule.ID, runID, terminal)
	return nil
}
