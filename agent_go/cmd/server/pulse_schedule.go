package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// The workflow's own Pulse schedule. Normal schedules only run basic
// stewardship after a run; the full Pulse runs here. In self mode each Pulse
// pass chooses its next run with record_pulse_next_run, and the platform only
// enforces the min/max interval guards. The state is workflow-local, next to
// pulse_fast_request, so it travels with the workflow database.
const pulseScheduleStateSchema = `CREATE TABLE IF NOT EXISTS pulse_schedule_state (
	workspace_path TEXT PRIMARY KEY,
	next_at TEXT NOT NULL DEFAULT '',
	next_reason TEXT NOT NULL DEFAULT '',
	next_set_by_pulse_run_id TEXT NOT NULL DEFAULT '',
	next_set_at TEXT NOT NULL DEFAULT '',
	last_started_at TEXT NOT NULL DEFAULT '',
	last_pulse_run_id TEXT NOT NULL DEFAULT '',
	previous_started_at TEXT NOT NULL DEFAULT ''
)`

type pulseScheduleState struct {
	NextAt              time.Time
	NextReason          string
	NextSetByPulseRunID string
	NextSetAt           time.Time
	LastStartedAt       time.Time
	LastPulseRunID      string
	// PreviousStartedAt is the start of the Pulse before the latest one: the
	// running Pulse's evidence window starts there (step concerns).
	PreviousStartedAt time.Time
}

func ensurePulseScheduleStateSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, pulseScheduleStateSchema); err != nil {
		return err
	}
	// Tables created before previous_started_at existed gain it in place.
	if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_schedule_state ADD COLUMN previous_started_at TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	return nil
}

func parseStoredTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func formatStoredTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// readPulseScheduleState returns nil when the workflow has no state yet.
func readPulseScheduleState(ctx context.Context, workspacePath string) (*pulseScheduleState, error) {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if err := ensurePulseScheduleStateSchema(ctx, db); err != nil {
		return nil, err
	}
	var nextAt, nextReason, setBy, setAt, lastStarted, lastRun, previousStarted string
	err = db.QueryRowContext(ctx, `SELECT next_at,next_reason,next_set_by_pulse_run_id,next_set_at,last_started_at,last_pulse_run_id,previous_started_at
		FROM pulse_schedule_state WHERE workspace_path=?`, normalized).Scan(&nextAt, &nextReason, &setBy, &setAt, &lastStarted, &lastRun, &previousStarted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pulseScheduleState{
		NextAt: parseStoredTime(nextAt), NextReason: nextReason, NextSetByPulseRunID: setBy,
		NextSetAt: parseStoredTime(setAt), LastStartedAt: parseStoredTime(lastStarted), LastPulseRunID: lastRun,
		PreviousStartedAt: parseStoredTime(previousStarted),
	}, nil
}

// setNextPulse records the next Pulse time chosen by a Pulse pass (or the
// first-run default). The launcher still clamps it to the interval guards.
func setNextPulse(ctx context.Context, workspacePath string, at time.Time, reason, pulseRunID string) error {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseScheduleStateSchema(ctx, db); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_schedule_state
		(workspace_path,next_at,next_reason,next_set_by_pulse_run_id,next_set_at) VALUES (?,?,?,?,?)
		ON CONFLICT(workspace_path) DO UPDATE SET next_at=excluded.next_at, next_reason=excluded.next_reason,
		next_set_by_pulse_run_id=excluded.next_set_by_pulse_run_id, next_set_at=excluded.next_set_at`,
		normalized, formatStoredTime(at), strings.TrimSpace(reason), strings.TrimSpace(pulseRunID), formatStoredTime(time.Now()))
	return err
}

// markPulseStarted records a started full Pulse (scheduled or manual) and
// clears the consumed next time, so the new pass must choose again.
func markPulseStarted(ctx context.Context, workspacePath, pulseRunID string, startedAt time.Time) error {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseScheduleStateSchema(ctx, db); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_schedule_state
		(workspace_path,last_started_at,last_pulse_run_id) VALUES (?,?,?)
		ON CONFLICT(workspace_path) DO UPDATE SET previous_started_at=pulse_schedule_state.last_started_at,
		last_started_at=excluded.last_started_at,
		last_pulse_run_id=excluded.last_pulse_run_id, next_at='', next_reason='', next_set_by_pulse_run_id='', next_set_at=''`,
		normalized, formatStoredTime(startedAt), strings.TrimSpace(pulseRunID))
	return err
}

