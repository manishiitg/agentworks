package virtualtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlstd "html"
	"maps"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// CreateHumanTools creates human interaction tools
func CreateHumanTools() []llmtypes.Tool {
	var humanTools []llmtypes.Tool

	// Add human_feedback tool
	humanFeedbackTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "human_feedback",
			Description: "Request urgent, short-lived input that only a human can provide, such as an OTP/2FA code, CAPTCHA completion, explicit approval, a subjective decision, private information, or an explicit test of the human-feedback channel. This tool pauses only the calling agent turn until the human answers directly in the AgentWorks UI. It never sends through notify_user, Gmail, workflow webhooks, or account-level notification connectors. Do not use it for an ordinary Builder/chat question, something another agent can answer, or something that may wait hours or days. Choose the shortest realistic timeout_seconds; use an expiry shown by the external service when available. The tool returns the human's response as text. Bridge-only coding CLIs invoking the HTTP endpoint through execute_shell_command must keep curl in the foreground and wait for the same call to return; never use nohup, append &, delegate/background it, write the result to a temporary file, poll for completion, or ask the user to send another message after responding. Do not set the shell timeout shorter than timeout_seconds. Cursor CLI has an approximately 60-second silent MCP-call ceiling, so Cursor agents must set timeout_seconds to at most 45 seconds and may retry after an explicit expiry only when the input is still required.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"message_for_user": map[string]interface{}{
						"type":        "string",
						"description": "Message to display to the user requesting their feedback",
					},
					"unique_id": map[string]interface{}{
						"type":        "string",
						"description": "Unique identifier for this feedback request. Always generate a UUID (e.g., '550e8400-e29b-41d4-a716-446655440000').",
					},
					"options": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of choices to present as buttons. When provided, the user clicks a button instead of typing. Use for multiple-choice questions (e.g. ['Option A: Use REST API', 'Option B: Use GraphQL', 'Option C: Use gRPC']). Omit for free-text input.",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"minimum":     30,
						"maximum":     1800,
						"default":     300,
						"description": "How long to wait for the human before the request expires. Choose the shortest realistic duration. Defaults to 300 seconds and is bounded to 30-1800 seconds.",
					},
				},
				"required": []string{"unique_id", "message_for_user"},
			}),
		},
	}
	humanTools = append(humanTools, humanFeedbackTool)

	// The email_* fields are exposed only when Gmail is an enabled channel, so
	// the agent doesn't see email-specific knobs it can't use.
	notifyProps := map[string]interface{}{
		"message_for_user": map[string]interface{}{
			"type":        "string",
			"description": "Concise plain-text summary sent to every channel and used as the automatic email fallback. When supplying rich Slack or email fields, make this the lead verdict rather than duplicating every detail.",
		},
		"slack_title": map[string]interface{}{
			"type":        "string",
			"maxLength":   150,
			"description": "Optional Block Kit header. Use by default for workflow, Pulse, Goal Advisor, and other structured summaries. The backend owns the webhook URL and renders this safely; never post to a webhook directly.",
		},
		"slack_color": map[string]interface{}{
			"type":        "string",
			"enum":        []string{"neutral", "success", "warning", "danger"},
			"description": "Block Kit accent color chosen from the factual outcome: success only when healthy, warning for incomplete/blocked, danger for confirmed failure, otherwise neutral.",
		},
		"slack_fields": map[string]interface{}{
			"type":     "array",
			"maxItems": 10,
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label": map[string]interface{}{"type": "string"},
					"value": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"label", "value"},
				"additionalProperties": false,
			},
			"description": "Optional compact Block Kit metric fields. Use for counts/statuses. Other channels ignore these fields.",
		},
		"slack_sections": map[string]interface{}{
			"type":     "array",
			"maxItems": 12,
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"heading": map[string]interface{}{"type": "string"},
					"body":    map[string]interface{}{"type": "string"},
				},
				"required":             []string{"heading", "body"},
				"additionalProperties": false,
			},
			"description": "Optional ordered Block Kit sections for changed areas, findings, issues, blockers, or next actions. Slack mrkdwn and <url|label> links are supported. Other channels ignore these fields.",
		},
		"slack_footer": map[string]interface{}{
			"type":        "string",
			"maxLength":   2000,
			"description": "Optional short Block Kit context/footer such as scope and date. Other channels ignore this field.",
		},
		"exclude_channels": map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string", "enum": []string{"gmail", "slack", "whatsapp"}},
			"description": "Optional one-off override to SKIP delivery channels for THIS notification only, by name (\"gmail\", \"slack\", \"whatsapp\"). The DURABLE per-workflow preference belongs in workflow.json notifications.exclude_channels and is applied automatically on every send — use this arg only for a one-time skip beyond that. Suppresses the channel for this send only; never changes the account-wide configuration. Omit to deliver to every enabled channel not already excluded by workflow.json. Excluding slack suppresses BOTH account Slack and every workflow Slack webhook. A workflow Slack webhook replaces the global Slack destination; it does not send an extra copy. The always-on web UI is unaffected.",
		},
		"delivery_mode": map[string]interface{}{
			"type":        "string",
			"enum":        []string{"all", "dashboard_only", "external_only"},
			"description": "Delivery scope for classified summaries. Use dashboard_only to record an unchanged run summary in Activity without Slack/Gmail/WhatsApp or an in-app alert. Use external_only for a material Pulse update whose current review results are already projected into Activity. Omit or use all when both Activity and configured external channels should receive the summary. Custom notification instructions may explicitly choose a different mode.",
		},
		"notification_kind": map[string]interface{}{
			"type":        "string",
			"enum":        []string{"general", "run_summary", "pulse_summary"},
			"description": "Classifies this notification so the backend can enforce its workflow-configured channel routing. Pulse finalizers use run_summary for the execution outcome and pulse_summary for Pulse review/fix activity. Use general for ordinary notifications.",
		},
		"summary_title": map[string]interface{}{
			"type":        "string",
			"maxLength":   150,
			"description": "Channel-neutral title used by the Org Dashboard and as the default title for rich channel renderings. For run_summary and pulse_summary, set this instead of relying on a channel-specific title.",
		},
		"summary_status": map[string]interface{}{
			"type":        "string",
			"enum":        []string{"completed", "failed", "blocked", "waiting_for_user", "waiting_for_platform", "monitoring", "informational", "no_run"},
			"description": "Channel-neutral description of what the workflow is doing now. Use completed when done, failed for a confirmed run failure, blocked when work cannot continue, waiting_for_user or waiting_for_platform when that is the specific blocker, monitoring when awaiting natural evidence, informational for an update, and no_run when a scheduled run did not start.",
		},
		"summary_route": map[string]interface{}{
			"type":        "string",
			"maxLength":   200,
			"description": "Legacy single route label; prefer summary_routes with exact routing_step_id/route_id identities. Omit for shared workflow work. Only run_summary may inherit a scheduled selection; Pulse review scope must be explicit. Do not combine with summary_routes.",
		},
		"summary_fields": map[string]interface{}{
			"type":     "array",
			"maxItems": 10,
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label": map[string]interface{}{"type": "string"},
					"value": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"label", "value"},
				"additionalProperties": false,
			},
			"description": "Channel-neutral compact facts persisted for the Org Dashboard. Rich external renderers may reuse them.",
		},
		"summary_sections": map[string]interface{}{
			"type":     "array",
			"maxItems": 12,
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"heading": map[string]interface{}{"type": "string"},
					"body":    map[string]interface{}{"type": "string"},
				},
				"required":             []string{"heading", "body"},
				"additionalProperties": false,
			},
			"description": "Channel-neutral ordered details persisted for the Org Dashboard. Use for fixes, blockers, decisions, evidence, and next actions.",
		},
	}
	notifyProps["summary_routes"] = notificationRoutesSchema(notifyProps["summary_fields"], notifyProps["summary_sections"])
	if gmailEnabled() {
		notifyProps["email_subject"] = map[string]interface{}{
			"type":        "string",
			"description": "Required non-empty plain-text subject whenever this notification sends Gmail, including general notifications. summary_title is not a substitute. Omit only when Gmail is excluded or not used. Do not supply MIME-encoded text or line breaks.",
		}
		notifyProps["email_attachments"] = map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string"},
			"description": "Optional. Absolute file paths on the server host to attach to the email (Gmail only).",
		}
		notifyProps["block_recipients"] = map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string"},
			"description": "Optional one-off email denylist for THIS notification (Gmail only). Addresses listed here are rejected as To or CC recipients, on top of BOTH the account-wide disallowed-recipients list AND the durable per-workflow denylist in workflow.json notifications.block_recipients — it can only block MORE, never unblock a globally-blocked address. Put addresses the workflow must never email in workflow.json notifications.block_recipients (applied automatically); use this arg only for a one-time block beyond that. A blocked address is removed from the To/CC list and the message still goes to the remaining recipients; only when EVERY recipient is blocked is the email skipped entirely. Does not change any account-wide configuration; other channels ignore this.",
		}
		notifyProps["email_html"] = map[string]interface{}{
			"type":        "string",
			"description": "The single rich email body (Gmail only). For workflow/Pulse/Goal Advisor notifications, supply this by default unless the user explicitly asked not to email. MUST be EMAIL-SAFE: use INLINE styles only (a style attribute on each element). Gmail strips <style> blocks, <head>, <script>, and class-based CSS, so a full browser HTML document or a generated *.html report (e.g. pulse/org-pulse.html) arrives UNSTYLED — build a compact inline-styled summary and link to the full report instead of pasting it. Do not create a separate plain email version; message_for_user is the automatic fallback. Other channels ignore this.",
		}
		notifyProps["email_html_file"] = map[string]interface{}{
			"type":        "string",
			"description": "Optional. Absolute path to an .html file on the server host to use as the HTML email body (alternative to email_html). The file MUST be email-safe — INLINE styles only; Gmail strips <style>/<head>/class CSS, so a browser-oriented report file (e.g. pulse/org-pulse.html) renders UNSTYLED. Point this at an email-specific inline-styled file, not the full browser report. If the file can't be read, the tool returns an error so you can fix the path.",
		}
	}
	notifyUserTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "notify_user",
			Description: buildNotifyDescription(),
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type":       "object",
				"properties": notifyProps,
				"required":   []string{"message_for_user"},
			}),
		},
	}
	humanTools = append(humanTools, notifyUserTool)
	humanTools = append(humanTools, createSendEmailTool())
	humanTools = append(humanTools, createRecordSummaryTool(notifyProps))
	humanTools = append(humanTools, llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "get_notification_history",
			Description: "Read prior channel-neutral run or Pulse summaries for the current workflow before deciding whether an external notification is new and important. This is read-only and uses the same durable Activity history shown in the Org Dashboard. Records prove that a summary was created; they are not per-provider Slack/Gmail delivery receipts.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"notification_kind": map[string]interface{}{
						"type": "string", "enum": []string{"run_summary", "pulse_summary"},
						"description": "Summary stream to compare against.",
					},
					"limit": map[string]interface{}{
						"type": "integer", "minimum": 1, "maximum": 20, "default": 5,
						"description": "Maximum prior summaries to return.",
					},
					"exclude_pulse_run_id": map[string]interface{}{
						"type":        "string",
						"description": "For a Pulse finalizer, pass the current pulse_run_id so the just-projected current Activity item is excluded from prior history.",
					},
				},
				"required": []string{"notification_kind"},
			}),
		},
	})
	humanTools = append(humanTools, createGoogleCLITool(), createSlackMessageTool(), createSlackCLITool())

	return humanTools
}

