package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

// Owner alerts (owner, 2026-10-24). Routine successful runs are recorded in the
// app only (after_run.go), so on 23-24 Oct a Goal whose sending had stopped
// reached nobody: its runs "succeeded" with nothing sent, and the decision
// asking the owner to act sat in Needs you. Two things now go to the
// workflow's saved channels: a decision that says the Goal's work has
// stopped, and one short daily digest of the day's runs.

// workflowSummaryContext carries a workflow's saved notification destination,
// the same one its after-run summaries use.
func (api *StreamingAPI) workflowSummaryContext(ctx context.Context, workspacePath, owner string, manifest *WorkflowManifest) context.Context {
	req := QueryRequest{SelectedFolder: workspacePath, notificationItemWorkspace: personalConnectionItemRoot(owner, workspacePath)}
	applyMultiAgentCapabilitiesToRequest(&req, manifest.Capabilities)
	api.resolveNotificationSecretForRequest(ctx, owner, workspacePath, &req)
	nctx := context.WithValue(ctx, common.UserIDKey, owner)
	if dest := notificationDestinationFromQuery(req, owner); dest != nil {
		nctx = context.WithValue(nctx, virtualtools.BotNotificationDestinationKey, dest)
	}
	return nctx
}

// alertOwnerWorkStopped emails a new work-stopped decision to the workflow's
// saved channels. Refreshing the same open decision does not send again.
func alertOwnerWorkStopped(ctx context.Context, input *ReportHumanInput) {
	api := runningServerAPI
	if api == nil || input == nil || input.Status != "pending" || input.CreatedAt != input.UpdatedAt {
		return
	}
	manifest, exists, err := ReadWorkflowManifest(ctx, input.WorkspacePath)
	if err != nil || !exists || manifest == nil {
		return
	}
	name := workflowDisplayLabel(input.WorkspacePath, manifest)
	title := name + ": work has stopped until you answer"
	message := strings.TrimSpace(input.Question)
	if c := strings.TrimSpace(input.Context); c != "" {
		message += "\n\n" + c
	}
	footer := "Answer it in AgentWorks: open " + name + ", then Needs you."
	htmlBody := ownerEmailHTML(title, message, nil, nil, footer)
	message += "\n\n" + footer
	args := map[string]interface{}{
		"email_html":        htmlBody,
		"message_for_user":  message,
		"notification_kind": "pulse_summary",
		"delivery_mode":     "external_only",
		"summary_title":     title,
		"summary_status":    "blocked",
		"email_subject":     title,
	}
	nctx := api.workflowSummaryContext(context.WithoutCancel(ctx), input.WorkspacePath, workflowExecutionOwnerUserID(manifest), manifest)
	// A slow mail or Slack send must not hold up the Builder turn that raised it.
	go func() {
		if _, err := virtualtools.DeliverWorkflowNotification(nctx, args); err != nil {
			log.Printf("[OWNER_ALERT] work-stopped decision %s in %s not sent: %v", input.ID, input.WorkspacePath, err)
		}
	}()
}

// dailyDigestHour is the local hour (in the workflow's schedule timezone) after
// which the day's digest is sent.
const dailyDigestHour = 20

type dailyDigestRun struct {
	title, status, message string
}

