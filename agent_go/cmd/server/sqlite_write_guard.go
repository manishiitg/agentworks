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
