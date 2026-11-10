package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// generateScheduleID creates a new UUID for a schedule entry.
func generateScheduleID() string {
	return uuid.New().String()
}

// ScheduleRunEntry represents a single scheduled job execution record.
// Stored in the service-owned schedule history database, scoped to the project.
type ScheduleRunEntry struct {
	ID         string `json:"id"`
	ScheduleID string `json:"schedule_id"`
	// TriggerSource distinguishes an actual cron/calendar occurrence from a
	// user clicking Run now. Without it the history UI has to guess from the
	// session ID and can make a manual run look as if it fulfilled a missed
	// scheduled slot.
	TriggerSource string              `json:"trigger_source,omitempty"`
	Webhook       *WebhookRunMetadata `json:"webhook,omitempty"`
	// ScheduledFor is the durable identity of the cron/calendar occurrence.
	// It is intentionally nil for manual runs, whose start time is not a
	// scheduled slot.
	ScheduledFor             *time.Time `json:"scheduled_for,omitempty"`
	RunFolder                string     `json:"run_folder,omitempty"`
	ExecutionWorkspacePath   string     `json:"execution_workspace_path,omitempty"`
	ConcurrencyMode          string     `json:"concurrency_mode,omitempty"`
	ParallelRiskAcknowledged bool       `json:"parallel_risk_acknowledged,omitempty"`
	SessionID                string     `json:"session_id,omitempty"`
	Status                   string     `json:"status"` // queued, running, success, error, stopped, partial, interrupted
	Error                    string     `json:"error,omitempty"`
	// FinalResponse is the exact assistant answer produced by this run. Keeping
	// it on the run avoids guessing from a persistent chat that may have received
	// newer interactive or automation turns since this execution completed.
	FinalResponse string     `json:"final_response,omitempty"`
	RanWorkflow   *bool      `json:"ran_workflow,omitempty"`
	DurationMs    *int64     `json:"duration_ms,omitempty"`
	GroupNames    []string   `json:"group_names"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	// Caller stamps the workflow step that invoked this run through a
	// platform-internal trigger. It is nil for every other run, so
	// Crew-side cost history can show what each caller spent.
	Caller *workflowtypes.CrewRunCaller `json:"caller,omitempty"`
	// Usage is the run's token spend summed from the cost ledger when the
	// run completes. It is nil when cost recording is unavailable.
	Usage *workflowtypes.CrewRunTokenUsage `json:"usage,omitempty"`
}

const maxScheduleRuns = 200
const workflowScheduleRunRetentionDays = 90

type scheduleRunDurationAverage struct {
	DurationMs int64
	Samples    int
}

// recentSuccessfulRunAverages uses the latest successful, timed runs for each
// schedule. Missed, running, failed and stopped occurrences do not describe
// the duration of a completed run.
func recentSuccessfulRunAverages(runs []ScheduleRunEntry, sampleLimit int) map[string]scheduleRunDurationAverage {
	result := make(map[string]scheduleRunDurationAverage)
	if sampleLimit <= 0 {
		return result
	}
	ordered := append([]ScheduleRunEntry(nil), runs...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].StartedAt.After(ordered[j].StartedAt)
	})
	for _, run := range ordered {
		if run.ScheduleID == "" || run.TriggerSource == "manual" || run.Status != "success" || run.DurationMs == nil || *run.DurationMs <= 0 {
			continue
		}
		average := result[run.ScheduleID]
		if average.Samples >= sampleLimit {
			continue
		}
		average.DurationMs += *run.DurationMs
		average.Samples++
		result[run.ScheduleID] = average
	}
	for id, average := range result {
		average.DurationMs /= int64(average.Samples)
		result[id] = average
	}
	return result
}

// Other scheduler resources (folder allocation, Slack bindings and webhook
// delivery receipts) still use this process lock. Run history uses SQLite.
var scheduleRunFileLocks sync.Map

func scheduleRunFileLock(path string) *sync.Mutex {
	lock, _ := scheduleRunFileLocks.LoadOrStore(path, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// scheduleRunsPath identifies the old import source and the panel change topic.
// New history is never written to this workspace path.
func scheduleRunsPath(workspacePath string) string { return workspacePath + "/schedule-runs.json" }

// ReadScheduleRuns is the explicit complete-history API. Runtime polling uses
// readScheduleRunStates so it does not decode stored responses or cost receipts.
func ReadScheduleRuns(ctx context.Context, workspacePath string) ([]ScheduleRunEntry, error) {
	h, err := openScheduleHistory(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	defer h.db.Close()
	rows, err := h.db.QueryContext(ctx, `SELECT payload FROM history_entries WHERE scope=? ORDER BY serial DESC`, h.scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanScheduleRunPayloads(rows)
}

func scanScheduleRunPayloads(rows *sql.Rows) ([]ScheduleRunEntry, error) {
	out := []ScheduleRunEntry{}
	for rows.Next() {
		var payload string
		var run ScheduleRunEntry
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &run); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// WriteScheduleRuns atomically replaces a scope. Online reconciliation updates
// individual rows instead, so another run's completion cannot be overwritten.
func WriteScheduleRuns(ctx context.Context, workspacePath string, runs []ScheduleRunEntry) error {
	return withScheduleHistoryWrite(ctx, workspacePath, func(h *scheduleHistory, tx *sql.Tx) (bool, error) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM history_entries WHERE scope=?`, h.scope); err != nil {
			return false, err
		}
		for i := len(runs) - 1; i >= 0; i-- {
			if err := h.insert(ctx, tx, runs[i]); err != nil {
				return false, err
			}
		}
		return true, nil
	})
}

