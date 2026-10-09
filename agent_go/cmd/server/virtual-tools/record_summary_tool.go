package virtualtools

import (
	"context"
	"fmt"
	"strings"

	llmtypes "github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// record_summary records what a workflow run or a Pulse review did, and the server delivers it. It replaces the
// summary use of notify_user (PLAT-736): the agent supplies content (verdict, fields, sections, optional rich email
// and Slack renderings); it cannot name recipients, channels or webhooks. The Activity record and the delivery to the
// workflow's saved channels, recipients and webhooks (by kind) are the existing notify_user summary path, unchanged,
// so a summary is still recorded when nothing is configured to receive it.

const recordSummaryDescription = "Record what this workflow run or Pulse review did, in the Activity feed, and deliver it to the workflow's saved channels and recipients automatically. You supply the content only: `kind` (run_summary for the execution outcome, pulse_summary for Pulse review and fix activity), a short plain-text `message` (the verdict), a `title` and `status`, and structured `fields` and `sections` (use the labels the workflow's run-summary instructions name). You cannot choose recipients, channels or webhooks: those come from the workflow's saved settings (notifications.run_summary_channels, run_summary_recipients, pulse_summary_recipients). For an unchanged run that should be recorded without notifying anyone, set `delivery` to record_only; for a Pulse update whose results are already in Activity, set it to deliver_only. Read prior summaries first with get_notification_history so you do not repeat an unchanged status. Rich renderings: `email_subject` with one inline-styled `email_html` body, and `slack_title`/`slack_color`/`slack_fields`/`slack_sections`. Reports a receipt per channel. To email or Slack specific people, use send_email or send_slack_message instead."

func createRecordSummaryTool(notifyProps map[string]interface{}) llmtypes.Tool {
	props := map[string]interface{}{
		"kind": map[string]interface{}{
			"type": "string", "enum": []string{"run_summary", "pulse_summary"},
			"description": "run_summary for the execution outcome of a run; pulse_summary for Pulse review, fix and goal activity. Selects the saved channels and recipients.",
		},
		"delivery": map[string]interface{}{
			"type": "string", "enum": []string{"record_and_deliver", "record_only", "deliver_only"},
			"description": "record_and_deliver (default): record in Activity and deliver to the saved channels. record_only: record without emailing or posting (an unchanged run). deliver_only: deliver without a new Activity item (a Pulse update whose results are already projected).",
		},
	}
	// Reuse notify_user's content schemas under shorter names.
	for newName, oldName := range map[string]string{
		"message": "message_for_user", "title": "summary_title", "status": "summary_status", "fields": "summary_fields",
		"sections": "summary_sections", "route": "summary_route", "routes": "summary_routes",
		"attachments": "email_attachments",
		"slack_title": "slack_title", "slack_color": "slack_color", "slack_fields": "slack_fields",
		"slack_sections": "slack_sections", "slack_footer": "slack_footer",
		"email_subject": "email_subject", "email_html": "email_html", "email_html_file": "email_html_file",
	} {
		if schema, ok := notifyProps[oldName]; ok {
			props[newName] = schema
		}
	}
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "record_summary",
			Description: recordSummaryDescription,
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   []string{"kind", "message"},
			}),
		},
	}
}

// recordSummaryRenames maps record_summary's argument names to the notify pipeline's.
var recordSummaryRenames = map[string]string{
	"message": "message_for_user", "title": "summary_title", "status": "summary_status", "fields": "summary_fields",
	"sections": "summary_sections", "route": "summary_route", "routes": "summary_routes", "attachments": "email_attachments",
}

func handleRecordSummary(ctx context.Context, args map[string]interface{}) (string, error) {
	for _, refused := range []string{"email_to", "email_cc", "block_recipients", "exclude_channels", "delivery_mode", "notification_kind", "message_for_user", "record_only"} {
		if _, ok := args[refused]; ok {
			return "", fmt.Errorf("%s is not accepted by record_summary: recipients, channels and webhooks come from the workflow's saved notification settings. To email or Slack specific people use send_email or send_slack_message", refused)
		}
	}
	kind, _ := args["kind"].(string)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "run_summary" && kind != "pulse_summary" {
		return "", fmt.Errorf("kind must be run_summary or pulse_summary")
	}
	mapped := map[string]interface{}{"notification_kind": kind}
	for name, value := range args {
		switch name {
		case "kind", "delivery":
			continue
		}
		if renamed, ok := recordSummaryRenames[name]; ok {
			mapped[renamed] = value
			continue
		}
		mapped[name] = value
	}
	delivery, _ := args["delivery"].(string)
	switch strings.ToLower(strings.TrimSpace(delivery)) {
	case "", "record_and_deliver":
	case "record_only":
		mapped["delivery_mode"] = "dashboard_only"
	case "deliver_only":
		mapped["delivery_mode"] = "external_only"
	default:
		return "", fmt.Errorf("delivery must be record_and_deliver, record_only or deliver_only")
	}
	return handleNotifyUser(ctx, mapped)
}
