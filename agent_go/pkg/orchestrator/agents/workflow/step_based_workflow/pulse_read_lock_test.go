package step_based_workflow

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

// TestPulseLifecycleReadsDoNotTakeWriteLock is the lifecycle-package half of
// PUL-7774A6D0 (customer-login, 2026-08-31): every open of db.sqlite runs the
// lifecycle and review-log schema ensures, whose idempotent migration
// UPDATE/INSERT..SELECT statements take SQLite's write lock even when they
// match nothing. Holding a write transaction elsewhere must not block the
// backlog, suppressed-concern, or review-history reads behind get_pulse_state.
func TestPulseLifecycleReadsDoNotTakeWriteLock(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	const workspacePath = "Workflow/example"

	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil {
		t.Fatalf("create lifecycle db: %v", err)
	}
	if err := ensurePulseFindingLifecycleSchema(ctx, db); err != nil {
		t.Fatalf("ensure lifecycle schema: %v", err)
	}
	if err := ensurePulseReviewLogSchema(ctx, db); err != nil {
		t.Fatalf("ensure review log schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	writer, err := sql.Open("sqlite", sqliteopen.DSN(runConcernsDBPath(workspacePath)))
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

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for name, read := range map[string]func() error{
		"LoadPulseFindingLifecycles": func() error {
			_, err := LoadPulseFindingLifecycles(readCtx, workspacePath, "", -1)
			return err
		},
		"LoadExternallyOwnedRunConcerns": func() error {
			_, err := LoadExternallyOwnedRunConcerns(readCtx, workspacePath)
			return err
		},
		"LoadModuleReviewHistory": func() error {
			_, err := LoadModuleReviewHistory(readCtx, workspacePath, 3)
			return err
		},
	} {
		started := time.Now()
		if err := read(); err != nil {
			t.Fatalf("%s while another connection holds the write lock: %v (after %s)", name, err, time.Since(started))
		}
	}
}
