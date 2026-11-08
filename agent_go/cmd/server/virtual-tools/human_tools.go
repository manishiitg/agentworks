package virtualtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlstd "html"
	"maps"
	"os"
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
			Description: "Request urgent, short-lived input that only a human can provide, such as an OTP/2FA code, CAPTCHA completion, explicit approval, a subjective decision, private information, or an explicit test of the human-feedback channel. This tool pauses only the calling agent turn until the human answers directly in the AgentWorks UI. It stays in the AgentWorks UI; Gmail, workflow webhooks and account notification connectors do not carry the answer. Do not use it for an ordinary Builder/chat question, something another agent can answer, or something that may wait hours or days. Choose the shortest realistic timeout_seconds; use an expiry shown by the external service when available. The tool returns the human's response as text. Bridge-only coding CLIs invoking the HTTP endpoint through execute_shell_command must keep curl in the foreground and wait for the same call to return; never use nohup, append &, delegate/background it, write the result to a temporary file, poll for completion, or ask the user to send another message after responding. Do not set the shell timeout shorter than timeout_seconds. Cursor CLI has an approximately 60-second silent MCP-call ceiling, so Cursor agents must set timeout_seconds to at most 45 seconds and may retry after an explicit expiry only when the input is still required.",
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

	humanTools = append(humanTools, createSendEmailTool())

	humanTools = append(humanTools, createGoogleCLITool(), createSlackMessageTool(), createSlackCLITool())

	return humanTools
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

// gmailContentFromArgs builds the per-channel Gmail content from summary
// delivery arguments, or (nil, nil) if no email-specific fields were provided. Returns an
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
	return []string{"human_feedback", "send_email", "send_slack_message", "slack", "google_workspace_cli"}
}

// CreateHumanToolExecutors creates the execution functions for human tools
func CreateHumanToolExecutors() map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	executors["human_feedback"] = handleHumanFeedback
	executors["send_email"] = handleSendEmail
	executors["send_slack_message"] = handleSlackMessage
	executors["slack"] = handleSlackCLI
	executors["google_workspace_cli"] = handleGoogleWorkspaceCLI

	return executors
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
	dest, _ := notificationDestinationWithSource(ctx)
	return dest
}

func notificationDestinationWithSource(ctx context.Context) (*services.NotificationDestination, string) {
	source := "none"
	var dest *services.NotificationDestination
	if explicit, ok := ctx.Value(BotNotificationDestinationKey).(*services.NotificationDestination); ok && explicit != nil {
		dest = cloneNotificationDestination(explicit)
		source = "request"
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
		source = "session"
	}
	if uid, ok := ctx.Value(common.UserIDKey).(string); ok && uid != "" {
		if dest == nil {
			dest = &services.NotificationDestination{}
			source = "caller"
		}
		if dest.UserID == "" {
			dest.UserID = uid
		}
	}
	if notificationDestinationEmpty(dest) {
		return nil, "none"
	}
	return dest, source
}

func cloneNotificationDestination(dest *services.NotificationDestination) *services.NotificationDestination {
	if dest == nil {
		return nil
	}
	clone := &services.NotificationDestination{
		UserID:          dest.UserID,
		WorkflowName:    dest.WorkflowName,
		WorkspacePath:   dest.WorkspacePath,
		RouteSelections: maps.Clone(dest.RouteSelections),
	}
	if dest.Slack != nil {
		clone.Slack = &services.SlackDest{
			ChannelID:    dest.Slack.ChannelID,
			ThreadTS:     dest.Slack.ThreadTS,
			ConnectionID: dest.Slack.ConnectionID,
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
			Workspace:         dest.Gmail.Workspace,
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
			(dest.WhatsApp == nil || (dest.WhatsApp.ChannelID == "" && dest.WhatsApp.PhoneE164 == "")) &&
			(dest.Gmail == nil || dest.Gmail.Email == ""))
}
