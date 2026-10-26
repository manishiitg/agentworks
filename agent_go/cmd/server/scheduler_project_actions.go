package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"sort"
	"strings"
	"time"
)

// dispatchProjectTimers belongs to the single scheduler clock. The dispatcher
// owns execution and per-person receipts, never a second polling service.
func (scheduler *SchedulerService) dispatchProjectTimers(ctx context.Context, now time.Time) {
	if scheduler == nil || scheduler.api == nil {
		return
	}
	s := scheduler.api.projectActions
	if s == nil {
		return
	}
	if s.api != nil && s.api.agentProfiles != nil {
		s.api.resumeAgentMessages()
	}

	users := map[string]struct{}{}
	for _, owner := range conditionCallerOwners() {
		users[owner] = struct{}{}
	}
	profiles := append(s.profilesWithSchedules(), s.profilesWithProjectSchedules()...)
	for _, profile := range profiles {
		for _, userID := range s.users(productAccessName(profile)) {
			users[userID] = struct{}{}
		}
	}
	// The scheduler pause holds timed runs here too: for every product (the global pause) or one at a time (PLAT-782).
	pauseConfig, pauseErr := LoadSchedulerConfig(ctx)
	if pauseErr != nil {
		scheduleLogf("[PRODUCT-SCHEDULE] cannot read the scheduler pause, timed runs continue: %v", pauseErr)
	}
	for userID := range users {
		jobs, err := s.JobsForUser(ctx, userID)
		if err != nil {
			scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot discover schedules: %v", userID, err)
			continue
		}
		for _, job := range jobs {
			if pauseConfig.ProductPaused(job.Profile.ID) {
				continue
			}
			if job.Schedule.Condition != nil && job.Effective().Enabled {
				go s.conditionService().drain(context.Background(), job)
				if job.Schedule.Condition.WakesOnChanges() {
					go s.conditionService().processWake(context.Background(), job)
				}
			}
			activatedAt := job.activatedAt()
			if strings.TrimSpace(job.Schedule.CronExpression) != "" && job.lastRun().IsZero() && strings.TrimSpace(job.State.ActivatedAt) == "" {
				// Persist the baseline once. Using this tick's Now without saving it
				// would make a never-run cron schedule chase tomorrow forever.
				if activatedAt.IsZero() {
					activatedAt = now.UTC()
				}
				if err := s.updateStateByKey(ctx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
					if strings.TrimSpace(st.ActivatedAt) == "" {
						st.ActivatedAt = activatedAt.Format(time.RFC3339)
					}
				}); err != nil {
					scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot persist activation baseline for %s: %v", job.ID(), userID, err)
					continue
				}
				job.State.ActivatedAt = activatedAt.Format(time.RFC3339)
			}
			in := productschedule.Inputs{
				Now:                 now,
				ActivatedAt:         activatedAt,
				LastRun:             job.lastRun(),
				LastAttempt:         job.lastAttempt(),
				ConsecutiveFailures: job.State.ConsecutiveFailures,
				SinceInteractive:    productInteractionNever,
			}
			if s.sinceInteractive != nil {
				in.SinceInteractive = s.sinceInteractive(userID, job.Profile)
			}
			d := productschedule.Decide(job.Effective(), in)
			if job.ProjectID != "" {
				d = projectTimerDecision(job, now)
			}
			if d.Deferred {
				s.setDeferred(userID, job.ID(), d.Reason)
			} else {
				s.setDeferred(userID, job.ID(), "")
			}
			if !d.Run {
				if job.ProjectID != "" && !d.ScheduledFor.IsZero() {
					if err := s.updateStateByKey(ctx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
						st.LastAttemptAt = d.ScheduledFor.UTC().Format(time.RFC3339)
					}); err != nil {
						scheduleLogf("[SCHEDULER] cannot record skipped project occurrence job=%s error=%v", job.ID(), err)
					}
				}
				if d.Deferred {
					scheduleLogf("[PRODUCT-SCHEDULE] %s deferring for %s: %s", job.ID(), userID, d.Reason)
				}
				continue
			}
			// Claim this timer occurrence before dispatch. A later tick can queue
			// the next occurrence, but cannot replay the one already accepted.
			if job.ProjectID != "" {
				if err := s.updateStateByKey(ctx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
					st.LastAttemptAt = d.ScheduledFor.UTC().Format(time.RFC3339)
				}); err != nil {
					scheduleLogf("[SCHEDULER] cannot record project occurrence job=%s error=%v", job.ID(), err)
					continue
				}
			}
			go func(job productScheduleJob, scheduledFor time.Time) {
				if when := job.Schedule.When; when != nil {
					loc, err := time.LoadLocation(when.Timezone)
					if err != nil {
						scheduleLogf("[SCHEDULER] invalid calendar timezone job=%s", job.ID())
						return
					}
					for _, date := range when.Dates {
						at, err := time.ParseInLocation("2006-01-02 15:04", date.Date+" "+date.Time, loc)
						if err != nil || !at.Equal(scheduledFor) || len(date.Input) == 0 {
							continue
						}
						var input scheduleExecutionInput
						if err := json.Unmarshal(date.Input, &input); err != nil {
							scheduleLogf("[SCHEDULER] invalid calendar input job=%s", job.ID())
							return
						}
						if input.Messages != nil {
							job.Schedule.Messages = input.Messages
						}
					}
				}
				if _, err := s.Run(context.Background(), job, "cron", scheduledFor); err != nil && !errors.Is(err, productschedule.ErrAlreadyRunning) {
					scheduleLogf("[PRODUCT-SCHEDULE] %s failed for %s: %v", job.ID(), job.UserID, err)
				}
			}(job, d.ScheduledFor)
		}
	}
}

func projectTimerDecision(job productScheduleJob, now time.Time) productschedule.Decision {
	s := job.Effective()
	if !s.Enabled {
		return productschedule.Decision{Reason: "disabled"}
	}
	if s.Condition != nil && (s.Condition.CheckMode == "event" || s.Condition.CheckMode == "inbox") {
		return productschedule.Decision{Reason: "waiting for an event"}
	}
	after := job.activatedAt()
	if last := job.lastRun(); last.After(after) {
		after = last
	}
	if last := job.lastAttempt(); last.After(after) {
		after = last
	}
	if after.IsZero() {
		return productschedule.Decision{Reason: "waiting for activation"}
	}
	var occurrences []time.Time
	if s.When != nil && len(s.When.Dates) > 0 {
		loc, err := time.LoadLocation(s.When.Timezone)
		if err != nil {
			return productschedule.Decision{Reason: err.Error()}
		}
		for _, date := range s.When.Dates {
			at, err := time.ParseInLocation("2006-01-02 15:04", date.Date+" "+date.Time, loc)
			if err != nil {
				return productschedule.Decision{Reason: err.Error()}
			}
			if at.After(after) && !at.After(now) {
				occurrences = append(occurrences, at)
			}
		}
		sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].Before(occurrences[j]) })
	} else {
		clock, err := (projectactions.When{Cron: s.CronExpression, Timezone: s.Timezone}).CronSchedule()
		if err != nil {
			return productschedule.Decision{Reason: err.Error()}
		}
		occurrences = dueCronOccurrences(clock, after, now)
	}
	skipped, due := splitOnTimeOccurrences(occurrences, now)
	if due == nil {
		d := productschedule.Decision{Reason: "no on-time occurrence"}
		if len(skipped) > 0 {
			d.ScheduledFor = skipped[len(skipped)-1]
		}
		return d
	}
	return productschedule.Decision{Run: true, ScheduledFor: *due, Reason: "due"}
}
