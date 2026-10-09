package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulepolicy"
)

// After-run options (PLAT-697 phase 0). Backup, Publish and Notify used to be
// the Pulse finalizer's work, chosen by a schedule's pulse_mode (off / basic /
// full). They are now plain options on each schedule, plus the same choice
// for manual runs, and run after the run without a Pulse pass:
//
//   - Notify is code: a run summary built from the run's facts, through the
//     normal notification path (configured channels and recipients). Routine
//     successes are recorded in the dashboard only; failures and changes of
//     status go out to the channels.
//   - Backup and Publish are skipped in code when nothing changed since the
//     last backup / publish (source hashes). Only when there is something to
//     back up or publish does one short turn run, limited to those actions,
//     because each destination's provider steps live in backup-strategy.md and
//     publish-strategy.md.
//
// pulse_mode is read for one release: a schedule without after_run gets the
// options its pulse_mode stood for (basic, and the retired full, did all
// three; off did none), and the migration writes after_run. pulse_mode is
// kept in step with after_run so an older server reads the same choice.

// ScheduleAfterRun is a schedule's after-run options.
type ScheduleAfterRun struct {
	Backup  bool `json:"backup"`
	Publish bool `json:"publish"`
	Notify  bool `json:"notify"`
}

// Any reports whether any option is on.
func (a ScheduleAfterRun) Any() bool { return a.Backup || a.Publish || a.Notify }

// afterRunFromPulseMode is what a legacy pulse_mode did after a run. The basic
// finalizer backed up, published the run's report targets and sent the run
// summary; legacy full ran as basic after a normal run; off ran nothing.
func afterRunFromPulseMode(mode string) ScheduleAfterRun {
	switch schedulepolicy.NormalizePulse(mode) {
	case schedulePulseModeBasic:
		return ScheduleAfterRun{Backup: true, Publish: true, Notify: true}
	}
	return ScheduleAfterRun{}
}

// EffectiveAfterRun returns a schedule's after-run options: its after_run, or
// for one release what its pulse_mode did.
func (m *WorkflowManifest) EffectiveAfterRun(schedule WorkflowSchedule) ScheduleAfterRun {
	if m != nil && m.Kind == "relay" {
		return ScheduleAfterRun{}
	}
	if strings.EqualFold(strings.TrimSpace(schedule.ScheduleType), "webhook") {
		return ScheduleAfterRun{}
	}
	if schedule.AfterRun != nil {
		return *schedule.AfterRun
	}
	return afterRunFromPulseMode(m.EffectivePulseMode(schedule))
}

// EffectiveManualAfterRun returns the after-run options for manual full runs:
// after_manual_run, or what the backup and publish configs' own
// after_manual_run triggers asked for before.
func (m *WorkflowManifest) EffectiveManualAfterRun() ScheduleAfterRun {
	if m == nil || m.Kind == "relay" {
		return ScheduleAfterRun{}
	}
	if m.AfterManualRun != nil {
		return *m.AfterManualRun
	}
	return ScheduleAfterRun{
		Backup:  m.Backup != nil && m.Backup.Enabled && m.Backup.Triggers.AfterManualRun,
		Publish: m.Publish != nil && m.Publish.Enabled && m.Publish.Triggers.AfterManualRun,
	}
}

// syncLegacyPulseMode keeps pulse_mode matching after_run for one release, so
// an older server (and the contract stamp check) read the same choice.
func syncLegacyPulseMode(schedule *WorkflowSchedule) {
	if schedule == nil || schedule.AfterRun == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(schedule.ScheduleType), "webhook") {
		return
	}
	mode := schedulePulseModeOff
	if schedule.AfterRun.Any() {
		mode = schedulePulseModeBasic
	}
	if schedule.PulseMode != mode || strings.TrimSpace(schedule.PulseModeReason) == "" {
		schedule.PulseMode = mode
		if strings.TrimSpace(schedule.PulseModeReason) == "" {
			schedule.PulseModeReason = "Set from the schedule's after-run options (backup, publish, notify)."
		}
	}
}

// setScheduleAfterRun sets a schedule's options and its legacy mirror.
func setScheduleAfterRun(schedule *WorkflowSchedule, options ScheduleAfterRun) {
	copied := options
	schedule.AfterRun = &copied
	syncLegacyPulseMode(schedule)
}

