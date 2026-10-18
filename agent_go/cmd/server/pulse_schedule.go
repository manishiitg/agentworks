package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	previous_started_at TEXT NOT NULL DEFAULT ''
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
	columns, err := pulseStateColumns(ctx, db, "pulse_schedule_state")
	if err != nil {
		return err
	}
	if !columns["previous_started_at"] {
		if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_schedule_state ADD COLUMN previous_started_at TEXT NOT NULL DEFAULT ''`); err != nil {
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
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	columns, err := pulseStateColumns(ctx, db, "pulse_schedule_state")
	if err != nil || len(columns) == 0 {
		return nil, err
	}
	previousColumn := "''"
	if columns["previous_started_at"] {
		previousColumn = "previous_started_at"
	}
	var lastStarted, lastRun, previousStarted string
	err = db.QueryRowContext(ctx, `SELECT last_started_at,last_pulse_run_id,`+previousColumn+` FROM pulse_schedule_state WHERE workspace_path=?`, normalized).Scan(&lastStarted, &lastRun, &previousStarted)
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
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, true)
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

// recordPulseFastRequestFromToolArgs backs the record_pulse_fast_request tool.
func recordPulseFastRequestFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := strings.TrimSpace(stringToolArg(args, "workspace_path"))
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_fast_request requires workspace_path")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return "", err
	}
	if !found || !manifest.PulseEnabled() {
		return "", errPulseOff
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

// Reads never install schema. The writer adds only missing columns, while older
// stored history remains visible without GET requests changing its database.
func pulseStateColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	switch table {
	case "pulse_schedule_state", "pulse_goal_checks", "pulse_module_audit", "pulse_review_notes":
	default:
		return nil, fmt.Errorf("unsupported Pulse state table")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func pulseNextRunView(ctx context.Context, workspacePath string) *PulseNextRunView {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest.Kind == "relay" || !manifest.PulseEnabled() || !workflowHasSoul(ctx, workspacePath) {
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
