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
	withVaultSecretGrant(t, "owner", "SLACK_NOTIFICATION_WEBHOOK_URL", "APPLICATION_TOKEN")
	managedGlobalsMu.Lock()
	previous := managedGlobals
	managedGlobals = map[string]string{"SLACK_NOTIFICATION_WEBHOOK_URL": "https://hooks.slack.com/services/T123/B456/secret", "APPLICATION_TOKEN": "app-secret"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() { managedGlobalsMu.Lock(); managedGlobals = previous; managedGlobalsMu.Unlock() })

	selected := []string{"SLACK_NOTIFICATION_WEBHOOK_URL", "APPLICATION_TOKEN"}
	req := QueryRequest{NotificationSlackWebhookSecretName: "SLACK_NOTIFICATION_WEBHOOK_URL", SelectedGlobalSecrets: &selected}
	api := &StreamingAPI{}
	api.resolveNotificationSecretForRequest(context.Background(), "owner", "", &req)

	if req.notificationSlackWebhookURL == "" {
		t.Fatal("notification webhook was not moved to backend-only storage")
	}
	if req.SelectedGlobalSecrets == nil || len(*req.SelectedGlobalSecrets) != 1 || (*req.SelectedGlobalSecrets)[0] != "APPLICATION_TOKEN" {
		t.Fatalf("agent secrets = %#v, want only APPLICATION_TOKEN", req.SelectedGlobalSecrets)
	}
}
