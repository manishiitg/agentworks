package services

import (
	"encoding/json"
	"testing"
)

func TestMultiAgentChatCapabilitiesDecodeNotificationReference(t *testing.T) {
	var caps MultiAgentChatCapabilities
	if err := json.Unmarshal([]byte(`{
		"notifications":{"slack_webhook_secret_name":"CHIEF_SLACK_WEBHOOK"}
	}`), &caps); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	if caps.Notifications == nil || caps.Notifications.SlackWebhookSecretName != "CHIEF_SLACK_WEBHOOK" {
		t.Fatalf("notifications = %#v", caps.Notifications)
	}
}
