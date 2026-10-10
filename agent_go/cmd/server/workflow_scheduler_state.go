package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// WorkflowSchedulerState is a read-only projection of current pause flags, not
// an inference from run history or permission to change the owner's schedules.
type WorkflowSchedulerState struct {
	ObservedAt                   time.Time                     `json:"observed_at"`
	ConfigState                  string                        `json:"config_state"`
	GloballyPaused               *bool                         `json:"globally_paused"`
	Product                      string                        `json:"product,omitempty"`
	ProductPaused                *bool                         `json:"product_paused"`
	ProductEffectivelyPaused     *bool                         `json:"product_effectively_paused"`
	UpdatedAt                    *time.Time                    `json:"updated_at,omitempty"`
	PausedAt                     *time.Time                    `json:"paused_at,omitempty"`
	WorkflowAllSchedulesDisabled *bool                         `json:"workflow_all_schedules_disabled"`
	HistoryState                 string                        `json:"history_state"`
	RecentPauseEvents            []workflowSchedulerPauseEvent `json:"recent_pause_events"`
	Schedules                    []workflowScheduleState       `json:"schedules"`
	Note                         string                        `json:"note"`
}

type workflowSchedulerPauseEvent struct {
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Scope   string    `json:"scope"`
	Product string    `json:"product,omitempty"`
}

type workflowScheduleState struct {
	ID                    string     `json:"id"`
	Enabled               bool       `json:"enabled"`
	SchedulerPauseApplies bool       `json:"scheduler_pause_applies"`
	State                 string     `json:"state"`
	BlockingReasons       []string   `json:"blocking_reasons"`
	NextScheduledAt       *time.Time `json:"next_scheduled_at,omitempty"`
}

func readWorkflowSchedulerState(ctx context.Context, manifest *WorkflowManifest, now time.Time) WorkflowSchedulerState {
	view := WorkflowSchedulerState{
		ObservedAt: now.UTC(), ConfigState: "unknown", HistoryState: "unknown",
		RecentPauseEvents: []workflowSchedulerPauseEvent{}, Schedules: []workflowScheduleState{},
		Note: "Current persisted flags at observed_at. product_paused is the product's own flag; product_effectively_paused includes the global pause. Global/product pauses hold timed runs, not manual or function calls. eligible means pause/enabled flags allow timing, not a promise of execution: dependencies, capacity and runtime health still apply. Past skipped_paused runs, pause events, latest_check and memory are historical, not evidence of a current pause. Unknown reads are not permission to resume or run anything. Re-read list_schedules before claiming a current pause or asking the owner to resume.",
	}
	if manifest != nil {
		view.Product = workflowPauseProduct(manifest.Kind)
		allDisabled := workflowSchedulesAllPaused(manifest)
		view.WorkflowAllSchedulesDisabled = &allDisabled
	}
	if cfg, err := LoadSchedulerConfig(ctx); err == nil {
		view.ConfigState = "known"
		view.GloballyPaused = &cfg.GloballyPaused
		view.UpdatedAt, view.PausedAt = cfg.UpdatedAt, cfg.PausedAt
		if manifest != nil {
			productPaused := false
			for _, product := range cfg.PausedProducts {
				productPaused = productPaused || product == view.Product
			}
			effective := cfg.ProductPaused(view.Product)
			view.ProductPaused, view.ProductEffectivelyPaused = &productPaused, &effective
		}
	}
	// Share only the relevant pause timestamps/actions, never identity/client
	// metadata or pauses belonging to another product. Audit failure cannot
	// override successfully read current flags.
	if content, exists, err := readFileFromWorkspace(ctx, schedulerPauseLogPath); err == nil {
		view.HistoryState = "known"
		if exists {
			lines := strings.Split(content, "\n")
			for i := len(lines) - 1; i >= 0 && len(view.RecentPauseEvents) < 5; i-- {
				line := strings.TrimSpace(lines[i])
				if line == "" {
					continue
				}
				var event SchedulerPauseEvent
				if json.Unmarshal([]byte(line), &event) != nil || event.At.IsZero() || (event.Action != "paused" && event.Action != "resumed") {
					view.HistoryState = "partial"
					continue
				}
				if event.Product != "" && (manifest == nil || event.Product != view.Product) {
					continue
				}
				scope := "global"
				if event.Product != "" {
					scope = "product"
				}
				view.RecentPauseEvents = append(view.RecentPauseEvents, workflowSchedulerPauseEvent{At: event.At.UTC(), Action: event.Action, Scope: scope, Product: event.Product})
			}
		}
	}
	if manifest == nil {
		return view
	}
	for _, schedule := range manifest.Schedules {
		kind := scheduleTypeOrDefault(schedule.ScheduleType)
		state := workflowScheduleState{ID: schedule.ID, Enabled: schedule.Enabled, SchedulerPauseApplies: kind != "webhook", State: "eligible", BlockingReasons: []string{}}
		if !schedule.Enabled {
			state.BlockingReasons = append(state.BlockingReasons, "schedule_disabled")
		}
		if state.SchedulerPauseApplies {
			if view.ConfigState == "unknown" {
				state.BlockingReasons = append(state.BlockingReasons, "scheduler_state_unknown")
			} else {
				if *view.GloballyPaused {
					state.BlockingReasons = append(state.BlockingReasons, "global_pause")
				}
				if *view.ProductPaused {
					state.BlockingReasons = append(state.BlockingReasons, "product_pause")
				}
			}
		}
		switch {
		case !schedule.Enabled:
			state.State = "disabled"
		case state.SchedulerPauseApplies && view.ConfigState == "unknown":
			state.State = "unknown"
		case len(state.BlockingReasons) > 0:
			state.State = "paused"
		case kind == "cron":
			state.NextScheduledAt = getNextRunTimeAt(schedule.CronExpression, schedule.Timezone, now)
		case kind == "calendar":
			state.NextScheduledAt = getNextRunTimeForCalendarAt(schedule, now)
		}
		if state.NextScheduledAt != nil && !state.NextScheduledAt.After(now) {
			state.NextScheduledAt = nil
		}
		view.Schedules = append(view.Schedules, state)
	}
	return view
}