// applyScheduleAfterRunPolicy applies a create/update request's after_run.
// A new schedule without after_run gets what its pulse_mode stands for, or
// all three when it names neither; an update that changes only pulse_mode
// moves after_run with it.
func applyScheduleAfterRunPolicy(schedule *WorkflowSchedule, policy stepworkflow.ScheduleRuntimePolicy, creating bool) {
	if schedule == nil {
		return
	}
	switch {
	case policy.SetAfterRun:
		setScheduleAfterRun(schedule, ScheduleAfterRun{Backup: policy.AfterRun.Backup, Publish: policy.AfterRun.Publish, Notify: policy.AfterRun.Notify})
	case creating && strings.TrimSpace(schedule.PulseMode) == "":
		setScheduleAfterRun(schedule, ScheduleAfterRun{Backup: true, Publish: true, Notify: true})
	case creating || policy.SetPulseMode:
		setScheduleAfterRun(schedule, afterRunFromPulseMode(schedule.PulseMode))
	}
}

// MigrateScheduleAfterRun writes after_run on every schedule that has none,
// from what its pulse_mode did, and keeps a legacy full schedule's workflow on
// its own Pulse schedule (pulse.enabled). Returns whether anything changed.
func (m *WorkflowManifest) MigrateScheduleAfterRun() bool {
	if m == nil || m.Kind == "relay" {
		return false
	}
	changed := false
	for i := range m.Schedules {
		schedule := &m.Schedules[i]
		if schedule.AfterRun != nil || schedule.PulseReviewOnly || strings.EqualFold(strings.TrimSpace(schedule.ScheduleType), "webhook") {
			continue
		}
		if schedule.Enabled && strings.EqualFold(strings.TrimSpace(schedule.PulseMode), schedulepolicy.LegacyFullPulseMode) {
			if m.Pulse == nil {
				m.Pulse = &WorkflowPulseConfig{}
			}
			m.Pulse.Enabled = true
		}
		options := afterRunFromPulseMode(m.EffectivePulseMode(*schedule))
		setScheduleAfterRun(schedule, options)
		changed = true
	}
	return changed
}

// effectiveScheduleAfterRun reads the schedule from the freshly read manifest,
// as effectiveSchedulePulseMode did: a change saved while the run ran counts.
func effectiveScheduleAfterRun(sctx *ScheduleContext, manifest *WorkflowManifest) ScheduleAfterRun {
	if sctx == nil || manifest == nil || sctx.WebhookInput != nil || sctx.Schedule.ScheduleType == "webhook" {
		return ScheduleAfterRun{}
	}
	for _, schedule := range manifest.Schedules {
		if schedule.ID != "" && schedule.ID == sctx.Schedule.ID {
			return manifest.EffectiveAfterRun(schedule)
		}
	}
	return manifest.EffectiveAfterRun(sctx.Schedule)
}

// afterRunWork is what the code check found for backup and publish.
type afterRunWork struct {
	Backup, Publish bool
	Notes           []string
}

// afterRunWorkDue decides in code whether backup and publish have anything
// to do. Unchanged sources need no agent.
func (s *SchedulerService) afterRunWorkDue(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, options ScheduleAfterRun) afterRunWork {
	var work afterRunWork
	if options.Backup {
		// The managed SQLite snapshot is part of the backup source hash; make
		// it current first so a database-only change counts.
		_ = s.prepareWorkflowDatabaseBackupSnapshot(ctx, sctx)
		hash, _ := computeWorkflowBackupSourceHash(ctx, sctx.WorkspacePath)
		status, _, _ := readWorkflowBackupStatus(ctx, sctx.WorkspacePath)
		if hash != "" && status != nil && strings.TrimSpace(status.LastSourceHash) == hash {
			work.Notes = append(work.Notes, "backup skipped: nothing changed since the last backup")
		} else {
			work.Backup = true
		}
	}
	if options.Publish {
		switch {
		case manifest == nil || manifest.Publish == nil || !manifest.Publish.Enabled:
			work.Notes = append(work.Notes, "publish skipped: publishing is not set up")
		case !publishHasReportTarget(manifest.Publish):
			work.Notes = append(work.Notes, "publish skipped: only the Pulse page is published")
		default:
			hash := computeWorkflowPublishSourceHash(ctx, sctx.WorkspacePath)
			status, _, _ := readWorkflowPublishStatus(ctx, sctx.WorkspacePath)
			if hash != "" && status != nil && strings.TrimSpace(status.LastSourceHash) == hash {
				work.Notes = append(work.Notes, "publish skipped: the published report is current")
			} else {
				work.Publish = true
			}
		}
	}
	return work
}

// publishHasReportTarget reports whether publish covers anything besides the
// Pulse page. No targets means the workflow's report.
func publishHasReportTarget(config *WorkflowPublishConfig) bool {
	if config == nil {
		return false
	}
	if len(config.Targets) == 0 {
		return true
	}
	for _, raw := range config.Targets {
		var name string
		if json.Unmarshal(raw, &name) == nil {
			if !strings.EqualFold(strings.TrimSpace(name), "pulse") {
				return true
			}
			continue
		}
		var object map[string]interface{}
		if json.Unmarshal(raw, &object) == nil {
			for _, key := range []string{"id", "name", "type", "kind"} {
				if value, ok := object[key].(string); ok && strings.EqualFold(strings.TrimSpace(value), "pulse") {
					object = nil
					break
				}
			}
			if object != nil {
				return true
			}
		}
	}
	return false
}

