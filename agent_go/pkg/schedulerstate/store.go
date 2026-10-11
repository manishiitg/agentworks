package schedulerstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type State string

const (
	StateStarting           State = "starting"
	StateWaitingForCapacity State = "waiting_for_capacity"
	StateWorkflowRunning    State = "workflow_running"
	StateWorkflowFinished   State = "workflow_finished"
	StatePulseGate          State = "pulse_gate"
	StatePulseModules       State = "pulse_modules"
	StatePulseFinalizing    State = "pulse_finalizing"
	StateCompleted          State = "completed"
	StatePartial            State = "partial"
	StateFailed             State = "failed"
	StateStopped            State = "stopped"
	StateInterrupted        State = "interrupted"
)

var (
	ErrRunAlreadyActive         = errors.New("schedule run already active for scope")
	ErrRunNotFound              = errors.New("schedule run not found")
	ErrPendingOccurrenceMissing = errors.New("pending schedule occurrence not found")
	ErrInvalidTransition        = errors.New("invalid schedule run transition")
)

var allowedTransitions = map[State]map[State]bool{
	StateWaitingForCapacity: {StateStarting: true, StateFailed: true, StateStopped: true, StateInterrupted: true},
	StateStarting: {
		StateWorkflowRunning: true, StateFailed: true, StateStopped: true, StateInterrupted: true,
	},
	StateWorkflowRunning: {
		StateWorkflowFinished: true, StateFailed: true, StateStopped: true, StateInterrupted: true,
	},
	StateWorkflowFinished: {
		StatePulseGate: true, StateWaitingForCapacity: true, StateCompleted: true, StatePartial: true, StateFailed: true, StateStopped: true, StateInterrupted: true,
	},
	StatePulseGate: {
		StatePulseModules: true, StatePulseFinalizing: true, StateCompleted: true, StatePartial: true, StateFailed: true, StateStopped: true, StateInterrupted: true,
	},
	StatePulseModules: {
		StatePulseFinalizing: true, StateCompleted: true, StatePartial: true, StateFailed: true, StateStopped: true, StateInterrupted: true,
	},
	StatePulseFinalizing: {
		StateCompleted: true, StatePartial: true, StateFailed: true, StateStopped: true, StateInterrupted: true,
	},
}

func IsTerminal(state State) bool {
	switch state {
	case StateCompleted, StatePartial, StateFailed, StateStopped, StateInterrupted:
		return true
	default:
		return false
	}
}

type Run struct {
	RunID           string
	ScopeType       string
	ScopeID         string
	LockKey         string
	ScheduleID      string
	TriggerSource   string
	ScheduledFor    time.Time
	State           State
	StartedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
	ActiveSessionID string
	RunFolder       string
	ErrorMessage    string
}

type Transition struct {
	RunID        string
	To           State
	Reason       string
	SessionID    string
	SessionKind  string
	RunFolder    string
	ErrorMessage string
	At           time.Time
}

type FireDecision struct {
	DecisionID    string
	ScopeType     string
	ScopeID       string
	ScheduleID    string
	TriggerSource string
	Decision      string
	Reason        string
	RunID         string
	ScheduledFor  time.Time
	FiredAt       time.Time
}

// PendingOccurrence is the durable queue entry for a schedule occurrence that
// could not start because its workflow was owned or a dependency had not yet
// completed. One row per schedule implements queue_latest semantics.
type PendingOccurrence struct {
	ScopeType          string
	ScopeID            string
	ScheduleID         string
	ScheduledFor       time.Time
	LatestScheduledFor time.Time
	QueuedAt           time.Time
	ExpiresAt          time.Time
	TriggerSource      string
	Policy             string
	OccurrenceCount    int
	Reason             string
}

type Event struct {
	Sequence  int64
	RunID     string
	FromState State
	ToState   State
	Reason    string
	CreatedAt time.Time
}

type Store struct {
	db *sql.DB
}

