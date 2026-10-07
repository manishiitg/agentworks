package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
)

// QA and Architecture owned by the workflow's Pulse conversation (PLAT-697,
// design "QA and Architecture owned by Pulse"; the code keeps the goal_lead
// names, users see "Pulse").
//
// For a workflow with a goal (workflowHasGoal), the full Pulse pass runs Gate,
// Goal Work in the persistent Pulse conversation, and Finalize. It runs no
// Architecture or Technical turn of its own:
//   - Architecture is the conversation's skill (goal-lead-architecture.md);
//   - QA (Technical Review+Fix) runs only when the conversation asks for it
//     (record_pulse_qa_request -> a fix run, result back into it);
//   - the safety net Technical gave after runs is kept by code: a failed run
//     wakes the conversation for one short turn (at most once per failed run),
//     and its goal check reads failed runs, CONCERNS: lines and open issues.
// Workflows without a goal keep the full pass and the automatic fix runs.

// goalLeadOwnedModules are the reviewers a goal workflow's pass does not run.
var goalLeadOwnedModules = []string{pulseModuleArchitectureReview, pulseModuleTechnicalReview}

const goalLeadOwnsReviewsReason = "This workflow has a goal: its Pulse conversation owns QA (it asks for a QA run) and architecture (its skill); the full pass runs no separate turn."

const goalLeadOwnsReviewsEvidence = "pulse:goal_lead_owns_reviews"

// goalLeadGateNote tells a goal workflow's Gate what not to select.
const goalLeadGateNote = "\n\nTHIS WORKFLOW HAS A GOAL. Its Pulse conversation owns QA and architecture: record architecture_review and technical_review as not due (reason: owned by the Pulse conversation, cooldown_runs 1). Decide only whether Goal Work (strategic_review) is due."

// keepGoalLeadReviewsOutOfPulse records Architecture and Technical not due in
// a goal workflow's Gate worklist, whatever Gate decided.
func keepGoalLeadReviewsOutOfPulse(decisions []PulseWorklistDecision) []PulseWorklistDecision {
	out := append([]PulseWorklistDecision(nil), decisions...)
	for index := range out {
		module := normalizePulseModule(out[index].Module)
		if module != pulseModuleArchitectureReview && module != pulseModuleTechnicalReview {
			continue
		}
		out[index].Due = false
		out[index].Reason = goalLeadOwnsReviewsReason
		out[index].Evidence = []string{goalLeadOwnsReviewsEvidence}
		out[index].NextCheckAt = ""
		out[index].NextCheckAfterRunID = ""
		out[index].DeferReason = ""
		out[index].CooldownRuns = 1
	}
	return out
}

// closeGoalLeadOwnedModules gives a terminal "skipped" result to an
// Architecture or Technical row that a backend rule (a pending recovery, the
// prompt budget, a protected boundary) made due in a goal workflow's pass, so
// it is not left pending and its recovery is cleared.
func closeGoalLeadOwnedModules(ctx context.Context, workspacePath, pulseRunID string) error {
	worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil || !ok {
		return err
	}
	for _, module := range goalLeadOwnedModules {
		state, exists := worklist[module]
		if !exists || !strings.EqualFold(strings.TrimSpace(state.LastDecision), "due") || strings.TrimSpace(state.LastResult) != "" {
			continue
		}
		if _, err := markPulseModuleResult(ctx, workspacePath, module, pulseRunID, "skipped", goalLeadOwnsReviewsReason, []string{goalLeadOwnsReviewsEvidence}); err != nil {
			return err
		}
	}
	return nil
}