// runAfterRunOptions runs a scheduled run's after-run options. It never
// changes the run's recorded result; a failed housekeeping turn makes the
// scheduled job partial, as the basic finalizer did.
func (s *SchedulerService) runAfterRunOptions(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, status, errMsg, runFolder, sessionID, runID string, duration time.Duration) pulseLifecycleResult {
	options := effectiveScheduleAfterRun(sctx, manifest)
	result := pulseLifecycleNotRun
	if !options.Any() {
		return result
	}
	if strings.TrimSpace(runFolder) != "" && (options.Backup || options.Publish) {
		work := s.afterRunWorkDue(ctx, sctx, manifest, options)
		for _, note := range work.Notes {
			s.sessionLogf(sctx, sessionID, "[AFTER RUN] %s", note)
		}
		if work.Backup || work.Publish {
			sctx.AfterRunBackup, sctx.AfterRunPublish = work.Backup, work.Publish
			result = s.runPulseLifecycle(ctx, sctx, schedulePulseModeBasic, status, runFolder, sessionID, runID, errMsg)
		}
	}
	if options.Notify {
		if err := s.sendAfterRunNotification(ctx, sctx, manifest, runID, status, errMsg, duration); err != nil {
			s.sessionLogf(sctx, sessionID, "[AFTER RUN] run notification not sent: %v", err)
		}
	}
	return result
}

// afterRunNotificationArgs builds the code-only run summary.
func afterRunNotificationArgs(workflowName, scheduleName, status, errMsg string, duration time.Duration, external bool) map[string]interface{} {
	summaryStatus := "completed"
	verb := "finished"
	switch status {
	case "error", "failed":
		summaryStatus, verb = "failed", "failed"
		if strings.Contains(errMsg, "Workflow Review found problems") {
			summaryStatus, verb = "blocked", "did not start"
		}
	case "partial":
		summaryStatus, verb = "completed", "finished with warnings"
	case "stopped":
		summaryStatus, verb = "informational", "was stopped"
	}
	name := strings.TrimSpace(workflowName)
	if name == "" {
		name = "Workflow"
	}
	title := fmt.Sprintf("%s run %s", name, verb)
	message := title + "."
	if strings.TrimSpace(scheduleName) != "" {
		message = fmt.Sprintf("%s (%s) %s.", name, scheduleName, verb)
	}
	if errMsg = strings.TrimSpace(errMsg); errMsg != "" {
		if len(errMsg) > 600 {
			errMsg = errMsg[:600] + "…"
		}
		message += " " + errMsg
	}
	fields := []interface{}{map[string]interface{}{"label": "Status", "value": verb}}
	if strings.TrimSpace(scheduleName) != "" {
		fields = append(fields, map[string]interface{}{"label": "Schedule", "value": scheduleName})
	}
	if duration > 0 {
		fields = append(fields, map[string]interface{}{"label": "Duration", "value": duration.Round(time.Second).String()})
	}
	args := map[string]interface{}{
		"message_for_user":  message,
		"notification_kind": "run_summary",
		"summary_title":     title,
		"summary_status":    summaryStatus,
		"summary_fields":    fields,
		"email_subject":     title,
	}
	if !external {
		args["delivery_mode"] = "dashboard_only"
	}
	return args
}

// afterRunNotifyExternally is the code form of the finalizer's "new and
// important" default: a failure, or a status different from this schedule's
// previous run, goes to the channels; a routine repeat is recorded only.
func afterRunNotifyExternally(ctx context.Context, workspacePath, scheduleID, runID, status string) bool {
	if status != "success" {
		return true
	}
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return true
	}
	var previous *ScheduleRunEntry
	for i := range runs {
		run := runs[i]
		if run.ID == runID || run.ScheduleID != scheduleID || run.Status == "running" {
			continue
		}
		if previous == nil || run.StartedAt.After(previous.StartedAt) {
			previous = &runs[i]
		}
	}
	return previous == nil || previous.Status != status
}

