package server

import (
	"context"
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
// Stored in <workspace>/schedule-runs.json.
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

var scheduleRunFileLocks sync.Map

func scheduleRunFileLock(path string) *sync.Mutex {
	lock, _ := scheduleRunFileLocks.LoadOrStore(path, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func scheduleRunsPath(workspacePath string) string {
	return workspacePath + "/schedule-runs.json"
}

// ReadScheduleRuns reads all run entries from <workspace>/schedule-runs.json.
func ReadScheduleRuns(ctx context.Context, workspacePath string) ([]ScheduleRunEntry, error) {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()
	return readScheduleRunsUnlocked(ctx, workspacePath)
}

func readScheduleRunsUnlocked(ctx context.Context, workspacePath string) ([]ScheduleRunEntry, error) {
	content, exists, err := readFileFromWorkspace(ctx, scheduleRunsPath(workspacePath))
	if err != nil {
		return nil, fmt.Errorf("failed to read schedule-runs.json: %w", err)
	}
	if !exists {
		return []ScheduleRunEntry{}, nil
	}

	var runs []ScheduleRunEntry
	if err := json.Unmarshal([]byte(content), &runs); err != nil {
		return nil, fmt.Errorf("failed to parse schedule-runs.json: %w", err)
	}
	return runs, nil
}

// WriteScheduleRuns writes run entries to <workspace>/schedule-runs.json.
func WriteScheduleRuns(ctx context.Context, workspacePath string, runs []ScheduleRunEntry) error {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()
	return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
}

func writeScheduleRunsUnlocked(ctx context.Context, workspacePath string, runs []ScheduleRunEntry) error {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal schedule runs: %w", err)
	}
	return writeFileToWorkspace(ctx, scheduleRunsPath(workspacePath), string(data))
}

// retainedScheduleRuns keeps workflow history for 90 days so the existing
// paginated readers can reach older runs. Other schedule stores keep their
// historical 200-entry cap. Active and undated runs are never pruned here.
func retainedScheduleRuns(workspacePath string, runs []ScheduleRunEntry, now time.Time) []ScheduleRunEntry {
	if strings.HasPrefix(workspacePath, "Workflow/") {
		cutoff := now.AddDate(0, 0, -workflowScheduleRunRetentionDays)
		kept := runs[:0]
		for _, run := range runs {
			if run.StartedAt.IsZero() || !isTerminalScheduleRunStatus(run.Status) || !run.StartedAt.Before(cutoff) {
				kept = append(kept, run)
			}
		}
		return kept
	}
	if len(runs) > maxScheduleRuns {
		return runs[:maxScheduleRuns]
	}
	return runs
}

// AppendScheduleRun adds a run entry and applies the retention policy.
func AppendScheduleRun(ctx context.Context, workspacePath string, run *ScheduleRunEntry) error {
	if run == nil {
		return fmt.Errorf("schedule run is required")
	}
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return err
	}

	runs = retainedScheduleRuns(workspacePath, append([]ScheduleRunEntry{*run}, runs...), time.Now().UTC()) // prepend (newest first)

	return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
}

// ClaimScheduleRun inserts a run entry only when no entry with the same ID
// exists yet, atomically under the run-file lock. It reports the existing
// entry when the ID is already claimed, so redeliveries reattach to the live
// run instead of forking history.
func ClaimScheduleRun(ctx context.Context, workspacePath string, run *ScheduleRunEntry) (existing *ScheduleRunEntry, claimed bool, err error) {
	if run == nil {
		return nil, false, fmt.Errorf("schedule run is required")
	}
	if strings.TrimSpace(run.ID) == "" {
		return nil, false, fmt.Errorf("schedule run id is required")
	}
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return nil, false, err
	}
	for i := range runs {
		if runs[i].ID == run.ID {
			found := runs[i]
			return &found, false, nil
		}
	}
	runs = retainedScheduleRuns(workspacePath, append([]ScheduleRunEntry{*run}, runs...), time.Now().UTC())
	if err := writeScheduleRunsUnlocked(ctx, workspacePath, runs); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

// FindScheduleRun returns one run entry by ID.
func FindScheduleRun(ctx context.Context, workspacePath, runID string) (*ScheduleRunEntry, error) {
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if runs[i].ID == runID {
			found := runs[i]
			return &found, nil
		}
	}
	return nil, fmt.Errorf("schedule run %q not found in %s", runID, scheduleRunsPath(workspacePath))
}

// UpdateScheduleRun finds a run by ID and updates its fields.
func UpdateScheduleRun(ctx context.Context, workspacePath string, runID string, status string, errMsg string, durationMs *int64, runFolder string, sessionID string) error {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return err
	}

	for i := range runs {
		if runs[i].ID == runID {
			runs[i].Status = status
			runs[i].Error = errMsg
			runs[i].DurationMs = durationMs
			if runFolder != "" {
				runs[i].RunFolder = runFolder
			}
			if sessionID != "" {
				runs[i].SessionID = sessionID
			}
			if isTerminalScheduleRunStatus(status) {
				now := time.Now().UTC()
				runs[i].CompletedAt = &now
			}
			return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
		}
	}

	return fmt.Errorf("schedule run %q not found in %s", runID, path)
}

