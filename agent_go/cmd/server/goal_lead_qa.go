package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// QA as the Pulse's sub-agent (PLAT-697 phase 4, design "Reviewers become
// skills and sub-agents"): technical review and repair is long, noisy work
// that would flood the persistent conversation, so the Pulse does not do
// it there. record_pulse_qa_request records what to check; the scheduler starts
// a separate Pulse fix run (Technical Review+Fix, its own session) when the
// workflow is free, and when that run ends writes its short result back into
// the Pulse conversation's log and into the next turn's context
// (goal_lead.qa_results). The fix run judges the steps; the Pulse judges
// the effect on the goal (the proposer is not the evaluator).

const goalLeadQARequestsSchema = `CREATE TABLE IF NOT EXISTS goal_lead_qa_requests (
	id TEXT PRIMARY KEY,
	requested_at TEXT NOT NULL,
	reason TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('pending','started','done','failed')),
	run_id TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT ''
)`

const (
	goalLeadQAMaxPerDay = 3
	// goalLeadQAPendingTTL fails a request no run could start for.
	goalLeadQAPendingTTL = 24 * time.Hour
)

type goalLeadQARequest struct {
	ID          string `json:"id"`
	RequestedAt string `json:"requested_at"`
	Reason      string `json:"reason"`
	Status      string `json:"status"`
	RunID       string `json:"run_id,omitempty"`
	Result      string `json:"result,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
}

func listGoalLeadQARequests(ctx context.Context, db *sql.DB, where string, args ...interface{}) ([]goalLeadQARequest, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, requested_at, reason, status, run_id, result, finished_at FROM goal_lead_qa_requests `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []goalLeadQARequest{}
	for rows.Next() {
		var req goalLeadQARequest
		if err := rows.Scan(&req.ID, &req.RequestedAt, &req.Reason, &req.Status, &req.RunID, &req.Result, &req.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

// recordPulseQARequestFromToolArgs backs record_pulse_qa_request.
func recordPulseQARequestFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_qa_request requires workspace_path")
	}
	reason := strings.Join(strings.Fields(stringToolArg(args, "what")), " ")
	if reason == "" {
		return "", fmt.Errorf("what is required: what the QA run should check or fix, with the evidence (run, step, symptom)")
	}
	if len(reason) > 600 {
		return "", fmt.Errorf("what is limited to 600 characters; point to files for detail")
	}
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := ensureGoalLeadSchema(ctx, db); err != nil {
		return "", err
	}
	open, err := listGoalLeadQARequests(ctx, db, `WHERE status IN ('pending','started')`)
	if err != nil {
		return "", err
	}
	if len(open) > 0 {
		return fmt.Sprintf("A QA run is already requested (%s: %s); its result comes back to this conversation. Add to it only after it finishes.", open[0].ID, open[0].Reason), nil
	}
	now := time.Now().UTC()
	var lastDay int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM goal_lead_qa_requests WHERE requested_at >= ?`, formatStoredTime(now.Add(-24*time.Hour))).Scan(&lastDay)
	if lastDay >= goalLeadQAMaxPerDay {
		return "", fmt.Errorf("refused: %d QA runs were requested in the last day; wait for their results or ask the owner", goalLeadQAMaxPerDay)
	}
	id := newGoalLeadID("QA-")
	if _, err := db.ExecContext(ctx, `INSERT INTO goal_lead_qa_requests (id, requested_at, reason, status) VALUES (?,?,?,'pending')`, id, formatStoredTime(now), reason); err != nil {
		return "", err
	}
	_ = insertGoalLeadMessage(ctx, db, GoalLeadMessage{Role: "qa", Source: "request", Text: "Asked for a QA run: " + reason}, now)
	return fmt.Sprintf("QA request %s recorded. A separate Technical Review+Fix run starts when the workflow is free; its short result comes back to this conversation. Do not wait for it in this turn.", id), nil
}

// recentGoalLeadQAResults is the context's view of finished QA runs.
func recentGoalLeadQAResults(ctx context.Context, workspacePath string, limit int) []goalLeadQARequest {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return []goalLeadQARequest{}
	}
	defer db.Close()
	if err := ensureGoalLeadSchema(ctx, db); err != nil {
		return []goalLeadQARequest{}
	}
	out, err := listGoalLeadQARequests(ctx, db, `ORDER BY requested_at DESC LIMIT ?`, limit)
	if err != nil {
		return []goalLeadQARequest{}
	}
	return out
}

// launchGoalLeadQARequests runs on every scheduler tick with the other Pulse
// launchers: it starts a fix run for a pending request and reports a finished
// one back to the Pulse conversation.
func (s *SchedulerService) launchGoalLeadQARequests(ctx context.Context) {
	if paused, _, err := s.IsGloballyPaused(ctx); err != nil || paused {
		return
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, item := range discovered {
		if item.Manifest == nil {
			continue
		}
		s.advanceGoalLeadQARequests(ctx, item.WorkspacePath, now)
	}
}

func (s *SchedulerService) advanceGoalLeadQARequests(ctx context.Context, workspacePath string, now time.Time) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return
	}
	defer db.Close()
	if err := ensureGoalLeadSchema(ctx, db); err != nil {
		return
	}
	open, err := listGoalLeadQARequests(ctx, db, `WHERE status IN ('pending','started') ORDER BY requested_at`)
	if err != nil || len(open) == 0 {
		return
	}
	for _, req := range open {
		switch req.Status {
		case "pending":
			if now.Sub(parseStoredTime(req.RequestedAt)) > goalLeadQAPendingTTL {
				s.finishGoalLeadQARequest(ctx, db, req, "failed", "No QA run could start within a day (the workflow stayed busy or Pulse runs were at their limit).", now)
				continue
			}
			if s.runningPulseRuns() >= maxConcurrentPulseRuns {
				return
			}
			runID, err := s.TriggerPulseFixRun(workspacePath, "the Pulse asked: "+req.Reason)
			if err != nil {
				// The workflow or another Pulse is running; a later tick retries.
				continue
			}
			_, _ = db.ExecContext(ctx, `UPDATE goal_lead_qa_requests SET status='started', run_id=? WHERE id=?`, runID, req.ID)
			_ = insertGoalLeadMessage(ctx, db, GoalLeadMessage{Role: "qa", Source: "started", Text: "QA run started for: " + req.Reason}, now)
			scheduleLogf("[PULSE] QA run %s started for %s: %s", runID, workspacePath, req.Reason)
		case "started":
			entry, found := findScheduleRun(ctx, workspacePath, req.RunID)
			if !found {
				if now.Sub(parseStoredTime(req.RequestedAt)) > goalLeadQAPendingTTL*2 {
					s.finishGoalLeadQARequest(ctx, db, req, "failed", "The QA run's record was not found.", now)
				}
				continue
			}
			switch entry.Status {
			case "queued", "running", scheduleRunStatusWaitingForCapacity:
				continue
			}
			s.finishGoalLeadQARequest(ctx, db, req, "done", goalLeadQAResultText(ctx, workspacePath, entry), now)
		}
	}
}

func findScheduleRun(ctx context.Context, workspacePath, runID string) (ScheduleRunEntry, bool) {
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return ScheduleRunEntry{}, false
	}
	for _, run := range runs {
		if run.ID == runID {
			return run, true
		}
	}
	return ScheduleRunEntry{}, false
}

// goalLeadQAResultText is the short result: the run's status and the
// Technical Review's own result reason.
func goalLeadQAResultText(ctx context.Context, workspacePath string, entry ScheduleRunEntry) string {
	text := fmt.Sprintf("QA run finished (%s).", entry.Status)
	if worklist, ok, err := getPulseWorklistForRun(ctx, workspacePath, entry.SessionID); err == nil && ok {
		if state, ok := worklist[pulseModuleTechnicalReview]; ok {
			if result := strings.TrimSpace(state.LastResult); result != "" {
				text = fmt.Sprintf("QA run finished (%s): technical review %s.", entry.Status, result)
			}
			if reason := strings.TrimSpace(state.LastResultReason); reason != "" {
				text += " " + reason
			}
		}
	}
	if entry.Error != "" && entry.Status != "success" {
		text += " " + entry.Error
	}
	if runes := []rune(text); len(runes) > 1200 {
		text = string(runes[:1200]) + "…"
	}
	return text
}

func (s *SchedulerService) finishGoalLeadQARequest(ctx context.Context, db *sql.DB, req goalLeadQARequest, status, result string, now time.Time) {
	_, _ = db.ExecContext(ctx, `UPDATE goal_lead_qa_requests SET status=?, result=?, finished_at=? WHERE id=?`, status, result, formatStoredTime(now), req.ID)
	_ = insertGoalLeadMessage(ctx, db, GoalLeadMessage{Role: "qa", Source: status, Text: fmt.Sprintf("%s (asked: %s)", result, req.Reason)}, now)
}
