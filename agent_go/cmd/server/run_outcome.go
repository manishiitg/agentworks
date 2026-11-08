package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Run outcomes: the run record is the summary (PLAT-746, decision "no
// run-summary concept"). An outcome is written to the workflow's Activity and
// wakes the linked Crew chat; nothing is sent to Slack, Gmail or WhatsApp. A
// workflow that must alert people calls send_email or send_slack itself.

// runOutcomeSummary builds the code-only Activity record for one run.
func runOutcomeSummary(workflowName, scheduleName, status, errMsg string, duration time.Duration) (string, *services.NotificationSummary) {
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
	fields := []services.NotificationSummaryField{{Label: "Status", Value: verb}}
	if strings.TrimSpace(scheduleName) != "" {
		fields = append(fields, services.NotificationSummaryField{Label: "Schedule", Value: scheduleName})
	}
	if duration > 0 {
		fields = append(fields, services.NotificationSummaryField{Label: "Duration", Value: duration.Round(time.Second).String()})
	}
	return message, &services.NotificationSummary{Kind: "run_summary", Title: title, Status: summaryStatus, Fields: fields}
}

// reportScheduledRunOutcome records the same outcome used to wake the linked
// Crew. Needs-you reads that durable status; it creates no question or decision.
func (s *SchedulerService) reportScheduledRunOutcome(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, runID, status, errMsg string, duration time.Duration) {
	reason := wakeReasonForRun(status, sctx.ProducedRunEvidence)
	s.wakeAgentTab(ctx, sctx, manifest, reason, errMsg, runID)
	outcomeStatus := status
	if status == "success" && reason != "" {
		outcomeStatus = "no_run"
	}
	if err := s.recordRunOutcome(ctx, sctx, manifest, runID, outcomeStatus, errMsg, duration); err != nil {
		s.logf(sctx, "[RUN STATUS] run=%s outcome could not be recorded: %v", runID, err)
	}
}

// recordRunOutcome writes one run outcome to the workflow's Activity.
func (s *SchedulerService) recordRunOutcome(ctx context.Context, sctx *ScheduleContext, manifest *WorkflowManifest, runID, status, errMsg string, duration time.Duration) error {
	if s == nil || sctx == nil || manifest == nil {
		return fmt.Errorf("scheduler not available")
	}
	message, summary := runOutcomeSummary(firstNonEmptyString(manifest.Label, sctx.WorkflowLabel), sctx.Schedule.Name, status, errMsg, duration)
	dest := &services.NotificationDestination{
		UserID:        strings.TrimSpace(sctx.OwnerUserID),
		WorkspacePath: sctx.WorkspacePath,
		Content:       &services.NotificationContent{Text: message, Summary: summary},
	}
	_, err := services.NewOrgDashboardConnector().SendUserNotification(ctx, message, "", dest)
	s.logf(sctx, "[RUN STATUS] run=%s schedule=%s outcome=%s recorded=%t", runID, sctx.Schedule.ID, status, err == nil)
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

// reportManualRunOutcome records the outcome of a full run started from
// a chat. Scheduled runs are reported by the scheduler.
func (s *SchedulerService) reportManualRunOutcome(workspacePath, runFolder, status, sessionID string) {
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
	if _, busy := manualAfterRunInflight.LoadOrStore(workspacePath, true); busy {
		return
	}
	defer manualAfterRunInflight.Delete(workspacePath)

	sched := WorkflowSchedule{
		ID:           manualAfterRunScheduleID,
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
	_ = s.recordRunOutcome(ctx, sctx, manifest, runFolder, scheduleStatus, "", 0)
}