// ScheduleRunCompletion carries everything recorded when an automation run
// finishes: terminal status, error, duration, session, final response, and
// token usage.
type ScheduleRunCompletion struct {
	Status        string
	Error         string
	DurationMs    *int64
	SessionID     string
	FinalResponse string
	Usage         *workflowtypes.CrewRunTokenUsage
}

// UpdateScheduleRunResult records one run's completion in a single
// read-modify-write cycle. Status, final response, and usage must land
// together: separate writes let a poller observe terminal success with an
// empty response and no cost data. Empty response and usage leave any
// previously stored values untouched.
func UpdateScheduleRunResult(ctx context.Context, workspacePath, runID string, completion ScheduleRunCompletion) error {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return err
	}
	for i := range runs {
		if runs[i].ID == runID {
			runs[i].Status = completion.Status
			runs[i].Error = completion.Error
			runs[i].DurationMs = completion.DurationMs
			if completion.SessionID != "" {
				runs[i].SessionID = completion.SessionID
			}
			if completion.FinalResponse != "" {
				runs[i].FinalResponse = completion.FinalResponse
			}
			if completion.Usage != nil && !completion.Usage.Empty() {
				runs[i].Usage = completion.Usage
			}
			if isTerminalScheduleRunStatus(completion.Status) {
				now := time.Now().UTC()
				runs[i].CompletedAt = &now
			}
			return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
		}
	}
	return fmt.Errorf("schedule run %q not found in %s", runID, path)
}

// UpdateScheduleRunTokenUsage durably associates one execution's token spend
// with its history entry. It overwrites any previous value; callers
// accumulate multi-turn runs before writing.
func UpdateScheduleRunTokenUsage(ctx context.Context, workspacePath, runID string, usage *workflowtypes.CrewRunTokenUsage) error {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return err
	}
	for i := range runs {
		if runs[i].ID == runID {
			runs[i].Usage = usage
			return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
		}
	}
	return fmt.Errorf("schedule run %q not found in %s", runID, path)
}

// UpdateScheduleRunFinalResponse durably associates one execution's exact
// assistant answer with its history entry.
func UpdateScheduleRunFinalResponse(ctx context.Context, workspacePath, runID, finalResponse string) error {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return err
	}
	for i := range runs {
		if runs[i].ID == runID {
			runs[i].FinalResponse = strings.TrimSpace(finalResponse)
			return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
		}
	}
	return fmt.Errorf("schedule run %q not found in %s", runID, path)
}

func isTerminalScheduleRunStatus(status string) bool {
	switch status {
	case "success", "error", "stopped", "partial", "failed", "interrupted":
		return true
	default:
		return false
	}
}

// DeleteScheduleRunsOlderThan drops terminal run entries started more than
// olderThanDays ago, restricted to scheduleIDs when non-empty. Running and
// queued entries are never deleted, and neither are run folders or
// conversation transcripts — chat-history cleanup owns those. Entries with
// no start time are kept: unknown age must not read as old. It returns the
// number of deleted entries.
func DeleteScheduleRunsOlderThan(ctx context.Context, workspacePath string, scheduleIDs []string, olderThanDays int) (int, error) {
	if olderThanDays <= 0 {
		olderThanDays = 30
	}
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()

	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().AddDate(0, 0, -olderThanDays)
	only := make(map[string]bool, len(scheduleIDs))
	for _, id := range scheduleIDs {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}
	kept := []ScheduleRunEntry{}
	deleted := 0
	for _, run := range runs {
		if len(only) > 0 && !only[run.ScheduleID] {
			kept = append(kept, run)
			continue
		}
		if run.StartedAt.IsZero() || !isTerminalScheduleRunStatus(run.Status) || run.StartedAt.After(cutoff) {
			kept = append(kept, run)
			continue
		}
		deleted++
	}
	if deleted == 0 {
		return 0, nil
	}
	if err := writeScheduleRunsUnlocked(ctx, workspacePath, kept); err != nil {
		return 0, err
	}
	return deleted, nil
}

// ListScheduleRuns returns runs for a specific schedule ID with pagination.
// Runs are returned newest-first.
func ListScheduleRuns(ctx context.Context, workspacePath string, scheduleID string, limit, offset int) ([]ScheduleRunEntry, int, error) {
	allRuns, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return nil, 0, err
	}

	// Filter by schedule ID
	var filtered []ScheduleRunEntry
	for _, r := range allRuns {
		if r.ScheduleID == scheduleID {
			filtered = append(filtered, r)
		}
	}

	// Sort newest first (should already be, but ensure)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].StartedAt.After(filtered[j].StartedAt)
	})

	total := len(filtered)

	// Pagination
	if offset >= total {
		return []ScheduleRunEntry{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

// SetScheduleRunRanWorkflow records whether an invocation actually ran the
// workflow. Missing runs are ignored: the history write is best-effort.
func SetScheduleRunRanWorkflow(ctx context.Context, workspacePath, runID string, ran bool) error {
	path := scheduleRunsPath(workspacePath)
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()
	runs, err := readScheduleRunsUnlocked(ctx, workspacePath)
	if err != nil {
		return err
	}
	for i := range runs {
		if runs[i].ID == runID {
			value := ran
			runs[i].RanWorkflow = &value
			return writeScheduleRunsUnlocked(ctx, workspacePath, runs)
		}
	}
	return nil
}
