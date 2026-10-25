package server

import (
	"testing"
)

func TestMergeWorkflowCapabilitiesUpdatePreservesNotificationsForOlderClients(t *testing.T) {
	globalNames := []string{"SLACK_WEBHOOK", "GLOBAL_APP_TOKEN"}
	existing := WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{SlackWebhookSecretName: "SLACK_WEBHOOK"},
	}
	incoming := &WorkflowCapabilities{
		BrowserMode:               "auto",
		SelectedSecrets:           []string{"SLACK_WEBHOOK", "APP_TOKEN"},
		SelectedGlobalSecretNames: &globalNames,
	}
	got := mergeWorkflowCapabilitiesUpdate(existing, incoming)
	if got.Notifications == nil || got.Notifications.SlackWebhookSecretName != "SLACK_WEBHOOK" {
		t.Fatalf("notifications were not preserved: %#v", got.Notifications)
	}
	if len(got.SelectedSecrets) != 1 || got.SelectedSecrets[0] != "APP_TOKEN" {
		t.Fatalf("agent secrets = %#v, want only APP_TOKEN", got.SelectedSecrets)
	}
	if got.SelectedGlobalSecretNames == nil || len(*got.SelectedGlobalSecretNames) != 1 || (*got.SelectedGlobalSecretNames)[0] != "GLOBAL_APP_TOKEN" {
		t.Fatalf("global agent secrets = %#v, want only GLOBAL_APP_TOKEN", got.SelectedGlobalSecretNames)
	}
}

func TestNotifySenderSaveReplacesLegacySummarySenders(t *testing.T) {
	config := &WorkflowNotificationConfig{
		RunSummaryGmailConnectionIDs: []string{"gmail_001", "gmail_002"},
		RunSummaryRecipients:         []string{"owner@example.com"},
	}
	setWorkflowNotificationSender(config, " gmail_004 ")
	if config.GmailConnectionID != "gmail_004" || len(config.RunSummaryGmailConnectionIDs) != 0 {
		t.Fatalf("sender selection did not replace legacy overrides: %+v", config)
	}
	if len(config.RunSummaryRecipients) != 1 || config.RunSummaryRecipients[0] != "owner@example.com" {
		t.Fatalf("saving sender changed recipients: %+v", config)
	}
	setWorkflowNotificationSender(config, "")
	if config.GmailConnectionID != "" {
		t.Fatalf("clearing sender should inherit account default: %+v", config)
	}
}

func TestMergeWorkflowCapabilitiesUpdateCanDisableNotifications(t *testing.T) {
	existing := WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{SlackWebhookSecretName: "SLACK_WEBHOOK"},
	}
	incoming := &WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{},
	}
	got := mergeWorkflowCapabilitiesUpdate(existing, incoming)
	if got.Notifications != nil {
		t.Fatalf("notifications = %#v, want disabled", got.Notifications)
	}
}

func TestMergeWorkflowCapabilitiesUpdateKeepsNotificationInstructions(t *testing.T) {
	incoming := &WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{
			RunSummaryInstructions: "Include delivered outputs and the primary metric.",
		},
	}
	got := mergeWorkflowCapabilitiesUpdate(WorkflowCapabilities{}, incoming)
	if got.Notifications == nil {
		t.Fatal("notification instructions were discarded")
	}
	if got.Notifications.RunSummaryInstructions != incoming.Notifications.RunSummaryInstructions {
		t.Fatalf("run instructions = %q, want %q", got.Notifications.RunSummaryInstructions, incoming.Notifications.RunSummaryInstructions)
	}

}

func TestMergeWorkflowCapabilitiesUpdateKeepsSummaryChannelRoutes(t *testing.T) {
	incoming := &WorkflowCapabilities{Notifications: &WorkflowNotificationConfig{
		RunSummaryChannels: []string{"slack"},
	}}
	got := mergeWorkflowCapabilitiesUpdate(WorkflowCapabilities{}, incoming)
	if got.Notifications == nil {
		t.Fatal("notification channel routes were discarded")
	}
	if len(got.Notifications.RunSummaryChannels) != 1 || got.Notifications.RunSummaryChannels[0] != "slack" {
		t.Fatalf("run channels = %#v, want slack", got.Notifications.RunSummaryChannels)
	}

}

func TestLegacyNotificationInstructionsRemainEffectiveForRunSummary(t *testing.T) {
	config := &WorkflowNotificationConfig{Instructions: "Use a detailed owner summary."}
	if got := config.EffectiveRunSummaryInstructions(); got != config.Instructions {
		t.Fatalf("run instructions = %q, want legacy %q", got, config.Instructions)
	}

}
