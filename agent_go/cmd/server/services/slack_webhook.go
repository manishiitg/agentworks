package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const slackWebhookResponseLimit = 1024

const (
	slackHeaderTextLimit   = 150
	slackSectionTextLimit  = 3000
	slackFieldTextLimit    = 2000
	slackContextTextLimit  = 2000
	slackFallbackTextLimit = 40000
	slackMaxFields         = 10
	slackMaxSections       = 12
	slackMaxBlocks         = 50
)

// SlackWebhookContent is the safe, presentation-only Slack shape exposed by
// workflow summary delivery. Agents choose content and hierarchy; the backend owns the
// webhook URL and converts this typed structure into valid Block Kit JSON.
type SlackWebhookContent struct {
	Title    string
	Color    string
	Fields   []SlackWebhookField
	Sections []SlackWebhookSection
	Footer   string
}

type SlackWebhookField struct {
	Label string
	Value string
}

type SlackWebhookSection struct {
	Heading string
	Body    string
}

var slackWebhookHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ValidateSlackIncomingWebhookURL accepts only Slack's official Incoming
// Webhook endpoints. This strict allow-list prevents a workflow secret from
// turning workflow summary delivery into a general-purpose server-side HTTP request.
func ValidateSlackIncomingWebhookURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid Slack Incoming Webhook URL")
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid Slack Incoming Webhook URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "hooks.slack.com" && host != "hooks.slack-gov.com" {
		return fmt.Errorf("Slack Incoming Webhook must use an official Slack webhook host")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 4 || segments[0] != "services" {
		return fmt.Errorf("invalid Slack Incoming Webhook path")
	}
	for _, segment := range segments[1:] {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(strings.ToLower(segment), "%2f") {
			return fmt.Errorf("invalid Slack Incoming Webhook path")
		}
	}
	return nil
}

// SendSlackIncomingWebhook sends a one-way workflow notification using a safe
// default Block Kit card. It remains as the plain-call compatibility wrapper;
// all new workflow summary delivery calls flow through SendRichSlackIncomingWebhook.
func SendSlackIncomingWebhook(ctx context.Context, webhookURL, message string) (string, error) {
	return SendRichSlackIncomingWebhook(ctx, webhookURL, message, SlackWebhookContent{})
}

// SendRichSlackIncomingWebhook sends a backend-owned Block Kit notification.
// Incoming Webhooks return only "ok", so a synthetic non-empty message ID is
// returned on success. Errors intentionally never contain the secret URL.
func SendRichSlackIncomingWebhook(ctx context.Context, webhookURL, message string, content SlackWebhookContent) (string, error) {
	if err := ValidateSlackIncomingWebhookURL(webhookURL); err != nil {
		return "", err
	}
	payloadValue, err := buildSlackWebhookPayload(message, content)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return "", fmt.Errorf("could not encode Slack webhook message")
	}

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, strings.TrimSpace(webhookURL), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("could not create Slack webhook request")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := slackWebhookHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Slack webhook delivery failed")
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, slackWebhookResponseLimit))
	if readErr != nil {
		return "", fmt.Errorf("Slack webhook response could not be read")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || strings.TrimSpace(string(body)) != "ok" {
		return "", fmt.Errorf("Slack webhook rejected the notification (HTTP %d)", resp.StatusCode)
	}
	return "webhook_ok", nil
}

