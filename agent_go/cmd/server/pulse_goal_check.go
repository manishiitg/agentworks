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
// check comes first. Code keeps scheduling and stored evidence available;
// Pulse chooses what to read through tools or discuss with Builder. One short
// agent turn judges the goal and its approach, follows due work, and acts
// within the saved Pulse permissions. Its conclusion is shown in the existing Pulse summary;
// material owner advice goes through Builder, without another review lifecycle.

const (
	// goalCheckInterval: the next goal check when an older check recorded no
	// time. Newer checks store the time the Pulse chose within its pace
	// (pulse_check_timing.go).
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
	next_check_reason TEXT NOT NULL DEFAULT '',
 report_only INTEGER NOT NULL DEFAULT 0
)`

const pulseGoalCheckRunsSchema = `CREATE TABLE IF NOT EXISTS pulse_goal_check_runs (
	run_id TEXT PRIMARY KEY,
	started_at TEXT NOT NULL
)`

// PulseGoalCheck is one recorded goal check.
type PulseGoalCheck struct {
	ReportOnly       bool              `json:"report_only"`
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

// GoalStatusView is the owner view and optional goal-status tool response.
type GoalStatusView struct {
	SchedulerState     WorkflowSchedulerState  `json:"scheduler_state"`
	Facts              goalcheck.Facts         `json:"facts"`
	LatestCheck        *PulseGoalCheck         `json:"latest_check,omitempty"`
	Note               string                  `json:"note"`
	MeasurementUpgrade *goalMeasurementUpgrade `json:"measurement_upgrade,omitempty"`
	// GoalLead: the workflow's Pulse conversation owns QA and architecture
	// (set for the Pulse tab only).
	GoalLead bool `json:"goal_lead,omitempty"`
}

// This is a targeted workflow migration, not a mandatory version upgrade for
// workflows that do not use Pulse. No files or business data are changed here.
type goalMeasurementUpgrade struct {
	Required bool     `json:"required"`
	Reasons  []string `json:"reasons"`
}

func pulseMeasurementUpgrade(manifest *WorkflowManifest, metrics []stepworkflow.GoalMetric, facts goalcheck.Facts) *goalMeasurementUpgrade {
	if manifest == nil || !manifest.PulseEnabled() {
		return nil
	}
	u := &goalMeasurementUpgrade{Reasons: []string{}}
	if !facts.HasGoal {
		u.Reasons = append(u.Reasons, "Agree meaningful primary metrics and their recording steps with Builder.")
	}
	for i, m := range metrics {
		if m.CriterionID == "" || m.Unit == "" || m.Definition == "" || m.Source == "" || m.Window == "" || m.CollectionFrequency == "" || m.FreshnessHours <= 0 {
			u.Reasons = append(u.Reasons, m.ID+": complete the measurement definition with Builder; changed meaning requires a new ID.")
		}
		if i >= len(facts.Measurements) {
			continue
		}
		f := facts.Measurements[i]
		if f.Latest == nil {
			u.Reasons = append(u.Reasons, m.ID+": verify that an ordinary producing step records source-backed DB observations.")
			continue
		}
		// A valid unavailable outcome needs source recovery, not a format rewrite.
		if !goalcheck.Numeric(*f.Latest) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(m.Window)) {
		case "instant", "snapshot", "point_in_time":
			continue
		}
		if f.Latest.WindowStart == "" || f.Latest.WindowEnd == "" {
			u.Reasons = append(u.Reasons, m.ID+": update the existing period-producing step to record actual window_start/window_end for future readings; preserve old unknown periods.")
		}
	}
	u.Required = len(u.Reasons) > 0
	return u
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
	if _, err := db.ExecContext(ctx, `ALTER TABLE pulse_goal_checks ADD COLUMN report_only INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
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
	err = db.QueryRowContext(ctx, `SELECT check_id,checked_at,pulse_run_id,status,key_number,summary,action_taken,decision_id,alarms_json,pause_fingerprint,next_check_at,next_check_reason,report_only
		FROM pulse_goal_checks ORDER BY checked_at DESC LIMIT 1`).Scan(&check.CheckID, &check.CheckedAt, &check.PulseRunID, &check.Status,
		&check.KeyNumber, &check.Summary, &check.ActionTaken, &check.DecisionID, &alarms, &check.PauseFingerprint, &check.NextCheckAt, &check.NextCheckReason, &check.ReportOnly)
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
	in := stepworkflow.GoalCheckInput(ledger, now)
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
	view := &GoalStatusView{Facts: goalcheck.Evaluate(in), LatestCheck: latest, SchedulerState: readWorkflowSchedulerState(ctx, manifest, now)}
	view.MeasurementUpgrade = pulseMeasurementUpgrade(manifest, ledger.Metrics, view.Facts)
	view.Note = "DB measurement facts: each active metric's source-backed history, current value, configured freshness and comparable numerical difference. Run recording coverage is separate and may lack attribution. Builder and Pulse agree measurement meaning and verify sources; Pulse judges progress and asks Builder to improve gaps. A numerical difference alone is not causal evidence or an on-track verdict. Metric route/scope labels never identify goal-driving executions. Recent run routes/statuses and measurement attribution are evidence; Pulse and Builder judge goal contribution. Old route-based goal-work alarms in latest_check are historical obsolete inferences, not current facts. latest_check is the previous agent verdict. facts.schedules_paused means all individual schedule flags are disabled, not a global or product pause; scheduler_state is the current pause/enabled view."
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
	nextGap := pulseCheckDefaultGap
	nextReason := strings.TrimSpace(stringToolArg(args, "next_check_reason"))
	if hours, ok := args["next_check_in_hours"].(float64); ok && hours > 0 {
		nextGap = time.Duration(hours * float64(time.Hour))
		if nextGap < pulseCheckMinGap {
			nextGap = pulseCheckMinGap
		}
		if nextGap > pulseCheckMaxGap {
			nextGap = pulseCheckMaxGap
		}
	} else {
		nextReason = ""
	}
	perms, _ := pulsePermissionsForWorkflow(ctx, workspacePath)
	reportOnly := !perms.Run
	if reportOnly {
		nextGap = 7 * 24 * time.Hour
		nextReason = "Pulse is off; weekly suggestions report."
	}
	nextAt := now.Add(nextGap)
	if due, err := nextPulseGoalWorkQuestion(ctx, workspacePath, now); err != nil {
		scheduleLogf("[PULSE] dated Goal Work unavailable for %s; keeping chosen next check: %v", workspacePath, err)
	} else if !reportOnly && !due.IsZero() {
		bounded := due
		if floor := now.Add(pulseCheckMinGap); bounded.Before(floor) {
			bounded = floor
		}
		if bounded.Before(nextAt) {
			nextAt, nextReason = bounded, "A recorded Goal Work question or outcome is due."
		}
	}
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	checkID := "GC-" + strings.ToUpper(hex.EncodeToString(buf))
	alarms, _ := json.Marshal(view.Facts.Alarms)
	_, err = db.ExecContext(ctx, `INSERT INTO pulse_goal_checks (check_id,checked_at,pulse_run_id,status,key_number,summary,action_taken,decision_id,alarms_json,pause_fingerprint,next_check_at,next_check_reason,report_only)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, checkID, formatStoredTime(now), stringToolArg(args, "pulse_run_id"), status,
		strings.TrimSpace(stringToolArg(args, "key_number")), summary, strings.TrimSpace(stringToolArg(args, "action_taken")),
		strings.TrimSpace(stringToolArg(args, "decision_id")), string(alarms), view.Facts.PauseFingerprint, formatStoredTime(nextAt), nextReason, reportOnly)
	if err != nil {
		return "", err
	}
	next := fmt.Sprintf("Goal check recorded. Next check: %s.", nextAt.Format("Mon 2 Jan 15:04 MST"))
	return next, nil
}

// decideGoalCheckDue keeps one clock even when producing schedules are disabled.
// A reported pause suppresses duplicate notifications, not observation of the goal.
func decideGoalCheckDue(_ goalcheck.Facts, nextDue, lastStarted, now time.Time) (bool, string) {
	if !lastStarted.IsZero() && now.Sub(lastStarted) < goalCheckRetryGap {
		return false, "a goal check started recently"
	}
	if !nextDue.IsZero() && now.Before(nextDue) {
		return false, "next check chosen for " + nextDue.Format(time.RFC3339)
	}
	return true, "goal check and due Goal Work questions"
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
		if item.Manifest == nil || item.Manifest.Kind == "relay" || !workflowHasSoul(ctx, item.WorkspacePath) {
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
		nextDue, _ := nextPulseCheckAt(ctx, workspacePath, item.Manifest, latest, lastStarted)

		if !nextDue.IsZero() && now.Before(nextDue) {
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
func pulseLifecycleGoalCheckStep(ctx context.Context, workspacePath, pulseRunID string, _ workflowNotificationContentInstructions) pulseLifecycleStep {
	perms, autonomyText := pulsePermissionsForWorkflow(ctx, workspacePath)
	if !perms.Run {
		return pulseLifecycleStep{label: "goal-check", goalWork: &perms, goalLead: true, query: fmt.Sprintf(`WEEKLY GOAL REPORT. pulse_run_id=%q. Pulse is off. Read the written goal, relevant DB measurements/history, outputs, prior Goal Work and current plan consistency evidence. Judge progress, uncertainty and useful improvements yourself. Do not edit the workflow or skills, run steps, send action requests, create decisions or arrange follow-up wakeups. Do not ask Builder to act. Save one record_pulse_goal_check with the evidence-based conclusion, suggestions and any measurement gaps; its next report is weekly. Include plan drift findings as suggestions only. The existing report and change history are visible to workflow users. No separate reviewer or report ledger.`, pulseRunID)}
	}
	return pulseLifecycleStep{label: "goal-check", goalWork: &perms, query: fmt.Sprintf(`GOAL CHECK AND USEFUL WORK. pulse_run_id=%q. One continuing Pulse turn: no Gate, separate reviewers or finalizer.

Choose evidence through your tools or Builder messages. Use tools when you need evidence; no operational packet is attached. Read soul/soul.md and relevant goal memory. get_goal_metrics reads source-backed DB history; get_pulse_state views goal_status, goal_work, step_outputs, step_concerns and backlog are optional. No metric-to-route declaration is required. Missing evidence stays unknown; delivery, completed runs and numerical differences do not establish goal improvement.

Load the goal-lead-check skill for this check. Follow dated Goal Work questions, outcome checks and experiments before starting more work. Judge whether the approach serves the current goal even when a repair is pending. Use the architecture, funnel, costs or work skill as useful. Prefer useful results and removing unnecessary work over another rule. At a monthly objective checkpoint, compare current owner intent, company facts and actual outcomes; a new approach or retirement is an owner suggestion only.

Read list_schedules before current pause claims; global/product pause and disabled producing schedules differ. Past skipped_paused runs, old checks and memory never establish a current pause. Continue observing disabled producing schedules without resuming them or repeating a pause notification. Keep measurement repairs in ordinary recording steps, preserve DB history and old unknown periods, and never replay business actions just to migrate measurements.

Record one plain conclusion with record_pulse_goal_check: goal progress, judgment and recommendation when assessed, actual action and the next useful check. Keep dated approach questions in existing Goal Work items (idea, check_at); reuse them. Choose the next check for the earliest useful question or outcome within the fixed check bounds, not just a repair. Update other records only when they changed. On track with no due question or useful work is a valid quiet result, not an instruction to skip judgment.

You reason; Builder acts within the shared permissions. Messages are explicit, replies optional: ask_builder delivers, read_agent_messages reads replies, send_message replies, and schedule_message_wakeup chooses a later conversation follow-up. Final text is never forwarded. Give material evidence and your recommendation to Builder for the owner summary or Needs you; reuse an existing decision if owner authority is needed. An authorized request needs no new confirmation. Finish with the actual outcome or named uncertainty, not merely the technical handoff.

%s`, pulseRunID, autonomyText) + "\n\n" + pulseTimingInstructions}
}

// One clock with two existing settings: active checks choose a useful time;
// OFF reports weekly. Turning ON after a report makes active work eligible.
func nextPulseCheckForSetting(enabled bool, check *PulseGoalCheck) time.Time {
	if check == nil {
		return time.Time{}
	}
	if enabled {
		if check.ReportOnly {
			return time.Time{}
		}
		return nextGoalCheckDue(check)
	}
	checked := parseStoredTime(check.CheckedAt)
	if checked.IsZero() {
		return time.Time{}
	}
	return checked.Add(7 * 24 * time.Hour)
}

// nextPulseCheckAt is the common timing projection for the launcher and screen.
// It reads existing dates/wake intents; it never judges metric meaning.
func nextPulseCheckAt(ctx context.Context, workspacePath string, manifest *WorkflowManifest, check *PulseGoalCheck, lastStarted time.Time) (time.Time, string) {
	enabled := manifest != nil && manifest.PulseEnabled()
	next := nextPulseCheckForSetting(enabled, check)
	reason := ""
	if check != nil {
		reason = check.NextCheckReason
	}
	if !enabled {
		return next, "Pulse is off; weekly suggestions report."
	}
	if check == nil || check.ReportOnly {
		return next, "Pulse is ready for its goal check."
	}
	checked := parseStoredTime(check.CheckedAt)
	if question, err := nextPulseGoalWorkQuestion(ctx, workspacePath, checked); err == nil && !question.IsZero() {
		if floor := checked.Add(pulseCheckMinGap); question.Before(floor) {
			question = floor
		}
		if next.IsZero() || question.Before(next) {
			next, reason = question, "A recorded Goal Work question or outcome is due."
		}
	}
	if fast, err := pendingFastPulseRequest(ctx, workspacePath); err == nil && fast != nil {
		earliest := lastStarted.Add(pulseCheckMinGap)
		if lastStarted.IsZero() {
			earliest = checked.Add(pulseCheckMinGap)
		}
		if next.IsZero() || earliest.Before(next) {
			next, reason = earliest, fast.Reason
		}
	}
	return next, reason
}
