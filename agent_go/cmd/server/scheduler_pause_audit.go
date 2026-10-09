package server

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"
)

// The global scheduler pause used to leave no trace of who set it: resuming
// cleared paused_by and the server logs rotated, so a Sunday of skipped runs
// (2026-09-26/27) could not be attributed to a person or an agent. Every
// change is now appended to a durable log, and a resume reports the runs the
// pause skipped so they can be caught up by hand.

const schedulerPauseLogPath = "config/scheduler-pause-log.jsonl"

const schedulerPauseLogKeep = 200

// SchedulerPauseEvent is one pause or resume.
type SchedulerPauseEvent struct {
	At          time.Time  `json:"at"`
	Action      string     `json:"action"`            // "paused" | "resumed"
	Product     string     `json:"product,omitempty"` // set when one product was paused or resumed on its own
	UserID      string     `json:"user_id,omitempty"`
	Username    string     `json:"username,omitempty"`
	Via         string     `json:"via,omitempty"` // the client's paused_by label, e.g. frontend-user
	UserAgent   string     `json:"user_agent,omitempty"`
	PausedSince *time.Time `json:"paused_since,omitempty"` // on resume: when the pause began
}

// SkippedWhilePaused is one schedule's occurrences the pause skipped.
type SkippedWhilePaused struct {
	WorkspacePath      string    `json:"workspace_path"`
	WorkflowLabel      string    `json:"workflow_label,omitempty"`
	ScheduleID         string    `json:"schedule_id"`
	ScheduleName       string    `json:"schedule_name,omitempty"`
	Count              int       `json:"count"`
	LatestScheduledFor time.Time `json:"latest_scheduled_for"`
}

func readSchedulerPauseEvents(ctx context.Context) []SchedulerPauseEvent {
	content, exists, err := readFileFromWorkspace(ctx, schedulerPauseLogPath)
	if err != nil || !exists {
		return nil
	}
	var events []SchedulerPauseEvent
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev SchedulerPauseEvent
		if json.Unmarshal([]byte(line), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events
}

// appendSchedulerPauseEvent records ev durably (keeping the newest entries)
// and in the server log.
func appendSchedulerPauseEvent(ctx context.Context, ev SchedulerPauseEvent) {
	who := strings.TrimSpace(ev.Username)
	if who == "" {
		who = ev.UserID
	}
	log.Printf("[SCHEDULER] global pause %s by user=%q via=%q agent=%q", ev.Action, who, ev.Via, ev.UserAgent)
	events := append(readSchedulerPauseEvents(ctx), ev)
	if len(events) > schedulerPauseLogKeep {
		events = events[len(events)-schedulerPauseLogKeep:]
	}
	var sb strings.Builder
	for _, e := range events {
		if raw, err := json.Marshal(e); err == nil {
			sb.Write(raw)
			sb.WriteByte('\n')
		}
	}
	if err := writeFileToWorkspace(ctx, schedulerPauseLogPath, sb.String()); err != nil {
		log.Printf("[SCHEDULER] Warning: could not record the pause change: %v", err)
	}
}

// recentSchedulerPauseEvents returns the newest n events, newest first.
func recentSchedulerPauseEvents(ctx context.Context, n int) []SchedulerPauseEvent {
	events := readSchedulerPauseEvents(ctx)
	out := make([]SchedulerPauseEvent, 0, n)
	for i := len(events) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, events[i])
	}
	return out
}

// skippedWhilePaused lists, per enabled schedule, the occurrences the global
// pause skipped between since and until.
func (s *SchedulerService) skippedWhilePaused(ctx context.Context, claims *UserClaims, since, until time.Time) []SkippedWhilePaused {
	if s == nil {
		return nil
	}
	workflows, err := s.DiscoverWorkflowManifestsCached(ctx, 5*time.Second)
	if err != nil {
		return nil
	}
	var out []SkippedWhilePaused
	for _, dw := range workflows {
		// Only workflows the caller may see, so a resume never lists another
		// user's schedules.
		if dw.Manifest == nil || workflowAccessForManifest(claims, dw.Manifest) == WorkflowAccessNone {
			continue
		}
		for _, sched := range dw.Manifest.Schedules {
			if !sched.Enabled {
				continue
			}
			decisions, err := s.ListFireDecisions(ctx, dw.WorkspacePath, sched.ID, 200)
			if err != nil {
				continue
			}
			entry := SkippedWhilePaused{WorkspacePath: dw.WorkspacePath, WorkflowLabel: dw.Manifest.Label, ScheduleID: sched.ID, ScheduleName: sched.Name}
			for _, d := range decisions {
				if d.Decision != "skipped_paused" {
					continue
				}
				at := d.ScheduledFor
				if at.IsZero() {
					at = d.FiredAt
				}
				if at.Before(since) || at.After(until) {
					continue
				}
				entry.Count++
				if at.After(entry.LatestScheduledFor) {
					entry.LatestScheduledFor = at
				}
			}
			if entry.Count > 0 {
				out = append(out, entry)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LatestScheduledFor.After(out[j].LatestScheduledFor) })
	return out
}

// SchedulerConfigResponse is the scheduler config plus its recent pause
// history and, on a resume, what the pause skipped.
type SchedulerConfigResponse struct {
	*SchedulerConfig
	RecentPauseEvents  []SchedulerPauseEvent `json:"recent_pause_events,omitempty"`
	SkippedWhilePaused []SkippedWhilePaused  `json:"skipped_while_paused,omitempty"`
}
