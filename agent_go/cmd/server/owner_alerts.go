package server

import (
	"context"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"log"
	"strings"
	"time"
)

// gmailSendingInputID prefixes the one Needs you item a workflow keeps open
// while its Gmail notifications cannot be sent; it is refreshed, never
// duplicated, and a later outage opens a new one.
const gmailSendingInputID = "gmail-notifications-failing"

// pendingGmailSendingInputs lists the workflow's open failing-Gmail notices.
func pendingGmailSendingInputs(ctx context.Context, workspacePath string) []string {
	inputs, err := listReportHumanInputs(ctx, workspacePath, "pending", "goal")
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
	// The account-wide sender is the server's, which most Goal owners cannot
	// reconnect themselves; a Goal's own selected sender is theirs to fix.
	fix := "Reconnect the Gmail account this Goal sends from"
	if manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath); err == nil && exists && manifest != nil {
		if n := manifest.Capabilities.Notifications; n == nil || strings.TrimSpace(n.GmailConnectionID) == "" {
			fix = "The server's notification email account needs reconnecting; ask your administrator"
		}
	}
	id := gmailSendingInputID + "-" + time.Now().UTC().Format("20060102-150405")
	if open := pendingGmailSendingInputs(ctx, workspacePath); len(open) > 0 {
		id = open[0]
	}
	_, err := createReportHumanInput(ctx, workspacePath, ReportHumanInputCreateRequest{
		WorkspacePath: workspacePath,
		InputID:       id,
		Source:        "goal",
		Priority:      "high",
		Question:      "Email notifications from this Goal are failing. Can you get its Gmail sender reconnected?",
		Context:       "Configured team emails are not being sent because " + reason + ". They are still recorded in the app. " + fix + "; this notice clears itself after the next email is sent.",
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