// dailyDigestArgs builds the one digest of a day's run summaries, or nil when
// the workflow recorded no runs that day.
func dailyDigestArgs(workflowName, day string, runs []dailyDigestRun) map[string]interface{} {
	if len(runs) == 0 {
		return nil
	}
	counts := map[string]int{}
	lines := make([]string, 0, len(runs))
	htmlLines := make([]ownerEmailLine, 0, len(runs))
	for _, run := range runs {
		counts[run.status]++
		line := run.title
		if msg := strings.TrimSpace(run.message); msg != "" && msg != run.title+"." {
			if len(msg) > 200 {
				msg = msg[:200] + "…"
			}
			line += ": " + msg
		}
		lines = append(lines, "- "+line)
		htmlLines = append(htmlLines, ownerEmailLine{status: run.status, text: line})
	}
	failed := counts["failed"] + counts["blocked"]
	headline := fmt.Sprintf("%s today: %d runs", workflowName, len(runs))
	if len(runs) >= 50 {
		// The activity feed keeps the latest 50 summaries per kind.
		headline = fmt.Sprintf("%s today: latest %d runs", workflowName, len(runs))
	}
	if failed > 0 {
		headline += fmt.Sprintf(", %d failed or blocked", failed)
	}
	status := "completed"
	if failed > 0 {
		status = "failed"
	}
	return map[string]interface{}{
		"message_for_user":  headline + ".\n\n" + strings.Join(lines, "\n"),
		"notification_kind": "run_summary",
		"delivery_mode":     "external_only",
		"summary_title":     headline,
		"summary_status":    status,
		"summary_fields": []interface{}{
			map[string]interface{}{"label": "Day", "value": day},
			map[string]interface{}{"label": "Runs", "value": fmt.Sprint(len(runs))},
		},
		"email_subject": headline,
		"email_html": ownerEmailHTML(headline, "", [][2]string{{"Day", day}, {"Runs", fmt.Sprint(len(runs))}}, htmlLines,
			"Turn this daily email off in the Goal's Notify settings, or ask its chat to turn off the daily digest."),
	}
}

