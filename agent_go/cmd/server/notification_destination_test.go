package server

import (
	"context"
	"testing"
)

func TestNotificationDestinationFromQueryResolvesWorkflowSlackWebhookSecret(t *testing.T) {
	req := QueryRequest{
		NotificationSlackWebhookSecretName: "SLACK_NOTIFICATION_WEBHOOK_URL",
		notificationSlackWebhookURL:        "https://hooks.slack.com/services/T123/B456/secret",
		SelectedFolder:                     "Workflow/latency-report",
	}
	dest := notificationDestinationFromQuery(req, "user-1")
	if dest == nil || dest.SlackWebhook == nil {
		t.Fatalf("destination = %#v, want Slack webhook", dest)
	}
	if dest.SlackWebhook.SecretName != "SLACK_NOTIFICATION_WEBHOOK_URL" {
		t.Fatalf("secret name = %q", dest.SlackWebhook.SecretName)
	}
	if dest.SlackWebhook.URL != "https://hooks.slack.com/services/T123/B456/secret" {
		t.Fatal("webhook secret was not resolved")
	}
	if dest.WorkflowName != "latency-report" {
		t.Fatalf("workflow name = %q, want latency-report", dest.WorkflowName)
	}
}

func TestNotificationDestinationFromQueryKeepsMissingWorkflowWebhookVisible(t *testing.T) {
	req := QueryRequest{NotificationSlackWebhookSecretName: "MISSING_SLACK_WEBHOOK"}
	dest := notificationDestinationFromQuery(req, "")
	if dest == nil || dest.SlackWebhook == nil {
		t.Fatalf("destination = %#v, want unresolved Slack webhook marker", dest)
	}
	if dest.SlackWebhook.URL != "" {
		t.Fatal("unexpected value for missing webhook secret")
	}
}

func TestResolveNotificationSecretStripsWebhookFromAgentSecrets(t *testing.T) {
	previousGlobals := globalSecrets
	globalSecrets = []globalSecretEntry{
		{Name: "SLACK_NOTIFICATION_WEBHOOK_URL", Value: "global-webhook"},
		{Name: "GLOBAL_APPLICATION_TOKEN", Value: "global-app-secret"},
	}
	t.Cleanup(func() { globalSecrets = previousGlobals })

	req := QueryRequest{
		NotificationSlackWebhookSecretName: "SLACK_NOTIFICATION_WEBHOOK_URL",
		DecryptedSecrets: []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{
			{Name: "SLACK_NOTIFICATION_WEBHOOK_URL", Value: "https://hooks.slack.com/services/T123/B456/secret"},
			{Name: "APPLICATION_TOKEN", Value: "app-secret"},
		},
	}

	api := &StreamingAPI{}
	api.resolveNotificationSecretForRequest(context.Background(), "", "", &req)

	if req.notificationSlackWebhookURL == "" {
		t.Fatal("notification webhook was not moved to backend-only storage")
	}
	if len(req.DecryptedSecrets) != 1 || req.DecryptedSecrets[0].Name != "APPLICATION_TOKEN" {
		t.Fatalf("agent secrets = %#v, want only APPLICATION_TOKEN", req.DecryptedSecrets)
	}
	if req.SelectedGlobalSecrets == nil || len(*req.SelectedGlobalSecrets) != 0 {
		t.Fatalf("global agent secrets = %#v, want none when omitted", req.SelectedGlobalSecrets)
	}
}