// channelLabels maps connector Name() values to human-friendly labels used in
// the dynamic notify_user description.
var channelLabels = map[string]string{
	"slack":         "Slack",
	"whatsapp":      "WhatsApp",
	"gmail":         "Gmail (email)",
	"org_dashboard": "Org Dashboard",
}

var sendRichSlackIncomingWebhook = services.SendRichSlackIncomingWebhook

const notifyUserPurposeNote = "PREFER record_summary for a workflow run or Pulse summary, and send_email or send_slack_message to reach specific people; use notify_user only for a plain message to the workflow's owner. "

// buildNotifyDescription renders the notify_user description with the set of
// channels enabled when the tool list is built (per session/run), so the agent
// knows where its message will actually land. The always-on web UI connector is
// not framed as an external channel.
func buildNotifyDescription() string {
	base := "Send one non-blocking notification through the configured providers. Gmail, Slack, and WhatsApp are external delivery providers; Org Dashboard durably records run_summary and pulse_summary notifications for the current workflow. Use this for FYIs, progress updates, alerts, and completion notices when you do not need to wait for a reply. For workflow, Pulse, Goal Advisor, and other structured summaries, always set the channel-neutral summary_title, summary_status, summary_fields, and summary_sections, plus summary_routes for route-specific actions and review outcomes (major routing choices are sub-workflows; branch choices are not). Keep one digest per existing notification kind, shared work at the top level, and distinct route statuses; do not infer Pulse coverage from the schedule. summary_status must plainly describe what the workflow is doing now; explain why in the title, message, facts, or sections. Channel-specific rich fields may improve presentation but must not contain facts omitted from the neutral summary. If the workflow has a Slack Incoming Webhook configured, the backend sends a backend-owned rich Block Kit card there instead of using the global Slack connector. Excluding slack suppresses both delivery paths. Never access a SECRET_* webhook variable, construct a webhook payload in shell, post with curl, disable notify_user to avoid duplication, or ask for the URL after an encrypted webhook reference is configured—the backend exclusively owns delivery. If you need the human to answer before continuing, use human_feedback instead. Returns a JSON delivery result — status (delivered|partial|failed|no_recipient|no_channels_configured) plus delivered/skipped/failed channel lists. Report it honestly to the user: do NOT claim an external message was sent when only Org Dashboard succeeded."

	var labels []string
	gmailOn := false
	if nm := services.GetNotificationManager(); nm != nil {
		for _, name := range nm.ListEnabledConnectors() {
			if name == "web_simulator" {
				continue
			}
			if name == "gmail" {
				gmailOn = true
			}
			if l, ok := channelLabels[name]; ok {
				labels = append(labels, l)
			} else {
				labels = append(labels, name)
			}
		}
	}

	if len(labels) == 0 {
		return notifyUserPurposeNote + base + " NOTE: No notification providers are currently enabled. A configured workflow Slack webhook may still receive the message."
	}
	desc := base + " Currently enabled delivery channels: " + strings.Join(labels, ", ") + ". The message is delivered to all enabled channels — you do not choose which."
	if gmailOn {
		desc += " Gmail is enabled, so email_subject, email_html, email_html_file, and email_attachments are available for the email rendering (other channels ignore these). For workflow, Pulse, org pulse, and Goal Advisor notifications, treat email as the default rich rendering: set email_subject and one inline-styled email_html body on the same notify_user call unless the user's notification preference explicitly says not to email. Do not write a separate plain email body; message_for_user is the automatic fallback. notify_user tells the owner through the configured channels and cannot name recipients: the workflow's saved recipients (notifications.run_summary_recipients / pulse_summary_recipients) apply by notification_kind. To email specific people, use the send_email tool."
	}
	return notifyUserPurposeNote + desc
}

