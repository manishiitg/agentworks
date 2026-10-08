package schedulerstate

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const LeaseDuration = 90 * time.Second

// RenewLease updates only the exact active run. Terminal runs and old owners
// cannot revive a lock. The unique active-lock index remains the authority:
// expiry signals recovery, never permission to overlap an unverified worker.
func (s *Store) RenewLease(ctx context.Context, runID string, now time.Time) error {
	r, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET updated_at=? WHERE run_id=?
		AND state IN ('starting','workflow_running','workflow_finished','pulse_gate','pulse_modules','pulse_finalizing')`, formatTime(now), runID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return ErrRunNotFound
	}
	return err
}

// ActiveRunForScope uses the durable lock, including the starting and Pulse
// phases. Historical unfinished UI projections do not participate. Runs of the
// schedules in skipScheduleIDs are ignored (Pulse turns, which only read and
// must not block the work they ask for).
func (s *Store) ActiveRunForScope(ctx context.Context, scopeType, scopeID string, skipScheduleIDs ...string) (*Run, error) {
	var id string
	query := `SELECT run_id FROM schedule_runs WHERE scope_type=? AND scope_id=?
		AND state IN ('starting','workflow_running','workflow_finished','pulse_gate','pulse_modules','pulse_finalizing')`
	args := []interface{}{scopeType, scopeID}
	for _, skip := range skipScheduleIDs {
		query += ` AND schedule_id<>?`
		args = append(args, skip)
	}
	err := s.db.QueryRowContext(ctx, query+` LIMIT 1`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	return &run, nil
}