func buildSlackWebhookPayload(message string, content SlackWebhookContent) (map[string]interface{}, error) {
	fallback := strings.TrimSpace(convertMarkdownToSlackMrkdwn(message))
	if fallback == "" {
		return nil, fmt.Errorf("Slack webhook message is required")
	}

	content.Title = strings.TrimSpace(content.Title)
	content.Footer = strings.TrimSpace(content.Footer)
	if len([]rune(content.Title)) > slackHeaderTextLimit {
		return nil, fmt.Errorf("slack_title exceeds %d characters", slackHeaderTextLimit)
	}
	if len([]rune(content.Footer)) > slackContextTextLimit {
		return nil, fmt.Errorf("slack_footer exceeds %d characters", slackContextTextLimit)
	}
	if len(content.Fields) > slackMaxFields {
		return nil, fmt.Errorf("slack_fields exceeds %d items", slackMaxFields)
	}
	if len(content.Sections) > slackMaxSections {
		return nil, fmt.Errorf("slack_sections exceeds %d items", slackMaxSections)
	}

	blocks := make([]map[string]interface{}, 0, 4+len(content.Sections))
	if content.Title != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "header",
			"text": map[string]interface{}{"type": "plain_text", "text": content.Title, "emoji": true},
		})
	}

	// message_for_user is both the universal fallback and the lead summary in
	// Slack. Split long existing messages across blocks so enabling the richer
	// renderer does not make previously valid notifications fail at 3,000 chars.
	fallbackRunes := []rune(fallback)
	if len(fallbackRunes) > slackFallbackTextLimit {
		return nil, fmt.Errorf("message_for_user exceeds Slack's %d-character limit", slackFallbackTextLimit)
	}
	for start := 0; start < len(fallbackRunes); start += slackSectionTextLimit {
		end := start + slackSectionTextLimit
		if end > len(fallbackRunes) {
			end = len(fallbackRunes)
		}
		blocks = append(blocks, slackMrkdwnSection(string(fallbackRunes[start:end])))
	}

	if len(content.Fields) > 0 {
		fields := make([]map[string]interface{}, 0, len(content.Fields))
		for i, field := range content.Fields {
			label := strings.TrimSpace(field.Label)
			value := strings.TrimSpace(field.Value)
			if label == "" || value == "" {
				return nil, fmt.Errorf("slack_fields[%d] requires non-empty label and value", i)
			}
			fieldText := fmt.Sprintf("*%s*\n%s", convertMarkdownToSlackMrkdwn(label), convertMarkdownToSlackMrkdwn(value))
			if len([]rune(fieldText)) > slackFieldTextLimit {
				return nil, fmt.Errorf("slack_fields[%d] exceeds %d characters", i, slackFieldTextLimit)
			}
			fields = append(fields, map[string]interface{}{"type": "mrkdwn", "text": fieldText})
		}
		blocks = append(blocks, map[string]interface{}{"type": "section", "fields": fields})
	}

	for i, section := range content.Sections {
		heading := strings.TrimSpace(section.Heading)
		body := strings.TrimSpace(section.Body)
		if heading == "" || body == "" {
			return nil, fmt.Errorf("slack_sections[%d] requires non-empty heading and body", i)
		}
		text := fmt.Sprintf("*%s*\n%s", convertMarkdownToSlackMrkdwn(heading), convertMarkdownToSlackMrkdwn(body))
		if len([]rune(text)) > slackSectionTextLimit {
			return nil, fmt.Errorf("slack_sections[%d] exceeds %d characters", i, slackSectionTextLimit)
		}
		blocks = append(blocks, slackMrkdwnSection(text))
	}

	if content.Footer != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "context",
			"elements": []map[string]interface{}{{
				"type": "mrkdwn",
				"text": convertMarkdownToSlackMrkdwn(content.Footer),
			}},
		})
	}
	if len(blocks) > slackMaxBlocks {
		return nil, fmt.Errorf("Slack notification exceeds %d Block Kit blocks", slackMaxBlocks)
	}

	color, err := normalizeSlackColor(content.Color)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"attachments": []map[string]interface{}{{
			// Top-level text is visibly rendered above attachment blocks.
			// Keep the plain alternative in attachment fallback instead.
			"fallback": fallback,
			"color":    color,
			"blocks":   blocks,
		}},
	}, nil
}

func slackMrkdwnSection(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "section",
		"text": map[string]interface{}{"type": "mrkdwn", "text": text},
	}
}

func normalizeSlackColor(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "neutral":
		return "#4f46e5", nil
	case "success":
		return "#1a7f37", nil
	case "warning":
		return "#e8912d", nil
	case "danger":
		return "#cf222e", nil
	default:
		return "", fmt.Errorf("slack_color must be one of neutral, success, warning, or danger")
	}
}
