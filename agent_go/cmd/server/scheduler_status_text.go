package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Plain status text for the Schedules list, written for people who are not
// technical. The server decides it (owner rule: the backend decides) so every
// screen says the same thing: while the product's timed runs are paused every
// schedule reads Paused, never Missed, and the raw error text stays in
// last_error as detail for a tooltip or inspector.

// schedulePauseProduct names the pause group a schedule job belongs to:
// "agentworks" (Goals workflows), "relays", "work" (Crews) or "code".
func schedulePauseProduct(job ScheduledJobResponse) string {
	if profile, _, ok := parseProductScheduleJobID(job.ID); ok {
		return pauseProductForProfile(profile)
	}
	if profile, _, _, ok := parseProjectScheduleJobID(job.ID); ok {
		return pauseProductForProfile(profile)
	}
	if job.pauseProduct != "" {
		return job.pauseProduct
	}
	return "agentworks"
}

func pauseProductForProfile(profile string) string {

	return "work"
}

func scheduleStatusWord(job ScheduledJobResponse, paused bool) string {
	switch {
	case paused || !job.Enabled:
		return "Paused"
	case job.LastStatus == "running":
		return "Running"
	case job.LastStatus == "waiting_for_capacity":
		return "Waiting for capacity"
	case job.LastStatus == "waiting_for_workflow":
		return "Queued"
	case job.LastStatus == "partial":
		return "Partly completed"
	case job.LastStatus == "error" || job.LastStatus == "interrupted":
		return "Needs attention"
	case job.MissedRunCount > 0:
		return "Missed"
	}
	return "Scheduled"
}

// scheduleLastResult is one plain sentence about the last run, or empty.
func scheduleLastResult(job ScheduledJobResponse) string {
	switch job.LastStatus {
	case "error":
		return "The last run did not finish."
	case "partial", "interrupted":
		return "The last run finished with some problems."
	case "stopped":
		return "The last run was stopped."
	}
	return ""
}

// scheduleTimingSummary describes saved timing, never the caller's intended
// cadence. in_minutes is a one-time date, not a repeating interval (PLAT-871-23).
func scheduleTimingSummary(job ScheduledJobResponse) string {
	zone := strings.TrimSpace(job.Timezone)
	if zone == "" {
		zone = "UTC"
	}
	if job.ScheduleType == "calendar" {
		if len(job.CalendarItems) == 1 {
			date := job.CalendarItems[0]
			return fmt.Sprintf("One time at %s %s (%s); does not repeat.", date.Date, date.Time, zone)
		}
		return fmt.Sprintf("Runs once on each of %d saved dates (%s); does not repeat.", len(job.CalendarItems), zone)
	}
	interval := func(value string, base int) int {
		if value == "*" {
			return 1
		}
		if strings.HasPrefix(value, "*/") {
			n, err := strconv.Atoi(strings.TrimPrefix(value, "*/"))
			if err == nil && n > 0 && n < base && base%n == 0 {
				return n
			}
		}
		return 0
	}
	repeats := func(n int, unit string) string {
		if n != 1 {
			unit += "s"
		}
		return fmt.Sprintf("Repeats every %d %s (%s).", n, unit, zone)
	}
	cron := strings.TrimSpace(job.CronExpression)
	parts := strings.Fields(cron)
	if len(parts) == 5 && parts[2] == "*" && parts[3] == "*" && parts[4] == "*" {
		if parts[1] == "*" {
			if n := interval(parts[0], 60); n > 0 {
				return repeats(n, "minute")
			}
		}
		if parts[0] == "0" {
			if n := interval(parts[1], 24); n > 0 {
				return repeats(n, "hour")
			}
		}
	}
	if len(parts) == 5 && parts[2] == "*" && parts[3] == "*" && (parts[4] == "*" || parts[4] == "1-5") {
		minute, minuteErr := strconv.Atoi(parts[0])
		hour, hourErr := strconv.Atoi(parts[1])
		if minuteErr == nil && hourErr == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24 {
			days := "every day"
			if parts[4] == "1-5" {
				days = "on weekdays"
			}
			return fmt.Sprintf("Repeats %s at %02d:%02d (%s).", days, hour, minute, zone)
		}
	}
	if cron != "" {
		return fmt.Sprintf("Repeats on the saved cron schedule %s (%s).", cron, zone)
	}
	if job.CadenceHours > 0 {
		return repeats(job.CadenceHours, "hour")
	}
	return ""
}

// applyScheduleStatusText fills status_word and last_result on every job.
func applyScheduleStatusText(jobs []ScheduledJobResponse, config *SchedulerConfig) {
	for i := range jobs {
		jobs[i].TimingSummary = scheduleTimingSummary(jobs[i])
		paused := config.ProductPaused(schedulePauseProduct(jobs[i]))
		jobs[i].StatusWord = scheduleStatusWord(jobs[i], paused)
		jobs[i].RunOutcome = scheduleRunOutcome(jobs[i].LastStatus)
		jobs[i].HasRunIssue = jobs[i].Enabled && (jobs[i].LastStatus == "error" || jobs[i].LastStatus == "partial" || jobs[i].LastStatus == "interrupted")
		jobs[i].LastResult = ""
		if !paused {
			jobs[i].LastResult = scheduleLastResult(jobs[i])
		}
	}
}

// withScheduleStatusText is applyScheduleStatusText for one job's response, so
// after enable, disable, create, update or run-now the row is updated from the
// server and the browser never has to guess.
func withScheduleStatusText(ctx context.Context, job ScheduledJobResponse) ScheduledJobResponse {
	config, _ := LoadSchedulerConfig(ctx)
	jobs := []ScheduledJobResponse{job}
	applyScheduleStatusText(jobs, config)
	return jobs[0]
}

// scheduleRunOutcome describes history even when a schedule is now paused.
func scheduleRunOutcome(status string) string {
	switch status {
	case "success":
		return "Succeeded"
	case "error":
		return "Failed"
	case "partial":
		return "Partly completed"
	case "interrupted":
		return "Interrupted"
	case "stopped":
		return "Stopped"
	case "running":
		return "Running now"
	case "waiting_for_capacity":
		return "Waiting for capacity"
	case "waiting_for_workflow":
		return "Queued"
	default:
		return "Not run yet"
	}
}
