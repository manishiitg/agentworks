package server

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Only an occurrence seen on time enters the existing dependency retry queue.
// Waiting preserves that occurrence after the ordinary two-minute allowance;
// an offline gap without such a pending row never becomes catch-up work.
func (s *SchedulerService) crewTimerDependencyDecision(ctx context.Context, job productScheduleJob, now time.Time) (productschedule.Decision, string) {
	d := projectTimerDecision(job, now)
	if job.Profile.ID != "work" || job.ProjectID == "" || job.Schedule.Condition != nil {
		return d, "cron"
	}
	s.stateStoreMu.RLock()
	defer s.stateStoreMu.RUnlock()
	store := s.stateStore
	when := projectactions.When{}
	if job.Schedule.When != nil {
		when = *job.Schedule.When
	}
	when.Timezone = job.Schedule.Timezone
	deferRead := func(err error) (productschedule.Decision, string) {
		scheduleLogf("[CREW_TIMER] dependency_state_failed crew=%s schedule=%s error=%v", job.ProjectID, job.Schedule.ID, err)
		// Do not consume the clock cursor if durable state could not be read.
		return productschedule.Decision{Deferred: true, Reason: "waiting for saved schedule results"}, "cron"
	}
	if store == nil {
		if when.After != nil {
			return deferRead(errors.New("schedule run ledger is unavailable"))
		}
		return d, "cron"
	}
	pending, err := store.GetPendingOccurrence(ctx, "crew", job.ProjectID, job.Schedule.ID)
	if err != nil && !errors.Is(err, schedulerstate.ErrRunNotFound) {
		return deferRead(err)
	}
	hasPending := err == nil
	record := func(decision, reason string, occurrence time.Time) error {
		return store.RecordFireDecision(ctx, schedulerstate.FireDecision{DecisionID: uuid.NewString(), ScopeType: "crew", ScopeID: job.ProjectID, ScheduleID: job.Schedule.ID,
			TriggerSource: "cron", ScheduledFor: occurrence, FiredAt: now, Decision: decision, Reason: reason})
	}
	finishPending := func(decision, reason string) (productschedule.Decision, string) {
		if err := record(decision, reason, pending.ScheduledFor); err != nil {
			return deferRead(err)
		}
		if err := store.DeletePendingOccurrence(ctx, "crew", job.ProjectID, job.Schedule.ID); err != nil {
			return deferRead(err)
		}
		return productschedule.Decision{ScheduledFor: pending.ScheduledFor, Reason: reason}, "cron"
	}
	trigger := "cron"
	if hasPending {
		if !job.Effective().Enabled {
			return finishPending("skipped_disabled", "schedule disabled while waiting")
		}
		// An admitted occurrence must never start again after interruption or
		// a stale pending projection. Its recorded outcome remains authoritative.
		prior, priorErr := store.RunForScheduleOccurrence(ctx, "crew", job.ProjectID, job.Schedule.ID, pending.ScheduledFor, pending.ScheduledFor.Add(time.Nanosecond))
		if priorErr == nil {
			if prior.State == schedulerstate.StateInterrupted {
				return finishPending("skipped_interrupted", "this occurrence was interrupted; waiting for the next scheduled time")
			}
			if err := store.DeletePendingOccurrence(ctx, "crew", job.ProjectID, job.Schedule.ID); err != nil {
				return deferRead(err)
			}
			return productschedule.Decision{ScheduledFor: pending.ScheduledFor, Reason: "occurrence already admitted"}, "cron"
		}
		if !errors.Is(priorErr, schedulerstate.ErrRunNotFound) {
			return deferRead(priorErr)
		}
		if !now.Before(crewDependencyDayEnd(pending.ScheduledFor, when.Timezone)) {
			return finishPending("expired_dependency", "the scheduled day ended before its prerequisite finished")
		}
		d = productschedule.Decision{Run: true, ScheduledFor: pending.ScheduledFor, Reason: "retrying dependency occurrence"}
		trigger = "queued"
	}
	if !d.Run {
		if !d.ScheduledFor.IsZero() {
			if err := record("missed_scheduler_gap", "the scheduled time passed while the clock was offline", d.ScheduledFor); err != nil {
				return deferRead(err)
			}
		}
		return d, trigger
	}
	result, reason := productschedule.EvaluateDependencies(ctx, store, "crew", job.ProjectID, when, d.ScheduledFor, now)
	switch result {
	case productschedule.DependencyWaiting:
		if err := store.UpsertPendingOccurrence(ctx, schedulerstate.PendingOccurrence{ScopeType: "crew", ScopeID: job.ProjectID, ScheduleID: job.Schedule.ID,
			ScheduledFor: d.ScheduledFor, LatestScheduledFor: d.ScheduledFor, QueuedAt: now, ExpiresAt: crewDependencyDayEnd(d.ScheduledFor, when.Timezone), TriggerSource: "cron", Policy: "retry", Reason: reason}); err != nil {
			return deferRead(err)
		}
		if err := record("waiting_for_dependency", reason, d.ScheduledFor); err != nil {
			return deferRead(err)
		}
		return productschedule.Decision{Deferred: true, ScheduledFor: d.ScheduledFor, Reason: reason}, trigger
	case productschedule.DependencyBlocked:
		if err := record("blocked_dependency", reason, d.ScheduledFor); err != nil {
			return deferRead(err)
		}
		if hasPending {
			if err := store.DeletePendingOccurrence(ctx, "crew", job.ProjectID, job.Schedule.ID); err != nil {
				return deferRead(err)
			}
		}
		return productschedule.Decision{ScheduledFor: d.ScheduledFor, Reason: reason}, trigger
	default:
		return d, trigger
	}
}

// The occurrence's local day bounds dependency lookup and the lifetime of a
// wait. Add a calendar day, not 24 hours, so daylight-saving changes agree.
func crewDependencyDayEnd(occurrence time.Time, timezone string) time.Time {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	local := occurrence.In(location)
	year, month, day := local.Date()
	return time.Date(year, month, day+1, 0, 0, 0, 0, location).UTC()
}
