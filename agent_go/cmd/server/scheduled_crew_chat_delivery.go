package server

import (
	"context"
	"strings"
)

// Only the trusted product scheduler can set this routing intent. It is not a
// JSON request field and never changes the acting user or project permissions.
type scheduledCrewChatDeliveryKey struct{}

func isScheduledCrewChatDelivery(ctx context.Context) bool {
	allowed, _ := ctx.Value(scheduledCrewChatDeliveryKey{}).(bool)
	return allowed
}

func scheduleUsesCrewChatDelivery(job productScheduleJob) bool {
	if strings.TrimSpace(job.ProjectID) == "" || job.Schedule.Isolated || job.GuestCallerID != "" || job.PinRunMode {
		return false
	}
	if kind := strings.TrimSpace(job.AutomationKind); kind != "" && kind != "schedule" {
		return false
	}
	switch job.Profile.ID {
	case "work", "crew":
		return true
	default:
		return false
	}
}