func AppendScheduleRun(ctx context.Context, workspacePath string, run *ScheduleRunEntry) error {
	if run == nil {
		return fmt.Errorf("schedule run is required")
	}
	return withScheduleHistoryWrite(ctx, workspacePath, func(h *scheduleHistory, tx *sql.Tx) (bool, error) {
		err := h.insert(ctx, tx, *run)
		return err == nil, err
	})
}

// ClaimScheduleRun serializes the ID lookup and insertion in a SQLite writer
// transaction, including across service processes and logical path aliases.
func ClaimScheduleRun(ctx context.Context, workspacePath string, run *ScheduleRunEntry) (existing *ScheduleRunEntry, claimed bool, err error) {
	if run == nil {
		return nil, false, fmt.Errorf("schedule run is required")
	}
	if strings.TrimSpace(run.ID) == "" {
		return nil, false, fmt.Errorf("schedule run id is required")
	}
	err = withScheduleHistoryWrite(ctx, workspacePath, func(h *scheduleHistory, tx *sql.Tx) (bool, error) {
		_, found, e := h.find(ctx, tx, run.ID)
		if e == nil {
			existing = found
			return false, nil
		}
		if e != sql.ErrNoRows {
			return false, e
		}
		e = h.insert(ctx, tx, *run)
		claimed = e == nil
		return claimed, e
	})
	if err != nil {
		claimed = false
	}
	return
}