// gmailEnabled reports whether the Gmail connector is currently an enabled
// delivery channel.
func gmailEnabled() bool {
	if nm := services.GetNotificationManager(); nm != nil {
		for _, n := range nm.ListEnabledConnectors() {
			if n == "gmail" {
				return true
			}
		}
	}
	return false
}

// gmailContentFromArgs builds the per-channel Gmail content from notify_user
// tool args, or (nil, nil) if no email-specific fields were provided. Returns an
// error (surfaced to the agent) when a referenced file can't be read.
func gmailContentFromArgs(args map[string]interface{}) (*services.GmailContent, error) {
	subject, _ := args["email_subject"].(string)
	html, _ := args["email_html"].(string)

	// email_html_file: absolute path to an .html file on the server host; its
	// contents become the HTML body (an alternative to inline email_html).
	if hf, _ := args["email_html_file"].(string); strings.TrimSpace(hf) != "" {
		data, err := os.ReadFile(strings.TrimSpace(hf))
		if err != nil {
			return nil, fmt.Errorf("email_html_file %q could not be read: %w", strings.TrimSpace(hf), err)
		}
		html = string(data)
	}

	var attachments []string
	if raw, ok := args["email_attachments"].([]interface{}); ok {
		for _, a := range raw {
			if s, ok := a.(string); ok && strings.TrimSpace(s) != "" {
				attachments = append(attachments, strings.TrimSpace(s))
			}
		}
	}
	if strings.TrimSpace(subject) == "" && strings.TrimSpace(html) == "" && len(attachments) == 0 {
		return nil, nil
	}
	return &services.GmailContent{
		Subject:     strings.TrimSpace(subject),
		HTMLBody:    html,
		Attachments: attachments,
	}, nil
}

func emailListFromArg(raw interface{}) []string {
	switch v := raw.(type) {
	case []interface{}:
		values := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				values = append(values, s)
			}
		}
		return normalizeNotifyEmailList(values)
	case []string:
		return normalizeNotifyEmailList(v)
	case string:
		return normalizeNotifyEmailList([]string{v})
	default:
		return nil
	}
}

// stringListFromArg reads an array-or-string tool argument into a trimmed,
// lowercased, de-duplicated slice. Used for simple token lists such as
// exclude_channels ("gmail", "slack", "whatsapp") where email-style splitting
// isn't needed.
func stringListFromArg(raw interface{}) []string {
	var values []string
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				values = append(values, s)
			}
		}
	case []string:
		values = v
	case string:
		values = []string{v}
	default:
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

func normalizeNotifyEmailList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			email := strings.ToLower(strings.TrimSpace(part))
			if email == "" || seen[email] {
				continue
			}
			seen[email] = true
			out = append(out, email)
		}
	}
	return out
}

// GetToolCategory returns the category name for human tools
func GetHumanToolCategory() string {
	return "human_tools"
}

// IsHumanToolCategory checks the single canonical category used by tool
// registration, workflow configuration, filtering, and context injection.
func IsHumanToolCategory(category string) bool {
	return strings.TrimSpace(category) == GetHumanToolCategory()
}

// HumanToolImplementationNames inventories the Go implementations that may be
// assembled into the human_tools category. It is not a chat admission list;
// AgentWorks product.yaml owns Builder/Run admission.
func HumanToolImplementationNames() []string {
	return []string{"human_feedback", "notify_user", "send_email", "record_summary", "get_notification_history", "send_slack_message", "slack", "google_workspace_cli", "get_human_input_request", "create_human_input_request", "answer_human_input_request", "mark_human_input_consumed", "dismiss_duplicate_human_input_request"}
}

// CreateHumanToolExecutors creates the execution functions for human tools
func CreateHumanToolExecutors() map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	executors["human_feedback"] = handleHumanFeedback
	executors["notify_user"] = handleNotifyUser
	executors["send_email"] = handleSendEmail
	executors["record_summary"] = handleRecordSummary
	executors["get_notification_history"] = handleGetNotificationHistory
	executors["send_slack_message"] = handleSlackMessage
	executors["slack"] = handleSlackCLI
	executors["google_workspace_cli"] = handleGoogleWorkspaceCLI

	return executors
}

