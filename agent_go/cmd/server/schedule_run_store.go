package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Schedule history is service state, not an agent-editable workspace document.
// Row payloads preserve the public record; indexed columns serve small status
// queries without loading final responses, input metadata or token receipts.
type scheduleHistory struct {
	db                               *sql.DB
	scope, legacy, backup, importDir string
}

const scheduleHistoryTerminal = `status IN ('success','error','stopped','partial','failed','interrupted')`

func scheduleHistoryHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func openScheduleHistory(ctx context.Context, workspace string) (*scheduleHistory, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("choose a project to open its run history")
	}
	docs, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return nil, err
	}
	if real, e := filepath.EvalSymlinks(docs); e == nil {
		docs = real
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	legacy, err := fsutil.WorkspacePath(docs, scheduleRunsPath(workspace))
	if err != nil {
		return nil, err
	}
	legacy, err = filepath.Abs(legacy)
	if err != nil {
		return nil, err
	}
	scope := filepath.Dir(legacy)
	if real, e := filepath.EvalSymlinks(scope); e == nil {
		scope = real
		legacy = filepath.Join(scope, "schedule-runs.json")
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(state, "schedule-history", scheduleHistoryHash(docs))
	if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "history.sqlite")
	if info, e := os.Lstat(file); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("run history must be an ordinary private database")
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(file)+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	h := &scheduleHistory{db: db, scope: scope, legacy: legacy, importDir: filepath.Join(dir, "imports", scheduleHistoryHash(scope))}
	h.backup = filepath.Join(h.importDir, "schedule-runs.json.imported")
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS history_imports (scope TEXT PRIMARY KEY,cleanup_pending INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS history_entries (serial INTEGER PRIMARY KEY AUTOINCREMENT,scope TEXT NOT NULL,run_id TEXT NOT NULL,schedule_id TEXT NOT NULL,status TEXT NOT NULL,trigger_source TEXT NOT NULL,run_folder TEXT NOT NULL,has_webhook INTEGER NOT NULL,started_ns INTEGER,started_at TEXT NOT NULL,session_id TEXT NOT NULL,error_text TEXT NOT NULL,duration_ms INTEGER,payload TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS history_order ON history_entries(scope,serial DESC)`,
		`CREATE INDEX IF NOT EXISTS history_run ON history_entries(scope,run_id,serial DESC)`,
		`CREATE INDEX IF NOT EXISTS history_schedule ON history_entries(scope,schedule_id,started_ns DESC,serial DESC)`,
		`CREATE INDEX IF NOT EXISTS history_status ON history_entries(scope,status,started_ns)`,
		`CREATE INDEX IF NOT EXISTS history_folder ON history_entries(scope,run_folder,serial DESC)`,
	} {
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err = h.importOnce(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return h, nil
}

func (h *scheduleHistory) importOnce(ctx context.Context) error {
	var pending bool
	err := h.db.QueryRowContext(ctx, `SELECT cleanup_pending FROM history_imports WHERE scope=?`, h.scope).Scan(&pending)
	if err == nil && !pending {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = agentworksclient.EnsurePrivateDirectory(h.importDir); err != nil {
		return err
	}
	if err = os.Chmod(h.importDir, 0700); err != nil {
		return err
	}
	// All new agent writers use SQLite. This filesystem lock also serializes
	// concurrent first opens before any source is retired.
	unlock, err := wf.LockWorkspace(ctx, h.importDir)
	if err != nil {
		return err
	}
	defer unlock()
	err = h.db.QueryRowContext(ctx, `SELECT cleanup_pending FROM history_imports WHERE scope=?`, h.scope).Scan(&pending)
	if err == nil && !pending {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		info, e := os.Lstat(h.legacy)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		exists := e == nil
		if exists && !info.Mode().IsRegular() {
			return errors.New("run history import must be an ordinary file")
		}
		var raw []byte
		var runs []ScheduleRunEntry
		if exists {
			raw, e = os.ReadFile(h.legacy)
			if e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &runs); e != nil {
				return fmt.Errorf("run history import failed: %w", e)
			}
		}
		tx, e := h.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		// Preserve the original array order and duplicate/empty legacy IDs; the
		// private serial key makes lookups update only the same first matching row.
		for i := len(runs) - 1; i >= 0; i-- {
			if e = h.insert(ctx, tx, runs[i]); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO history_imports(scope,cleanup_pending) VALUES (?,?)`, h.scope, exists); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		pending = exists
		log.Printf("[SCHEDULE_HISTORY] scope=%s event=import rows=%d source_bytes=%d cleanup_pending=%t", scheduleHistoryHash(h.scope), len(runs), len(raw), pending)
	}
	if pending {
		if err = h.retireSource(); err != nil {
			return err
		}
		if _, err = h.db.ExecContext(ctx, `UPDATE history_imports SET cleanup_pending=0 WHERE scope=?`, h.scope); err != nil {
			return err
		}
		log.Printf("[SCHEDULE_HISTORY] scope=%s event=source_retired", scheduleHistoryHash(h.scope))
	}
	return nil
}

