package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Failed runs: the facts the conversation reads and the wake-up.

const (
	goalLeadRunHealthWindow   = 7 * 24 * time.Hour
	goalLeadRunFailureWindow  = 24 * time.Hour
	goalLeadRunHealthMaxItems = 10
	goalLeadRunErrorRunes     = 300
)

const goalLeadRunFailuresSchema = `CREATE TABLE IF NOT EXISTS goal_lead_run_failures (
	run_id TEXT PRIMARY KEY,
	noticed_at TEXT NOT NULL
)`

type goalLeadFailedRun struct {
	RunID      string `json:"run_id"`
	ScheduleID string `json:"schedule_id,omitempty"`
	RunFolder  string `json:"run_folder,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	FinishedAt string `json:"finished_at"`
}

// goalLeadFailedRuns are the workflow's own runs (not Pulse passes) that
// failed after since, in the store's order (newest first).
func goalLeadFailedRuns(entries []ScheduleRunEntry, since time.Time) []goalLeadFailedRun {
	out := []goalLeadFailedRun{}
	for _, entry := range entries {
		if isPulseScheduleID(entry.ScheduleID) || !isFailedWorkflowRunStatus(entry.Status) {
			continue
		}
		finished := entry.StartedAt
		if entry.CompletedAt != nil {
			finished = *entry.CompletedAt
		}
		if !finished.After(since) {
			continue
		}
		errText := strings.Join(strings.Fields(entry.Error), " ")
		if runes := []rune(errText); len(runes) > goalLeadRunErrorRunes {
			errText = string(runes[:goalLeadRunErrorRunes]) + "…"
		}
		out = append(out, goalLeadFailedRun{RunID: entry.ID, ScheduleID: entry.ScheduleID, RunFolder: entry.RunFolder,
			Status: entry.Status, Error: errText, FinishedAt: formatStoredTime(finished.UTC())})
	}
	return out
}

// goalLeadRunHealth is what Technical Review read after runs, for the
// conversation's goal check: failed runs, steps' CONCERNS: lines, open
// workflow issues and whether each schedule's runs ran the workflow.

// goalLeadRunHealthSince: since the last goal check, at most a week back.

const goalLeadRunHealthNote = "Read failed runs, step concerns and relevant goal evidence. For a failure that threatens the goal, ask Builder for one coherent repair and verified results under the current permissions. Reuse ongoing work and pending decisions; a failure does not create a separate reviewer or repair session."

// claimGoalLeadRunFailures returns the failed runs of the last day that the
// conversation has not been woken for, and marks them noticed.
func claimGoalLeadRunFailures(ctx context.Context, workspacePath string, now time.Time) ([]goalLeadFailedRun, error) {
	entries, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	failed := goalLeadFailedRuns(entries, now.Add(-goalLeadRunFailureWindow))
	if len(failed) == 0 {
		return nil, nil
	}
	_, db, err := openPulseStoreDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, goalLeadRunFailuresSchema); err != nil {
		return nil, err
	}
	claimed := []goalLeadFailedRun{}
	for _, run := range failed {
		res, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO goal_lead_run_failures (run_id, noticed_at) VALUES (?, ?)`, run.RunID, formatStoredTime(now))
		if err != nil {
			return claimed, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			claimed = append(claimed, run)
		}
	}
	return claimed, nil
}

// startGoalLeadBackgroundTurn runs a scheduler-started conversation turn
// (tests run it inline).
var startGoalLeadBackgroundTurn = func(run func()) { go run() }

// wakeGoalLeadOnRunFailures gives newly failed runs one turn in the
// workflow's Pulse conversation. Code decides that a run failed; the turn
// decides whether it threatens the goal and asks Builder for repair. Each failed run wakes
// it once. It reports whether a turn was started.
func (s *SchedulerService) wakeGoalLeadOnRunFailures(ctx context.Context, workspacePath string, now time.Time) bool {
	claimed, err := claimGoalLeadRunFailures(ctx, workspacePath, now)
	if err != nil {
		scheduleLogf("[PULSE] cannot read failed runs for %s: %v", workspacePath, err)
	}
	if len(claimed) == 0 {
		return false
	}
	_ = appendGoalLeadMessage(ctx, workspacePath, GoalLeadMessage{Role: "system", Source: goalLeadTurnRunFailed, Text: goalLeadRunFailureMessage(len(claimed))})
	// The Builder chat it asks to fix the failure works within Pulse's own
	// permissions, as for any other turn.
	turn := goalLeadTurn{Kind: goalLeadTurnRunFailed, Body: goalLeadRunFailureBody(claimed)}
	startGoalLeadBackgroundTurn(func() {
		turnCtx, cancel := context.WithTimeout(context.Background(), goalLeadTurnHardCap)
		defer cancel()
		if _, _, err := s.api.runGoalLeadTurn(turnCtx, workspacePath, turn); err != nil {
			log.Printf("[PULSE] run-failure turn for %s failed: %v", workspacePath, err)
		}
	})
	scheduleLogf("[PULSE] %d failed run(s) of %s sent to its Pulse conversation", len(claimed), workspacePath)
	return true
}

func goalLeadRunFailureBody(runs []goalLeadFailedRun) string {
	var b strings.Builder
	b.WriteString("Failed runs (code-detected; each reaches you once):\n")
	for _, run := range runs {
		fmt.Fprintf(&b, "- run %s (schedule %s, folder %s): %s at %s", run.RunID, firstNonEmptyTrimmed(run.ScheduleID, "-"), firstNonEmptyTrimmed(run.RunFolder, "-"), run.Status, run.FinishedAt)
		if run.Error != "" {
			fmt.Fprintf(&b, ": %s", run.Error)
		}
		b.WriteString("\n")
	}
	b.WriteString(`
Inspect the relevant run evidence with your ordinary tools and decide whether the failure threatens the goal. Ask Builder for a useful repair and actual verification within current permissions, reusing ongoing work. Outward actions follow the workflow's ask-or-allow setting. Keep the actual outcome, blockers and follow-up in this conversation; do not claim that delivery means work finished.`)
	return b.String()
}

// goalLeadRunFailureMessage is the conversation log line for the wake-up.
func goalLeadRunFailureMessage(runs int) string {
	if runs == 1 {
		return "A run failed; Pulse is looking at it."
	}
	return fmt.Sprintf("%d runs failed; Pulse is looking at them.", runs)
}

func isFailedWorkflowRunStatus(status string) bool {
	switch status {
	case "error", "failed", "interrupted":
		return true
	}
	return false
}