func FindScheduleRun(ctx context.Context, workspacePath, runID string) (*ScheduleRunEntry, error) {
	h, err := openScheduleHistory(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	defer h.db.Close()
	var payload string
	if err = h.db.QueryRowContext(ctx, `SELECT payload FROM history_entries WHERE scope=? AND run_id=? ORDER BY serial DESC LIMIT 1`, h.scope, runID).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("schedule run %q not found", runID)
		}
		return nil, err
	}
	var run ScheduleRunEntry
	if err = json.Unmarshal([]byte(payload), &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func UpdateScheduleRun(ctx context.Context, workspacePath, runID, status, errMsg string, durationMs *int64, runFolder, sessionID string) error {
	return updateScheduleHistoryRun(ctx, workspacePath, runID, false, func(run *ScheduleRunEntry) {
		run.Status, run.Error, run.DurationMs = status, errMsg, durationMs
		if runFolder != "" {
			run.RunFolder = runFolder
		}
		if sessionID != "" {
			run.SessionID = sessionID
		}
		if isTerminalScheduleRunStatus(status) {
			now := time.Now().UTC()
			run.CompletedAt = &now
		}
	})
}

// ScheduleRunCompletion records the whole outcome in one row transaction.
type ScheduleRunCompletion struct {
	Status        string
	Error         string
	DurationMs    *int64
	SessionID     string
	FinalResponse string
	Usage         *workflowtypes.CrewRunTokenUsage
}

func UpdateScheduleRunResult(ctx context.Context, workspacePath, runID string, completion ScheduleRunCompletion) error {
	return updateScheduleHistoryRun(ctx, workspacePath, runID, false, func(run *ScheduleRunEntry) {
		run.Status, run.Error, run.DurationMs = completion.Status, completion.Error, completion.DurationMs
		if completion.SessionID != "" {
			run.SessionID = completion.SessionID
		}
		if completion.FinalResponse != "" {
			run.FinalResponse = completion.FinalResponse
		}
		if completion.Usage != nil && !completion.Usage.Empty() {
			run.Usage = completion.Usage
		}
		if isTerminalScheduleRunStatus(completion.Status) {
			now := time.Now().UTC()
			run.CompletedAt = &now
		}
	})
}

func UpdateScheduleRunTokenUsage(ctx context.Context, workspacePath, runID string, usage *workflowtypes.CrewRunTokenUsage) error {
	return updateScheduleHistoryRun(ctx, workspacePath, runID, false, func(run *ScheduleRunEntry) { run.Usage = usage })
}
func UpdateScheduleRunFinalResponse(ctx context.Context, workspacePath, runID, finalResponse string) error {
	return updateScheduleHistoryRun(ctx, workspacePath, runID, false, func(run *ScheduleRunEntry) { run.FinalResponse = strings.TrimSpace(finalResponse) })
}
func isTerminalScheduleRunStatus(status string) bool {
	switch status {
	case "success", "error", "stopped", "partial", "failed", "interrupted":
		return true
	default:
		return false
	}
}

// Explicit user cleanup is separate from automatic retention and may remove
// recent terminal runs. Active and undated records are always preserved.
func DeleteScheduleRunsOlderThan(ctx context.Context, workspacePath string, scheduleIDs []string, olderThanDays int) (int, error) {
	if olderThanDays <= 0 {
		olderThanDays = 30
	}
	deleted := 0
	err := withScheduleHistoryWrite(ctx, workspacePath, func(h *scheduleHistory, tx *sql.Tx) (bool, error) {
		query := `DELETE FROM history_entries WHERE scope=? AND ` + scheduleHistoryTerminal + ` AND started_ns IS NOT NULL AND started_ns<=?`
		args := []any{h.scope, time.Now().UTC().AddDate(0, 0, -olderThanDays).UnixNano()}
		ids := []string{}
		for _, id := range scheduleIDs {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			query += ` AND schedule_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
			for _, id := range ids {
				args = append(args, id)
			}
		}
		result, e := tx.ExecContext(ctx, query, args...)
		if e != nil {
			return false, e
		}
		count, e := result.RowsAffected()
		deleted = int(count)
		return deleted > 0, e
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// ListScheduleRuns selects only the requested page, newest start time first.
func ListScheduleRuns(ctx context.Context, workspacePath, scheduleID string, limit, offset int) ([]ScheduleRunEntry, int, error) {
	if limit < 0 || offset < 0 {
		return nil, 0, fmt.Errorf("run history limit and offset must not be negative")
	}
	h, err := openScheduleHistory(ctx, workspacePath)
	if err != nil {
		return nil, 0, err
	}
	defer h.db.Close()
	// Count and page share one read snapshot.
	tx, err := h.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	total := 0
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM history_entries WHERE scope=? AND schedule_id=?`, h.scope, scheduleID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM history_entries WHERE scope=? AND schedule_id=? ORDER BY started_ns DESC,serial DESC LIMIT ? OFFSET ?`, h.scope, scheduleID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	runs, err := scanScheduleRunPayloads(rows)
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}

func SetScheduleRunRanWorkflow(ctx context.Context, workspacePath, runID string, ran bool) error {
	return updateScheduleHistoryRun(ctx, workspacePath, runID, true, func(run *ScheduleRunEntry) { value := ran; run.RanWorkflow = &value })
}
