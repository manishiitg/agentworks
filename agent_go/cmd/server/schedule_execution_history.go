package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	workflowScheduleExecutionHistoryVersion = 1
	workflowScheduleHistoryRetention        = 7 * 24 * time.Hour
	workflowScheduleMissedGracePeriod       = 1 * time.Minute
	workflowScheduleMatchTolerance          = 5 * time.Minute

	workflowScheduleMissedReasonNoExecution = "no_execution_recorded"
)

var workflowScheduleExecutionHistoryMu sync.Mutex

type WorkflowScheduleExecutionHistoryFile struct {
	Version   int                                       `json:"version"`
	Schedules map[string]WorkflowScheduleExecutionTrack `json:"schedules"`
}

type WorkflowScheduleExecutionTrack struct {
	ScheduleID     string                            `json:"schedule_id"`
	CronExpression string                            `json:"cron_expression"`
	Timezone       string                            `json:"timezone,omitempty"`
	Enabled        bool                              `json:"enabled"`
	WindowStartAt  time.Time                         `json:"window_start_at"`
	UpdatedAt      time.Time                         `json:"updated_at"`
	Executions     []WorkflowScheduleExecutionRecord `json:"executions,omitempty"`
}

type WorkflowScheduleExecutionRecord struct {
	StartedAt time.Time `json:"started_at"`
}

type WorkflowScheduleMissedStatus struct {
	MissedRunCount    int
	LatestMissedRunAt *time.Time
	MissedRunReason   string
}

func workflowScheduleExecutionHistoryPath(workspacePath string) string {
	return workspacePath + "/config/schedule-execution-history.json"
}

func ReadWorkflowScheduleExecutionHistory(ctx context.Context, workspacePath string) (*WorkflowScheduleExecutionHistoryFile, error) {
	content, exists, err := readFileFromWorkspace(ctx, workflowScheduleExecutionHistoryPath(workspacePath))
	if err != nil {
		return nil, fmt.Errorf("failed to read schedule execution history: %w", err)
	}
	if !exists {
		return &WorkflowScheduleExecutionHistoryFile{
			Version:   workflowScheduleExecutionHistoryVersion,
			Schedules: map[string]WorkflowScheduleExecutionTrack{},
		}, nil
	}

	var history WorkflowScheduleExecutionHistoryFile
	if err := json.Unmarshal([]byte(content), &history); err != nil {
		return nil, fmt.Errorf("failed to parse schedule execution history: %w", err)
	}
	normalizeWorkflowScheduleExecutionHistory(&history, time.Now().UTC())
	return &history, nil
}

func WriteWorkflowScheduleExecutionHistory(ctx context.Context, workspacePath string, history *WorkflowScheduleExecutionHistoryFile) error {
	if history == nil {
		history = &WorkflowScheduleExecutionHistoryFile{}
	}
	normalizeWorkflowScheduleExecutionHistory(history, time.Now().UTC())

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal schedule execution history: %w", err)
	}
	return writeFileToWorkspace(ctx, workflowScheduleExecutionHistoryPath(workspacePath), string(data))
}

func EnsureWorkflowScheduleExecutionTracker(ctx context.Context, workspacePath string, sched WorkflowSchedule, now time.Time) error {
	workflowScheduleExecutionHistoryMu.Lock()
	defer workflowScheduleExecutionHistoryMu.Unlock()

	history, err := ReadWorkflowScheduleExecutionHistory(ctx, workspacePath)
	if err != nil {
		history = &WorkflowScheduleExecutionHistoryFile{
			Version:   workflowScheduleExecutionHistoryVersion,
			Schedules: map[string]WorkflowScheduleExecutionTrack{},
		}
	}

	tracker, changed := ensureWorkflowScheduleExecutionTracker(history, sched, now.UTC())
	if !changed {
		return nil
	}
	history.Schedules[sched.ID] = tracker
	return WriteWorkflowScheduleExecutionHistory(ctx, workspacePath, history)
}

// WorkflowScheduleTrackingWindowStart returns the earliest occurrence cursor
// that the scheduler promised to account for in the current configuration
// window. The durable scheduler-state database is normally authoritative, but
// a newly-created/replaced state database has no fire decision to resume from.
// In that case this tracker prevents a restart from silently forgetting cron
// occurrences between the schedule's creation (or last config change) and the
// server coming back up.
func WorkflowScheduleTrackingWindowStart(ctx context.Context, workspacePath, scheduleID string) (time.Time, bool) {
	workflowScheduleExecutionHistoryMu.Lock()
	defer workflowScheduleExecutionHistoryMu.Unlock()

	history, err := ReadWorkflowScheduleExecutionHistory(ctx, workspacePath)
	if err != nil || history == nil {
		return time.Time{}, false
	}
	tracker, ok := history.Schedules[scheduleID]
	if !ok || tracker.WindowStartAt.IsZero() {
		return time.Time{}, false
	}
	return tracker.WindowStartAt.UTC(), true
}

