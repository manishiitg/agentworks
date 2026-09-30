package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

const (
	pulseFinalCommandBackup  = "backup"
	pulseFinalCommandPublish = "publish"
	pulseFinalCommandNotify  = "notify"
)

var pulseFinalCommandOrder = []string{
	pulseFinalCommandBackup,
	pulseFinalCommandPublish,
	pulseFinalCommandNotify,
}

// pulseFinalCommandResultValues is the agent-reportable status set, shared by
// the accept check and every rejection message that names it.
var pulseFinalCommandResultValues = []string{"running", "done", "skipped", "blocked", "failed"}

var validPulseFinalCommands = map[string]bool{
	pulseFinalCommandBackup:  true,
	pulseFinalCommandPublish: true,
	pulseFinalCommandNotify:  true,
}

const pulseFinalCommandStateSchema = `CREATE TABLE IF NOT EXISTS pulse_final_command_state (
	workspace_path TEXT NOT NULL,
	command TEXT NOT NULL,
	pulse_run_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL,
	PRIMARY KEY (workspace_path, command)
)`

type PulseFinalCommandState struct {
	WorkspacePath string `json:"workspace_path"`
	Command       string `json:"command"`
	PulseRunID    string `json:"pulse_run_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func initializePulseFinalCommandStates(ctx context.Context, workspacePath, pulseRunID string) error {
	pulseRunID = strings.TrimSpace(pulseRunID)
	if pulseRunID == "" {
		return fmt.Errorf("pulse_run_id is required")
	}
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, command := range pulseFinalCommandOrder {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pulse_final_command_state (
				workspace_path, command, pulse_run_id, status, reason, started_at, finished_at, updated_at
			) VALUES (?, ?, ?, 'waiting', 'Waiting for Pulse finalization', '', '', ?)
			ON CONFLICT(workspace_path, command) DO UPDATE SET
				pulse_run_id=excluded.pulse_run_id,
				status=excluded.status,
				reason=excluded.reason,
				started_at='',
				finished_at='',
				updated_at=excluded.updated_at`,
			normalized, command, pulseRunID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func markPulseFinalCommandState(ctx context.Context, workspacePath, command, pulseRunID, status, reason string) (*PulseFinalCommandState, error) {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return markPulseFinalCommandStateInDB(ctx, db, normalized, command, pulseRunID, status, reason)
}

func markPulseFinalCommandStateFromAgent(ctx context.Context, workspacePath, command, pulseRunID, status, reason string) (*PulseFinalCommandState, error) {
	command = strings.TrimSpace(strings.ToLower(command))
	if !validPulseFinalCommands[command] {
		return nil, fmt.Errorf("command %q is not a valid Pulse final command. Must be one of: %s", command, strings.Join(pulseFinalCommandOrder, ", "))
	}
	pulseRunID = strings.TrimSpace(pulseRunID)
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "running", "done", "skipped", "blocked", "failed":
	default:
		return nil, fmt.Errorf("result %q is not valid for command %q. Must be one of: %s", status, command, strings.Join(pulseFinalCommandResultValues, ", "))
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("reason is required: one short factual sentence stating what this command did or why it could not run")
	}

	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	existing, err := getPulseFinalCommandStateByCommand(ctx, db, normalized, command)
	if err != nil {
		return nil, fmt.Errorf("final command %q was not initialized for this Pulse run: %w", command, err)
	}
	if existing.PulseRunID != pulseRunID {
		// A Pulse run has two names: the session-style ID its final commands
		// are stored under and the fix-run record's UUID. The run's own session
		// writing under either name is still writing its own run.
		if strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx)) != existing.PulseRunID {
			return nil, fmt.Errorf("final command %q belongs to Pulse run %q, not %q; pass pulse_run_id=%q (or \"current\") from inside that run", command, existing.PulseRunID, pulseRunID, existing.PulseRunID)
		}
		pulseRunID = existing.PulseRunID
	}
	if existing.Status == status {
		return existing, nil
	}
	if isTerminalPulseFinalCommandStatus(existing.Status) {
		return nil, fmt.Errorf("final command %q is already terminal with status %q", command, existing.Status)
	}
	if status == "done" && existing.Status != "running" {
		return nil, fmt.Errorf("final command %q must be marked running before done", command)
	}

	commandIndex := -1
	for index, candidate := range pulseFinalCommandOrder {
		if candidate == command {
			commandIndex = index
			break
		}
	}
	if commandIndex < 0 {
		return nil, fmt.Errorf("final command %q has no configured order", command)
	}
	for _, prior := range pulseFinalCommandOrder[:commandIndex] {
		priorState, err := getPulseFinalCommandStateByCommand(ctx, db, normalized, prior)
		if err != nil || priorState.PulseRunID != pulseRunID || !isTerminalPulseFinalCommandStatus(priorState.Status) {
			return nil, fmt.Errorf("final command %q cannot start before %q is terminal", command, prior)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	startedAt := existing.StartedAt
	finishedAt := ""
	if startedAt == "" {
		startedAt = now
	}
	if status != "running" {
		finishedAt = now
	}
	result, err := db.ExecContext(ctx, `UPDATE pulse_final_command_state SET
			status = ?, reason = ?, started_at = ?, finished_at = ?, updated_at = ?
		WHERE workspace_path = ? AND command = ? AND pulse_run_id = ? AND status = ?`,
		status, reason, startedAt, finishedAt, now,
		normalized, command, pulseRunID, existing.Status)
	if err != nil {
		return nil, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if changed != 1 {
		return nil, fmt.Errorf("final command %q changed concurrently; refresh its state before retrying", command)
	}
	if status == "done" {
		if err := reconcileFinalCommandOwnedFindings(ctx, db, command, pulseRunID, now); err != nil {
			return nil, err
		}
	}
	return getPulseFinalCommandStateByCommand(ctx, db, normalized, command)
}

func isTerminalPulseFinalCommandStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "done", "skipped", "blocked", "failed", "timed_out":
		return true
	default:
		return false
	}
}