// Commit precedes rename. Across volumes publish a complete private backup
// before removing the source; a committed marker resumes cleanup, never import.
func (h *scheduleHistory) retireSource() error {
	if _, err := os.Lstat(h.legacy); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if backup, err := os.ReadFile(h.backup); err == nil {
		source, e := os.ReadFile(h.legacy)
		if e != nil {
			return e
		}
		if !bytes.Equal(backup, source) {
			return errors.New("run history and its imported backup differ; ask an administrator to compare them before retrying")
		}
		return os.Remove(h.legacy)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(h.legacy, h.backup); err == nil {
		return os.Chmod(h.backup, 0600)
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	source, err := os.Open(h.legacy)
	if err != nil {
		return err
	}
	defer source.Close()
	temp, err := os.CreateTemp(h.importDir, "history-backup-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = io.Copy(temp, source)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), h.backup); err != nil {
		return err
	}
	return os.Remove(h.legacy)
}

func (h *scheduleHistory) insert(ctx context.Context, tx *sql.Tx, run ScheduleRunEntry) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	var started any
	if !run.StartedAt.IsZero() {
		started = run.StartedAt.UnixNano()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO history_entries(scope,run_id,schedule_id,status,trigger_source,run_folder,has_webhook,started_ns,started_at,session_id,error_text,duration_ms,payload) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, h.scope, run.ID, run.ScheduleID, run.Status, run.TriggerSource, run.RunFolder, run.Webhook != nil, started, run.StartedAt.Format(time.RFC3339Nano), run.SessionID, run.Error, run.DurationMs, string(payload))
	return err
}

func (h *scheduleHistory) prune(ctx context.Context, tx *sql.Tx) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -workflowScheduleRunRetentionDays).UnixNano()
	result, err := tx.ExecContext(ctx, `DELETE FROM history_entries WHERE scope=? AND `+scheduleHistoryTerminal+` AND started_ns IS NOT NULL AND started_ns<? AND serial NOT IN (SELECT serial FROM history_entries WHERE scope=? AND `+scheduleHistoryTerminal+` ORDER BY started_ns DESC,serial DESC LIMIT ?)`, h.scope, cutoff, h.scope, maxScheduleRuns)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func withScheduleHistoryWrite(ctx context.Context, workspace string, change func(*scheduleHistory, *sql.Tx) (bool, error)) error {
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		return err
	}
	defer h.db.Close()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed, err := change(h, tx)
	if err != nil {
		return err
	}
	var removed int64
	if changed {
		if removed, err = h.prune(ctx, tx); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if removed > 0 {
		log.Printf("[SCHEDULE_HISTORY] scope=%s event=pruned rows_removed=%d", scheduleHistoryHash(h.scope), removed)
	}
	if changed {
		noteScheduleSummaryChange()
		publishCrewPanelPathChanged(scheduleRunsPath(workspace))
	}
	return nil
}

