package schedulerstate

import (
	"context"
	"fmt"
	"time"
)

// RelayDBOSBinding contains immutable, non-secret invocation inputs. Attempts
// and the original deadline live in the platform ledger, outside authored code.
type RelayDBOSBinding struct {
	RunID, OwnerID, ReleaseHash, InputJSON, VariablesJSON string
	Deadline                                              time.Time
	Attempt                                               int
}

func (s *Store) ReserveRelayDBOSAttempt(ctx context.Context, binding RelayDBOSBinding) (RelayDBOSBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return binding, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO relay_dbos_runs (run_id, owner_id, release_hash, input_json, variables_json, deadline, attempts) VALUES (?, ?, ?, ?, ?, ?, 0) ON CONFLICT(run_id) DO NOTHING`, binding.RunID, binding.OwnerID, binding.ReleaseHash, binding.InputJSON, binding.VariablesJSON, formatTime(binding.Deadline))
	if err != nil {
		return binding, err
	}
	var saved RelayDBOSBinding
	var deadline string
	err = tx.QueryRowContext(ctx, `SELECT owner_id, release_hash, input_json, variables_json, deadline, attempts FROM relay_dbos_runs WHERE run_id = ?`, binding.RunID).Scan(&saved.OwnerID, &saved.ReleaseHash, &saved.InputJSON, &saved.VariablesJSON, &deadline, &saved.Attempt)
	if err != nil {
		return binding, err
	}
	if saved.OwnerID != binding.OwnerID || saved.ReleaseHash != binding.ReleaseHash || saved.InputJSON != binding.InputJSON || saved.VariablesJSON != binding.VariablesJSON {
		return binding, fmt.Errorf("DBOS invocation binding changed; recovery refused")
	}
	binding.Deadline, err = parseTime(deadline)
	if err != nil {
		return binding, err
	}
	if !time.Now().Before(binding.Deadline) {
		return binding, fmt.Errorf("DBOS invocation deadline exceeded")
	}
	if saved.Attempt >= 3 {
		return binding, fmt.Errorf("DBOS invocation exhausted its three process attempts")
	}
	binding.Attempt = saved.Attempt + 1
	if _, err = tx.ExecContext(ctx, `UPDATE relay_dbos_runs SET attempts = ? WHERE run_id = ?`, binding.Attempt, binding.RunID); err != nil {
		return binding, err
	}
	return binding, tx.Commit()
}

// InterruptedDBOSRuns excludes stopped/failed runs and invocations that never
// opted into DBOS. Only a process restart can make these recovery candidates.
func (s *Store) InterruptedDBOSRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.run_id FROM schedule_runs s JOIN relay_dbos_runs d ON d.run_id = s.run_id WHERE s.state = ? AND s.error_message = 'interrupted: server restarted'`, StateInterrupted)
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
		runs = append(runs, run)
	}
	return runs, nil
}

// ResumeInterruptedDBOSRun is an explicit CAS, not a generic permission to
// reopen terminal runs. The scheduler invokes it once after restart admission.
func (s *Store) ResumeInterruptedDBOSRun(ctx context.Context, id string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE schedule_runs SET state = ?, completed_at = NULL, updated_at = ?, active_session_id = '', error_message = '' WHERE run_id = ? AND state = ? AND error_message = 'interrupted: server restarted' AND EXISTS (SELECT 1 FROM relay_dbos_runs WHERE run_id = ?)`, StateStarting, formatTime(at), id, StateInterrupted, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: DBOS recovery already claimed or unavailable", ErrRunAlreadyActive)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO schedule_run_events (run_id, from_state, to_state, reason, created_at) VALUES (?, ?, ?, ?, ?)`, id, StateInterrupted, StateStarting, "DBOS recovery after server restart", formatTime(at)); err != nil {
		return err
	}
	return tx.Commit()
}