func handleGetNotificationHistory(ctx context.Context, args map[string]interface{}) (string, error) {
	dest := NotificationDestinationFromContext(ctx)
	if dest == nil || strings.TrimSpace(dest.WorkspacePath) == "" {
		return "", fmt.Errorf("notification history requires a workflow workspace")
	}
	kind, _ := args["notification_kind"].(string)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "run_summary" && kind != "pulse_summary" {
		return "", fmt.Errorf("notification_kind must be run_summary or pulse_summary")
	}
	limit := 5
	if raw, ok := args["limit"].(float64); ok {
		limit = int(raw)
	} else if raw, ok := args["limit"].(int); ok {
		limit = raw
	}
	if limit < 1 || limit > 20 {
		return "", fmt.Errorf("limit must be between 1 and 20")
	}
	excludeID := ""
	if pulseRunID, _ := args["exclude_pulse_run_id"].(string); strings.TrimSpace(pulseRunID) != "" {
		var err error
		excludeID, err = services.PulseResultActivityID(dest.WorkspacePath, pulseRunID)
		if err != nil {
			return "", err
		}
	}
	history, err := services.ListOrgDashboardNotifications(ctx, dest.WorkspacePath, 50)
	if err != nil {
		return "", err
	}
	items := make([]services.OrgDashboardNotification, 0, limit)
	for _, item := range history.Recent {
		if item.Kind != kind || item.ID == excludeID {
			continue
		}
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	payload := map[string]interface{}{
		"workspace_path":    dest.WorkspacePath,
		"notification_kind": kind,
		"summaries":         items,
		"note":              "These are durable summaries, not per-provider delivery receipts. Compare semantic status, routes, blockers, decisions, findings, and outcomes; ignore timestamps and wording-only differences.",
	}
	b, _ := json.Marshal(payload)
	return string(b), nil
}

func handleNotifyUser(ctx context.Context, args map[string]interface{}) (string, error) {
	messageForUser, _ := args["message_for_user"].(string)
	messageForUser = strings.TrimSpace(messageForUser)
	if messageForUser == "" {
		return "", fmt.Errorf("message_for_user is required")
	}

	notificationManager := services.GetNotificationManager()
	if notificationManager == nil {
		return "", fmt.Errorf("notification manager not available")
	}

	// notify_user cannot name recipients any more (PLAT-736): fail loudly rather than ignore an old caller.
	if _, ok := args["email_to"]; ok {
		return "", fmt.Errorf("email_to was removed from notify_user: it tells the owner through the configured channels. To email specific people use send_email; to change the saved recipients set notifications.run_summary_recipients in workflow.json")
	}
	if _, ok := args["email_cc"]; ok {
		return "", fmt.Errorf("email_cc was removed from notify_user: use send_email to email specific people with cc")
	}
	dest := NotificationDestinationFromContext(ctx)
	// Optional one-off email denylist for this send, unioned with both the
	// account-wide blocked list and the per-workflow workflow.json
	// notifications.block_recipients already carried on dest.Gmail.
	if blocked := emailListFromArg(args["block_recipients"]); len(blocked) > 0 {
		if dest == nil {
			dest = &services.NotificationDestination{}
		}
		if dest.Gmail == nil {
			dest.Gmail = &services.GmailDest{}
		}
		dest.Gmail.BlockedRecipients = append(dest.Gmail.BlockedRecipients, blocked...)
	}
	gc, err := gmailContentFromArgs(args)
	if err != nil {
		return "", err // e.g. email_html_file not found — feed the problem back to the agent
	}
	if gc != nil {
		addWorkflowIdentityToGmailContent(gc, workflowNameFromNotificationDestination(dest))
		if dest == nil {
			dest = &services.NotificationDestination{}
		}
		dest.Content = &services.NotificationContent{Gmail: gc}
	}
	slackContent, err := slackContentFromArgs(args)
	if err != nil {
		return "", err
	}

	// Per-workflow channel opt-out. The durable preference comes from workflow.json
	// notifications.exclude_channels (carried on dest.ExcludeChannels); the optional
	// exclude_channels arg adds a one-off skip for this send. Both are unioned.
	excludeChannels := stringListFromArg(args["exclude_channels"])
	// The caller narrowed delivery itself (for example a Slack-only alert).
	// Remembered so a channel it kept but that cannot deliver is reported as
	// a failure instead of silently disappearing from the receipt.
	callerSelectedChannels := len(excludeChannels) > 0
	if dest != nil && len(dest.ExcludeChannels) > 0 {
		excludeChannels = append(excludeChannels, dest.ExcludeChannels...)
	}
	notificationKind, _ := args["notification_kind"].(string)
	notificationKind = strings.ToLower(strings.TrimSpace(notificationKind))
	if notificationKind == "" {
		notificationKind = "general"
	}
	deliveryMode, _ := args["delivery_mode"].(string)
	deliveryMode = strings.ToLower(strings.TrimSpace(deliveryMode))
	if deliveryMode == "" {
		deliveryMode = "all"
	}
	if deliveryMode != "all" && deliveryMode != "dashboard_only" && deliveryMode != "external_only" {
		return "", fmt.Errorf("delivery_mode must be all, dashboard_only, or external_only")
	}
	if notificationKind == "general" && deliveryMode != "all" {
		return "", fmt.Errorf("delivery_mode %s requires run_summary or pulse_summary", deliveryMode)
	}
	if deliveryMode == "dashboard_only" {
		excludeChannels = append(excludeChannels, "gmail", "slack", "whatsapp")
	} else if deliveryMode == "external_only" {
		excludeChannels = append(excludeChannels, "org_dashboard")
	}
	if notificationKind == "general" {
		// Org Dashboard records only run and Pulse summaries; for anything
		// else it sends nothing, which the receipt reported as a failed
		// channel (PUL-70AD0472: a Slack-only secret alert showed "failed").
		excludeChannels = append(excludeChannels, "org_dashboard")
	}
	summary, err := notificationSummaryFromArgs(args, notificationKind, gc, slackContent)
	if err != nil {
		return "", err
	}
	if dest == nil {
		dest = &services.NotificationDestination{}
	}
	if notificationKind == "run_summary" && len(summary.Routes) == 0 && strings.TrimSpace(summary.Route) == "" {
		summary.Route = notificationRouteFromSelections(dest.RouteSelections)
	}
	summaryMessage := messageForUser
	// The in-app copy, independent of the external channels below: shown
	// regardless of whether any of them are even configured, so the message
	// still reaches the person in front of the screen when delivery
	// elsewhere fails or nothing is set up. Engine-independent by
	// construction — this fires because the tool executed, not because a
	// CLI's own transcript happened to narrate the call in a shape a
	// consumer recognizes.
	inAppShown := false
	if deliveryMode == "all" {
		if emitter, ok := ctx.Value(SessionEventEmitterKey).(SessionEventEmitter); ok && emitter != nil {
			emitter.EmitProductInteraction("notify", map[string]interface{}{"title": summary.Title, "message": summaryMessage})
			inAppShown = true
		}
	}
	messageForUser = appendNotificationRouteContent(messageForUser, summary.Routes)
	if dest.Content == nil {
		dest.Content = &services.NotificationContent{}
	}
	dest.Content.Text = summaryMessage
	dest.Content.Summary = summary
	// Durable per-workflow recipients for this summary kind. This must run after notification_kind is read, since the
	// kind is what selects between the run and Pulse lists. The denylist on dest.Gmail is left untouched and is still
	// enforced at send time.
	if routedTo := summaryRecipientsForKind(dest, notificationKind); len(routedTo) > 0 {
		if dest.Gmail == nil {
			dest.Gmail = &services.GmailDest{}
		}
		dest.Gmail.Email = strings.Join(routedTo, ", ")
	}
	routedChannels := summaryChannelsForKind(dest, notificationKind)
	if len(routedChannels) > 0 {
		excludeChannels = append(excludeChannels, excludedNotificationChannels(routedChannels)...)
	}
	if !containsNotificationChannel(excludeChannels, "gmail") {
		sender, err := notificationSenderForKind(dest, notificationKind)
		if err != nil {
			return "", err
		}
		if sender != "" {
			if dest.Gmail == nil {
				dest.Gmail = &services.GmailDest{}
			}
			dest.Gmail.ConnectionIDs = []string{sender}
		}
	}

	expectedGmail := gmailEnabled() || containsNotificationChannel(routedChannels, "gmail") || gc != nil ||
		(dest.Gmail != nil && (dest.Gmail.Email != "" || len(dest.Gmail.ConnectionIDs) > 0))
	if err := validateNotificationEmailSubject(args, expectedGmail, excludeChannels); err != nil {
		return "", err
	}

	// Synchronous send so we can report real per-channel delivery to the agent
	// (and so the send isn't killed when this turn's context is canceled).
	webhooks := webhooksForKind(dest, notificationKind)
	// Workflow delivery replaces the account Slack destination, never duplicates
	// it or falls back to it after a webhook failure.
	accountExclusions := append([]string{}, excludeChannels...)
	if len(webhooks) > 0 {
		accountExclusions = append(accountExclusions, "slack")
	}
	results := notificationManager.SendUserNotificationSync(ctx, messageForUser, "", dest, accountExclusions...)
	if inAppShown {
		// The session's own screen: not one of the configured external
		// providers described to the agent above (like the always-on web UI
		// connector, it is not framed as one), but a real delivered result —
		// omitting it reported "failed" whenever nothing else was configured,
		// even though the person at the screen had already seen the message.
		results = append(results, services.ConnectorResult{Channel: "in_app", OK: true, MsgID: "shown"})
	}
	results = explainMissingGmail(results, expectedGmail, excludeChannels, services.GetGmailService())
	webhookAllowed := deliveryMode != "dashboard_only" && !containsNotificationChannel(excludeChannels, "slack") &&
		(len(routedChannels) == 0 || containsNotificationChannel(routedChannels, "slack"))
	if webhookAllowed {
		// Each webhook is its own Slack channel, so a summary configured for two
		// channels posts twice. Results are reported per webhook: one channel
		// failing must not read as "Slack delivered" or hide the others. The
		// label stays the plain "slack_webhook" for a single channel so existing
		// delivery reports keep their shape, and is qualified by secret name only
		// when there is more than one channel to tell apart.
		for _, webhook := range webhooks {
			msgID, sendErr := sendRichSlackIncomingWebhook(ctx, webhook.URL, messageForUser, slackContent)
			result := services.ConnectorResult{
				Channel: webhookResultChannel(webhook, len(webhooks) > 1),
				OK:      sendErr == nil,
				MsgID:   msgID,
			}
			if sendErr != nil {
				result.Err = sendErr.Error()
			}
			results = append(results, result)
		}
	}

	expectedChannels := append([]string{}, routedChannels...)
	expectedChannels = append(expectedChannels, notificationManager.ListEnabledConnectors()...)
	if dest.Slack != nil {
		expectedChannels = append(expectedChannels, "slack")
	}
	if dest.WhatsApp != nil {
		expectedChannels = append(expectedChannels, "whatsapp")
	}
	if callerSelectedChannels && !containsNotificationChannel(excludeChannels, "slack") && !containsNotificationChannel(expectedChannels, "slack") {
		expectedChannels = append(expectedChannels, "slack")
	}
	results = explainMissingChannels(results, expectedChannels, excludeChannels)

	delivered := []string{}
	skipped := []string{}
	failed := map[string]string{}
	for _, r := range results {
		switch {
		case !r.OK:
			failed[r.Channel] = r.Err
		case r.MsgID == "":
			skipped = append(skipped, r.Channel) // connector had no destination for this recipient
		default:
			delivered = append(delivered, r.Channel)
		}
	}

	var status string
	switch {
	case len(results) == 0:
		status = "no_channels_configured" // nothing connected; not delivered anywhere
	case len(delivered) == 0 && len(failed) == 0:
		status = "no_recipient" // all connectors skipped (no destination resolved)
	case len(delivered) == 0:
		status = "failed"
	case len(failed) > 0:
		status = "partial"
	default:
		status = "delivered"
	}

	result := map[string]interface{}{
		"status":    status,
		"delivered": delivered,
		"skipped":   skipped,
		"failed":    failed,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func notificationRouteFromSelections(selections map[string]string) string {
	keys := make([]string, 0, len(selections))
	for stepID, routeID := range selections {
		if strings.TrimSpace(stepID) != "" && strings.TrimSpace(routeID) != "" {
			keys = append(keys, stepID)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return strings.TrimSpace(selections[keys[0]])
	}
	parts := make([]string, 0, len(keys))
	for _, stepID := range keys {
		parts = append(parts, strings.TrimSpace(stepID)+"="+strings.TrimSpace(selections[stepID]))
	}
	return strings.Join(parts, " · ")
}

func summaryChannelsForKind(dest *services.NotificationDestination, kind string) []string {
	if dest == nil {
		return nil
	}
	switch kind {
	case "run_summary":
		return dest.RunSummaryChannels
	case "pulse_summary":
		return dest.PulseSummaryChannels
	default:
		return nil
	}
}

// webhooksForKind picks which Slack channels this notification posts to. A
// Slack Incoming Webhook is bound to one channel, so choosing a channel means
// choosing a webhook. When the kind has no channels of its own it falls back to
// the workflow's single configured webhook, which is the pre-split behavior.
func webhooksForKind(dest *services.NotificationDestination, kind string) []services.SlackWebhookDest {
	if dest == nil {
		return nil
	}
	var configured []services.SlackWebhookDest
	switch kind {
	case "run_summary":
		configured = dest.RunSummaryWebhooks
	case "pulse_summary":
		configured = dest.PulseSummaryWebhooks
	}
	if len(configured) > 0 {
		return configured
	}
	if dest.SlackWebhook != nil && strings.TrimSpace(dest.SlackWebhook.URL) != "" {
		return []services.SlackWebhookDest{*dest.SlackWebhook}
	}
	return nil
}

// webhookResultChannel labels a per-webhook delivery result. With several Slack
// channels in play, a bare "slack_webhook" for each would make the agent's
// delivery report ambiguous about which channel actually received the message —
// but qualifying the single-channel case would change a report shape callers
// already read, so the name is appended only when it disambiguates something.
func webhookResultChannel(webhook services.SlackWebhookDest, qualify bool) string {
	if name := strings.TrimSpace(webhook.SecretName); qualify && name != "" {
		return "slack_webhook:" + name
	}
	return "slack_webhook"
}

// summaryRecipientsForKind picks the workflow's configured Gmail To list for
// this notification kind. A "general" notification has no configured list and
// falls through to the per-user preference and account default, matching how
// channel routing treats an unclassified send.
func summaryRecipientsForKind(dest *services.NotificationDestination, kind string) []string {
	if dest == nil {
		return nil
	}
	switch kind {
	case "run_summary":
		return dest.RunSummaryRecipients
	case "pulse_summary":
		return dest.PulseSummaryRecipients
	default:
		return nil
	}
}

// notificationSenderForKind picks the one Gmail account that sends a Notify
// message. Workflow and Builder Gmail actions use their own connection_id and
// are not restricted by this notification setting.
//
// The FROM counterpart to summaryRecipientsForKind, and deliberately a separate
// function: one decides which mailbox sends, the other who receives, and a
// change to either must not be able to silently move the other.
//
// Precedence: the per-summary sender, then the workflow-wide one, then "" which
// means inherit the account default connection.
func notificationSenderForKind(dest *services.NotificationDestination, kind string) (string, error) {
	if dest == nil {
		return "", nil
	}
	var perSummary []string
	switch kind {
	case "run_summary":
		perSummary = dest.RunSummaryGmailConnectionIDs
	case "pulse_summary":
		perSummary = dest.PulseSummaryGmailConnectionIDs
	}
	if len(perSummary) > 0 {
		if len(perSummary) > 1 {
			return "", fmt.Errorf("Notify has multiple Gmail senders configured for %s; select one sender in Notify settings", kind)
		}
		return strings.TrimSpace(perSummary[0]), nil
	}
	if dest.Gmail != nil && len(dest.Gmail.ConnectionIDs) > 0 {
		if len(dest.Gmail.ConnectionIDs) > 1 {
			return "", fmt.Errorf("Notify has multiple Gmail senders configured; select one sender in Notify settings")
		}
		return strings.TrimSpace(dest.Gmail.ConnectionIDs[0]), nil
	}
	return "", nil
}

func excludedNotificationChannels(allowed []string) []string {
	allowedSet := map[string]bool{}
	for _, channel := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(channel))] = true
	}
	excluded := make([]string, 0, 3)
	for _, channel := range []string{"gmail", "slack", "whatsapp"} {
		if !allowedSet[channel] {
			excluded = append(excluded, channel)
		}
	}
	return excluded
}

func containsNotificationChannel(channels []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, channel := range channels {
		if strings.ToLower(strings.TrimSpace(channel)) == target {
			return true
		}
	}
	return false
}

func slackContentFromArgs(args map[string]interface{}) (services.SlackWebhookContent, error) {
	content := services.SlackWebhookContent{}
	content.Title, _ = args["slack_title"].(string)
	content.Color, _ = args["slack_color"].(string)
	content.Footer, _ = args["slack_footer"].(string)

	if rawFields, ok := args["slack_fields"].([]interface{}); ok {
		for i, raw := range rawFields {
			entry, ok := raw.(map[string]interface{})
			if !ok {
				return content, fmt.Errorf("slack_fields[%d] must be an object", i)
			}
			label, _ := entry["label"].(string)
			value, _ := entry["value"].(string)
			content.Fields = append(content.Fields, services.SlackWebhookField{Label: label, Value: value})
		}
	}
	if rawSections, ok := args["slack_sections"].([]interface{}); ok {
		for i, raw := range rawSections {
			entry, ok := raw.(map[string]interface{})
			if !ok {
				return content, fmt.Errorf("slack_sections[%d] must be an object", i)
			}
			heading, _ := entry["heading"].(string)
			body, _ := entry["body"].(string)
			content.Sections = append(content.Sections, services.SlackWebhookSection{Heading: heading, Body: body})
		}
	}
	return content, nil
}

func notificationSummaryFromArgs(
	args map[string]interface{},
	kind string,
	gmail *services.GmailContent,
	slack services.SlackWebhookContent,
) (*services.NotificationSummary, error) {
	title, _ := args["summary_title"].(string)
	if strings.TrimSpace(title) == "" {
		title = slack.Title
	}
	if strings.TrimSpace(title) == "" && gmail != nil {
		title = gmail.Subject
	}

	status, _ := args["summary_status"].(string)
	if strings.TrimSpace(status) == "" {
		status = slack.Color
	}
	status = normalizedNotificationSummaryStatus(status)
	if status == "" {
		return nil, fmt.Errorf("summary_status must be completed, failed, blocked, waiting_for_user, waiting_for_platform, monitoring, informational, or no_run")
	}
	route, _ := args["summary_route"].(string)

	fields, err := notificationSummaryFieldsFromArg(args["summary_fields"])
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		for _, field := range slack.Fields {
			fields = append(fields, services.NotificationSummaryField{Label: field.Label, Value: field.Value})
		}
	}
	sections, err := notificationSummarySectionsFromArg(args["summary_sections"])
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		for _, section := range slack.Sections {
			sections = append(sections, services.NotificationSummarySection{Heading: section.Heading, Body: section.Body})
		}
	}
	routes, err := notificationRoutesFromArg(args["summary_routes"])
	if err != nil {
		return nil, err
	}
	reviews, err := pulseReviewsFromArg(args["pulse_reviews"])
	if err != nil {
		return nil, err
	}
	if len(routes) > 0 && strings.TrimSpace(route) != "" {
		return nil, fmt.Errorf("use summary_routes or legacy summary_route, not both")
	}

	return &services.NotificationSummary{
		Kind:     strings.ToLower(strings.TrimSpace(kind)),
		Title:    strings.TrimSpace(title),
		Status:   status,
		Route:    strings.TrimSpace(route),
		Reviews:  reviews,
		Fields:   fields,
		Sections: sections,
		Routes:   routes,
	}, nil
}

func normalizedNotificationSummaryStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "neutral":
		return "informational"
	case "success":
		return "completed"
	case "warning":
		return "blocked"
	case "danger":
		return "failed"
	case "completed", "failed", "blocked", "waiting_for_user", "waiting_for_platform", "monitoring", "informational", "no_run":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

func notificationSummaryFieldsFromArg(raw interface{}) ([]services.NotificationSummaryField, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("summary_fields must be an array")
	}
	fields := make([]services.NotificationSummaryField, 0, len(items))
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("summary_fields[%d] must be an object", i)
		}
		label, _ := item["label"].(string)
		value, _ := item["value"].(string)
		fields = append(fields, services.NotificationSummaryField{Label: strings.TrimSpace(label), Value: strings.TrimSpace(value)})
	}
	return fields, nil
}

func notificationSummarySectionsFromArg(raw interface{}) ([]services.NotificationSummarySection, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("summary_sections must be an array")
	}
	sections := make([]services.NotificationSummarySection, 0, len(items))
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("summary_sections[%d] must be an object", i)
		}
		heading, _ := item["heading"].(string)
		body, _ := item["body"].(string)
		sections = append(sections, services.NotificationSummarySection{Heading: strings.TrimSpace(heading), Body: strings.TrimSpace(body)})
	}
	return sections, nil
}

