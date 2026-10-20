package server

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Only the basic periodic Pulse clock is persisted; conversation history is separate.
const pulseScheduleStateSchema = `CREATE TABLE IF NOT EXISTS pulse_schedule_state (
 workspace_path TEXT PRIMARY KEY,
 last_started_at TEXT NOT NULL DEFAULT '',
 last_pulse_run_id TEXT NOT NULL DEFAULT ''
)`

type pulseScheduleState struct {
	LastStartedAt  time.Time
	LastPulseRunID string
}

func parseStoredTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
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

func readPulseScheduleState(ctx context.Context, workspacePath string) (*pulseScheduleState, error) {
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	var exists bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='pulse_schedule_state')`).Scan(&exists); err != nil || !exists {
		return nil, err
	}
	var started, run string
	err = db.QueryRowContext(ctx, `SELECT last_started_at,last_pulse_run_id FROM pulse_schedule_state WHERE workspace_path=?`, normalized).Scan(&started, &run)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pulseScheduleState{LastStartedAt: parseStoredTime(started), LastPulseRunID: run}, nil
}

func markPulseStarted(ctx context.Context, workspacePath, pulseRunID string, startedAt time.Time) error {
	normalized, db, err := openPulseStoreDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, pulseScheduleStateSchema); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_schedule_state(workspace_path,last_started_at,last_pulse_run_id) VALUES (?,?,?) ON CONFLICT(workspace_path) DO UPDATE SET last_started_at=excluded.last_started_at,last_pulse_run_id=excluded.last_pulse_run_id`, normalized, formatStoredTime(startedAt), pulseRunID)
	return err
}
