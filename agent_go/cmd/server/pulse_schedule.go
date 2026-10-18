package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

// Retain the existing start/evidence window history. Next-check scheduling is
// read only from pulse_goal_checks; old next_* columns stay as saved history.
const pulseScheduleStateSchema = `CREATE TABLE IF NOT EXISTS pulse_schedule_state (
	workspace_path TEXT PRIMARY KEY,
	next_at TEXT NOT NULL DEFAULT '',
	next_reason TEXT NOT NULL DEFAULT '',
	next_set_by_pulse_run_id TEXT NOT NULL DEFAULT '',
	next_set_at TEXT NOT NULL DEFAULT '',
	last_started_at TEXT NOT NULL DEFAULT '',
	last_pulse_run_id TEXT NOT NULL DEFAULT '',
	previous_started_at TEXT NOT NULL DEFAULT '',
 first_report_at TEXT NOT NULL DEFAULT ''
)`

type pulseScheduleState struct {
	LastStartedAt  time.Time
	LastPulseRunID string
	// PreviousStartedAt is the start of the Pulse before the latest one: the
	// running Pulse's evidence window starts there (step concerns).
	PreviousStartedAt time.Time
}

func ensurePulseScheduleStateSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, pulseScheduleStateSchema); err != nil {
		return err
	}
	// Add bootstrap/evidence timestamps to older state without resetting them.
	for _, column := range []string{"previous_started_at", "first_report_at"} {
		if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_schedule_state ADD COLUMN `+column+` TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
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
	var lastStarted, lastRun, previousStarted string
	err = db.QueryRowContext(ctx, `SELECT last_started_at,last_pulse_run_id,previous_started_at
		FROM pulse_schedule_state WHERE workspace_path=?`, normalized).Scan(&lastStarted, &lastRun, &previousStarted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pulseScheduleState{
		LastStartedAt: parseStoredTime(lastStarted), LastPulseRunID: lastRun,
		PreviousStartedAt: parseStoredTime(previousStarted),
	}, nil
}

// markPulseStarted records the evidence window for this cycle.
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

// pulseScheduleTimingSummary reads the same clock as the launcher.
func pulseScheduleTimingSummary(ctx context.Context, workspacePath string, manifest *WorkflowManifest) string {
	if manifest == nil || !manifest.PulseEnabled() {
		return ""
	}
	view := pulseNextRunView(ctx, workspacePath)
	if view == nil || view.NextAt == "" {
		return "Pulse has not chosen its next check yet. Request an earlier check only for material new evidence."
	}
	return "The next Pulse check is " + view.NextAt + ": " + view.Reason + "."
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
	NextAt        string `json:"next_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
	LastStartedAt string `json:"last_started_at,omitempty"`
}

func pulseNextRunView(ctx context.Context, workspacePath string) *PulseNextRunView {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest.Kind == "relay" || !workflowHasSoul(ctx, workspacePath) {
		return nil
	}
	latest, lastStarted, err := latestPulseGoalCheck(ctx, workspacePath)
	if err != nil {
		return nil
	}
	view := &PulseNextRunView{LastStartedAt: formatStoredTime(lastStarted)}
	next, reason := nextPulseCheckAt(ctx, workspacePath, manifest, latest, lastStarted)
	view.NextAt, view.Reason = formatStoredTime(next), reason
	return view
}

// First OFF reports are spread across their first week once, in the existing
// Pulse state. After the first saved report, only pulse_goal_checks sets time.
func firstWeeklyReportDue(ctx context.Context, workspacePath, workflowID string, now time.Time) (time.Time, error) {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return time.Time{}, err
	}
	defer db.Close()
	if err := ensurePulseScheduleStateSchema(ctx, db); err != nil {
		return time.Time{}, err
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(firstNonEmptyTrimmed(workflowID, normalized)))
	due := now.UTC().Add(time.Hour + time.Duration(h.Sum64()%uint64(7*24*time.Hour-time.Hour)))
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_schedule_state (workspace_path,first_report_at) VALUES (?,?) ON CONFLICT(workspace_path) DO UPDATE SET first_report_at=excluded.first_report_at WHERE first_report_at=''`, normalized, formatStoredTime(due))
	if err != nil {
		return time.Time{}, err
	}
	var saved string
	err = db.QueryRowContext(ctx, `SELECT first_report_at FROM pulse_schedule_state WHERE workspace_path=?`, normalized).Scan(&saved)
	return parseStoredTime(saved), err
}
