package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/goalcheck"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Pulse phase 1 (PLAT-697, docs/design/pulse_goal_owner.md): the goal
// check comes first. Code computes the cheap facts (is the goal measured, does
// the goal-driving work run, the silence alarm; pkg/goalcheck). One short
// agent turn a day judges them: "on track" ends the turn, otherwise it acts
// within pulse.autonomy or asks the owner one batched decision. The result is
// shown at the top of the Pulse tab and leads the Pulse summary notification.

const (
	// goalCheckInterval: the next goal check when an older check recorded no
	// time. Newer checks store the time the Pulse chose within its pace
	// (pulse_pace.go).
	goalCheckInterval = 24 * time.Hour
	// goalCheckRetryGap stops a goal check that did not record a result from
	// being started again every tick.
	goalCheckRetryGap = 3 * time.Hour
	// goalCheckHistoryWindow bounds the runs the silence alarm reads.
	goalCheckHistoryWindow = 45 * 24 * time.Hour
	// goalCheckEvaluateGap bounds how often the launcher computes one
	// workflow's facts.
	goalCheckEvaluateGap = 30 * time.Minute
)

// goalCheckEvaluatedAt is the launcher's last fact computation per workflow,
// guarded by pulseLauncherMu.
var goalCheckEvaluatedAt = map[string]time.Time{}

// isPulseScheduleID reports the synthetic schedules of Pulse passes (full,
// goal check, fix run): they are not workflow runs.
func isPulseScheduleID(id string) bool {
	return id == manualWorkflowPulseScheduleID || id == pulseFixRunScheduleID
}

var goalCheckStatuses = map[string]bool{"on_track": true, "at_risk": true, "off_track": true, "not_measured": true}

const pulseGoalChecksSchema = `CREATE TABLE IF NOT EXISTS pulse_goal_checks (
	check_id TEXT PRIMARY KEY,
	checked_at TEXT NOT NULL,
	pulse_run_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK(status IN ('on_track','at_risk','off_track','not_measured')),
	key_number TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL,
	action_taken TEXT NOT NULL DEFAULT '',
	decision_id TEXT NOT NULL DEFAULT '',
	alarms_json TEXT NOT NULL DEFAULT '[]',
	pause_fingerprint TEXT NOT NULL DEFAULT '',
	next_check_at TEXT NOT NULL DEFAULT '',
	next_check_reason TEXT NOT NULL DEFAULT ''
)`

const pulseGoalCheckRunsSchema = `CREATE TABLE IF NOT EXISTS pulse_goal_check_runs (
	run_id TEXT PRIMARY KEY,
	started_at TEXT NOT NULL
)`

// PulseGoalCheck is one recorded goal check.
type PulseGoalCheck struct {
	CheckID          string            `json:"check_id"`
	CheckedAt        string            `json:"checked_at"`
	PulseRunID       string            `json:"pulse_run_id,omitempty"`
	Status           string            `json:"status"`
	KeyNumber        string            `json:"key_number,omitempty"`
	Summary          string            `json:"summary"`
	ActionTaken      string            `json:"action_taken,omitempty"`
	DecisionID       string            `json:"decision_id,omitempty"`
	Alarms           []goalcheck.Alarm `json:"alarms"`
	PauseFingerprint string            `json:"pause_fingerprint,omitempty"`
	// NextCheckAt is when this check chose the next one; NextCheckReason why.
	NextCheckAt     string `json:"next_check_at,omitempty"`
	NextCheckReason string `json:"next_check_reason,omitempty"`
}

// nextGoalCheckDue is when the next goal check is due after check: the time it
// chose, or a day after it.
func nextGoalCheckDue(check *PulseGoalCheck) time.Time {
	if check == nil {
		return time.Time{}
	}
	checked := parseStoredTime(check.CheckedAt)
	if checked.IsZero() {
		return time.Time{}
	}
	if chosen := parseStoredTime(check.NextCheckAt); !chosen.IsZero() {
		return chosen
	}
	return checked.Add(goalCheckInterval)
}

// GoalStatusView is the goal status shown first to the agent and the owner.
type GoalStatusView struct {
	Facts       goalcheck.Facts `json:"facts"`
	LatestCheck *PulseGoalCheck `json:"latest_check,omitempty"`
	Note        string          `json:"note"`
	// GoalLead: the workflow's Pulse conversation owns QA and architecture
	// (set for the Pulse tab only).
	GoalLead bool `json:"goal_lead,omitempty"`
}

