package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

// TestPulseStateReadsDoNotTakeWriteLock reproduces PUL-7774A6D0 (customer-login,
// 2026-08-31): get_pulse_state(view=module) and the scheduler's fast-Pulse poll
// returned SQLITE_BUSY for ten minutes while view=backlog and query_workflow_db
// succeeded against the same file. The reads were not reads -- every open ran
// the schema ensure/migration helpers, whose no-op UPDATE/INSERT..SELECT
// statements still take SQLite's write lock in WAL mode. Holding a write
// transaction on a second connection must therefore not block any of them.
func TestPulseStateReadsDoNotTakeWriteLock(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ctx := context.Background()
	const workspacePath = "Workflow/example"

	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		t.Fatalf("create module state db: %v", err)
	}
	if err := ensurePulseFastRequestSchema(ctx, db); err != nil {
		t.Fatalf("ensure fast request schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	writer, err := sql.Open("sqlite", sqliteopen.DSN(filepath.Join(root, "Workflow", "example", "db", "db.sqlite")))
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()
	conn, err := writer.Conn(ctx)
	if err != nil {
		t.Fatalf("writer conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold write lock: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") }()

	// Well under sqliteopen.DSN's 5s busy_timeout: a read that needs the write
	// lock cannot finish in time, a genuine read finishes in milliseconds.
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for name, read := range map[string]func() error{
		"getPulseRunMode": func() error {
			_, err := getPulseRunMode(readCtx, workspacePath, "pulse-run-1")
			return err
		},
		"getLatestPulseRunMode": func() error {
			_, err := getLatestPulseRunMode(readCtx, workspacePath)
			return err
		},
		"pendingFastPulseRequest": func() error {
			_, err := pendingFastPulseRequest(readCtx, workspacePath)
			return err
		},
		"openPulseModuleStateDB(create=false)": func() error {
			_, db, err := openPulseModuleStateDB(readCtx, workspacePath, false)
			if err != nil {
				return err
			}
			return db.Close()
		},
	} {
		started := time.Now()
		if err := read(); err != nil {
			t.Fatalf("%s while another connection holds the write lock: %v (after %s)", name, err, time.Since(started))
		}
	}
}
