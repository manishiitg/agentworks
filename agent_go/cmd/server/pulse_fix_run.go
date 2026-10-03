package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Pulse fix runs close the loop between finding a problem and fixing it. The
// full Pulse runs on its own daily-to-weekly schedule; a fix run is a short
// Technical Review+Fix pass (after Plan Drift when due) that starts as soon as
// a workflow has something to fix: an open workflow issue, a new step concern,
// or a failed scheduled run. A stable workflow with nothing to fix gets none,
// so Pulse is fast while there are problems and quiet once there are not.
// Fix runs never move the full Pulse schedule.

const (
	pulseFixRunScheduleID = "pulse-fix-run"
	// The gaps and daily cap bound cost. Fresh trouble (a failed run or new
	// step concerns) is fixed quickly; a backlog of older issues a pass could
	// not close is retried, but not continuously.
	pulseFixRunFreshGap   = 90 * time.Minute
	pulseFixRunBacklogGap = 4 * time.Hour
	pulseFixRunMaxPerDay  = 6
)

// maxConcurrentPulseRuns bounds Pulse work server-wide: full Pulses and fix
// runs together. Every workflow can be due at once (after a restart, or when a
// pacing change makes waiting requests due), and each run is a long agent
// session; the rest wait for later scheduler ticks. Full Pulses launch first.
const maxConcurrentPulseRuns = 2

// pulseLauncherMu serializes the per-tick Pulse launcher passes, so the cap
// check and the start it guards are never interleaved across ticks.
var pulseLauncherMu sync.Mutex

// runningPulseRuns counts full Pulses and fix runs in progress on any workflow.
func (s *SchedulerService) runningPulseRuns() int {
	s.runtimeStatesMu.RLock()
	defer s.runtimeStatesMu.RUnlock()
	running := 0
	for key, state := range s.runtimeStates {
		if state == nil || state.LastStatus != "running" {
			continue
		}
		if strings.HasSuffix(key, scheduleScopeSeparator+manualWorkflowPulseScheduleID) ||
			strings.HasSuffix(key, scheduleScopeSeparator+pulseFixRunScheduleID) {
			running++
		}
	}
	return running
}

const pulseFixRunsSchema = `CREATE TABLE IF NOT EXISTS pulse_fix_runs (
	run_id TEXT PRIMARY KEY,
	started_at TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT ''
)`

func ensurePulseFixRunsSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, pulseFixRunsSchema)
	return err
}

// recordPulseFixRunStarted stores one started fix run.
func recordPulseFixRunStarted(ctx context.Context, workspacePath, runID, reason string, startedAt time.Time) error {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseFixRunsSchema(ctx, db); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO pulse_fix_runs (run_id, started_at, reason) VALUES (?,?,?)`,
		strings.TrimSpace(runID), formatStoredTime(startedAt), strings.TrimSpace(reason))
	return err
}

// pulseFixRunHistory returns the latest fix-run start and how many started in
// the 24 hours before now.
func pulseFixRunHistory(ctx context.Context, workspacePath string, now time.Time) (last time.Time, lastDay int, err error) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return time.Time{}, 0, err
	}
	defer db.Close()
	if err := ensurePulseFixRunsSchema(ctx, db); err != nil {
		return time.Time{}, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT started_at FROM pulse_fix_runs`)
	if err != nil {
		return time.Time{}, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return time.Time{}, 0, err
		}
		started := parseStoredTime(raw)
		if started.After(last) {
			last = started
		}
		if now.Sub(started) < 24*time.Hour {
			lastDay++
		}
	}
	return last, lastDay, rows.Err()
}

// pulseFixSignals is what a fix run would work on.
type pulseFixSignals struct {
	OpenIssues  int
	NewConcerns int
	FailedRuns  int
	// OpenIssuesLastActivity is the latest time any open issue was seen again
	// or had an event recorded.
	OpenIssuesLastActivity time.Time
}

func (s pulseFixSignals) any() bool { return s.OpenIssues > 0 || s.NewConcerns > 0 || s.FailedRuns > 0 }

func (s pulseFixSignals) reason() string {
	parts := []string{}
	if s.OpenIssues > 0 {
		parts = append(parts, fmt.Sprintf("%d open issue(s)", s.OpenIssues))
	}
	if s.NewConcerns > 0 {
		parts = append(parts, fmt.Sprintf("%d new step concern(s)", s.NewConcerns))
	}
	if s.FailedRuns > 0 {
		parts = append(parts, fmt.Sprintf("%d failed scheduled run(s)", s.FailedRuns))
	}
	return strings.Join(parts, ", ")
}

// decidePulseFixRun applies the cost guards. since is the latest Pulse or fix
// run start: new concerns and failed runs count only after it, so a run that
// already looked at them does not retrigger.
func decidePulseFixRun(signals pulseFixSignals, lastFix time.Time, fixesLastDay int, now time.Time) (bool, string) {
	if !signals.any() {
		return false, "nothing to fix"
	}
	if fixesLastDay >= pulseFixRunMaxPerDay {
		return false, fmt.Sprintf("daily limit of %d fix runs reached", pulseFixRunMaxPerDay)
	}
	// Only old open issues, none touched since the last fix run started: a new
	// pass would see exactly what the last one left, so it would end the same
	// way. They wait for new trouble or the full Pulse.
	if signals.NewConcerns == 0 && signals.FailedRuns == 0 && !lastFix.IsZero() &&
		!signals.OpenIssuesLastActivity.After(lastFix) {
		return false, "open issues unchanged since the last fix run"
	}
	gap := pulseFixRunBacklogGap
	if signals.NewConcerns > 0 || signals.FailedRuns > 0 {
		gap = pulseFixRunFreshGap
	}
	if !lastFix.IsZero() && now.Sub(lastFix) < gap {
		return false, fmt.Sprintf("last fix run started %s ago; minimum gap is %s", now.Sub(lastFix).Round(time.Minute), gap)
	}
	return true, signals.reason()
}

