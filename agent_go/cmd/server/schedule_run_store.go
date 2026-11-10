package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/schedulehistory"
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

var errScheduleHistoryBackupDiffers = errors.New("imported source differs from its private backup; keeping SQL history authoritative and both files unchanged")

const scheduleHistoryTerminal = `status IN ('success','error','stopped','partial','failed','interrupted')`

func scheduleHistoryHash(value string) string { return schedulehistory.Hash(value) }

func scheduleHistoryScope(workspace string) (string, error) {
	docs, err := schedulehistory.PhysicalPath(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", err
	}
	file, err := fsutil.WorkspacePath(docs, scheduleRunsPath(workspace))
	if err != nil {
		return "", err
	}
	return schedulehistory.PhysicalPath(filepath.Dir(file))
}

func scheduleHistoryRemovalScope(workspace string) (string, error) {
	docs, err := schedulehistory.PhysicalPath(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", err
	}
	file, err := fsutil.WorkspacePath(docs, scheduleRunsPath(workspace))
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(file)
	if info, e := os.Lstat(dir); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", nil
	} else if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	return schedulehistory.PhysicalPath(dir)
}

func openScheduleHistory(ctx context.Context, workspace string) (*scheduleHistory, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("choose a project to open its run history")
	}
	docs, err := scheduleHistoryPhysicalPath(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return nil, err
	}
	legacy, err := fsutil.WorkspacePath(docs, scheduleRunsPath(workspace))
	if err != nil {
		return nil, err
	}
	legacy, err = filepath.Abs(legacy)
	if err != nil {
		return nil, err
	}
	scope, err := scheduleHistoryPhysicalPath(filepath.Dir(legacy))
	if err != nil {
		return nil, err
	}
	legacy = filepath.Join(scope, "schedule-runs.json")
	file, err := schedulehistory.DatabasePath(docs)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(file)
	db, err := openScheduleHistoryDatabase(ctx, dir)
	if err != nil {
		return nil, err
	}
	h := &scheduleHistory{db: db, scope: scope, legacy: legacy, importDir: filepath.Join(dir, "imports", scheduleHistoryHash(scope))}
	h.backup = filepath.Join(h.importDir, "schedule-runs.json.imported")
	if err = h.importOnce(ctx); err != nil {
		return nil, err
	}
	return h, nil
}

func scheduleHistoryPhysicalPath(value string) (string, error) {
	return schedulehistory.PhysicalPath(value)
}

// Connections and schema initialization are process resources, not a cache of
// workspace documents or rows. Every operation resolves its scope afresh.
var scheduleHistoryConnections = struct {
	sync.Mutex
	byFile map[string]*sql.DB
}{byFile: map[string]*sql.DB{}}

func openScheduleHistoryDatabase(ctx context.Context, dir string) (*sql.DB, error) {
	file := filepath.Join(dir, "history.sqlite")
	scheduleHistoryConnections.Lock()
	defer scheduleHistoryConnections.Unlock()
	if db := scheduleHistoryConnections.byFile[file]; db != nil {
		return db, nil
	}
	if err := agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
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
	db.SetMaxOpenConns(4)
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
	scheduleHistoryConnections.byFile[file] = db
	return db, nil
}

func (h *scheduleHistory) importOnce(ctx context.Context) error {
	var pending int
	err := h.db.QueryRowContext(ctx, `SELECT cleanup_pending FROM history_imports WHERE scope=?`, h.scope).Scan(&pending)
	if err == nil && pending == 0 {
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
	if err == nil && pending == 0 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		// The first source read belongs inside the same SQLite writer boundary
		// as deletion. A folder purge waits for this import (all descendants share
		// this DB), or wins first and the importer sees no old file afterward.
		tx, e := h.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
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
				// Bad history must not block future schedule admission. Keep all bytes
				// privately after committing an empty history; never use partial rows.
				runs = nil
				pending = 2
			}
		}
		// Preserve the original array order and duplicate/empty legacy IDs; the
		// private serial key makes lookups update only the same first matching row.
		for i := len(runs) - 1; i >= 0; i-- {
			if e = h.insert(ctx, tx, runs[i]); e != nil {
				return e
			}
		}
		if exists && pending == 0 {
			pending = 1
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO history_imports(scope,cleanup_pending) VALUES (?,?)`, h.scope, pending); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		event := "import"
		if pending == 2 {
			event = "import_unreadable"
		}
		log.Printf("[SCHEDULE_HISTORY] scope=%s event=%s rows=%d source_bytes=%d cleanup_pending=%d", scheduleHistoryHash(h.scope), event, len(runs), len(raw), pending)
	}
	if pending != 0 {
		mode := pending
		if mode < 0 {
			mode = -mode
		}
		backup := h.backup
		if mode == 2 {
			backup = filepath.Join(h.importDir, "schedule-runs.json.unreadable")
		}
		if err = h.retireSource(backup); err != nil {
			// SQL is authoritative after commit. Report cleanup failure once, and
			// retry retirement later without blocking reads or new schedule runs.
			if pending > 0 || errors.Is(err, errScheduleHistoryBackupDiffers) {
				next := -mode
				if errors.Is(err, errScheduleHistoryBackupDiffers) {
					next = 0
				}
				if _, markErr := h.db.ExecContext(ctx, `UPDATE history_imports SET cleanup_pending=? WHERE scope=?`, next, h.scope); markErr != nil {
					return markErr
				}
				log.Printf("[SCHEDULE_HISTORY] scope=%s event=source_retirement_failed history=sqlite error=%v", scheduleHistoryHash(h.scope), err)
			}
			return nil
		}
		if _, err = h.db.ExecContext(ctx, `UPDATE history_imports SET cleanup_pending=0 WHERE scope=?`, h.scope); err != nil {
			return err
		}
		log.Printf("[SCHEDULE_HISTORY] scope=%s event=source_retired unreadable=%t", scheduleHistoryHash(h.scope), mode == 2)
	}

	return nil
}

// Commit precedes rename. Across volumes publish a complete private backup
// before removing the source; a committed marker resumes cleanup, never import.
func (h *scheduleHistory) retireSource(backupPath string) error {
	if _, err := os.Lstat(h.legacy); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if backup, err := os.ReadFile(backupPath); err == nil {
		source, e := os.ReadFile(h.legacy)
		if e != nil {
			return e
		}
		if !bytes.Equal(backup, source) {
			return errScheduleHistoryBackupDiffers
		}
		return os.Remove(h.legacy)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(h.legacy, backupPath); err == nil {
		return os.Chmod(backupPath, 0600)
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
	if err = os.Rename(temp.Name(), backupPath); err != nil {
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

func purgeRemovedScheduleHistory(ctx context.Context, physical string) error {
	if physical == "" {
		return nil
	} // removing only a symlink left the project intact
	return schedulehistory.PurgeTree(ctx, fsutil.WorkspaceDocsRoot(), physical)
}