func markPulseFinalCommandStateInDB(ctx context.Context, db *sql.DB, workspacePath, command, pulseRunID, status, reason string) (*PulseFinalCommandState, error) {
	command = strings.TrimSpace(strings.ToLower(command))
	if !validPulseFinalCommands[command] {
		return nil, fmt.Errorf("final command %q is not valid", command)
	}
	pulseRunID = strings.TrimSpace(pulseRunID)
	if pulseRunID == "" {
		return nil, fmt.Errorf("pulse_run_id is required")
	}
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "waiting", "running", "done", "skipped", "blocked", "failed", "timed_out":
	default:
		return nil, fmt.Errorf("status must be one of waiting, running, done, skipped, blocked, failed, timed_out")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("reason is required")
	}

	var existingRunID, startedAt string
	err := db.QueryRowContext(ctx, `SELECT pulse_run_id, started_at FROM pulse_final_command_state
		WHERE workspace_path = ? AND command = ?`, workspacePath, command).Scan(&existingRunID, &startedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existingRunID != pulseRunID {
		startedAt = ""
	}

	now := time.Now().UTC().Format(time.RFC3339)
	finishedAt := ""
	switch status {
	case "waiting":
		startedAt = ""
	case "running":
		if startedAt == "" {
			startedAt = now
		}
	default:
		if startedAt == "" {
			startedAt = now
		}
		finishedAt = now
	}

	_, err = db.ExecContext(ctx, `INSERT INTO pulse_final_command_state (
			workspace_path, command, pulse_run_id, status, reason, started_at, finished_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_path, command) DO UPDATE SET
			pulse_run_id=excluded.pulse_run_id,
			status=excluded.status,
			reason=excluded.reason,
			started_at=excluded.started_at,
			finished_at=excluded.finished_at,
			updated_at=excluded.updated_at`,
		workspacePath, command, pulseRunID, status, reason, startedAt, finishedAt, now)
	if err != nil {
		return nil, err
	}
	if status == "done" {
		if err := reconcileFinalCommandOwnedFindings(ctx, db, command, pulseRunID, now); err != nil {
			return nil, err
		}
	}
	return getPulseFinalCommandStateByCommand(ctx, db, workspacePath, command)
}

var pulseFinalCommandOwnedReasonCodes = map[string][]string{
	pulseFinalCommandPublish: {
		"finalizer_publish_owned",
		"published_snapshots_are_publish_owned",
	},
}