// Five-field cron has one-minute resolution. Retain at least the complete
// seven-day recovery window for each trigger source so manual fires cannot
// evict the cron cursor or its occurrence audit trail.
const fireDecisionRetentionPerSchedule = 10_080

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("schedule state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create schedule state directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open schedule state: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db}
	if err := store.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS relay_dbos_runs (
			run_id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, release_hash TEXT NOT NULL,
			input_json TEXT NOT NULL, variables_json TEXT NOT NULL,
			deadline TEXT NOT NULL, attempts INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_runs (
			run_id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			lock_key TEXT NOT NULL,
			schedule_id TEXT NOT NULL,
			trigger_source TEXT NOT NULL,
			scheduled_for TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			completed_at TEXT,
			active_session_id TEXT NOT NULL DEFAULT '',
			run_folder TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_schedule_runs_active_lock
			ON schedule_runs(lock_key)
			WHERE state IN ('starting','workflow_running','workflow_finished','pulse_gate','pulse_modules','pulse_finalizing')`,
		`CREATE INDEX IF NOT EXISTS idx_schedule_runs_scope_started
			ON schedule_runs(scope_type, scope_id, schedule_id, started_at DESC)`,
		`CREATE TABLE IF NOT EXISTS schedule_run_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			from_state TEXT NOT NULL,
			to_state TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES schedule_runs(run_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_run_sessions (
			run_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			session_kind TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			PRIMARY KEY(run_id, session_id),
			FOREIGN KEY(run_id) REFERENCES schedule_runs(run_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_fire_decisions (
			decision_id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			schedule_id TEXT NOT NULL,
			trigger_source TEXT NOT NULL,
			decision TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			run_id TEXT NOT NULL DEFAULT '',
			scheduled_for TEXT NOT NULL,
			fired_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_schedule_fire_scope_time
			ON schedule_fire_decisions(scope_type, scope_id, schedule_id, fired_at DESC)`,
		`CREATE TABLE IF NOT EXISTS schedule_pending_occurrences (
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			schedule_id TEXT NOT NULL,
			scheduled_for TEXT NOT NULL,
			latest_scheduled_for TEXT NOT NULL DEFAULT '',
			queued_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			trigger_source TEXT NOT NULL,
			policy TEXT NOT NULL DEFAULT 'queue_latest',
			occurrence_count INTEGER NOT NULL DEFAULT 1,
			reason TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(scope_type, scope_id, schedule_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_schedule_pending_expiry
			ON schedule_pending_occurrences(expires_at, queued_at)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize schedule state: %w", err)
		}
	}
	// Existing installations predate occurrence identity. Backfill those rows
	// from fired_at, which was the only timestamp previously retained.
	if err := ensureColumn(ctx, s.db, "schedule_fire_decisions", "scheduled_for", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("initialize schedule state: %w", err)
	}
	if err := ensureColumn(ctx, s.db, "schedule_runs", "scheduled_for", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("initialize schedule state: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET scheduled_for=started_at WHERE scheduled_for=''`); err != nil {
		return fmt.Errorf("backfill schedule run occurrence: %w", err)
	}
	if err := ensureColumn(ctx, s.db, "schedule_pending_occurrences", "latest_scheduled_for", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("initialize schedule state: %w", err)
	}
	if err := ensureColumn(ctx, s.db, "schedule_pending_occurrences", "policy", "TEXT NOT NULL DEFAULT 'queue_latest'"); err != nil {
		return fmt.Errorf("initialize schedule state: %w", err)
	}
	if err := ensureColumn(ctx, s.db, "schedule_pending_occurrences", "occurrence_count", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return fmt.Errorf("initialize schedule state: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schedule_pending_occurrences SET latest_scheduled_for=scheduled_for WHERE latest_scheduled_for=''`); err != nil {
		return fmt.Errorf("backfill pending latest occurrence: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schedule_fire_decisions SET scheduled_for=fired_at WHERE scheduled_for=''`); err != nil {
		return fmt.Errorf("backfill schedule fire occurrence: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_schedule_fire_occurrence
		ON schedule_fire_decisions(scope_type, scope_id, schedule_id, trigger_source, scheduled_for)`); err != nil {
		return fmt.Errorf("index schedule fire occurrence: %w", err)
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if strings.EqualFold(name, column) {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

func (s *Store) BeginRun(ctx context.Context, run Run) error {
	return s.beginRun(ctx, run, false)
}

// BeginQueuedRun atomically consumes the durable pending occurrence and claims
// its workflow lease. If the lease insert fails, SQLite rolls the deletion
// back, so a busy workflow or process crash cannot lose queued work.
func (s *Store) BeginQueuedRun(ctx context.Context, run Run) error {
	return s.beginRun(ctx, run, true)
}

func (s *Store) beginRun(ctx context.Context, run Run, consumePending bool) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("schedule state store is unavailable")
	}
	if strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(run.LockKey) == "" || strings.TrimSpace(run.ScheduleID) == "" {
		return fmt.Errorf("run_id, lock_key, and schedule_id are required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	if run.ScheduledFor.IsZero() {
		run.ScheduledFor = run.StartedAt
	}
	run.StartedAt = run.StartedAt.UTC()
	run.ScheduledFor = run.ScheduledFor.UTC()
	if run.State == "" {
		run.State = StateStarting
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if consumePending {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM schedule_pending_occurrences
			WHERE scope_type=? AND scope_id=? AND schedule_id=?`, run.ScopeType, run.ScopeID, run.ScheduleID)
		if deleteErr != nil {
			return deleteErr
		}
		deleted, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if deleted != 1 {
			return fmt.Errorf("%w: %s/%s/%s", ErrPendingOccurrenceMissing, run.ScopeType, run.ScopeID, run.ScheduleID)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO schedule_runs (
		run_id, scope_type, scope_id, lock_key, schedule_id, trigger_source,
		scheduled_for, state, started_at, updated_at, active_session_id, run_folder, error_message
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.ScopeType, run.ScopeID, run.LockKey, run.ScheduleID, run.TriggerSource,
		formatTime(run.ScheduledFor), run.State, formatTime(run.StartedAt), formatTime(run.StartedAt), run.ActiveSessionID, run.RunFolder, run.ErrorMessage)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return fmt.Errorf("%w: %s", ErrRunAlreadyActive, run.LockKey)
		}
		return fmt.Errorf("insert schedule run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_run_events (run_id, from_state, to_state, reason, created_at)
		VALUES (?, '', ?, 'run claimed', ?)`, run.RunID, run.State, formatTime(run.StartedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Transition(ctx context.Context, transition Transition) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("schedule state store is unavailable")
	}
	if transition.At.IsZero() {
		transition.At = time.Now().UTC()
	}
	transition.At = transition.At.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM schedule_runs WHERE run_id = ?`, transition.RunID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrRunNotFound, transition.RunID)
		}
		return err
	}
	from := State(current)
	if from == transition.To {
		return tx.Commit()
	}
	if IsTerminal(from) || !allowedTransitions[from][transition.To] {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, transition.To)
	}

	completedAt := ""
	if IsTerminal(transition.To) {
		completedAt = formatTime(transition.At)
	}
	_, err = tx.ExecContext(ctx, `UPDATE schedule_runs SET
		state = ?, updated_at = ?,
		completed_at = CASE WHEN ? <> '' THEN ? ELSE completed_at END,
		active_session_id = CASE WHEN ? <> '' THEN ? ELSE active_session_id END,
		run_folder = CASE WHEN ? <> '' THEN ? ELSE run_folder END,
		error_message = CASE WHEN ? <> '' THEN ? ELSE error_message END
		WHERE run_id = ?`,
		transition.To, formatTime(transition.At), completedAt, completedAt,
		transition.SessionID, transition.SessionID, transition.RunFolder, transition.RunFolder,
		transition.ErrorMessage, transition.ErrorMessage, transition.RunID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_run_events (run_id, from_state, to_state, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`, transition.RunID, from, transition.To, transition.Reason, formatTime(transition.At)); err != nil {
		return err
	}
	if transition.SessionID != "" {
		sessionKind := transition.SessionKind
		if sessionKind == "" {
			sessionKind = "main"
		}
		sessionStatus := "running"
		var endedAt any
		if IsTerminal(transition.To) {
			sessionStatus = string(transition.To)
			endedAt = formatTime(transition.At)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schedule_run_sessions (
			run_id, session_id, session_kind, status, started_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, session_id) DO UPDATE SET
			session_kind = excluded.session_kind,
			status = excluded.status,
			ended_at = COALESCE(excluded.ended_at, schedule_run_sessions.ended_at)`,
			transition.RunID, transition.SessionID, sessionKind, sessionStatus, formatTime(transition.At), endedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ForceTerminal releases an active run lease after ordinary terminal
// transition retries fail. It never changes one terminal state into another.
func (s *Store) ForceTerminal(ctx context.Context, transition Transition) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("schedule state store is unavailable")
	}
	if !IsTerminal(transition.To) {
		return fmt.Errorf("forced transition must be terminal: %s", transition.To)
	}
	if transition.At.IsZero() {
		transition.At = time.Now().UTC()
	}
	transition.At = transition.At.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM schedule_runs WHERE run_id = ?`, transition.RunID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrRunNotFound, transition.RunID)
		}
		return err
	}
	from := State(current)
	if IsTerminal(from) {
		if from == transition.To {
			return tx.Commit()
		}
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, transition.To)
	}

	completedAt := formatTime(transition.At)
	if _, err := tx.ExecContext(ctx, `UPDATE schedule_runs SET
		state = ?, updated_at = ?, completed_at = ?,
		active_session_id = CASE WHEN ? <> '' THEN ? ELSE active_session_id END,
		run_folder = CASE WHEN ? <> '' THEN ? ELSE run_folder END,
		error_message = CASE WHEN ? <> '' THEN ? ELSE error_message END
		WHERE run_id = ?`,
		transition.To, completedAt, completedAt,
		transition.SessionID, transition.SessionID, transition.RunFolder, transition.RunFolder,
		transition.ErrorMessage, transition.ErrorMessage, transition.RunID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_run_events (run_id, from_state, to_state, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`, transition.RunID, from, transition.To, "recovered terminal transition: "+transition.Reason, completedAt); err != nil {
		return err
	}
	if transition.SessionID != "" {
		sessionKind := transition.SessionKind
		if sessionKind == "" {
			sessionKind = "main"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_run_sessions (
			run_id, session_id, session_kind, status, started_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, session_id) DO UPDATE SET
			session_kind = excluded.session_kind,
			status = excluded.status,
			ended_at = excluded.ended_at`, transition.RunID, transition.SessionID, sessionKind,
			transition.To, completedAt, completedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordFireDecision(ctx context.Context, decision FireDecision) error {
	if decision.FiredAt.IsZero() {
		decision.FiredAt = time.Now().UTC()
	}
	if decision.ScheduledFor.IsZero() {
		decision.ScheduledFor = decision.FiredAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_fire_decisions (
		decision_id, scope_type, scope_id, schedule_id, trigger_source, decision, reason, run_id, scheduled_for, fired_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(scope_type, scope_id, schedule_id, trigger_source, scheduled_for) DO UPDATE SET
		decision=excluded.decision, reason=excluded.reason, run_id=excluded.run_id, fired_at=excluded.fired_at`,
		decision.DecisionID, decision.ScopeType, decision.ScopeID,
		decision.ScheduleID, decision.TriggerSource, decision.Decision, decision.Reason, decision.RunID,
		formatTime(decision.ScheduledFor), formatTime(decision.FiredAt)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedule_fire_decisions
		WHERE scope_type = ? AND scope_id = ? AND schedule_id = ? AND trigger_source = ?
		AND decision_id NOT IN (
			SELECT decision_id FROM schedule_fire_decisions
			WHERE scope_type = ? AND scope_id = ? AND schedule_id = ? AND trigger_source = ?
			ORDER BY fired_at DESC, decision_id DESC LIMIT ?
		)`, decision.ScopeType, decision.ScopeID, decision.ScheduleID,
		decision.TriggerSource, decision.ScopeType, decision.ScopeID, decision.ScheduleID,
		decision.TriggerSource, fireDecisionRetentionPerSchedule); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListFireDecisions(ctx context.Context, scopeType, scopeID, scheduleID string, limit int) ([]FireDecision, error) {
	if limit <= 0 || limit > fireDecisionRetentionPerSchedule {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT decision_id, scope_type, scope_id, schedule_id,
		trigger_source, decision, reason, run_id, scheduled_for, fired_at
		FROM schedule_fire_decisions
		WHERE scope_type = ? AND scope_id = ? AND schedule_id = ?
		ORDER BY fired_at DESC LIMIT ?`, scopeType, scopeID, scheduleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []FireDecision
	for rows.Next() {
		var decision FireDecision
		var scheduledFor, firedAt string
		if err := rows.Scan(&decision.DecisionID, &decision.ScopeType, &decision.ScopeID, &decision.ScheduleID,
			&decision.TriggerSource, &decision.Decision, &decision.Reason, &decision.RunID, &scheduledFor, &firedAt); err != nil {
			return nil, err
		}
		decision.ScheduledFor, err = parseTime(scheduledFor)
		if err != nil {
			return nil, err
		}
		decision.FiredAt, err = parseTime(firedAt)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

// LatestFireDecision returns the latest durable occurrence for one trigger
// source. Cron registration uses it as its restart cursor; manual fires must
// never move that cursor.
func (s *Store) LatestFireDecision(ctx context.Context, scopeType, scopeID, scheduleID, triggerSource string) (FireDecision, error) {
	var decision FireDecision
	var scheduledFor, firedAt string
	err := s.db.QueryRowContext(ctx, `SELECT decision_id, scope_type, scope_id, schedule_id,
		trigger_source, decision, reason, run_id, scheduled_for, fired_at
		FROM schedule_fire_decisions
		WHERE scope_type=? AND scope_id=? AND schedule_id=? AND trigger_source=?
		ORDER BY scheduled_for DESC, fired_at DESC LIMIT 1`,
		scopeType, scopeID, scheduleID, triggerSource).Scan(
		&decision.DecisionID, &decision.ScopeType, &decision.ScopeID, &decision.ScheduleID,
		&decision.TriggerSource, &decision.Decision, &decision.Reason, &decision.RunID, &scheduledFor, &firedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FireDecision{}, ErrRunNotFound
	}
	if err != nil {
		return FireDecision{}, err
	}
	decision.ScheduledFor, err = parseTime(scheduledFor)
	if err != nil {
		return FireDecision{}, err
	}
	decision.FiredAt, err = parseTime(firedAt)
	return decision, err
}

func (s *Store) UpsertPendingOccurrence(ctx context.Context, pending PendingOccurrence) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("schedule state store is unavailable")
	}
	if strings.TrimSpace(pending.ScopeType) == "" || strings.TrimSpace(pending.ScopeID) == "" || strings.TrimSpace(pending.ScheduleID) == "" {
		return fmt.Errorf("scope_type, scope_id, and schedule_id are required")
	}
	if pending.QueuedAt.IsZero() {
		pending.QueuedAt = time.Now().UTC()
	}
	if pending.ScheduledFor.IsZero() {
		pending.ScheduledFor = pending.QueuedAt
	}
	if pending.LatestScheduledFor.IsZero() {
		pending.LatestScheduledFor = pending.ScheduledFor
	}
	if pending.OccurrenceCount < 1 {
		pending.OccurrenceCount = 1
	}
	if strings.TrimSpace(pending.Policy) == "" {
		pending.Policy = "queue_latest"
	}
	if pending.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	base := `INSERT INTO schedule_pending_occurrences (
		scope_type, scope_id, schedule_id, scheduled_for, latest_scheduled_for, queued_at,
		expires_at, trigger_source, policy, occurrence_count, reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	conflict := ` ON CONFLICT(scope_type, scope_id, schedule_id) DO UPDATE SET
		scheduled_for=excluded.scheduled_for, latest_scheduled_for=excluded.latest_scheduled_for,
		queued_at=excluded.queued_at, expires_at=excluded.expires_at,
		trigger_source=excluded.trigger_source, policy=excluded.policy,
		occurrence_count=excluded.occurrence_count, reason=excluded.reason`
	switch pending.Policy {
	case "retry":
		// Preserve the first blocked occurrence and its deadline. Later cron
		// occurrences remain visible in fire decisions but do not replace the
		// exact occurrence being retried.
		conflict = ` ON CONFLICT(scope_type, scope_id, schedule_id) DO UPDATE SET
			reason=excluded.reason`
	case "coalesce":
		// Preserve the first occurrence/deadline while recording the newest
		// occurrence and the number folded into the eventual catch-up run.
		conflict = ` ON CONFLICT(scope_type, scope_id, schedule_id) DO UPDATE SET
			latest_scheduled_for=excluded.latest_scheduled_for,
			occurrence_count=schedule_pending_occurrences.occurrence_count + 1,
			reason=excluded.reason`
	}
	_, err := s.db.ExecContext(ctx, base+conflict,
		pending.ScopeType, pending.ScopeID, pending.ScheduleID, formatTime(pending.ScheduledFor),
		formatTime(pending.LatestScheduledFor), formatTime(pending.QueuedAt), formatTime(pending.ExpiresAt),
		pending.TriggerSource, pending.Policy, pending.OccurrenceCount, pending.Reason)
	return err
}

func (s *Store) ListPendingOccurrences(ctx context.Context) ([]PendingOccurrence, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("schedule state store is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT scope_type, scope_id, schedule_id,
		scheduled_for, latest_scheduled_for, queued_at, expires_at, trigger_source, policy, occurrence_count, reason
		FROM schedule_pending_occurrences ORDER BY queued_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []PendingOccurrence
	for rows.Next() {
		var item PendingOccurrence
		var scheduledFor, latestScheduledFor, queuedAt, expiresAt string
		if err := rows.Scan(&item.ScopeType, &item.ScopeID, &item.ScheduleID, &scheduledFor,
			&latestScheduledFor, &queuedAt, &expiresAt, &item.TriggerSource, &item.Policy, &item.OccurrenceCount, &item.Reason); err != nil {
			return nil, err
		}
		if item.ScheduledFor, err = parseTime(scheduledFor); err != nil {
			return nil, err
		}
		if item.QueuedAt, err = parseTime(queuedAt); err != nil {
			return nil, err
		}
		if item.LatestScheduledFor, err = parseTime(latestScheduledFor); err != nil {
			return nil, err
		}
		if item.ExpiresAt, err = parseTime(expiresAt); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (s *Store) GetPendingOccurrence(ctx context.Context, scopeType, scopeID, scheduleID string) (PendingOccurrence, error) {
	if s == nil || s.db == nil {
		return PendingOccurrence{}, fmt.Errorf("schedule state store is unavailable")
	}
	var item PendingOccurrence
	var scheduledFor, latestScheduledFor, queuedAt, expiresAt string
	err := s.db.QueryRowContext(ctx, `SELECT scope_type, scope_id, schedule_id,
		scheduled_for, latest_scheduled_for, queued_at, expires_at, trigger_source, policy, occurrence_count, reason
		FROM schedule_pending_occurrences WHERE scope_type=? AND scope_id=? AND schedule_id=?`,
		scopeType, scopeID, scheduleID).Scan(&item.ScopeType, &item.ScopeID, &item.ScheduleID,
		&scheduledFor, &latestScheduledFor, &queuedAt, &expiresAt, &item.TriggerSource,
		&item.Policy, &item.OccurrenceCount, &item.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingOccurrence{}, ErrRunNotFound
	}
	if err != nil {
		return PendingOccurrence{}, err
	}
	if item.ScheduledFor, err = parseTime(scheduledFor); err != nil {
		return PendingOccurrence{}, err
	}
	if item.LatestScheduledFor, err = parseTime(latestScheduledFor); err != nil {
		return PendingOccurrence{}, err
	}
	if item.QueuedAt, err = parseTime(queuedAt); err != nil {
		return PendingOccurrence{}, err
	}
	if item.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return PendingOccurrence{}, err
	}
	return item, nil
}

func (s *Store) DeletePendingOccurrence(ctx context.Context, scopeType, scopeID, scheduleID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("schedule state store is unavailable")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM schedule_pending_occurrences
		WHERE scope_type=? AND scope_id=? AND schedule_id=?`, scopeType, scopeID, scheduleID)
	return err
}

// LatestRunForSchedule returns the most recently started run for a schedule.
func (s *Store) LatestRunForSchedule(ctx context.Context, scopeType, scopeID, scheduleID string) (Run, error) {
	var run Run
	var scheduledFor, startedAt, updatedAt, completedAt string
	err := s.db.QueryRowContext(ctx, `SELECT run_id, scope_type, scope_id, lock_key, schedule_id,
		trigger_source, scheduled_for, state, started_at, updated_at, COALESCE(completed_at, ''), active_session_id,
		run_folder, error_message FROM schedule_runs
		WHERE scope_type=? AND scope_id=? AND schedule_id=? ORDER BY started_at DESC LIMIT 1`,
		scopeType, scopeID, scheduleID).Scan(
		&run.RunID, &run.ScopeType, &run.ScopeID, &run.LockKey, &run.ScheduleID, &run.TriggerSource,
		&scheduledFor, &run.State, &startedAt, &updatedAt, &completedAt, &run.ActiveSessionID, &run.RunFolder, &run.ErrorMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return Run{}, err
	}
	if run.ScheduledFor, err = parseTime(scheduledFor); err != nil {
		return Run{}, err
	}
	if run.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Run{}, err
	}
	if completedAt != "" {
		completed, parseErr := parseTime(completedAt)
		if parseErr != nil {
			return Run{}, parseErr
		}
		run.CompletedAt = &completed
	}
	return run, nil
}

// RunForScheduleOccurrence returns the run linked to the latest accepted fire
// decision inside [start,end). The durable scheduled_for→run_id link prevents a
// different manual or same-day run from releasing a dependent occurrence.
func (s *Store) RunForScheduleOccurrence(ctx context.Context, scopeType, scopeID, scheduleID string, start, end time.Time) (Run, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM schedule_fire_decisions
		WHERE scope_type=? AND scope_id=? AND schedule_id=? AND run_id<>''
		  AND decision='started'
		  AND scheduled_for>=? AND scheduled_for<?
		ORDER BY scheduled_for DESC, fired_at DESC LIMIT 1`,
		scopeType, scopeID, scheduleID, formatTime(start), formatTime(end)).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, runID)
}

// WaitingCapacityRuns discovers suspended invocations independently of cron jobs.
func (s *Store) WaitingCapacityRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT run_id FROM schedule_runs WHERE state = ?", StateWaitingForCapacity)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var runs []Run
	for _, id := range ids {
		run, err := s.GetRun(ctx, id)
		if err != nil {
			return nil, err
		}
		if run.State == StateWaitingForCapacity {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

// ResumeCapacityRun atomically claims the existing row. Unlike idempotent
// Transition, only one tick may change waiting -> starting and launch work.
func (s *Store) ResumeCapacityRun(ctx context.Context, runID string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE schedule_runs SET state = ?, updated_at = ?, active_session_id = '', error_message = '' WHERE run_id = ? AND state = ?`, StateStarting, formatTime(at), runID, StateWaitingForCapacity)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: capacity resume already claimed", ErrRunAlreadyActive)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_run_events (run_id, from_state, to_state, reason, created_at) VALUES (?, ?, ?, ?, ?)`, runID, StateWaitingForCapacity, StateStarting, "capacity window reopened", formatTime(at)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetRun(ctx context.Context, runID string) (Run, error) {
	var run Run
	var scheduledFor, startedAt, updatedAt, completedAt string
	err := s.db.QueryRowContext(ctx, `SELECT run_id, scope_type, scope_id, lock_key, schedule_id,
		trigger_source, scheduled_for, state, started_at, updated_at, COALESCE(completed_at, ''), active_session_id,
		run_folder, error_message FROM schedule_runs WHERE run_id = ?`, runID).Scan(
		&run.RunID, &run.ScopeType, &run.ScopeID, &run.LockKey, &run.ScheduleID, &run.TriggerSource,
		&scheduledFor, &run.State, &startedAt, &updatedAt, &completedAt, &run.ActiveSessionID, &run.RunFolder, &run.ErrorMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	run.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return Run{}, err
	}
	run.ScheduledFor, err = parseTime(scheduledFor)
	if err != nil {
		return Run{}, err
	}
	run.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Run{}, err
	}
	if completedAt != "" {
		parsed, parseErr := parseTime(completedAt)
		if parseErr != nil {
			return Run{}, parseErr
		}
		run.CompletedAt = &parsed
	}
	return run, nil
}

// ActiveRunByLockKey returns the run currently holding a schedule/workflow
// lease. The partial unique index guarantees that at most one row can match.
func (s *Store) ActiveRunByLockKey(ctx context.Context, lockKey string) (Run, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM schedule_runs
		WHERE lock_key = ?
		  AND state IN ('starting','workflow_running','workflow_finished','pulse_gate','pulse_modules','pulse_finalizing')
		LIMIT 1`, lockKey).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, runID)
}

// ActiveRunForSchedule resolves an active occurrence by durable schedule
// identity, independent of which concurrency lane supplied its lock key. This
// is used after restart when the in-memory run ID is unavailable.
func (s *Store) ActiveRunForSchedule(ctx context.Context, scopeType, scopeID, scheduleID string) (Run, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM schedule_runs
		WHERE scope_type = ? AND scope_id = ? AND schedule_id = ? AND completed_at IS NULL
		ORDER BY started_at DESC LIMIT 1`, scopeType, scopeID, scheduleID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, runID)
}

func (s *Store) ListEvents(ctx context.Context, runID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence, run_id, from_state, to_state, reason, created_at
		FROM schedule_run_events WHERE run_id = ? ORDER BY sequence`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var createdAt string
		if err := rows.Scan(&event.Sequence, &event.RunID, &event.FromState, &event.ToState, &event.Reason, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) InterruptActiveRuns(ctx context.Context, reason string, at time.Time) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id FROM schedule_runs
		WHERE state IN ('starting','workflow_running','workflow_finished','pulse_gate','pulse_modules','pulse_finalizing')`)
	if err != nil {
		return 0, err
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, runID := range runIDs {
		if err := s.Transition(ctx, Transition{RunID: runID, To: StateInterrupted, Reason: reason, ErrorMessage: reason, At: at}); err != nil {
			return 0, err
		}
	}
	return len(runIDs), nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

// AssignRunFolder binds output identity once while the run is active.
func (s *Store) AssignRunFolder(ctx context.Context, runID, folder string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET run_folder = ? WHERE run_id = ? AND completed_at IS NULL AND (run_folder = '' OR run_folder = ?)`, folder, runID, folder)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("run folder already bound or run inactive")
	}
	return nil
}