// collectPulseFixSignals reads the workflow's current fix work.
func collectPulseFixSignals(ctx context.Context, workspacePath string, since time.Time) (pulseFixSignals, error) {
	var signals pulseFixSignals
	open, err := stepworkflow.CountPulseActionableWorkflowIssuesForPass(ctx, workspacePath, since)
	if err != nil {
		return signals, err
	}
	signals.OpenIssues = open
	if open > 0 {
		issues, err := stepworkflow.ListPulseActionableWorkflowIssues(ctx, workspacePath)
		if err != nil {
			return signals, err
		}
		for _, issue := range issues {
			if issue.LastActivity.After(signals.OpenIssuesLastActivity) {
				signals.OpenIssuesLastActivity = issue.LastActivity
			}
		}
	}
	signals.NewConcerns = collectStepConcerns(workspacePath, since).Total
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return signals, err
	}
	for _, run := range runs {
		if run.StartedAt.After(since) && isPulseFixFailedRunStatus(run.Status) &&
			run.ScheduleID != manualWorkflowPulseScheduleID && run.ScheduleID != pulseFixRunScheduleID {
			signals.FailedRuns++
		}
	}
	return signals, nil
}

func isPulseFixFailedRunStatus(status string) bool {
	switch status {
	case "error", "failed", "interrupted":
		return true
	}
	return false
}

// launchDueFixRuns runs on every scheduler tick after launchDuePulses.
func (s *SchedulerService) launchDueFixRuns(ctx context.Context) {
	if paused, _, err := s.IsGloballyPaused(ctx); err != nil || paused {
		return
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		scheduleLogf("[PULSE] cannot scan workflows for fix runs: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, item := range discovered {
		if item.Manifest == nil || !item.Manifest.PulseEnabled() || workflowSchedulesAllPaused(item.Manifest) {
			continue
		}
		workspacePath := item.WorkspacePath
		lastFix, fixesLastDay, err := pulseFixRunHistory(ctx, workspacePath, now)
		if err != nil {
			scheduleLogf("[PULSE] cannot read fix-run history for %s: %v", workspacePath, err)
			continue
		}
		since := lastFix
		if state, err := readPulseScheduleState(ctx, workspacePath); err == nil && state != nil && state.LastStartedAt.After(since) {
			since = state.LastStartedAt
		}
		if since.IsZero() {
			since = now.Add(-24 * time.Hour)
		}
		signals, err := collectPulseFixSignals(ctx, workspacePath, since)
		if err != nil {
			scheduleLogf("[PULSE] cannot read fix signals for %s: %v", workspacePath, err)
			continue
		}
		due, reason := decidePulseFixRun(signals, lastFix, fixesLastDay, now)
		if !due {
			continue
		}
		if s.runningPulseRuns() >= maxConcurrentPulseRuns {
			return
		}
		runID, err := s.TriggerPulseFixRun(workspacePath, reason)
		if err != nil {
			// A running workflow, schedule or Pulse retries on a later tick.
			continue
		}
		scheduleLogf("[PULSE] fix run started for %s (run %s): %s", workspacePath, runID, reason)
	}
}

// pulseFixRunWorklist is the Gate decision a fix run records itself: Technical
// is due, Plan Drift is due when its checks require it, and Goal Work and
// Architecture stay with the full Pulse.
func pulseFixRunWorklist(reason string, planDriftDue bool) []PulseWorklistDecision {
	driftReason := "Fix run: Plan Drift has nothing due."
	if planDriftDue {
		driftReason = "Fix run: plan changes need a drift check before Technical repairs."
	}
	return []PulseWorklistDecision{
		{Module: pulseModulePlanDriftReview, Due: planDriftDue, Reason: driftReason, CooldownRuns: boolToCooldown(!planDriftDue)},
		{Module: pulseModuleStrategicReview, Due: false, Reason: "Fix run: Goal Work runs in the full Pulse.", CooldownRuns: 1},
		{Module: pulseModuleArchitectureReview, Due: false, Reason: "Fix run: Architecture runs in the full Pulse.", CooldownRuns: 1},
		{Module: pulseModuleTechnicalReview, Due: true, Reason: "Fix run: " + reason + ". Close every open workflow issue."},
	}
}

func boolToCooldown(skip bool) int {
	if skip {
		return 1
	}
	return 0
}

// recordPulseFixRunWorklist writes the fix run's worklist. Plan Drift is made
// due only when the platform's own drift checks require it.
func recordPulseFixRunWorklist(ctx context.Context, workspacePath, pulseRunID, reason string) error {
	const mode, modeReason = pulseRunModeDiscovery, "Pulse fix run: Technical Review+Fix on current issues."
	if _, err := recordPulseWorklistWithMode(ctx, workspacePath, pulseRunID, mode, modeReason, pulseFixRunWorklist(reason, false)); err == nil {
		return nil
	}
	_, err := recordPulseWorklistWithMode(ctx, workspacePath, pulseRunID, mode, modeReason, pulseFixRunWorklist(reason, true))
	return err
}