// handleHumanFeedback handles the human_feedback tool execution
func handleHumanFeedback(ctx context.Context, args map[string]interface{}) (string, error) {
	// Extract parameters - message_for_user is optional, use default if missing
	messageForUser, ok := args["message_for_user"].(string)
	if !ok || messageForUser == "" {
		messageForUser = "Please provide your feedback here..."
	}

	uniqueID, ok := args["unique_id"].(string)
	uniqueID = strings.TrimSpace(uniqueID)
	if !ok || uniqueID == "" {
		return "", fmt.Errorf("unique_id is required and must be a string")
	}
	waitTimeout := humanFeedbackTimeoutFromArgs(args)

	// Extract optional options array
	var options []string
	if optionsRaw, ok := args["options"].([]interface{}); ok {
		for _, opt := range optionsRaw {
			if s, ok := opt.(string); ok && s != "" {
				options = append(options, s)
			}
		}
	}

	// Get global feedback store
	feedbackStore := GetHumanFeedbackStore()

	// Register the request before emitting UI/notification events so an immediate
	// Electron response can never race the store registration.
	expiryContext := fmt.Sprintf("This request expires in %d seconds.", int(waitTimeout/time.Second))
	sessionID, _ := ctx.Value(BGAgentSessionIDKey).(string)
	if err := feedbackStore.CreatePendingRequest(
		uniqueID,
		messageForUser,
		expiryContext,
		sessionID,
		options,
		len(options) == 0,
		waitTimeout,
		feedbackOperationFromContext(ctx),
	); err != nil {
		return "", fmt.Errorf("failed to create feedback request: %w", err)
	}

	// Emit blocking_human_feedback so the frontend renders a direct response UI.
	// The expiry is informational here; the store owns the authoritative timer.
	if emitter, ok := ctx.Value(SessionEventEmitterKey).(SessionEventEmitter); ok && emitter != nil {
		hasOptions := len(options) > 0
		emitter.EmitBlockingHumanFeedback(uniqueID, messageForUser, expiryContext, hasOptions, "", "", options...)
	}

	// Wait only for the bounded duration selected by the agent.
	response, err := feedbackStore.WaitForResponseCtx(ctx, uniqueID, waitTimeout)
	if resolver, ok := ctx.Value(SessionEventEmitterKey).(HumanFeedbackResolutionEmitter); ok && resolver != nil {
		outcome := "answered"
		if errors.Is(err, ErrFeedbackCancelled) {
			outcome = "cancelled"
		} else if err != nil {
			outcome = "expired"
		}
		resolver.EmitHumanFeedbackResolved(uniqueID, outcome)
	}
	if err != nil {
		return "", fmt.Errorf("human feedback request %s expired after %s: %w", uniqueID, waitTimeout, err)
	}

	return response, nil
}

