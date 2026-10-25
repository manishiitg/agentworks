package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// After-run options run backup and publish after ordinary Goal runs.
// Notifications use the configured channels and recipients. Backup and publish
// start a short provider turn only when their source files have changed.

// ScheduleAfterRun is a schedule's after-run options.
type ScheduleAfterRun struct {
	Backup  bool `json:"backup"`
	Publish bool `json:"publish"`
}

// Any reports whether any option is on.
func (a ScheduleAfterRun) Any() bool { return a.Backup || a.Publish }

// EffectiveAfterRun reads the canonical housekeeping settings. The raw manifest
// stores explicit choices; the one-off data migration preserves old choices.
func (m *WorkflowManifest) EffectiveAfterRun(schedule WorkflowSchedule) ScheduleAfterRun {
	if m == nil || m.Kind == "relay" || strings.EqualFold(strings.TrimSpace(schedule.ScheduleType), "webhook") || schedule.AfterRun == nil {
		return ScheduleAfterRun{}
	}
	return *schedule.AfterRun
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

func setScheduleAfterRun(schedule *WorkflowSchedule, options ScheduleAfterRun) {
	copied := options
	schedule.AfterRun = &copied
}

// applyScheduleAfterRunPolicy defaults new schedules to backup and publish.
func applyScheduleAfterRunPolicy(schedule *WorkflowSchedule, policy stepworkflow.ScheduleRuntimePolicy, creating bool) {
	if schedule == nil {
		return
	}
	switch {
	case policy.SetAfterRun:
		setScheduleAfterRun(schedule, ScheduleAfterRun{Backup: policy.AfterRun.Backup, Publish: policy.AfterRun.Publish})
	case creating:
		setScheduleAfterRun(schedule, ScheduleAfterRun{Backup: true, Publish: true})
	}
}

// effectiveScheduleAfterRun reads the schedule from the freshly read manifest,
// so a change saved during a run applies when that run finishes.
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

// runAfterRunOptions runs a scheduled run's after-run options. It never
// changes the run's recorded result; a failed housekeeping turn makes the
// scheduled job partial, as the basic finalizer did.
func (s *SchedulerService) runAfterRunOptions(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, status, errMsg, runFolder, sessionID, runID string, duration time.Duration) afterRunResult {
	options := effectiveScheduleAfterRun(sctx, manifest)
	result := afterRunNotRun
	if strings.TrimSpace(runFolder) != "" && (options.Backup || options.Publish) {
		work := s.afterRunWorkDue(ctx, sctx, manifest, options)
		for _, note := range work.Notes {
			s.sessionLogf(sctx, sessionID, "[AFTER RUN] %s", note)
		}
		if work.Backup || work.Publish {
			sctx.AfterRunBackup, sctx.AfterRunPublish = work.Backup, work.Publish
			result = s.runAfterRunHousekeeping(ctx, sctx, status, runFolder, sessionID, runID)
		}
	}
	return result
}

// afterRunNotificationArgs builds the code-only run summary.
func afterRunNotificationArgs(workflowName, scheduleName, status, errMsg string, duration time.Duration, external bool) map[string]interface{} {
	summaryStatus := "completed"
	verb := "finished"
	switch status {
	case "blocked", "no_run":
		summaryStatus, verb = status, "did not start"
	case "error", "failed":
		summaryStatus, verb = "failed", "failed"
		if strings.Contains(errMsg, "Workflow Review found problems") {
			summaryStatus, verb = "blocked", "did not start"
		}
	case "partial":
		summaryStatus, verb = "partial", "finished with warnings"
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
	htmlFields := [][2]string{{"Status", verb}}
	if strings.TrimSpace(scheduleName) != "" {
		htmlFields = append(htmlFields, [2]string{"Schedule", scheduleName})
	}
	if duration > 0 {
		htmlFields = append(htmlFields, [2]string{"Duration", duration.Round(time.Second).String()})
	}
	args := map[string]interface{}{
		"email_html":        ownerEmailHTML(title, message, htmlFields, nil, ""),
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

// reportScheduledRunOutcome records the same outcome used to wake the linked
// Crew. Needs-you reads that durable status; it creates no question or decision.
func (s *SchedulerService) reportScheduledRunOutcome(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, runID, status, errMsg string, duration time.Duration) {
	reason := wakeReasonForRun(status, sctx.ProducedRunEvidence)
	s.wakeAgentTab(ctx, sctx, manifest, reason, errMsg, runID)
	notificationStatus := status
	if status == "success" && reason != "" {
		notificationStatus = "no_run"
	}
	if err := s.sendAfterRunNotification(ctx, sctx, manifest, runID, notificationStatus, errMsg, duration); err != nil {
		s.logf(sctx, "[RUN STATUS] run=%s outcome could not be recorded: %v", runID, err)
	}
}

// shouldDeliverRunOutcome keeps routine repeated successes in Activity.
// The saved team channels receive failures and changes in scheduled status.
func shouldDeliverRunOutcome(ctx context.Context, workspacePath, scheduleID, runID, status string) bool {
	if wakeReasonForRun(status, true) != "" {
		return true
	}
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return true
	}
	var previous *ScheduleRunEntry
	for i := range runs {
		run := &runs[i]
		if run.ID == runID || run.ScheduleID != scheduleID || run.Status == "running" {
			continue
		}
		if previous == nil || run.StartedAt.After(previous.StartedAt) {
			previous = run
		}
	}
	return previous == nil || previous.Status != status
}

// sendAfterRunNotification uses the internal summary delivery path without an agent.
func (s *SchedulerService) sendAfterRunNotification(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, runID, status, errMsg string, duration time.Duration) error {
	if s == nil || s.api == nil || sctx == nil || manifest == nil {
		return fmt.Errorf("scheduler not available")
	}
	owner := strings.TrimSpace(sctx.OwnerUserID)
	req := QueryRequest{SelectedFolder: sctx.WorkspacePath, notificationItemWorkspace: personalConnectionItemRoot(owner, sctx.WorkspacePath)}
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
	// Manual outcomes are recorded only; explicit workflow/team messages remain independent.
	external := sctx.TriggerSource != "manual" && shouldDeliverRunOutcome(ctx, sctx.WorkspacePath, sctx.Schedule.ID, runID, status)
	_, err := virtualtools.DeliverWorkflowNotification(nctx, afterRunNotificationArgs(firstNonEmptyString(manifest.Label, sctx.WorkflowLabel), sctx.Schedule.Name, status, errMsg, duration, external))
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
	if isScheduledSessionIdentity(sessionID, "") {
		return
	}
	ctx := context.Background()
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return
	}
	options := manifest.EffectiveManualAfterRun()
	if _, busy := manualAfterRunInflight.LoadOrStore(workspacePath, true); busy {
		return
	}
	defer manualAfterRunInflight.Delete(workspacePath)

	sched := WorkflowSchedule{
		ID:           manualAfterRunScheduleID,
		Name:         "After-run options",
		Description:  "Backup and publish after a manual run",
		ScheduleType: "cron",
		Timezone:     "UTC",
		Mode:         "workshop",
		WorkshopMode: "workshop",
	}
	sctx := buildScheduleContext(workspacePath, manifest, sched)
	sctx.TriggerSource = "manual"
	sctx.ProducedRunEvidence = true
	scheduleStatus := status
	switch status {
	case "completed", "success":
		scheduleStatus = "success"
	case "partial", "stopped", "interrupted":
	default:
		scheduleStatus = "error"
	}
	if options.Backup || options.Publish {
		work := s.afterRunWorkDue(ctx, sctx, manifest, options)
		if work.Backup || work.Publish {
			sctx.AfterRunBackup, sctx.AfterRunPublish = work.Backup, work.Publish
			result := s.runAfterRunHousekeeping(ctx, sctx, scheduleStatus, runFolder, "", runFolder)
			if result == afterRunStopped {
				scheduleStatus = "stopped"
			} else if result == afterRunPartial && scheduleStatus == "success" {
				scheduleStatus = "partial"
			}
		}
	}
	sctx.Schedule.Name = ""
	_ = s.sendAfterRunNotification(ctx, sctx, manifest, runFolder, scheduleStatus, "", 0)
}

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
		s.runAfterRunHousekeeping(context.Background(), sctx, "success", "", "", "")
	}()
	return true, work.Notes, nil
}
