package server

import (
	"context"
	"fmt"
	"strings"
)

// Remove only the retired projection programs. Their saved tables remain history.
// Startup owns this cleanup; reading Pulse history never changes its schema.
func retirePulseProjectionTriggers(ctx context.Context, workspacePath string) (int, error) {
	_, db, err := openPulseStoreDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return 0, err
	}
	defer db.Close()
	known := map[string]bool{
		"pulse_compact_module_state_insert": true, "pulse_compact_module_state_update": true,
		"pulse_compact_module_audit_insert": true, "pulse_compact_module_audit_update": true,
		"pulse_compact_run_concern_insert": true, "pulse_compact_run_concern_update": true,
		"pulse_compact_finding_detail_insert": true, "pulse_compact_finding_detail_update": true,
		"pulse_compact_decision_insert": true, "pulse_compact_decision_update": true,
		"pulse_compact_finding_decision_insert": true,
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='trigger' AND name LIKE 'pulse_compact_%'`)
	if err != nil {
		return 0, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return 0, err
		}
		if known[name] {
			names = append(names, name)
		}
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return 0, scanErr
	}
	if len(names) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
			return 0, fmt.Errorf("retire Pulse projection %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(names), nil
}