// reconcileFinalCommandOwnedFindings closes the old false-positive class where
// a reviewer filed normal stage separation as an external platform defect.
// Matching uses the stable reason_code written by the lifecycle, never prose.
// A genuine terminal command failure is untouched and remains reportable.
func reconcileFinalCommandOwnedFindings(ctx context.Context, db *sql.DB, command, pulseRunID, recordedAt string) error {
	reasonCodes := pulseFinalCommandOwnedReasonCodes[command]
	if len(reasonCodes) == 0 {
		return nil
	}
	var lifecycleTableCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN ('run_concerns','pulse_finding_events')`).Scan(&lifecycleTableCount); err != nil {
		return err
	}
	if lifecycleTableCount != 2 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(reasonCodes)), ",")
	queryArgs := make([]interface{}, 0, len(reasonCodes))
	for _, reasonCode := range reasonCodes {
		queryArgs = append(queryArgs, reasonCode)
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT c.fingerprint,
		COALESCE((SELECT e.finding_id FROM pulse_finding_events e
			WHERE e.fingerprint=c.fingerprint ORDER BY e.recorded_at DESC, e._id DESC LIMIT 1), '')
		FROM run_concerns c
		WHERE c.status='external_action_required'
		AND COALESCE((SELECT json_extract(e.metadata_json, '$.reason_code')
			FROM pulse_finding_events e WHERE e.fingerprint=c.fingerprint
			AND e.event_type='external_action_required'
			ORDER BY e.recorded_at DESC, e._id DESC LIMIT 1), '') IN (%s)`, placeholders), queryArgs...)
	if err != nil {
		return err
	}
	type findingRef struct{ fingerprint, findingID string }
	var findings []findingRef
	for rows.Next() {
		var finding findingRef
		if err := rows.Scan(&finding.fingerprint, &finding.findingID); err != nil {
			_ = rows.Close()
			return err
		}
		findings = append(findings, finding)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, finding := range findings {
		summary := fmt.Sprintf("Pulse finalizer command %s completed successfully; normal stage ownership is not a platform defect.", command)
		if _, err := db.ExecContext(ctx, `UPDATE run_concerns SET status='resolved', resolved_at=?,
			resolved_by='pulse_finalizer', resolution_note=? WHERE fingerprint=? AND status='external_action_required'`,
			recordedAt, summary, finding.fingerprint); err != nil {
			return err
		}
		metadata := fmt.Sprintf(`{"command":%q,"status":"done","reconciled_reason":"finalizer_stage_completed"}`, command)
		if _, err := db.ExecContext(ctx, `INSERT INTO pulse_finding_events
			(fingerprint, finding_id, pulse_run_id, attempt_id, event_type, summary, metadata_json, recorded_at)
			VALUES (?, ?, ?, '', 'closed', ?, ?, ?)
			ON CONFLICT(fingerprint, pulse_run_id, attempt_id, event_type) DO UPDATE SET
				summary=excluded.summary, metadata_json=excluded.metadata_json, recorded_at=excluded.recorded_at`,
			finding.fingerprint, finding.findingID, strings.TrimSpace(pulseRunID), summary, metadata, recordedAt); err != nil {
			return err
		}
	}
	return nil
}

func getPulseFinalCommandStates(ctx context.Context, workspacePath string) ([]PulseFinalCommandState, error) {
	normalized, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return []PulseFinalCommandState{}, nil
	}
	defer db.Close()
	if err := ensurePulseModuleStateSchema(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT workspace_path, command, pulse_run_id, status, reason,
		started_at, finished_at, updated_at FROM pulse_final_command_state WHERE workspace_path = ?`, normalized)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byCommand := map[string]PulseFinalCommandState{}
	for rows.Next() {
		state, err := scanPulseFinalCommandState(rows)
		if err != nil {
			return nil, err
		}
		byCommand[state.Command] = *state
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	states := make([]PulseFinalCommandState, 0, len(byCommand))
	for _, command := range pulseFinalCommandOrder {
		if state, ok := byCommand[command]; ok {
			states = append(states, state)
		}
	}
	return states, nil
}

func getPulseFinalCommandStateByCommand(ctx context.Context, db *sql.DB, workspacePath, command string) (*PulseFinalCommandState, error) {
	row := db.QueryRowContext(ctx, `SELECT workspace_path, command, pulse_run_id, status, reason,
		started_at, finished_at, updated_at FROM pulse_final_command_state
		WHERE workspace_path = ? AND command = ?`, workspacePath, command)
	return scanPulseFinalCommandState(row)
}

type pulseFinalCommandScanner interface {
	Scan(dest ...interface{}) error
}

func scanPulseFinalCommandState(row pulseFinalCommandScanner) (*PulseFinalCommandState, error) {
	var state PulseFinalCommandState
	if err := row.Scan(&state.WorkspacePath, &state.Command, &state.PulseRunID, &state.Status, &state.Reason,
		&state.StartedAt, &state.FinishedAt, &state.UpdatedAt); err != nil {
		return nil, err
	}
	return &state, nil
}

// finalizeAllRunningPulseReviewLogs closes reviewer rows that were left mid-flight.
//
// PLAT-054 / PLAT-017. pulse_review_log is only ever written by an agent through
// recordPulseReviewOnDB, so a pass that dies between "review started" and
// "review recorded" strands its row at status='running' permanently. The
// startup sweep already reconciles pulse_final_command_state the same way;
// without this, Upwork accumulated three stranded workflow_review rows from
// three different runs on a single day, which then read as live reviewer work
// that no longer exists.
//
// Deliberately narrow: only rows already marked running are touched, and only
// the status/verdict fields are rewritten. Findings and verification counts the
// dead pass genuinely recorded stay exactly as they were — the row is being
// closed, not erased.
func finalizeAllRunningPulseReviewLogs(ctx context.Context, workspacePath, reason string) (int64, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, fmt.Errorf("reason is required")
	}
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return 0, err
	}
	defer db.Close()

	// The table is created by the workflow-side reviewer path, so a workspace
	// that has never run a Pulse review legitimately has no such table.
	var exists string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name='pulse_review_log'`).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}

	result, err := db.ExecContext(ctx, `UPDATE pulse_review_log SET
			status = 'failed',
			verdict = CASE WHEN TRIM(verdict) = '' THEN ? ELSE verdict END
		WHERE status = 'running'`, reason)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