// sendAfterRunNotification sends the run summary through the same path an
// agent's notify_user call takes, without an agent.
func (s *SchedulerService) sendAfterRunNotification(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, runID, status, errMsg string, duration time.Duration) error {
	if s == nil || s.api == nil || sctx == nil || manifest == nil {
		return fmt.Errorf("scheduler not available")
	}
	owner := strings.TrimSpace(sctx.OwnerUserID)
	req := QueryRequest{SelectedFolder: sctx.WorkspacePath}
	applyMultiAgentCapabilitiesToRequest(&req, manifest.Capabilities)
	s.api.resolveNotificationSecretForRequest(ctx, owner, sctx.WorkspacePath, &req)
	dest := notificationDestinationFromQuery(req, owner)
	if dest != nil && len(sctx.Schedule.RouteSelections) > 0 {
		dest.RouteSelections = sctx.Schedule.RouteSelections
	}
	nctx := context.WithValue(ctx, common.UserIDKey, owner)
	if dest != nil {
		nctx = context.WithValue(nctx, virtualtools.BotNotificationDestinationKey, dest)
	}
	notify := virtualtools.CreateHumanToolExecutors()["notify_user"]
	if notify == nil {
		return fmt.Errorf("notify_user is not available")
	}
	external := afterRunNotifyExternally(ctx, sctx.WorkspacePath, sctx.Schedule.ID, runID, status)
	_, err := notify(nctx, afterRunNotificationArgs(firstNonEmptyString(manifest.Label, sctx.WorkflowLabel), sctx.Schedule.Name, status, errMsg, duration, external))
	return err
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Manual runs

const manualAfterRunScheduleID = "manual-after-run"

var manualAfterRunInflight sync.Map // workspace path -> true

// runManualAfterRunOptions applies the workflow's manual-run options after a
// full run started from a chat. Scheduled runs are handled by the scheduler.
func (s *SchedulerService) runManualAfterRunOptions(workspacePath, runFolder, status, sessionID string) {
	if s == nil || s.api == nil || strings.TrimSpace(workspacePath) == "" {
		return
	}
	if isScheduledSessionIdentity(sessionID, "") || isWorkflowReviewSession(sessionID) {
		return
	}
	ctx := context.Background()
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return
	}
	options := manifest.EffectiveManualAfterRun()
	if !options.Any() {
		return
	}
	if _, busy := manualAfterRunInflight.LoadOrStore(workspacePath, true); busy {
		return
	}
	defer manualAfterRunInflight.Delete(workspacePath)

	sched := WorkflowSchedule{
		ID:           manualAfterRunScheduleID,
		Name:         "After-run options",
		Description:  "Backup, publish and notify after a manual run",
		ScheduleType: "cron",
		Timezone:     "UTC",
		Mode:         "workshop",
		WorkshopMode: "workshop",
	}
	sctx := buildScheduleContext(workspacePath, manifest, sched)
	sctx.TriggerSource = "manual"
	sctx.ProducedRunEvidence = true
	scheduleStatus := "success"
	if status != "completed" && status != "success" {
		scheduleStatus = "error"
	}
	if options.Backup || options.Publish {
		work := s.afterRunWorkDue(ctx, sctx, manifest, options)
		if work.Backup || work.Publish {
			sctx.AfterRunBackup, sctx.AfterRunPublish = work.Backup, work.Publish
			s.runPulseLifecycle(ctx, sctx, schedulePulseModeBasic, scheduleStatus, runFolder, "", runFolder, "")
		}
	}
	if options.Notify {
		sctx.Schedule.Name = ""
		_ = s.sendAfterRunNotification(ctx, sctx, manifest, "", scheduleStatus, "", 0)
	}
}

// runAfterRunNow starts the backup or publish pass now, outside a run: the
// same Pulse basic pass, in-flight guard and "anything to do" check as after
// a manual run. It reports why nothing started (not set up, nothing changed).
func (s *SchedulerService) runAfterRunNow(workspacePath, option string) (bool, []string, error) {
	if s == nil || s.api == nil {
		return false, nil, fmt.Errorf("the scheduler is not running")
	}
	ctx := context.Background()
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return false, nil, fmt.Errorf("workflow unavailable")
	}
	sched := WorkflowSchedule{ID: manualAfterRunScheduleID, Name: "After-run options", Description: "Backup or publish started now",
		ScheduleType: "cron", Timezone: "UTC", Mode: "workshop", WorkshopMode: "workshop"}
	sctx := buildScheduleContext(workspacePath, manifest, sched)
	sctx.TriggerSource = "manual"
	sctx.ProducedRunEvidence = true
	work := s.afterRunWorkDue(ctx, sctx, manifest, ScheduleAfterRun{Backup: option == "backup", Publish: option == "publish"})
	if !work.Backup && !work.Publish {
		return false, work.Notes, nil
	}
	if _, busy := manualAfterRunInflight.LoadOrStore(workspacePath, true); busy {
		return false, work.Notes, fmt.Errorf("a backup or publish pass is already running for this workflow")
	}
	sctx.AfterRunBackup, sctx.AfterRunPublish = work.Backup, work.Publish
	go func() {
		defer manualAfterRunInflight.Delete(workspacePath)
		s.runPulseLifecycle(context.Background(), sctx, schedulePulseModeBasic, "success", "", "", "", "")
	}()
	return true, work.Notes, nil
}
