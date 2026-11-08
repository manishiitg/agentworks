package server

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

type runOutcomeChannelSpy struct{ sent int }

func (*runOutcomeChannelSpy) Name() string    { return "test_run_outcome_channel" }
func (*runOutcomeChannelSpy) IsEnabled() bool { return true }
func (*runOutcomeChannelSpy) SendNotification(context.Context, string, string, string, *services.ButtonOptions, *services.NotificationDestination) (string, error) {
	return "", nil
}
func (c *runOutcomeChannelSpy) SendUserNotification(context.Context, string, string, *services.NotificationDestination) (string, error) {
	c.sent++
	return "sent", nil
}

// PLAT-746 "no run-summary concept": the run record is the summary. A run
// outcome lands in the workflow's Activity and is never pushed to a channel.
func TestRunOutcomeIsRecordedInActivityAndNotSentAnywhere(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	spy := &runOutcomeChannelSpy{}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(spy)
	t.Cleanup(func() { manager.UnregisterConnector(spy.Name()) })
	s := &SchedulerService{}
	sctx := &ScheduleContext{OwnerUserID: "owner", WorkspacePath: "Workflow/mail", Schedule: WorkflowSchedule{ID: "daily", Name: "Daily"}}
	if err := s.recordRunOutcome(t.Context(), sctx, &WorkflowManifest{Label: "Example workflow"}, "run-1", "error", "Missing input", time.Second); err != nil {
		t.Fatal(err)
	}
	history, err := services.ListOrgDashboardNotifications(t.Context(), "Workflow/mail", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Recent) != 1 || history.Recent[0].Kind != "run_summary" || history.Recent[0].Status != "failed" {
		t.Fatalf("run outcome missing from Activity: %+v", history.Recent)
	}
	if spy.sent != 0 {
		t.Fatalf("run outcome was pushed to a channel %d time(s)", spy.sent)
	}
}