func ensurePulseGoalCheckSchema(ctx context.Context, db *sql.DB) error {
	for _, ddl := range []string{pulseGoalChecksSchema, pulseGoalCheckRunsSchema} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	// Tables created before the Pulse chose its next check gain the columns.
	for _, column := range []string{"next_check_at", "next_check_reason"} {
		if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_goal_checks ADD COLUMN `+column+` TEXT NOT NULL DEFAULT ''`); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func latestPulseGoalCheck(ctx context.Context, workspacePath string) (*PulseGoalCheck, time.Time, error) {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, time.Time{}, err
	}
	defer db.Close()
	if err := ensurePulseGoalCheckSchema(ctx, db); err != nil {
		return nil, time.Time{}, err
	}
	var lastStarted string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(started_at),'') FROM pulse_goal_check_runs`).Scan(&lastStarted)
	var check PulseGoalCheck
	var alarms string
	err = db.QueryRowContext(ctx, `SELECT check_id,checked_at,pulse_run_id,status,key_number,summary,action_taken,decision_id,alarms_json,pause_fingerprint,next_check_at,next_check_reason
		FROM pulse_goal_checks ORDER BY checked_at DESC LIMIT 1`).Scan(&check.CheckID, &check.CheckedAt, &check.PulseRunID, &check.Status,
		&check.KeyNumber, &check.Summary, &check.ActionTaken, &check.DecisionID, &alarms, &check.PauseFingerprint, &check.NextCheckAt, &check.NextCheckReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, parseStoredTime(lastStarted), nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	check.Alarms = []goalcheck.Alarm{}
	_ = json.Unmarshal([]byte(alarms), &check.Alarms)
	return &check, parseStoredTime(lastStarted), nil
}

func recordGoalCheckRunStarted(ctx context.Context, workspacePath, runID string, startedAt time.Time) error {
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensurePulseGoalCheckSchema(ctx, db); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO pulse_goal_check_runs (run_id, started_at) VALUES (?,?)`, strings.TrimSpace(runID), formatStoredTime(startedAt))
	return err
}

// goalCheckRunsFromSchedules adds scheduled workflow runs the run-finish
// recorder has no row for (older runs, or runs before PLAT-697). The route is
// the schedule's route selection, or what the retained run folder recorded.
func goalCheckRunsFromSchedules(ctx context.Context, workspacePath string, manifest *WorkflowManifest, recorded []goalcheck.Run, since time.Time) []goalcheck.Run {
	entries, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return nil
	}
	routesBySchedule := map[string][]string{}
	if manifest != nil {
		for _, schedule := range manifest.Schedules {
			seen := map[string]bool{}
			for _, route := range schedule.RouteSelections {
				if route = strings.TrimSpace(route); route != "" && !seen[route] {
					seen[route] = true
					routesBySchedule[schedule.ID] = append(routesBySchedule[schedule.ID], route)
				}
			}
		}
	}
	top := func(folder string) string {
		folder = strings.Trim(strings.TrimSpace(folder), "/")
		if i := strings.Index(folder, "/"); i >= 0 {
			return folder[:i]
		}
		return folder
	}
	out := []goalcheck.Run{}
	for _, entry := range entries {
		if isPulseScheduleID(entry.ScheduleID) || strings.TrimSpace(entry.RunFolder) == "" || (entry.RanWorkflow != nil && !*entry.RanWorkflow) {
			continue
		}
		switch entry.Status {
		case "queued", "running", "waiting_for_capacity":
			continue
		}
		finished := entry.StartedAt
		if entry.CompletedAt != nil {
			finished = *entry.CompletedAt
		}
		if finished.Before(since) {
			continue
		}
		duplicate := false
		for _, run := range recorded {
			if top(run.RunID) == top(entry.RunFolder) && absDuration(run.FinishedAt.Sub(finished)) < 2*time.Hour {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		routes := routesBySchedule[entry.ScheduleID]
		if len(routes) == 0 {
			groups := entry.GroupNames
			if len(groups) == 0 {
				groups = []string{"default"}
			}
			for _, group := range groups {
				routes = append(routes, stepworkflow.RunRouteSelections(workspacePath, top(entry.RunFolder)+"/"+group)...)
			}
		}
		out = append(out, goalcheck.Run{RunID: entry.RunFolder, StartedAt: entry.StartedAt, FinishedAt: finished, Status: entry.Status, Routes: routes})
	}
	return out
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// computeGoalStatus loads the rows and evaluates the goal facts (code only).
func computeGoalStatus(ctx context.Context, workspacePath string, now time.Time) (*GoalStatusView, error) {
	ledger, err := stepworkflow.LoadPulseImpactLedger(ctx, workspacePath, 500)
	if err != nil {
		return nil, err
	}
	in := goalcheck.Input{Now: now}
	for _, m := range ledger.Metrics {
		in.Metrics = append(in.Metrics, goalcheck.Metric{ID: m.ID, Name: m.Name, Role: m.Role, Route: m.Route, Unit: m.Unit})
	}
	for _, o := range ledger.Observations {
		observed, _ := time.Parse(time.RFC3339Nano, o.ObservedAt)
		recordedAt, _ := time.Parse(time.RFC3339Nano, o.RecordedAt)
		in.Observations = append(in.Observations, goalcheck.Observation{Metric: o.Metric, RunID: o.RunID, Value: o.Value, Status: o.Status, ObservedAt: observed, RecordedAt: recordedAt})
	}
	since := now.Add(-goalCheckHistoryWindow)
	recorded, err := stepworkflow.LoadGoalRunFacts(ctx, workspacePath, since)
	if err != nil {
		return nil, err
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		manifest = nil
	}
	in.Runs = append(recorded, goalCheckRunsFromSchedules(ctx, workspacePath, manifest, recorded, since)...)
	in.SchedulesPaused = workflowSchedulesAllPaused(manifest)
	latest, _, err := latestPulseGoalCheck(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if latest != nil {
		in.ReportedPauseFingerprint = latest.PauseFingerprint
	}
	view := &GoalStatusView{Facts: goalcheck.Evaluate(in), LatestCheck: latest}
	view.Note = "Code-computed goal facts (no AI): is the goal measured by the workflow's own runs, does the goal's route run, and the silence alarm (no run or no reading for 3+ days). Judge these first. latest_check is the last goal check's verdict."
	return view, nil
}

func readGoalStatusView(ctx context.Context, workspacePath string) (string, error) {
	view, err := computeGoalStatus(ctx, workspacePath, time.Now().UTC())
	if err != nil {
		return "", err
	}
	// Phase 3: goal memory, decisions to recommend on and outcomes to record
	// ride along, so a turn that reads only this view reads memory first.
	out, err := json.MarshalIndent(struct {
		*GoalStatusView
		GoalLead map[string]interface{} `json:"goal_lead"`
	}{view, goalLeadAgentContext(ctx, workspacePath)}, "", "  ")
	return string(out), err
}

// recordPulseGoalCheckFromToolArgs backs record_pulse_goal_check.
func recordPulseGoalCheckFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_goal_check requires workspace_path")
	}
	status := strings.TrimSpace(stringToolArg(args, "status"))
	if !goalCheckStatuses[status] {
		return "", fmt.Errorf("status must be on_track, at_risk, off_track or not_measured")
	}
	summary := strings.TrimSpace(stringToolArg(args, "summary"))
	if summary == "" {
		return "", fmt.Errorf("summary is required: one or two plain sentences for the owner")
	}
	if err := checkPulsePlainText("the goal facts already stored with this check",
		plainTextField{name: "summary", text: summary, maxLen: 400},
		plainTextField{name: "key_number", text: stringToolArg(args, "key_number"), maxLen: 80},
		plainTextField{name: "action_taken", text: stringToolArg(args, "action_taken"), maxLen: 300}); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	view, err := computeGoalStatus(ctx, workspacePath, now)
	if err != nil {
		return "", err
	}
	_, db, err := openPulseModuleStateDB(ctx, workspacePath, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := ensurePulseGoalCheckSchema(ctx, db); err != nil {
		return "", err
	}
	// The Pulse chooses its next check: hours from now, within 1 hour and 7
	// days; none means a day.
	pace := workflowPulsePace(ctx, workspacePath)
	nextGap := pace.DefaultGap
	nextReason := strings.TrimSpace(stringToolArg(args, "next_check_reason"))
	if hours, ok := args["next_check_in_hours"].(float64); ok && hours > 0 {
		nextGap = time.Duration(hours * float64(time.Hour))
		if nextGap < pace.MinGap {
			nextGap = pace.MinGap
		}
		if nextGap > pace.MaxGap {
			nextGap = pace.MaxGap
		}
	} else {
		nextReason = ""
	}
	nextAt := now.Add(nextGap)
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	checkID := "GC-" + strings.ToUpper(hex.EncodeToString(buf))
	alarms, _ := json.Marshal(view.Facts.Alarms)
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_goal_checks (check_id,checked_at,pulse_run_id,status,key_number,summary,action_taken,decision_id,alarms_json,pause_fingerprint,next_check_at,next_check_reason)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, checkID, formatStoredTime(now), stringToolArg(args, "pulse_run_id"), status,
		strings.TrimSpace(stringToolArg(args, "key_number")), summary, strings.TrimSpace(stringToolArg(args, "action_taken")),
		strings.TrimSpace(stringToolArg(args, "decision_id")), string(alarms), view.Facts.PauseFingerprint, formatStoredTime(nextAt), nextReason)
	if err != nil {
		return "", err
	}
	next := fmt.Sprintf("Goal check recorded. Next check: %s.", nextAt.Format("Mon 2 Jan 15:04 MST"))
	if status == "on_track" && len(view.Facts.Alarms) == 0 {
		next += " On track: stop here."
	}
	return next, nil
}

// decideGoalCheckDue: daily, never right after a check that did not finish,
// and for a paused workflow only to report a pause (or a change) once.
func decideGoalCheckDue(facts goalcheck.Facts, nextDue, lastStarted, now time.Time) (bool, string) {
	if !lastStarted.IsZero() && now.Sub(lastStarted) < goalCheckRetryGap {
		return false, "a goal check started recently"
	}
	if facts.SchedulesPaused {
		if facts.PauseAlreadyReported {
			return false, "paused on purpose; already reported"
		}
		return true, "schedules are paused; report it once"
	}
	if !nextDue.IsZero() && now.Before(nextDue) {
		return false, "next check chosen for " + nextDue.Format(time.RFC3339)
	}
	return true, "goal check due"
}

// launchDueGoalChecks runs on every scheduler tick after the full Pulse and
// fix-run launchers. It shares their concurrency cap and the full Pulse's
// runtime key, so it never runs beside a Pulse pass on the same workflow.
func (s *SchedulerService) launchDueGoalChecks(ctx context.Context) {
	if paused, _, err := s.IsProductPaused(ctx, "agentworks"); err != nil || paused {
		return
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		scheduleLogf("[PULSE] cannot scan workflows for goal checks: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, item := range discovered {
		if item.Manifest == nil || !item.Manifest.PulseEnabled() {
			continue
		}
		workspacePath := item.WorkspacePath
		latest, lastStarted, err := latestPulseGoalCheck(ctx, workspacePath)
		if err != nil {
			continue
		}
		// Cheap gates first: the facts read the run history.
		if !lastStarted.IsZero() && now.Sub(lastStarted) < goalCheckRetryGap {
			continue
		}
		nextDue := nextGoalCheckDue(latest)
		if !workflowSchedulesAllPaused(item.Manifest) && !nextDue.IsZero() && now.Before(nextDue) {
			continue
		}
		// A paused workflow, or one with no goal, would otherwise be
		// evaluated every tick; the pulseLauncherMu caller serializes access.
		if evaluated, ok := goalCheckEvaluatedAt[workspacePath]; ok && now.Sub(evaluated) < goalCheckEvaluateGap {
			continue
		}
		goalCheckEvaluatedAt[workspacePath] = now
		view, err := computeGoalStatus(ctx, workspacePath, now)
		if err != nil {
			scheduleLogf("[PULSE] cannot compute goal facts for %s: %v", workspacePath, err)
			continue
		}
		due, reason := decideGoalCheckDue(view.Facts, nextDue, lastStarted, now)
		if !due {
			continue
		}
		if s.runningPulseRuns() >= maxConcurrentPulseRuns {
			return
		}
		runID, err := s.TriggerGoalCheck(workspacePath)
		if err != nil {
			continue
		}
		scheduleLogf("[PULSE] goal check started for %s (run %s): %s", workspacePath, runID, reason)
	}
}

// pulseLifecycleGoalCheckStep is the one turn of a daily goal check.
func pulseLifecycleGoalCheckStep(ctx context.Context, workspacePath, pulseRunID string, instructions workflowNotificationContentInstructions) pulseLifecycleStep {
	facts := "{}"
	paused := false
	if view, err := computeGoalStatus(ctx, workspacePath, time.Now().UTC()); err == nil {
		paused = view.Facts.SchedulesPaused
		if encoded, err := json.Marshal(view); err == nil {
			facts = string(encoded)
		}
	}
	routing := ""
	if len(instructions.pulseSummaryChannels) > 0 {
		routing = fmt.Sprintf(" Configured Pulse-summary channels: %s; the backend routes by notification_kind.", notificationChannelSummary(instructions.pulseSummaryChannels))
	}
	perms, autonomyText := goalWorkAutonomy(ctx, workspacePath)
	pausedRule := ""
	if paused {
		// Paused schedules are the owner's choice, not a reason to stop
		// working: Pulse keeps its permission levels and only leaves the
		// schedules to the owner (owner, 2026-10-08: more autonomy).
		pausedRule = "\n\nThe workflow's schedules are paused by the owner. Do not re-enable or trigger schedules without asking. Everything else follows your permission levels, including running a step yourself to measure or move the goal. Mention the pause once, not on every check."
	}
	goalLead := "{}"
	if encoded, err := json.Marshal(goalLeadAgentContext(ctx, workspacePath)); err == nil {
		goalLead = string(encoded)
	}
	return pulseLifecycleStep{label: "goal-check", goalWork: &perms, query: fmt.Sprintf(`GOAL CHECK, THEN GOAL WORK. pulse_run_id=%q. This is your one self-timed turn (you chose its time on your last check); no Gate, reviewers or finalizer follow. You are the workflow's Pulse: the goal comes first.

Code-computed goal facts (the silence alarm; already current, do not recompute them):
%s

Pulse context: goal memory (memory/goal.md), pending decisions to recommend on, answered decisions whose outcome is still to record, focus areas, QA results that came back, and run health (failed runs, steps' CONCERNS: lines, open issues since your last check):
%s

1. Read the goal memory above first: what the owner already answered, decisions and outcomes, lessons, open bets. soul/soul.md wins on any conflict; never re-ask what memory already answers. Then read soul/soul.md's objective and get_goal_metrics once. Decide: is the goal measured, is it moving, is the work that drives it running?
2. Every check, on track or not: for each pending decision in decisions_to_recommend with no current recommendation (or new evidence since), call record_pulse_recommendation once: the option, why, the evidence, confidence, what it blocks, and safe_default_by only when that default is safe and within the permission levels below. You never answer a decision; the owner accepts or changes your recommendation. For each item in outcomes_due, call record_pulse_decision_outcome with what happened after. Add a new dated result, lesson or open bet with record_pulse_goal_memory (one line, source marked); consolidate the memory when its note says so. For each active focus area in focus_areas, call record_pulse_focus_area(action="track") with moving, stuck (and the one clear ask) or done; close a done or expired one with action="close" and a one-line lesson (for an expired one, say why and propose extend, change or drop). Read run_health: no separate Technical review runs after this workflow's runs, so you are its safety net. For a failed run or step that blocks or threatens the goal, diagnose it and ask the Builder chat (ask_builder) to debug and fix it with your evidence; note the other failures and concerns in one line in your summary. If the goal has no metric yet, get one set up through the Builder chat first. Then read plan_changes, owner_answers, spend, error_rate, login_hints and builder_asks and act on each as its note says: ask the Builder chat (ask_builder) about a plan change that touches a goal-driving step or the metric and record the answer in goal memory (source builder_answer).
3. On track (measured recently, moving or holding as expected, goal work running, no alarm): call record_pulse_goal_check(status="on_track", key_number, summary, next_check_in_hours, next_check_reason) and stop. No notification.
4. Otherwise act within the permission levels below, smallest useful step first. You run and change nothing yourself: ask the Builder chat (ask_builder) to run the goal-driving step or route, or to make the change. At an auto level it does so without the owner; at ask, prepare it as a decision. Then, if something important needs the owner, ask the Builder chat to raise ONE decision for the owner that names the problem in one line, with the options; it tells you the decision id, and you attach your recommendation with record_pulse_recommendation (with a safe default by a time only when one is safe). Ask it to reuse a pending goal-check decision instead of raising another. Never guess the owner's preference: say you do not know it.
5. Call record_pulse_goal_check once with status (at_risk, off_track or not_measured), key_number (the key goal number and its date, e.g. "+2 subscribers on 7 Oct"), a plain one or two sentence summary, action_taken, and decision_id when the Builder raised one.
   Choose when to check next with next_check_in_hours and a short next_check_reason, within your pace below: soon after the next run that should move the goal, a few hours while a fix you asked for is pending, or days for a workflow that runs weekly.
6. You send no notifications. When the owner should know (the goal is off track, a decision waits), tell the Builder chat with ask_builder in a few plain lines: the status, key number and when it was last measured, what you did and what you need; it decides whether to notify the owner.%s%s
7. Then Goal Work, in this same turn: if something within your level would move the goal (load the goal-lead-work skill), get bounded items done through the Builder chat (as many as your pace allows) and record each with record_pulse_goal_work. Skip it when the check found nothing worth doing; that is a valid answer. There is no separate Goal Work pass: your next turn is the time you chose with next_check_in_hours, or sooner when a run fails.

%s`, pulseRunID, facts, goalLead, routing, finalizerRichEmailInstruction, autonomyText) + "\n\n" + pulsePaceText(workflowPulsePace(ctx, workspacePath)) + pausedRule}
}
