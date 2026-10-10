package server

import (
	"context"
	"database/sql"
)

// sqliteProbeDB is the subset of *sql.DB / *sql.Tx the write guards need.
type sqliteProbeDB interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

// sqliteRowsExist reports whether probe (a SELECT) returns at least one row.
//
// Every ensure*/migrate* helper on the workflow db.sqlite path is reached from
// pure reads (get_pulse_state, the scheduler's once-a-minute fast-Pulse poll,
// getPulseRunMode) as well as from writes. In WAL mode an UPDATE, DELETE or
// INSERT..SELECT that matches zero rows still takes SQLite's single write lock,
// so an idempotent "already migrated" statement turned every one of those reads
// into a writer. That is what made get_pulse_state(view=module) fail with
// SQLITE_BUSY on customer-login (2026-08-31, PUL-7774A6D0): the view is opened
// five times per call, each open ran ~8 no-op write transactions, and any real
// writer holding the lock for longer than busy_timeout failed the whole Gate.
// Probing with a SELECT first keeps the read paths off the write lock entirely;
// CREATE/DROP ... IF [NOT] EXISTS and PRAGMA table_info never need it.
func sqliteRowsExist(ctx context.Context, db sqliteProbeDB, probe string, args ...interface{}) (bool, error) {
	var found int
	if err := db.QueryRowContext(ctx, "SELECT EXISTS("+probe+")", args...).Scan(&found); err != nil {
		return false, err
	}
	return found == 1, nil
}

// sqliteExecIfRows runs stmt only when probe finds a row; see sqliteRowsExist.
func sqliteExecIfRows(ctx context.Context, db sqliteProbeDB, probe string, probeArgs []interface{}, stmt string, stmtArgs ...interface{}) error {
	found, err := sqliteRowsExist(ctx, db, probe, probeArgs...)
	if err != nil || !found {
		return err
	}
	_, err = db.ExecContext(ctx, stmt, stmtArgs...)
	return err
}