func pulseScheduleLocation(schedule WorkflowPulseSchedule) *time.Location {
	loc, err := time.LoadLocation(scheduleTimezoneOrDefault(strings.TrimSpace(schedule.Timezone)))
	if err != nil {
		return time.UTC
	}
	return loc
}

// firstPulseTime is the default first run: the next defaultPulseFirstRunHour
// local time, or the next fixed cron occurrence.
func firstPulseTime(schedule WorkflowPulseSchedule, now time.Time) time.Time {
	loc := pulseScheduleLocation(schedule)
	if schedule.Mode == pulseScheduleModeFixed {
		if next, ok := nextFixedPulse(schedule, now); ok {
			return next
		}
	}
	local := now.In(loc)
	first := time.Date(local.Year(), local.Month(), local.Day(), defaultPulseFirstRunHour, 0, 0, 0, loc)
	if !first.After(local) {
		first = first.AddDate(0, 0, 1)
	}
	return first.UTC()
}

func nextFixedPulse(schedule WorkflowPulseSchedule, after time.Time) (time.Time, bool) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse(strings.TrimSpace(schedule.Cron))
	if err != nil {
		return time.Time{}, false
	}
	return parsed.Next(after.In(pulseScheduleLocation(schedule))).UTC(), true
}

// pulseDueDecision is the launcher's view of one workflow at one instant.
type pulseDueDecision struct {
	Due    bool
	NextAt time.Time
	Reason string
}

// decidePulseDue applies the guards to the chosen time. The Pulse agent
// chooses the time; this only bounds it: never sooner than min_interval after
// the last started Pulse, never later than max_interval. A pending fast request
// asks for the earliest time the min guard allows.
func decidePulseDue(schedule WorkflowPulseSchedule, state pulseScheduleState, fastPending bool, now time.Time) pulseDueDecision {
	minInterval := time.Duration(schedule.MinIntervalHours) * time.Hour
	maxInterval := time.Duration(schedule.MaxIntervalHours) * time.Hour
	last := state.LastStartedAt

	candidate, reason := state.NextAt, state.NextReason
	if schedule.Mode == pulseScheduleModeFixed && !last.IsZero() {
		if next, ok := nextFixedPulse(schedule, last); ok {
			candidate, reason = next, "fixed Pulse schedule"
		}
	}
	if candidate.IsZero() {
		if last.IsZero() {
			// No run yet and no chosen time: the launcher persists a first time.
			return pulseDueDecision{}
		}
		// The floor allows a faster pace only when a pass chose it; with no
		// choice, the default stays one day.
		defaultGap := 24 * time.Hour
		if minInterval > defaultGap {
			defaultGap = minInterval
		}
		candidate, reason = last.Add(defaultGap), "no next time chosen yet; defaults to one day after the last Pulse"
	}
	if fastPending {
		earliest := now
		if !last.IsZero() {
			earliest = last.Add(minInterval)
		}
		if earliest.Before(candidate) {
			candidate, reason = earliest, "a workflow run requested an earlier Pulse"
		}
	}
	if !last.IsZero() {
		if floor := last.Add(minInterval); candidate.Before(floor) {
			candidate = floor
		}
		if ceiling := last.Add(maxInterval); candidate.After(ceiling) {
			candidate, reason = ceiling, "at-least-weekly guard"
		}
	}
	return pulseDueDecision{Due: !now.Before(candidate), NextAt: candidate, Reason: reason}
}

