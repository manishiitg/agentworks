package server

import (
	"strings"
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
		RunSummaryGmailConnectionIDs:   []string{"gmail_001", "gmail_002"},
		PulseSummaryGmailConnectionIDs: []string{"gmail_003"},
		RunSummaryRecipients:           []string{"owner@example.com"},
	}
	setWorkflowNotificationSender(config, " gmail_004 ")
	if config.GmailConnectionID != "gmail_004" || len(config.RunSummaryGmailConnectionIDs) != 0 || len(config.PulseSummaryGmailConnectionIDs) != 0 {
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
			RunSummaryInstructions:   "Include delivered outputs and the primary metric.",
			PulseSummaryInstructions: "Put decisions and material fixes first.",
		},
	}
	got := mergeWorkflowCapabilitiesUpdate(WorkflowCapabilities{}, incoming)
	if got.Notifications == nil {
		t.Fatal("notification instructions were discarded")
	}
	if got.Notifications.RunSummaryInstructions != incoming.Notifications.RunSummaryInstructions {
		t.Fatalf("run instructions = %q, want %q", got.Notifications.RunSummaryInstructions, incoming.Notifications.RunSummaryInstructions)
	}
	if got.Notifications.PulseSummaryInstructions != incoming.Notifications.PulseSummaryInstructions {
		t.Fatalf("pulse instructions = %q, want %q", got.Notifications.PulseSummaryInstructions, incoming.Notifications.PulseSummaryInstructions)
	}
}

func TestMergeWorkflowCapabilitiesUpdateKeepsSummaryChannelRoutes(t *testing.T) {
	incoming := &WorkflowCapabilities{Notifications: &WorkflowNotificationConfig{
		RunSummaryChannels:   []string{"slack"},
		PulseSummaryChannels: []string{"gmail"},
	}}
	got := mergeWorkflowCapabilitiesUpdate(WorkflowCapabilities{}, incoming)
	if got.Notifications == nil {
		t.Fatal("notification channel routes were discarded")
	}
	if len(got.Notifications.RunSummaryChannels) != 1 || got.Notifications.RunSummaryChannels[0] != "slack" {
		t.Fatalf("run channels = %#v, want slack", got.Notifications.RunSummaryChannels)
	}
	if len(got.Notifications.PulseSummaryChannels) != 1 || got.Notifications.PulseSummaryChannels[0] != "gmail" {
		t.Fatalf("pulse channels = %#v, want gmail", got.Notifications.PulseSummaryChannels)
	}
}

func TestLegacyNotificationInstructionsRemainEffectiveForBothSections(t *testing.T) {
	config := &WorkflowNotificationConfig{Instructions: "Use a detailed owner summary."}
	if got := config.EffectiveRunSummaryInstructions(); got != config.Instructions {
		t.Fatalf("run instructions = %q, want legacy %q", got, config.Instructions)
	}
	if got := config.EffectivePulseSummaryInstructions(); got != config.Instructions {
		t.Fatalf("pulse instructions = %q, want legacy %q", got, config.Instructions)
	}
}

func TestNotificationInstructionsDefaultToMaterialTransitions(t *testing.T) {
	var config *WorkflowNotificationConfig
	if got := runSummaryInstructionsOrDefault(config); !strings.Contains(got, "get_notification_history") || !strings.Contains(got, "record_only") {
		t.Fatalf("run default does not require history-aware quiet delivery: %q", got)
	}
	if got := pulseSummaryInstructionsOrDefault(config); !strings.Contains(got, "get_notification_history") || !strings.Contains(got, "deliver_only") {
		t.Fatalf("Pulse default does not require history-aware external delivery: %q", got)
	}

	custom := &WorkflowNotificationConfig{
		RunSummaryInstructions:   "Send every run.",
		PulseSummaryInstructions: "Never send externally.",
	}
	if got := custom.EffectiveRunSummaryInstructions(); got != "Send every run." {
		t.Fatalf("run custom instruction = %q", got)
	}
	if got := custom.EffectivePulseSummaryInstructions(); got != "Never send externally." {
		t.Fatalf("Pulse custom instruction = %q", got)
	}
}