func (h *scheduleHistory) find(ctx context.Context, tx *sql.Tx, id string) (int64, *ScheduleRunEntry, error) {
	var serial int64
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT serial,payload FROM history_entries WHERE scope=? AND run_id=? ORDER BY serial DESC LIMIT 1`, h.scope, id).Scan(&serial, &payload)
	if err != nil {
		return 0, nil, err
	}
	var run ScheduleRunEntry
	if err = json.Unmarshal([]byte(payload), &run); err != nil {
		return 0, nil, err
	}
	return serial, &run, nil
}

func updateScheduleHistoryRun(ctx context.Context, workspace, id string, missingOK bool, update func(*ScheduleRunEntry)) error {
	return mutateScheduleHistoryRun(ctx, workspace, id, missingOK, func(run *ScheduleRunEntry) bool { update(run); return true })
}

func mutateScheduleHistoryRun(ctx context.Context, workspace, id string, missingOK bool, update func(*ScheduleRunEntry) bool) error {
	return withScheduleHistoryWrite(ctx, workspace, func(h *scheduleHistory, tx *sql.Tx) (bool, error) {
		serial, run, err := h.find(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			if missingOK {
				return false, nil
			}
			return false, fmt.Errorf("schedule run %q not found", id)
		}
		if err != nil {
			return false, err
		}
		if !update(run) {
			return false, nil
		}
		payload, err := json.Marshal(run)
		if err != nil {
			return false, err
		}
		var started any
		if !run.StartedAt.IsZero() {
			started = run.StartedAt.UnixNano()
		}
		_, err = tx.ExecContext(ctx, `UPDATE history_entries SET schedule_id=?,status=?,trigger_source=?,run_folder=?,has_webhook=?,started_ns=?,started_at=?,session_id=?,error_text=?,duration_ms=?,payload=? WHERE scope=? AND serial=?`, run.ScheduleID, run.Status, run.TriggerSource, run.RunFolder, run.Webhook != nil, started, run.StartedAt.Format(time.RFC3339Nano), run.SessionID, run.Error, run.DurationMs, string(payload), h.scope, serial)
		return err == nil, err
	})
}

// Status polling never needs final responses, caller inputs or usage blobs.
func readScheduleRunStates(ctx context.Context, workspace, scheduleID string, runningOnly bool) ([]ScheduleRunEntry, error) {
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		return nil, err
	}
	defer h.db.Close()
	query := `SELECT run_id,schedule_id,status,trigger_source,started_at,session_id,error_text,duration_ms FROM history_entries WHERE scope=?`
	args := []any{h.scope}
	if scheduleID != "" {
		query += ` AND schedule_id=?`
		args = append(args, scheduleID)
	}
	if runningOnly {
		query += ` AND status='running'`
	}
	query += ` ORDER BY serial DESC`
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScheduleRunEntry{}
	for rows.Next() {
		var run ScheduleRunEntry
		var started string
		var duration sql.NullInt64
		if err = rows.Scan(&run.ID, &run.ScheduleID, &run.Status, &run.TriggerSource, &started, &run.SessionID, &run.Error, &duration); err != nil {
			return nil, err
		}
		if run.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
			return nil, err
		}
		if duration.Valid {
			value := duration.Int64
			run.DurationMs = &value
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// Preserve a completion written after a poller read a running row. In
// particular, reconciliation must not replace its final response or usage.
func finalizeRunningScheduleRun(ctx context.Context, workspace, id, status, message string, completedAt *time.Time) error {
	return mutateScheduleHistoryRun(ctx, workspace, id, true, func(run *ScheduleRunEntry) bool {
		if run.Status != "running" {
			return false
		}
		run.Status, run.Error, run.CompletedAt = status, message, completedAt
		if completedAt != nil {
			duration := completedAt.Sub(run.StartedAt).Milliseconds()
			if duration < 0 {
				duration = 0
			}
			run.DurationMs = &duration
		}
		return true
	})
}

// The runtime summary needs one latest record and a count, not the whole list.
func readScheduleRuntimeProjection(ctx context.Context, workspace, scheduleID string) ([]ScheduleRunEntry, int, error) {
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		return nil, 0, err
	}
	defer h.db.Close()
	var run ScheduleRunEntry
	var started string
	var duration sql.NullInt64
	var count int
	err = h.db.QueryRowContext(ctx, `SELECT run_id,schedule_id,status,started_at,session_id,error_text,duration_ms,count(*) OVER() FROM history_entries WHERE scope=? AND schedule_id=? ORDER BY started_ns DESC,serial DESC LIMIT 1`, h.scope, scheduleID).Scan(&run.ID, &run.ScheduleID, &run.Status, &started, &run.SessionID, &run.Error, &duration, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return []ScheduleRunEntry{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if run.StartedAt, err = time.Parse(time.RFC3339Nano, started); err != nil {
		return nil, 0, err
	}
	if duration.Valid {
		value := duration.Int64
		run.DurationMs = &value
	}
	return []ScheduleRunEntry{run}, count, nil
}

func readScheduleDurationAverages(ctx context.Context, workspace string, limit int) (map[string]scheduleRunDurationAverage, error) {
	result := map[string]scheduleRunDurationAverage{}
	if limit <= 0 {
		return result, nil
	}
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		return nil, err
	}
	defer h.db.Close()
	rows, err := h.db.QueryContext(ctx, `SELECT schedule_id,CAST(avg(duration_ms) AS INTEGER),count(*) FROM (SELECT schedule_id,duration_ms,row_number() OVER(PARTITION BY schedule_id ORDER BY started_ns DESC,serial DESC) AS recent FROM history_entries WHERE scope=? AND status='success' AND trigger_source!='manual' AND duration_ms>0 AND schedule_id!='') WHERE recent<=? GROUP BY schedule_id`, h.scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var average scheduleRunDurationAverage
		if err = rows.Scan(&id, &average.DurationMs, &average.Samples); err != nil {
			return nil, err
		}
		result[id] = average
	}
	return result, rows.Err()
}

// A few explicit detail paths need one full record, selected in SQLite instead
// of reading every response and usage receipt into the service heap.
func findScheduleHistoryDetail(ctx context.Context, workspace, predicate string, args ...any) (*ScheduleRunEntry, error) {
	h, err := openScheduleHistory(ctx, workspace)
	if err != nil {
		return nil, err
	}
	defer h.db.Close()
	values := append([]any{h.scope}, args...)
	var payload string
	err = h.db.QueryRowContext(ctx, `SELECT payload FROM history_entries WHERE scope=? AND `+predicate+` ORDER BY started_ns DESC,serial DESC LIMIT 1`, values...).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run ScheduleRunEntry
	if err = json.Unmarshal([]byte(payload), &run); err != nil {
		return nil, err
	}
	return &run, nil
}