const (
	defaultHumanFeedbackTimeout = 5 * time.Minute
	minHumanFeedbackTimeout     = 30 * time.Second
	maxHumanFeedbackTimeout     = 30 * time.Minute
)

func humanFeedbackTimeoutFromArgs(args map[string]interface{}) time.Duration {
	raw, ok := args["timeout_seconds"]
	if !ok {
		return defaultHumanFeedbackTimeout
	}

	var seconds int64
	switch value := raw.(type) {
	case int:
		seconds = int64(value)
	case int32:
		seconds = int64(value)
	case int64:
		seconds = value
	case float32:
		seconds = int64(value)
	case float64:
		seconds = int64(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return defaultHumanFeedbackTimeout
		}
		seconds = parsed
	default:
		return defaultHumanFeedbackTimeout
	}

	minSeconds := int64(minHumanFeedbackTimeout / time.Second)
	maxSeconds := int64(maxHumanFeedbackTimeout / time.Second)
	if seconds < minSeconds {
		return minHumanFeedbackTimeout
	}
	if seconds > maxSeconds {
		return maxHumanFeedbackTimeout
	}
	return time.Duration(seconds) * time.Second
}

// NotificationDestinationFromContext returns the best notification destination
// hint available for the current tool execution context.
func NotificationDestinationFromContext(ctx context.Context) *services.NotificationDestination {
	var dest *services.NotificationDestination
	if explicit, ok := ctx.Value(BotNotificationDestinationKey).(*services.NotificationDestination); ok && explicit != nil {
		dest = cloneNotificationDestination(explicit)
	}
	// Coding-agent tools execute through a separate HTTP request context. The
	// bridge preserves the trusted session ID, but not arbitrary values from the
	// original agent context, so resolve the latest session destination here.
	sessionID, _ := ctx.Value(common.ChatSessionIDKey).(string)
	if strings.TrimSpace(sessionID) == "" {
		sessionID = mcpexecutor.SessionIDFromContext(ctx)
	}
	if current := sessionNotificationDestination(sessionID); current != nil {
		dest = current
	}
	if uid, ok := ctx.Value(common.UserIDKey).(string); ok && uid != "" {
		if dest == nil {
			dest = &services.NotificationDestination{}
		}
		if dest.UserID == "" {
			dest.UserID = uid
		}
	}
	if notificationDestinationEmpty(dest) {
		return nil
	}
	return dest
}

