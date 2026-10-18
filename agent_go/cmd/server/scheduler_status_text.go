package server

import (
	"context"
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
	if strings.EqualFold(strings.TrimSpace(profile), "code") {
		return "code"
	}
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

// applyScheduleStatusText fills status_word and last_result on every job.
func applyScheduleStatusText(jobs []ScheduledJobResponse, config *SchedulerConfig) {
	for i := range jobs {
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
