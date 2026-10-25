package virtualtools

import (
	"context"
	"fmt"
	"strings"

	llmtypes "github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const recordSummaryDescription = "Record this workflow run's outcome in Activity and optionally deliver it to the saved team channels and recipients. Supply kind=run_summary, a plain message, title, status, fields and sections. Recipients, channels and webhooks come from workflow settings. Empty run_summary_recipients means no summary email. Use delivery=record_only to record without sending, or deliver_only for a run outcome already recorded. AgentWorks handles progress reports separately. Rich channel content can use email_subject/email_html and Slack fields. To contact specific people directly use send_email or send_slack_message."

func createRecordSummaryTool(summaryContentProps map[string]interface{}) llmtypes.Tool {
	props := map[string]interface{}{
		"kind": map[string]interface{}{
			"type": "string", "enum": []string{"run_summary"},
			"description": "run_summary for the execution outcome of a run. Selects the saved channels and recipients.",
		},
		"delivery": map[string]interface{}{
			"type": "string", "enum": []string{"record_and_deliver", "record_only", "deliver_only"},
			"description": "record_and_deliver (default): record in Activity and deliver to the saved channels. record_only: record without emailing or posting (an unchanged run). deliver_only: deliver without a new Activity item (a run outcome already recorded).",
		},
	}
	// Reuse the summary content schemas under shorter names.
	for newName, oldName := range map[string]string{
		"message": "message_for_user", "title": "summary_title", "status": "summary_status", "fields": "summary_fields",
		"sections": "summary_sections", "route": "summary_route", "routes": "summary_routes",
		"attachments": "email_attachments",
		"slack_title": "slack_title", "slack_color": "slack_color", "slack_fields": "slack_fields",
		"slack_sections": "slack_sections", "slack_footer": "slack_footer",
		"email_subject": "email_subject", "email_html": "email_html", "email_html_file": "email_html_file",
	} {
		if schema, ok := summaryContentProps[oldName]; ok {
			if fields, ok := schema.(map[string]interface{}); ok {
				copy := make(map[string]interface{}, len(fields))
				for key, value := range fields {
					copy[key] = value
				}
				if description, ok := copy["description"].(string); ok {
					copy["description"] = strings.NewReplacer("message_for_user", "message", "summary_title", "title", "summary_status", "status", "summary_fields", "fields", "summary_sections", "sections").Replace(description)
				}
				props[newName] = copy
			} else {
				props[newName] = schema
			}
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
	if kind != "run_summary" {
		return "", fmt.Errorf("kind must be run_summary")
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
	return DeliverWorkflowNotification(ctx, mapped)
}
