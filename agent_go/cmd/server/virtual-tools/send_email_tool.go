package virtualtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	llmtypes "github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// send_email emails the people the caller names, and only by Gmail. It is the
// counterpart of notify_user, which tells the workflow's owner through their
// configured channels: notify_user fans one message out to every channel, so it
// cannot require a recipient, and a call that named none went to the account's
// default recipient (PLAT-736). Here the recipient is mandatory, there is one
// channel and one receipt, and the Outward permission can hold the whole tool.

const sendEmailDescription = "Send one email to the recipients you name, by Gmail only. `to` is required (at least one address); `subject` is required; give a plain `body` or an email-safe `html` body (INLINE styles only; Gmail strips <style>, <head> and class CSS). Attach existing server files with `attachments` (absolute paths). The workflow's blocked-recipients list and the account's disallowed-recipients list still apply. This is the tool for emailing specific people; to tell the workflow's owner through their configured channels (Gmail, Slack, WhatsApp), use notify_user instead. Refused when the Outward permission is ask for this turn."

func createSendEmailTool() llmtypes.Tool {
	list := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": description}
	}
	to := list("Recipient email addresses. Required, at least one.")
	to["minItems"] = 1
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "send_email",
			Description: sendEmailDescription,
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"to":          to,
					"cc":          list("Optional CC addresses."),
					"subject":     map[string]interface{}{"type": "string", "description": "Required plain-text subject, no line breaks, no MIME encoding."},
					"body":        map[string]interface{}{"type": "string", "description": "Plain-text body. Required unless html or html_file is given."},
					"html":        map[string]interface{}{"type": "string", "description": "Optional email-safe HTML body (inline styles only). When given, body is the plain-text alternative."},
					"html_file":   map[string]interface{}{"type": "string", "description": "Optional absolute path to an email-safe .html file on the server host, used as the HTML body."},
					"attachments": list("Optional absolute paths of existing files on the server host to attach."),
				},
				"required": []string{"to", "subject"},
			}),
		},
	}
}

func handleSendEmail(ctx context.Context, args map[string]interface{}) (string, error) {
	to := emailListFromArg(args["to"])
	if len(to) == 0 {
		return "", fmt.Errorf("to is required: name at least one recipient address. To tell the workflow's owner through their configured channels, use notify_user instead")
	}
	subject, _ := args["subject"].(string)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", fmt.Errorf("subject is required")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return "", fmt.Errorf("subject must be one line of plain text")
	}
	html, _ := args["html"].(string)
	if file, _ := args["html_file"].(string); strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(strings.TrimSpace(file))
		if err != nil {
			return "", fmt.Errorf("html_file %q could not be read: %w", strings.TrimSpace(file), err)
		}
		html = string(data)
	}
	body, _ := args["body"].(string)
	if strings.TrimSpace(body) == "" && strings.TrimSpace(html) == "" {
		return "", fmt.Errorf("body or html is required")
	}
	// Emailing people the agent chose is an Outward action: with the permission at ask (Pulse Goal Work) the owner
	// decides first. The tool is held whole, not argument by argument.
	if common.OutwardHeld(ctx) {
		return "", fmt.Errorf("send_email refused: the Outward permission is ask for this turn (pulse.autonomy). Create a decision request (create_human_input_request) for sending to anyone else, or use notify_user to reach the owner's configured channels")
	}
	nm := services.GetNotificationManager()
	if nm == nil {
		return "", fmt.Errorf("notification manager not available")
	}

	base := NotificationDestinationFromContext(ctx)
	if base != nil {
		for _, channel := range base.ExcludeChannels {
			if strings.EqualFold(strings.TrimSpace(channel), "gmail") {
				return "", fmt.Errorf("send_email refused: this workflow excludes email (notifications.exclude_channels)")
			}
		}
	}
	var attachments []string
	if raw, ok := args["attachments"].([]interface{}); ok {
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				attachments = append(attachments, strings.TrimSpace(s))
			}
		}
	}
	content := &services.GmailContent{Subject: subject, CC: emailListFromArg(args["cc"]), HTMLBody: html, Attachments: attachments}
	dest := &services.NotificationDestination{
		Gmail:   &services.GmailDest{Email: strings.Join(to, ", ")},
		Content: &services.NotificationContent{Gmail: content},
	}
	// Keep the workflow's sender and denylist; never its recipients or other channels.
	if base != nil {
		dest.UserID, dest.WorkflowName, dest.WorkspacePath = base.UserID, base.WorkflowName, base.WorkspacePath
		if base.Gmail != nil {
			dest.Gmail.ConnectionIDs = append([]string(nil), base.Gmail.ConnectionIDs...)
			dest.Gmail.BlockedRecipients = append([]string(nil), base.Gmail.BlockedRecipients...)
		}
	}
	addWorkflowIdentityToGmailContent(content, workflowNameFromNotificationDestination(dest))

	// Gmail only: every other registered connector is excluded for this send.
	var excluded []string
	for _, name := range nm.ListConnectors() {
		if name != "gmail" {
			excluded = append(excluded, name)
		}
	}
	message := strings.TrimSpace(body)
	if message == "" {
		message = subject
	}
	results := nm.SendUserNotificationSync(ctx, message, "", dest, excluded...)
	results = explainMissingGmail(results, true, excluded, services.GetGmailService())

	out := map[string]interface{}{"status": "failed", "to": to}
	if len(content.CC) > 0 {
		out["cc"] = content.CC
	}
	for _, r := range results {
		if r.Channel != "gmail" {
			continue
		}
		if r.OK && r.MsgID != "" {
			out["status"], out["message_id"] = "delivered", r.MsgID
			delete(out, "error")
			break
		}
		if r.Err != "" {
			out["error"] = r.Err
		}
	}
	encoded, _ := json.Marshal(out)
	return string(encoded), nil
}