func RecordWorkflowScheduleExecution(ctx context.Context, workspacePath string, sched WorkflowSchedule, startedAt time.Time) error {
	workflowScheduleExecutionHistoryMu.Lock()
	defer workflowScheduleExecutionHistoryMu.Unlock()

	history, err := ReadWorkflowScheduleExecutionHistory(ctx, workspacePath)
	if err != nil {
		history = &WorkflowScheduleExecutionHistoryFile{
			Version:   workflowScheduleExecutionHistoryVersion,
			Schedules: map[string]WorkflowScheduleExecutionTrack{},
		}
	}

	tracker, _ := ensureWorkflowScheduleExecutionTracker(history, sched, startedAt.UTC())
	tracker.Executions = append(tracker.Executions, WorkflowScheduleExecutionRecord{StartedAt: startedAt.UTC()})
	tracker.UpdatedAt = startedAt.UTC()
	normalizeWorkflowScheduleExecutionTrack(&tracker, startedAt.UTC())
	history.Schedules[sched.ID] = tracker
	return WriteWorkflowScheduleExecutionHistory(ctx, workspacePath, history)
}

func ComputeWorkflowScheduleMissedStatus(sched WorkflowSchedule, tracker *WorkflowScheduleExecutionTrack, now time.Time) WorkflowScheduleMissedStatus {
	if !sched.Enabled || tracker == nil {
		return WorkflowScheduleMissedStatus{}
	}
	if scheduleTypeOrDefault(sched.ScheduleType) != "cron" {
		return WorkflowScheduleMissedStatus{}
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(sched.CronExpression)
	if err != nil {
		return WorkflowScheduleMissedStatus{}
	}

	loc, err := time.LoadLocation(sched.Timezone)
	if err != nil || loc == nil {
		loc = time.UTC
	}

	windowStart := now.UTC().Add(-workflowScheduleHistoryRetention)
	if !tracker.WindowStartAt.IsZero() && tracker.WindowStartAt.After(windowStart) {
		windowStart = tracker.WindowStartAt.UTC()
	}

	windowEnd := now.UTC().Add(-workflowScheduleMissedGracePeriod)
	if !windowEnd.After(windowStart) {
		return WorkflowScheduleMissedStatus{}
	}

	actualRuns := make([]time.Time, 0, len(tracker.Executions))
	for _, execution := range tracker.Executions {
		startedAt := execution.StartedAt.UTC()
		if startedAt.Before(windowStart.Add(-workflowScheduleMatchTolerance)) || startedAt.After(windowEnd.Add(workflowScheduleMatchTolerance)) {
			continue
		}
		actualRuns = append(actualRuns, startedAt)
	}
	sort.Slice(actualRuns, func(i, j int) bool {
		return actualRuns[i].Before(actualRuns[j])
	})
	if len(actualRuns) > 0 {
		latestActualRun := actualRuns[len(actualRuns)-1]
		if latestActualRun.After(windowStart) {
			windowStart = latestActualRun
			actualRuns = nil
		}
	}

	expectedRuns := make([]time.Time, 0)
	cursor := windowStart.In(loc).Add(-time.Second)
	windowEndLocal := windowEnd.In(loc)
	for {
		next := schedule.Next(cursor)
		// Next returns zero time for a date that never comes (30 February);
		// without this the loop appends zero times until memory runs out.
		if next.IsZero() || !next.After(cursor) || next.After(windowEndLocal) {
			break
		}
		expectedRuns = append(expectedRuns, next.UTC())
		cursor = next
	}

	missedCount := 0
	var latestMissed time.Time
	actualIdx := 0
	for _, expected := range expectedRuns {
		nextExpected := schedule.Next(expected.In(loc)).UTC()
		slotStart := expected.Add(-workflowScheduleMatchTolerance)
		slotEnd := nextExpected.Add(-workflowScheduleMatchTolerance)

		for actualIdx < len(actualRuns) && actualRuns[actualIdx].Before(slotStart) {
			actualIdx++
		}

		if actualIdx < len(actualRuns) && actualRuns[actualIdx].Before(slotEnd) {
			actualIdx++
			continue
		}

		missedCount++
		latestMissed = expected
	}

	if missedCount == 0 {
		return WorkflowScheduleMissedStatus{}
	}

	latestMissedUTC := latestMissed.UTC()
	return WorkflowScheduleMissedStatus{
		MissedRunCount:    missedCount,
		LatestMissedRunAt: &latestMissedUTC,
		MissedRunReason:   workflowScheduleMissedReasonNoExecution,
	}
}

func ensureWorkflowScheduleExecutionTracker(history *WorkflowScheduleExecutionHistoryFile, sched WorkflowSchedule, now time.Time) (WorkflowScheduleExecutionTrack, bool) {
	if history.Schedules == nil {
		history.Schedules = map[string]WorkflowScheduleExecutionTrack{}
	}
	history.Version = workflowScheduleExecutionHistoryVersion

	tracker, exists := history.Schedules[sched.ID]
	if !exists {
		return WorkflowScheduleExecutionTrack{
			ScheduleID:     sched.ID,
			CronExpression: sched.CronExpression,
			Timezone:       sched.Timezone,
			Enabled:        sched.Enabled,
			WindowStartAt:  now,
			UpdatedAt:      now,
			Executions:     []WorkflowScheduleExecutionRecord{},
		}, true
	}

	changed := false
	if tracker.ScheduleID == "" {
		tracker.ScheduleID = sched.ID
		changed = true
	}
	if tracker.CronExpression != sched.CronExpression || tracker.Timezone != sched.Timezone || tracker.Enabled != sched.Enabled {
		tracker.CronExpression = sched.CronExpression
		tracker.Timezone = sched.Timezone
		tracker.Enabled = sched.Enabled
		tracker.WindowStartAt = now
		tracker.UpdatedAt = now
		tracker.Executions = []WorkflowScheduleExecutionRecord{}
		return tracker, true
	}
	if tracker.WindowStartAt.IsZero() {
		tracker.WindowStartAt = now
		changed = true
	}
	if tracker.UpdatedAt.IsZero() {
		tracker.UpdatedAt = now
		changed = true
	}
	if normalizeWorkflowScheduleExecutionTrack(&tracker, now) {
		changed = true
	}
	return tracker, changed
}

func normalizeWorkflowScheduleExecutionHistory(history *WorkflowScheduleExecutionHistoryFile, now time.Time) {
	if history.Version == 0 {
		history.Version = workflowScheduleExecutionHistoryVersion
	}
	if history.Schedules == nil {
		history.Schedules = map[string]WorkflowScheduleExecutionTrack{}
	}

	cutoff := now.UTC().Add(-workflowScheduleHistoryRetention)
	for scheduleID, tracker := range history.Schedules {
		normalizeWorkflowScheduleExecutionTrack(&tracker, now.UTC())
		if len(tracker.Executions) == 0 && !tracker.UpdatedAt.IsZero() && tracker.UpdatedAt.UTC().Before(cutoff) {
			delete(history.Schedules, scheduleID)
			continue
		}
		history.Schedules[scheduleID] = tracker
	}
}

func normalizeWorkflowScheduleExecutionTrack(tracker *WorkflowScheduleExecutionTrack, now time.Time) bool {
	if tracker == nil {
		return false
	}
	changed := false
	if tracker.Executions == nil {
		tracker.Executions = []WorkflowScheduleExecutionRecord{}
		changed = true
	}
	if tracker.WindowStartAt.IsZero() {
		tracker.WindowStartAt = now.UTC()
		changed = true
	}
	if tracker.UpdatedAt.IsZero() {
		tracker.UpdatedAt = now.UTC()
		changed = true
	}

	cutoff := now.UTC().Add(-workflowScheduleHistoryRetention)
	trimmed := make([]WorkflowScheduleExecutionRecord, 0, len(tracker.Executions))
	for _, execution := range tracker.Executions {
		if execution.StartedAt.IsZero() {
			continue
		}
		startedAt := execution.StartedAt.UTC()
		if startedAt.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, WorkflowScheduleExecutionRecord{StartedAt: startedAt})
	}
	sort.Slice(trimmed, func(i, j int) bool {
		return trimmed[i].StartedAt.Before(trimmed[j].StartedAt)
	})
	if len(trimmed) != len(tracker.Executions) {
		changed = true
	} else {
		for i := range trimmed {
			if !trimmed[i].StartedAt.Equal(tracker.Executions[i].StartedAt.UTC()) {
				changed = true
				break
			}
		}
	}
	tracker.Executions = trimmed
	return changed
}
