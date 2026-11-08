package server

import (
	"testing"
)

func TestNotifySenderSavePreservesRecipients(t *testing.T) {
	config := &WorkflowNotificationConfig{
		GmailConnectionID: "old",
	}
	setWorkflowNotificationSender(config, " gmail_004 ")
	if config.GmailConnectionID != "gmail_004" {
		t.Fatalf("sender selection did not replace legacy overrides: %+v", config)
	}
	setWorkflowNotificationSender(config, "")
	if config.GmailConnectionID != "" {
		t.Fatalf("clearing sender should inherit account default: %+v", config)
	}
}

func TestMergeWorkflowCapabilitiesUpdateCanDisableNotifications(t *testing.T) {
	existing := WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{GmailConnectionID: "old"},
	}
	incoming := &WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{},
	}
	got := mergeWorkflowCapabilitiesUpdate(existing, incoming)
	if got.Notifications != nil {
		t.Fatalf("notifications = %#v, want disabled", got.Notifications)
	}
}
