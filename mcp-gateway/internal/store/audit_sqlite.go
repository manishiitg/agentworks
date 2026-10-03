package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type auditWrite struct {
	event AuditEvent
	ack   chan error
}

var ErrAuditQueueFull = errors.New("audit queue full; event was not accepted")

type sqliteAudit struct {
	db                    *sql.DB
	leaseDB               *sql.DB
	lease                 *sql.Conn
	retention             time.Duration
	now                   func() time.Time
	writes                chan auditWrite
	stop, done            chan struct{}
	mu                    sync.Mutex
	closed                bool
	writeMode             string
	writeErr              error // protected by mu; rejects new admissions while retrying
	pendingWrites         atomic.Int64
	writeFailures         atomic.Int64
	closeOnce             sync.Once
	shutdownErr, closeErr error
}

func openSQLiteAudit(path string, retention time.Duration, maxBytes int64, maintain bool) (*sqliteAudit, error) {
	return openSQLiteAuditMode(path, retention, maxBytes, maintain, "durable")
}

func openSQLiteAuditMode(path string, retention time.Duration, maxBytes int64, maintain bool, writeMode string) (*sqliteAudit, error) {
	if writeMode == "" {
		writeMode = "durable"
	}
	if writeMode != "durable" && writeMode != "async" {
		return nil, errors.New("invalid audit write mode")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, errors.New("audit database must be a regular file")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	leaseDB, lease, err := acquireStoreLease(path + ".lock")
	if err != nil {
		return nil, err
	}
	release := func() { lease.ExecContext(context.Background(), "ROLLBACK"); lease.Close(); leaseDB.Close() }
	db, err := sql.Open("sqlite", path)
	if err != nil {
		release()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*sqliteAudit, error) { db.Close(); release(); return nil, e }
	// FULL sync commits are durable; grouped writes amortize fsync across calls.
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA auto_vacuum=INCREMENTAL", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA wal_autocheckpoint=1000", "PRAGMA journal_size_limit=4194304"} {
		if _, err = db.Exec(q); err != nil {
			return fail(err)
		}
	}
	var pageSize int64
	if err = db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return fail(err)
	}
	if maxBytes > 0 {
		var actualLimit int64
		if err = db.QueryRow(fmt.Sprintf("PRAGMA max_page_count=%d", maxBytes/pageSize)).Scan(&actualLimit); err != nil {
			return fail(err)
		}
		if actualLimit > maxBytes/pageSize {
			return fail(errors.New("existing audit database exceeds VAULT_AUDIT_MAX_MB"))
		}
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS audit_events (
 ID TEXT PRIMARY KEY, WorkspaceID TEXT NOT NULL, TimestampMs INTEGER NOT NULL,
 UserID TEXT NOT NULL, ClientID TEXT NOT NULL, ConnectorID TEXT NOT NULL,
 PublicName TEXT NOT NULL, Decision TEXT NOT NULL, Outcome TEXT NOT NULL,
 DurationMs INTEGER NOT NULL, GroupIDs TEXT NOT NULL, Event TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS audit_time ON audit_events(TimestampMs);
 CREATE INDEX IF NOT EXISTS audit_workspace_time ON audit_events(WorkspaceID,TimestampMs DESC);
 CREATE INDEX IF NOT EXISTS audit_user_time ON audit_events(WorkspaceID,UserID,TimestampMs DESC);
 CREATE INDEX IF NOT EXISTS audit_connector_time ON audit_events(WorkspaceID,ConnectorID,TimestampMs DESC);
 CREATE INDEX IF NOT EXISTS audit_tool_time ON audit_events(WorkspaceID,PublicName,TimestampMs DESC);`)
	if err != nil {
		return fail(err)
	}
	a := &sqliteAudit{db: db, leaseDB: leaseDB, lease: lease, retention: retention, now: time.Now, writeMode: writeMode, writes: make(chan auditWrite, 1024), stop: make(chan struct{}), done: make(chan struct{})}
	if maintain {
		if err = a.cleanup(context.Background()); err != nil {
			return fail(err)
		}
	}
	go a.writer(maintain)
	return a, nil
}
func (a *sqliteAudit) Info() AuditInfo {
	a.mu.Lock()
	healthy := a.writeErr == nil && !a.closed
	a.mu.Unlock()
	return AuditInfo{Provider: "sqlite", Enabled: true, RetentionSeconds: int64(a.retention / time.Second), WriteMode: a.writeMode,
		PendingWrites: a.pendingWrites.Load(), WriteFailures: a.writeFailures.Load(), WriteHealthy: healthy}
}
func (a *sqliteAudit) Append(ctx context.Context, e AuditEvent) error {
	if e.ID == "" || e.WorkspaceID == "" || e.Timestamp.IsZero() {
		return errors.New("audit event requires ID, workspace and timestamp")
	}
	e = cloneAuditEvent(e)
	w := auditWrite{event: e}
	if a.writeMode == "durable" {
		w.ack = make(chan error, 1)
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("audit storage closed")
	}
	if err := ctx.Err(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.writeMode == "async" {
		if a.writeErr != nil {
			a.mu.Unlock()
			return errors.New("audit writer unavailable; queued events are awaiting retry")
		}
		a.pendingWrites.Add(1)
		select {
		case a.writes <- w:
			a.mu.Unlock()
			return nil
		default:
			a.pendingWrites.Add(-1)
			a.mu.Unlock()
			return ErrAuditQueueFull
		}
	}
	a.pendingWrites.Add(1)
	select {
	case a.writes <- w:
		a.mu.Unlock()
	case <-ctx.Done():
		a.pendingWrites.Add(-1)
		a.mu.Unlock()
		return ctx.Err()
	}
	select {
	case err := <-w.ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *sqliteAudit) writer(maintain bool) {
	defer close(a.done)
	flush := time.NewTicker(5 * time.Millisecond)
	defer flush.Stop()
	cleanup := time.NewTicker(5 * time.Minute)
	defer cleanup.Stop()
	batch := make([]auditWrite, 0, 256)
	commit := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := a.commit(batch)
		if err != nil {
			a.writeFailures.Add(1)
			if a.writeMode == "async" {
				a.mu.Lock()
				firstFailure := a.writeErr == nil
				a.writeErr = err
				a.mu.Unlock()
				if firstFailure {
					log.Print("Vault audit writer unavailable; queued events retained for retry")
				}
				return err // retain the entire uncommitted batch
			}
		}
		if a.writeMode == "async" {
			a.mu.Lock()
			a.writeErr = nil
			a.mu.Unlock()
		}
		a.pendingWrites.Add(-int64(len(batch)))
		for _, w := range batch {
			if w.ack != nil {
				w.ack <- err
			}
		}
		clear(batch)
		batch = batch[:0]
		return err
	}
	var retry *time.Timer
	var retryC <-chan time.Time
	retryDelay := 50 * time.Millisecond
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	commitOrRetry := func() {
		if err := commit(); err != nil && a.writeMode == "async" {
			retry = time.NewTimer(retryDelay)
			retryC = retry.C
			if retryDelay < 5*time.Second {
				retryDelay *= 2
				if retryDelay > 5*time.Second {
					retryDelay = 5 * time.Second
				}
			}
		} else {
			retryDelay = 50 * time.Millisecond
		}
	}
	for {
		incoming := a.writes
		if retryC != nil {
			incoming = nil
		} // bounded queue; no new batch while retrying
		select {
		case w := <-incoming:
			batch = append(batch, w)
			if len(batch) >= 256 {
				commitOrRetry()
			}
		case <-flush.C:
			if retryC == nil {
				commitOrRetry()
			}
		case <-retryC:
			retryC = nil
			commitOrRetry()
		case <-cleanup.C:
			if maintain && retryC == nil {
				_ = a.cleanup(context.Background())
			}
		case <-a.stop:
			if err := commit(); err != nil {
				a.shutdownErr = err
				return
			}
			for {
				select {
				case w := <-a.writes:
					batch = append(batch, w)
					if len(batch) >= 256 {
						if err := commit(); err != nil {
							a.shutdownErr = err
							return
						}
					}
				default:
					a.shutdownErr = commit()
					return
				}
			}
		}
	}
}
func (a *sqliteAudit) commit(batch []auditWrite) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO audit_events VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	cutoff := a.now().Add(-a.retention)
	for _, w := range batch {
		e := w.event
		if a.retention > 0 && e.Timestamp.Before(cutoff) {
			continue
		}
		groups, err := json.Marshal(e.GroupIDs)
		if err != nil {
			return err
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, e.ID, e.WorkspaceID, e.Timestamp.UnixMilli(), e.UserID, e.ClientID, e.ConnectorID, e.PublicName, e.Decision, e.Outcome, e.DurationMs, string(groups), string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (a *sqliteAudit) cleanup(ctx context.Context) error {
	if a.retention <= 0 {
		return nil
	}
	// Small deletes avoid long writer stalls when a busy install starts after downtime.
	for {
		r, err := a.db.ExecContext(ctx, `DELETE FROM audit_events WHERE ID IN (SELECT ID FROM audit_events WHERE TimestampMs < ? LIMIT 5000)`, a.now().Add(-a.retention).UnixMilli())
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n < 5000 {
			break
		}
	}
	_, err := a.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, "PRAGMA incremental_vacuum(256)")
	return err
}
func sqliteAuditWhere(f AuditFilter, retention time.Duration, now time.Time) (string, []any) {
	parts := []string{"WorkspaceID = ?"}
	args := []any{f.WorkspaceID}
	for _, v := range []struct{ column, value string }{{"UserID", f.UserID}, {"ClientID", f.ClientID}, {"ConnectorID", f.ConnectorID}, {"Decision", f.Decision}, {"Outcome", f.Outcome}} {
		if v.value != "" {
			parts = append(parts, v.column+" = ?")
			args = append(args, v.value)
		}
	}
	if f.GroupID != "" {
		parts = append(parts, "EXISTS (SELECT 1 FROM json_each(audit_events.GroupIDs) WHERE value = ?)")
		args = append(args, f.GroupID)
	}
	if f.PublicName != "" {
		parts = append(parts, "instr(lower(PublicName), lower(?)) > 0")
		args = append(args, f.PublicName)
	}
	after := f.After
	if retention > 0 && after.Before(now.Add(-retention)) {
		after = now.Add(-retention)
	}
	if !after.IsZero() {
		parts = append(parts, "TimestampMs >= ?")
		args = append(args, after.UnixMilli())
	}
	if !f.Before.IsZero() {
		parts = append(parts, "TimestampMs <= ?")
		args = append(args, f.Before.UnixMilli())
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}
func (a *sqliteAudit) Query(ctx context.Context, f AuditFilter) ([]AuditEvent, error) {
	where, args := sqliteAuditWhere(f, a.retention, a.now())
	q := "SELECT Event FROM audit_events" + where + " ORDER BY TimestampMs DESC, ID DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var raw string
		var e AuditEvent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (a *sqliteAudit) Summary(ctx context.Context, f AuditFilter) (AuditSummary, error) {
	where, args := sqliteAuditWhere(f, a.retention, a.now())
	out := emptyAuditSummary()
	q := `SELECT count(*), coalesce(sum(Decision='allow'),0), coalesce(sum(Decision!='allow'),0),coalesce(sum(Outcome='upstream_error'),0),coalesce(cast(avg(DurationMs) as integer),0) FROM audit_events` + where
	if err := a.db.QueryRowContext(ctx, q, args...).Scan(&out.Total, &out.Allowed, &out.Denied, &out.UpstreamErrors, &out.AvgDurationMs); err != nil {
		return out, err
	}
	for _, group := range []struct {
		expr, order string
		limit       int
		dest        *[]UsageBucket
	}{{"strftime('%Y-%m-%d',TimestampMs/1000,'unixepoch')", "Key DESC", 14, &out.ByDay}, {"PublicName", "Count DESC, Key ASC", 10, &out.ByTool}} {
		rows, err := a.db.QueryContext(ctx, "SELECT "+group.expr+" AS Key,count(*) AS Count,sum(Decision!='allow'),sum(Outcome='upstream_error') FROM audit_events"+where+" GROUP BY Key ORDER BY "+group.order+fmt.Sprintf(" LIMIT %d", group.limit), args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var b UsageBucket
			if err = rows.Scan(&b.Key, &b.Count, &b.Denied, &b.UpstreamErrors); err != nil {
				rows.Close()
				return out, err
			}
			*group.dest = append(*group.dest, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
func (a *sqliteAudit) pending(ctx context.Context, limit int) ([]AuditEvent, error) {
	rows, err := a.db.QueryContext(ctx, "SELECT Event FROM audit_events ORDER BY TimestampMs,ID LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var raw string
		var e AuditEvent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (a *sqliteAudit) delivered(ctx context.Context, events []AuditEvent) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		if _, err = tx.ExecContext(ctx, "DELETE FROM audit_events WHERE ID=?", e.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (a *sqliteAudit) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		close(a.stop)
		a.mu.Unlock()
		<-a.done
		_, _ = a.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		a.closeErr = errors.Join(a.shutdownErr, a.db.Close())
		a.lease.ExecContext(context.Background(), "ROLLBACK")
		a.lease.Close()
		a.leaseDB.Close()
	})
	return a.closeErr
}
