package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type slackWebhookRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn slackWebhookRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestValidateSlackIncomingWebhookURL(t *testing.T) {
	valid := []string{
		"https://hooks.slack.com/services/T123/B456/secret",
		"https://hooks.slack-gov.com/services/T123/B456/secret",
	}
	for _, raw := range valid {
		if err := ValidateSlackIncomingWebhookURL(raw); err != nil {
			t.Fatalf("expected valid URL %q: %v", raw, err)
		}
	}

	invalid := []string{
		"http://hooks.slack.com/services/T123/B456/secret",
		"https://example.com/services/T123/B456/secret",
		"https://hooks.slack.com.evil.test/services/T123/B456/secret",
		"https://hooks.slack.com:8443/services/T123/B456/secret",
		"https://hooks.slack.com/services/T123/B456",
		"https://hooks.slack.com/services/T123/B456/secret?redirect=https://example.com",
	}
	for _, raw := range invalid {
		if err := ValidateSlackIncomingWebhookURL(raw); err == nil {
			t.Fatalf("expected invalid URL %q", raw)
		}
	}
}

func TestSendSlackIncomingWebhook(t *testing.T) {
	original := slackWebhookHTTPClient
	t.Cleanup(func() { slackWebhookHTTPClient = original })

	var gotBody string
	slackWebhookHTTPClient = &http.Client{Transport: slackWebhookRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.String() != "https://hooks.slack.com/services/T123/B456/secret" {
			t.Fatalf("unexpected request URL")
		}
		if contentType := req.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
			t.Fatalf("content type = %q", contentType)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	})}

	msgID, err := SendSlackIncomingWebhook(context.Background(), "https://hooks.slack.com/services/T123/B456/secret", "**Done**")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if msgID != "webhook_ok" {
		t.Fatalf("message ID = %q", msgID)
	}
	var payload struct {
		Text        string `json:"text"`
		Attachments []struct {
			Color    string                   `json:"color"`
			Fallback string                   `json:"fallback"`
			Blocks   []map[string]interface{} `json:"blocks"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, gotBody)
	}
	if payload.Text != "" || len(payload.Attachments) != 1 || payload.Attachments[0].Fallback != "*Done*" || payload.Attachments[0].Color != "#4f46e5" || len(payload.Attachments[0].Blocks) != 1 {
		t.Fatalf("unexpected rich default payload: %#v", payload)
	}
}

func TestSendRichSlackIncomingWebhookBuildsValidatedBlockKit(t *testing.T) {
	original := slackWebhookHTTPClient
	t.Cleanup(func() { slackWebhookHTTPClient = original })

	var gotBody []byte
	slackWebhookHTTPClient = &http.Client{Transport: slackWebhookRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(req.Body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}

	content := SlackWebhookContent{
		Title: "customer QA",
		Color: "warning",
		Fields: []SlackWebhookField{
			{Label: "Passed", Value: "12"},
			{Label: "Incomplete", Value: "2"},
		},
		Sections: []SlackWebhookSection{{Heading: "Confirmed issues", Body: "<https://example.test/issues/42|#42> Login regression"}},
		Footer:   "staging · 19 Jul",
	}
	if _, err := SendRichSlackIncomingWebhook(context.Background(), "https://hooks.slack.com/services/T123/B456/secret", "Two checks need attention.", content); err != nil {
		t.Fatalf("send rich: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("decode rich body: %v", err)
	}
	attachments, _ := payload["attachments"].([]interface{})
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	attachment, _ := attachments[0].(map[string]interface{})
	if attachment["color"] != "#e8912d" {
		t.Fatalf("color = %#v", attachment["color"])
	}
	blocks, _ := attachment["blocks"].([]interface{})
	if len(blocks) != 5 { // header, lead summary, fields, section, footer
		t.Fatalf("blocks = %d, want 5: %s", len(blocks), gotBody)
	}
}

func TestBuildSlackWebhookPayloadRejectsInvalidRichContent(t *testing.T) {
	_, err := buildSlackWebhookPayload("done", SlackWebhookContent{Color: "chartreuse"})
	if err == nil || !strings.Contains(err.Error(), "slack_color") {
		t.Fatalf("invalid color error = %v", err)
	}
	_, err = buildSlackWebhookPayload("done", SlackWebhookContent{Fields: []SlackWebhookField{{Label: "", Value: "1"}}})
	if err == nil || !strings.Contains(err.Error(), "requires non-empty") {
		t.Fatalf("invalid field error = %v", err)
	}
}

func TestBuildSlackWebhookPayloadSplitsLongFallbackIntoBlocks(t *testing.T) {
	payload, err := buildSlackWebhookPayload(strings.Repeat("x", slackSectionTextLimit+25), SlackWebhookContent{})
	if err != nil {
		t.Fatalf("build long fallback: %v", err)
	}
	attachments, _ := payload["attachments"].([]map[string]interface{})
	blocks, _ := attachments[0]["blocks"].([]map[string]interface{})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
}

func TestSendSlackIncomingWebhookDoesNotLeakSecretURL(t *testing.T) {
	raw := "https://hooks.slack.com/services/T123/B456/super-secret-token"
	original := slackWebhookHTTPClient
	t.Cleanup(func() { slackWebhookHTTPClient = original })
	slackWebhookHTTPClient = &http.Client{Transport: slackWebhookRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("invalid_token")),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := SendSlackIncomingWebhook(context.Background(), raw, "test")
	if err == nil {
		t.Fatal("expected delivery error")
	}
	if strings.Contains(err.Error(), raw) || strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("error leaked webhook URL: %v", err)
	}
}

func TestSlackWebhookHasOneVisibleSummary(t *testing.T) {
	const message = "project-a AWS Pulse: security failed; cost OK."
	payload, err := buildSlackWebhookPayload(message, SlackWebhookContent{})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["text"]; exists {
		t.Fatal("top-level text duplicates attachment body")
	}
	attachments := payload["attachments"].([]map[string]interface{})
	if attachments[0]["fallback"] != message {
		t.Fatal("notification fallback missing")
	}
	blocks := attachments[0]["blocks"].([]map[string]interface{})
	if len(blocks) != 1 || blocks[0]["text"].(map[string]interface{})["text"] != message {
		t.Fatal("formatted body missing")
	}
}
