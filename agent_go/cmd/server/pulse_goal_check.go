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
// agent turn judges the goal: "on track" ends the turn, otherwise it acts
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
func pulseLifecycleGoalCheckStep(ctx context.Context, workspacePath, pulseRunID string, _ workflowNotificationContentInstructions) pulseLifecycleStep {
	perms, autonomyText := goalWorkAutonomy(ctx, workspacePath)
	return pulseLifecycleStep{label: "goal-check", goalWork: &perms, query: fmt.Sprintf(`GOAL CHECK, THEN GOAL WORK. pulse_run_id=%q. This is your self-timed turn; no Gate, reviewers or finalizer follow. You are the workflow's Pulse: the goal comes first.

Choose what you need to investigate from your conversation and the goal in soul/soul.md. You and Builder are colleagues with your own tools: form your own view, ask questions, share evidence and decide when a follow-up is useful. No execution summary or code-selected evidence packet is attached to this turn.

Use tools when you need evidence:
- Read memory/goal.md for lasting owner direction and earlier lessons; soul/soul.md wins on conflict. Never re-ask an answered question.
- get_goal_metrics reads source-backed DB measurements and history independently of execution folders.
- get_pulse_state(view="goal_status") is an optional snapshot of measurements, run records, your previous check, measurement-upgrade needs, memory, decisions, focus areas and run health. Choose it when useful; it is not a prerequisite to judging or recording a check.
- Inspect execution history with search_platform operations list_runs, get_run or get_logs and relevant workflow files, or get_pulse_state views step_outputs, step_concerns or backlog. Ask Builder when its implementation knowledge would help. Read only what you need.
- Read list_schedules before making current pause claims or requesting resume. Past skipped_paused runs, old checks and memory never establish a current pause. Unknown reads stay unknown. Global pause, product pause and individually disabled schedules are separate; respect owner authority to change or trigger schedules.

Judge which work advances the goal with Builder from the evidence you choose. No metric-to-route declaration is required. Metric scope labels never classify executions; measurement producers may differ from work improving the goal. Missing evidence is unknown, and a numerical difference alone does not prove causality or progress. Historical route-based goal-work alarms are obsolete inferences.

When measurement needs improvement, agree meaningful metrics and source methods with Builder and verify the DB readings. Target only the necessary definitions and ordinary recording steps, preserve history, and reuse existing work instead of duplicating it. Missing baselines do not justify repeating business actions or inventing old periods. Measurement upgrades apply only while Pulse is enabled; no general workflow-version migration is required.

Keep your own records when relevant: recommend on pending decisions without answering for the owner; record outcomes once you have evidence; track or close active focus areas; record useful results and lessons in goal memory. Diagnose failures that threaten the goal and discuss repairs with Builder. You read and reason; Builder runs or changes things within your shared permission levels. To involve the owner, ask Builder to raise one clear decision and attach your recommendation. Reuse an existing pending decision when applicable.

Messages are explicit and replies optional. ask_builder delivers your message; it does not prove completion or promise a reply. Read incoming replies with read_agent_messages and reply with send_message. Choose schedule_message_wakeup yourself when you want a later follow-up. Neither agent's final chat text is forwarded automatically.

Record one honest verdict with record_pulse_goal_check(status, key_number, summary, action_taken, decision_id when applicable, next_check_in_hours, next_check_reason). Choose the next check from what the goal needs within your pace. On track with nothing useful to do is a valid result. When something needs owner attention, tell Builder; it decides whether to notify the owner.

If useful work remains within your level, load the goal-lead-work skill, coordinate bounded work with Builder in this turn and record it with record_pulse_goal_work. There is no separate Goal Work pass.

%s`, pulseRunID, autonomyText) + "\n\n" + pulsePaceText(workflowPulsePace(ctx, workspacePath))}
}