// pulseScheduleTimingSummary supplies facts to the ordinary-run finalizer so it
// can judge whether to request an earlier Pulse. Returns "" when Pulse is off.
func pulseScheduleTimingSummary(ctx context.Context, workspacePath string, manifest *WorkflowManifest) string {
	if manifest == nil || !manifest.PulseEnabled() {
		return ""
	}
	state, err := readPulseScheduleState(ctx, workspacePath)
	if err != nil || state == nil {
		return "The workflow's own Pulse schedule has not chosen its next run yet. Use record_pulse_fast_request only for clear material evidence."
	}
	decision := decidePulseDue(manifest.EffectivePulseSchedule(), *state, false, time.Now().UTC())
	if decision.NextAt.IsZero() {
		return "The workflow's own Pulse schedule has not chosen its next run yet. Use record_pulse_fast_request only for clear material evidence."
	}
	summary := fmt.Sprintf("The next full Pulse is scheduled for %s (in about %s)", decision.NextAt.Format(time.RFC3339), time.Until(decision.NextAt).Round(time.Minute))
	if strings.TrimSpace(decision.Reason) != "" {
		summary += " because " + strings.TrimSpace(decision.Reason)
	}
	return summary + "."
}

// recordPulseNextRunFromToolArgs backs the record_pulse_next_run tool.
func recordPulseNextRunFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := strings.TrimSpace(stringToolArg(args, "workspace_path"))
	reason := strings.TrimSpace(stringToolArg(args, "reason"))
	pulseRunID := strings.TrimSpace(stringToolArg(args, "pulse_run_id"))
	rawAt := strings.TrimSpace(stringToolArg(args, "next_at"))
	if workspacePath == "" || reason == "" || rawAt == "" {
		return "", fmt.Errorf("record_pulse_next_run requires workspace_path, next_at and a concrete reason")
	}
	at, err := time.Parse(time.RFC3339, rawAt)
	if err != nil {
		return "", fmt.Errorf("next_at must be an RFC3339 time with a timezone offset, e.g. 2026-09-25T07:00:00+05:30: %w", err)
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("workflow manifest not found at %s", workspacePath)
	}
	if err := setNextPulse(ctx, workspacePath, at.UTC(), reason, pulseRunID); err != nil {
		return "", err
	}
	state, err := readPulseScheduleState(ctx, workspacePath)
	if err != nil || state == nil {
		return fmt.Sprintf("Next Pulse recorded for %s.", at.Format(time.RFC3339)), nil
	}
	decision := decidePulseDue(manifest.EffectivePulseSchedule(), *state, false, time.Now().UTC())
	effective := decision.NextAt
	if effective.IsZero() {
		effective = at.UTC()
	}
	note := ""
	if !effective.Equal(at.UTC()) {
		schedule := manifest.EffectivePulseSchedule()
		note = fmt.Sprintf(" The platform guards (at most every %dh, at least every %dh) move it to %s.", schedule.MinIntervalHours, schedule.MaxIntervalHours, effective.Format(time.RFC3339))
	}
	return fmt.Sprintf("Next Pulse recorded for %s: %s.%s", at.Format(time.RFC3339), reason, note), nil
}

// recordPulseFastRequestFromToolArgs backs the record_pulse_fast_request tool.
func recordPulseFastRequestFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := strings.TrimSpace(stringToolArg(args, "workspace_path"))
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_fast_request requires workspace_path")
	}
	request, err := requestFastPulse(ctx, workspacePath, stringToolArg(args, "run_id"), stringToolArg(args, "reason"), stringSliceFromToolArg(args["evidence"]))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Earlier Pulse requested for run %s: %s. The workflow's Pulse schedule will start it as soon as its minimum-interval guard allows.", request.RequestedRunID, request.Reason), nil
}

// PulseNextRunView is the Pulse schedule as shown in the Pulse view.
type PulseNextRunView struct {
	Mode          string `json:"mode"`
	NextAt        string `json:"next_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
	LastStartedAt string `json:"last_started_at,omitempty"`
}

func pulseNextRunView(ctx context.Context, workspacePath string) *PulseNextRunView {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || !manifest.PulseEnabled() {
		return nil
	}
	schedule := manifest.EffectivePulseSchedule()
	view := &PulseNextRunView{Mode: schedule.Mode}
	state, err := readPulseScheduleState(ctx, workspacePath)
	if err != nil || state == nil {
		return view
	}
	view.LastStartedAt = formatStoredTime(state.LastStartedAt)
	fast, _ := pendingFastPulseRequest(ctx, workspacePath)
	decision := decidePulseDue(schedule, *state, fast != nil, time.Now().UTC())
	view.NextAt = formatStoredTime(decision.NextAt)
	view.Reason = decision.Reason
	return view
}