// pulsePassModuleSteps are a full pass's module turns after Gate, in
// pulsemodules.PassOrder: a goal workflow gets only Goal Work, run in its
// Pulse conversation.
func pulsePassModuleSteps(pulseRunID string, hasGoal bool, due func(module string) bool) []pulseLifecycleStep {
	var steps []pulseLifecycleStep
	for _, module := range pulsemodules.PassOrder(hasGoal) {
		if !due(module) {
			continue
		}
		step := pulseLifecycleModuleReviewStep(pulseRunID, module)
		if module == pulseModuleStrategicReview && hasGoal {
			step = goalLeadGoalWorkStep(pulseRunID)
			step.goalLead = true
		}
		steps = append(steps, step)
	}
	return steps
}

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
		if isPulseScheduleID(entry.ScheduleID) || !isPulseFixFailedRunStatus(entry.Status) {
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
func goalLeadRunHealth(ctx context.Context, workspacePath string, since time.Time) map[string]interface{} {
	failed := []goalLeadFailedRun{}
	if entries, err := ReadScheduleRuns(ctx, workspacePath); err == nil {
		failed = goalLeadFailedRuns(entries, since)
	}
	if len(failed) > goalLeadRunHealthMaxItems {
		failed = failed[:goalLeadRunHealthMaxItems]
	}
	concerns := collectStepConcerns(workspacePath, since)
	shownConcerns := concerns.Concerns
	if len(shownConcerns) > goalLeadRunHealthMaxItems {
		shownConcerns = shownConcerns[:goalLeadRunHealthMaxItems]
	}
	openIssues := 0
	if issues, err := stepworkflow.ListPulseActionableWorkflowIssues(ctx, workspacePath); err == nil {
		openIssues = len(issues)
	}
	return map[string]interface{}{
		"since":               formatStoredTime(since.UTC()),
		"failed_runs":         failed,
		"step_concerns":       shownConcerns,
		"step_concern_count":  concerns.Total,
		"open_issue_count":    openIssues,
		"schedule_run_health": scheduleRunHealthForView(ctx, workspacePath),
	}
}

// goalLeadRunHealthSince: since the last goal check, at most a week back.
func goalLeadRunHealthSince(ctx context.Context, workspacePath string, now time.Time) time.Time {
	since := now.Add(-goalLeadRunHealthWindow)
	if latest, _, err := latestPulseGoalCheck(ctx, workspacePath); err == nil && latest != nil {
		if checked := parseStoredTime(latest.CheckedAt); checked.After(since) {
			since = checked
		}
	}
	return since
}

const goalLeadRunHealthNote = "Run health since your last goal check (code-collected, what Technical Review read after runs): failed runs with their error, steps' CONCERNS: lines, open workflow issues, and whether each schedule's runs ran the workflow. For a failure that blocks or threatens the goal (the goal-driving step or route failed, the goal cannot be measured, the same failure repeats), call record_pulse_qa_request once with the run, step and symptom. Note the others in one line in your check; do not repair steps yourself."

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
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
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

// wakeGoalLeadOnRunFailures gives newly failed runs one short turn in the
// workflow's Pulse conversation. Code decides that a run failed; the turn
// decides whether it threatens the goal and asks for QA. Each failed run wakes
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
	turn := goalLeadTurn{Kind: goalLeadTurnRunFailed, Body: goalLeadRunFailureBody(claimed),
		// Report and ask for QA only: no reruns, edits or messages in this turn.
		Perms: &stepworkflow.GoalWorkPermissions{}}
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
One short turn. Read only as much as you need (the run folder, get_pulse_state(view="step_concerns")). For each failure decide: does it block or threaten the goal (the goal-driving step or route failed, the goal cannot be measured, the same failure repeats)? If so, call record_pulse_qa_request once with the run, step and symptom; a QA run checks and repairs, and its result comes back here. Otherwise say in one line why it can wait; your next goal check reports it. Do not rerun the workflow, edit it or notify anyone in this turn.`)
	return b.String()
}

// goalLeadRunFailureMessage is the conversation log line for the wake-up.
func goalLeadRunFailureMessage(runs int) string {
	if runs == 1 {
		return "A run failed; Pulse is looking at it."
	}
	return fmt.Sprintf("%d runs failed; Pulse is looking at them.", runs)
}
