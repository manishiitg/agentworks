package step_based_workflow

import "context"

// pulseRowsExist reports whether probe (a SELECT) returns at least one row.
//
// ensurePulseFindingLifecycleSchema and ensurePulseReviewLogSchema run on every
// open of the workflow's db.sqlite, including the pure reads behind
// get_pulse_state (view=module and view=backlog) and the Pulse Gate. In WAL
// mode an UPDATE, DELETE or INSERT..SELECT that matches zero rows still takes
// SQLite's single write lock, so every idempotent "already migrated" statement
// made those reads contend with real writers and fail with SQLITE_BUSY once a
// writer held the lock past busy_timeout (customer-login, 2026-08-31). Probing
// with a SELECT first keeps read paths off the write lock; CREATE/DROP ... IF
// [NOT] EXISTS and PRAGMA table_info never need it.
func pulseRowsExist(ctx context.Context, db pulseFindingLifecycleDB, probe string, args ...interface{}) (bool, error) {
	var found int
	if err := db.QueryRowContext(ctx, "SELECT EXISTS("+probe+")", args...).Scan(&found); err != nil {
		return false, err
	}
	return found == 1, nil
}

// pulseExecIfRows runs stmt only when probe finds a row; see pulseRowsExist.
func pulseExecIfRows(ctx context.Context, db pulseFindingLifecycleDB, probe string, probeArgs []interface{}, stmt string, stmtArgs ...interface{}) error {
	found, err := pulseRowsExist(ctx, db, probe, probeArgs...)
	if err != nil || !found {
		return err
	}
	_, err = db.ExecContext(ctx, stmt, stmtArgs...)
	return err
}
