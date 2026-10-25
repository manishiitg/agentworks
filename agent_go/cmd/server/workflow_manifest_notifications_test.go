package server

import (
	"testing"
)

func TestNotifySenderSavePreservesRecipients(t *testing.T) {
	config := &WorkflowNotificationConfig{
		RunSummaryRecipients: []string{"owner@example.com"},
	}
	setWorkflowNotificationSender(config, " gmail_004 ")
	if config.GmailConnectionID != "gmail_004" {
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
		Notifications: &WorkflowNotificationConfig{RunSummaryChannels: []string{"slack"}},
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
