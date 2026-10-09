package server

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"
)

// Pulse fix runs: automatic fix runs no longer start (owner, 2026-10-08). A
// failed run wakes the workflow's Pulse conversation once and it asks the
// Builder chat to fix it; a run the Pulse asks for directly still uses the
// fix-run worklist below (goal_lead_qa.go).

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

func isPulseFixFailedRunStatus(status string) bool {
	switch status {
	case "error", "failed", "interrupted":
		return true
	}
	return false
}

// launchDueFixRuns runs on every scheduler tick.
func (s *SchedulerService) launchDueFixRuns(ctx context.Context) {
	if paused, _, err := s.IsProductPaused(ctx, "agentworks"); err != nil || paused {
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
		// No automatic fix runs (owner, 2026-10-08): a failed run wakes the
		// Pulse conversation once and it asks the Builder chat to fix it; a
		// workflow without a soul.md runs like Pulse off.
		// A calm pace leaves failures to the next goal check.
		if workflowHasGoal(ctx, item.WorkspacePath) && workflowPulsePace(ctx, item.WorkspacePath).WakeOnFailure {
			s.wakeGoalLeadOnRunFailures(ctx, item.WorkspacePath, now)
		}
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