func cloneNotificationDestination(dest *services.NotificationDestination) *services.NotificationDestination {
	if dest == nil {
		return nil
	}
	clone := &services.NotificationDestination{
		UserID:                         dest.UserID,
		WorkflowName:                   dest.WorkflowName,
		WorkspacePath:                  dest.WorkspacePath,
		RouteSelections:                maps.Clone(dest.RouteSelections),
		ExcludeChannels:                append([]string(nil), dest.ExcludeChannels...),
		RunSummaryChannels:             append([]string(nil), dest.RunSummaryChannels...),
		PulseSummaryChannels:           append([]string(nil), dest.PulseSummaryChannels...),
		RunSummaryGmailConnectionIDs:   append([]string(nil), dest.RunSummaryGmailConnectionIDs...),
		PulseSummaryGmailConnectionIDs: append([]string(nil), dest.PulseSummaryGmailConnectionIDs...),
		RunSummaryRecipients:           append([]string(nil), dest.RunSummaryRecipients...),
		PulseSummaryRecipients:         append([]string(nil), dest.PulseSummaryRecipients...),
		RunSummaryWebhooks:             append([]services.SlackWebhookDest(nil), dest.RunSummaryWebhooks...),
		PulseSummaryWebhooks:           append([]services.SlackWebhookDest(nil), dest.PulseSummaryWebhooks...),
	}
	if dest.Slack != nil {
		clone.Slack = &services.SlackDest{
			ChannelID:    dest.Slack.ChannelID,
			ThreadTS:     dest.Slack.ThreadTS,
			ConnectionID: dest.Slack.ConnectionID,
		}
	}
	if dest.SlackWebhook != nil {
		clone.SlackWebhook = &services.SlackWebhookDest{
			SecretName: dest.SlackWebhook.SecretName,
			URL:        dest.SlackWebhook.URL,
		}
	}
	if dest.WhatsApp != nil {
		clone.WhatsApp = &services.WhatsAppDest{
			ChannelID: dest.WhatsApp.ChannelID,
			PhoneE164: dest.WhatsApp.PhoneE164,
		}
	}
	if dest.Gmail != nil {
		clone.Gmail = &services.GmailDest{
			Email:             dest.Gmail.Email,
			ConnectionIDs:     append([]string(nil), dest.Gmail.ConnectionIDs...),
			BlockedRecipients: append([]string(nil), dest.Gmail.BlockedRecipients...),
		}
	}
	// Content is treated as read-only by connectors, so sharing the pointer is
	// safe and avoids a deep copy of attachment lists.
	clone.Content = dest.Content
	return clone
}

func workflowNameFromNotificationDestination(dest *services.NotificationDestination) string {
	if dest == nil {
		return ""
	}
	return strings.TrimSpace(dest.WorkflowName)
}

// addWorkflowIdentityToGmailContent makes workflow email identity a backend
// guarantee instead of a formatting instruction the notifying agent can omit.
// The destination carries only the safe workflow label, never its full path.
func addWorkflowIdentityToGmailContent(content *services.GmailContent, workflowName string) {
	if content == nil {
		return
	}
	workflowName = strings.TrimSpace(workflowName)
	if workflowName == "" {
		return
	}
	if subject := strings.TrimSpace(content.Subject); subject == "" {
		content.Subject = workflowName
	} else if !strings.HasPrefix(strings.ToLower(subject), strings.ToLower(workflowName)) {
		content.Subject = workflowName + " · " + subject
	}
	if strings.TrimSpace(content.HTMLBody) != "" {
		content.HTMLBody = `<div data-workflow-name="true" style="font-family:Arial,sans-serif;font-size:13px;color:#64748b;margin:0 0 14px 0">Workflow: <strong style="color:#0f172a">` + htmlstd.EscapeString(workflowName) + `</strong></div>` + content.HTMLBody
	}
}

func notificationDestinationEmpty(dest *services.NotificationDestination) bool {
	return dest == nil ||
		(dest.UserID == "" &&
			dest.WorkspacePath == "" &&
			(dest.Slack == nil || dest.Slack.ChannelID == "") &&
			(dest.SlackWebhook == nil || (dest.SlackWebhook.SecretName == "" && dest.SlackWebhook.URL == "")) &&
			(dest.WhatsApp == nil || (dest.WhatsApp.ChannelID == "" && dest.WhatsApp.PhoneE164 == "")) &&
			(dest.Gmail == nil || dest.Gmail.Email == "") &&
			len(dest.ExcludeChannels) == 0 &&
			len(dest.RunSummaryChannels) == 0 &&
			len(dest.PulseSummaryChannels) == 0 &&
			len(dest.RunSummaryGmailConnectionIDs) == 0 &&
			len(dest.PulseSummaryGmailConnectionIDs) == 0 &&
			len(dest.RunSummaryRecipients) == 0 &&
			len(dest.PulseSummaryRecipients) == 0 &&
			len(dest.RunSummaryWebhooks) == 0 &&
			len(dest.PulseSummaryWebhooks) == 0)
}
