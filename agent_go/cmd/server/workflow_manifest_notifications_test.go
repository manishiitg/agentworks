package server

import (
	"testing"
)

func TestMergeWorkflowCapabilitiesUpdateCanDisableNotifications(t *testing.T) {
	existing := WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{WakeTab: &WorkflowWakeTab{CrewID: "crew", ChatID: "main"}},
	}
	incoming := &WorkflowCapabilities{
		Notifications: &WorkflowNotificationConfig{},
	}
	got := mergeWorkflowCapabilitiesUpdate(existing, incoming)
	if got.Notifications != nil {
		t.Fatalf("notifications = %#v, want disabled", got.Notifications)
	}
}
