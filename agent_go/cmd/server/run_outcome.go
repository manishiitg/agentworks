package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Run outcomes: a code-only summary goes to the configured channels and wakes
// the linked Crew chat. No agent turn runs after a run.

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
	// Correlate the delivery decision with the durable Activity entry without
	// logging message content, addresses, webhook values or provider errors.
	s.logf(sctx, "[RUN STATUS] run=%s schedule=%s outcome=%s external_delivery=%t finished=%t", runID, sctx.Schedule.ID, status, external, err == nil)
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

// reportManualRunOutcome sends the outcome summary for a full run started from
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
	_ = s.sendAfterRunNotification(ctx, sctx, manifest, runFolder, scheduleStatus, "", 0)
}