// dailyDigestRuns reads the run summaries the workflow recorded on day (local).
func dailyDigestRuns(ctx context.Context, workspacePath, day string, loc *time.Location) ([]dailyDigestRun, error) {
	dbPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath), "db", "db.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(dbPath)+"&mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	start, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(title,''), COALESCE(status,''), COALESCE(message,''), created_at
		FROM org_dashboard_notifications WHERE notification_kind = 'run_summary' AND created_at >= ? AND created_at < ? ORDER BY created_at`,
		start.UTC().Add(-time.Hour).Format(time.RFC3339), start.UTC().Add(25*time.Hour).Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var runs []dailyDigestRun
	for rows.Next() {
		var run dailyDigestRun
		var createdAt string
		if err := rows.Scan(&run.title, &run.status, &run.message, &createdAt); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil || at.In(loc).Format("2006-01-02") != day {
			continue
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// dailyDigestStampPath records the last day a workflow's digest was sent. It is
// server state, not workflow content, so it never travels with exports or copies.
func dailyDigestStampPath(workspacePath string) string {
	name := strings.NewReplacer("/", "__", "\\", "__").Replace(strings.Trim(filepath.ToSlash(workspacePath), "/"))
	return filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "daily-digest", name+".txt")
}

var dailyDigestMu sync.Mutex

// sendDueDailyDigests sends each scheduled workflow's digest once a day, after
// dailyDigestHour in the timezone of its schedules.
func (s *SchedulerService) sendDueDailyDigests(ctx context.Context, now time.Time) {
	if s == nil || s.api == nil || !dailyDigestMu.TryLock() {
		return
	}
	defer dailyDigestMu.Unlock()
	s.mu.Lock()
	zones := map[string]string{}
	for _, job := range s.jobs {
		if job.sctx == nil || job.sctx.WorkspacePath == "" {
			continue
		}
		if _, seen := zones[job.sctx.WorkspacePath]; !seen || zones[job.sctx.WorkspacePath] == "" {
			zones[job.sctx.WorkspacePath] = job.sctx.Schedule.Timezone
		}
	}
	s.mu.Unlock()
	for workspacePath, zone := range zones {
		loc, err := time.LoadLocation(zone)
		if err != nil || zone == "" {
			loc = time.Local
		}
		local := now.In(loc)
		day := local.Format("2006-01-02")
		if local.Hour() < dailyDigestHour {
			continue
		}
		stamp := dailyDigestStampPath(workspacePath)
		if sent, _ := os.ReadFile(stamp); strings.TrimSpace(string(sent)) == day {
			continue
		}
		manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
		if err != nil || !exists || manifest == nil {
			continue
		}
		if n := manifest.Capabilities.Notifications; n != nil && n.DailyDigestOff {
			continue
		}
		runs, err := dailyDigestRuns(ctx, workspacePath, day, loc)
		if err != nil {
			log.Printf("[DAILY_DIGEST] %s: %v", workspacePath, err)
			continue
		}
		// Mark the day first so a failing channel is not retried every tick.
		_ = os.MkdirAll(filepath.Dir(stamp), 0o755)
		if err := os.WriteFile(stamp, []byte(day+"\n"), 0o644); err != nil {
			continue
		}
		args := dailyDigestArgs(workflowDisplayLabel(workspacePath, manifest), day, runs)
		if args == nil {
			continue
		}
		nctx := s.api.workflowSummaryContext(ctx, workspacePath, workflowExecutionOwnerUserID(manifest), manifest)
		if _, err := virtualtools.DeliverWorkflowNotification(nctx, args); err != nil {
			log.Printf("[DAILY_DIGEST] %s not sent: %v", workspacePath, err)
		}
	}
}

// gmailSendingInputID prefixes the one Needs you item a workflow keeps open
// while its Gmail notifications cannot be sent; it is refreshed, never
// duplicated, and a later outage opens a new one.
const gmailSendingInputID = "gmail-notifications-failing"

// pendingGmailSendingInputs lists the workflow's open failing-Gmail notices.
func pendingGmailSendingInputs(ctx context.Context, workspacePath string) []string {
	inputs, err := listReportHumanInputs(ctx, workspacePath, "pending", "pulse")
	if err != nil {
		return nil
	}
	var ids []string
	for _, input := range inputs {
		if strings.HasPrefix(input.ID, gmailSendingInputID) {
			ids = append(ids, input.ID)
		}
	}
	return ids
}

func init() {
	virtualtools.NotificationGmailFailed = recordGmailSendingFailure
	virtualtools.NotificationGmailDelivered = clearGmailSendingFailure
}

// recordGmailSendingFailure puts failing Gmail notifications in the
// workflow's Needs you, because the failed email cannot say so itself
// (sends failed silently for two days on 23-24 Oct with an expired sign-in).
func recordGmailSendingFailure(ctx context.Context, workspacePath, errText string) {
	reason := "the email could not be sent"
	lower := strings.ToLower(errText)
	if strings.Contains(lower, "invalid_grant") || strings.Contains(lower, "expired or revoked") || strings.Contains(lower, "auth add") {
		reason = "the Google sign-in of the sending Gmail account has expired"
	}
	id := gmailSendingInputID + "-" + time.Now().UTC().Format("20060102-150405")
	if open := pendingGmailSendingInputs(ctx, workspacePath); len(open) > 0 {
		id = open[0]
	}
	_, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		WorkspacePath: workspacePath,
		InputID:       id,
		Source:        "pulse",
		Priority:      "high",
		Question:      "Email notifications from this Goal are failing. Can you reconnect its Gmail account?",
		Context:       "Run summaries, daily digests and alerts are not reaching your inbox because " + reason + ". They are still recorded in the app. Reconnect the Gmail account used for notifications; this notice clears itself after the next email is sent.",
		AllowFreeText: true,
		CreatedBy:     "platform",
		CreatedByKind: "system",
		CreatedVia:    "notification_delivery",
	})
	if err != nil {
		log.Printf("[OWNER_ALERT] could not record failing Gmail notifications for %s: %v", workspacePath, err)
	}
}

// clearGmailSendingFailure dismisses the notice once an email goes through.
func clearGmailSendingFailure(ctx context.Context, workspacePath string) {
	for _, id := range pendingGmailSendingInputs(ctx, workspacePath) {
		_, _ = dismissReportHumanInput(ctx, workspacePath, id, ReportHumanInputAnswerRequest{
			Note: "Email notifications are being sent again.", AnsweredBy: "platform", AnsweredByKind: "system", AnsweredVia: "notification_delivery",
		})
	}
}
