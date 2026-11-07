package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

type afterRunSummaryConnector struct {
	received chan *services.NotificationDestination
}

func (*afterRunSummaryConnector) Name() string    { return "test_after_run_summary" }
func (*afterRunSummaryConnector) IsEnabled() bool { return true }
func (*afterRunSummaryConnector) SendNotification(context.Context, string, string, string, *services.ButtonOptions, *services.NotificationDestination) (string, error) {
	return "", nil
}
func (c *afterRunSummaryConnector) SendUserNotification(_ context.Context, _, _ string, dest *services.NotificationDestination) (string, error) {
	c.received <- dest
	return "summary-receipt", nil
}

// Exercise the scheduler's real internal delivery path after the agent tool is
// removed. The connector is local and records delivery without contacting anyone.
func TestAfterRunSummaryDeliveryIsIndependentOfAgentTool(t *testing.T) {
	api, ctx, _ := gmailConnectionFixture(t)
	connector := &afterRunSummaryConnector{received: make(chan *services.NotificationDestination, 1)}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(connector)
	t.Cleanup(func() { manager.UnregisterConnector(connector.Name()) })
	sctx := &ScheduleContext{OwnerUserID: "owner", WorkspacePath: "Workflow/mail", Schedule: WorkflowSchedule{ID: "daily", Name: "Daily"}}
	err := api.scheduler.sendAfterRunNotification(ctx, sctx, &WorkflowManifest{Label: "Example workflow"}, "run-1", "error", "Missing input", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case dest := <-connector.received:
		if dest.WorkspacePath != "Workflow/mail" || dest.Content == nil || dest.Content.Summary == nil || dest.Content.Summary.Kind != "run_summary" || dest.Content.Summary.Status != "failed" {
			t.Fatalf("run summary lost its scope or outcome: %+v", dest)
		}
	default:
		t.Fatal("server run summary was not delivered")
	}
}

// PLAT-825-102: removing notify must not turn repeat successes into team spam.
func TestScheduledOutcomeOnlyDeliversFailuresAndStatusChanges(t *testing.T) {
	f := newExternalToolsFixture(t)
	runs := []ScheduleRunEntry{
		{ID: "other", ScheduleID: "other", Status: "error", StartedAt: time.Now()},
		{ID: "prior", ScheduleID: "daily", Status: "success", StartedAt: time.Now().Add(-time.Hour)},
		{ID: "current", ScheduleID: "daily", Status: "success", StartedAt: time.Now()},
	}
	data, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, "Workflow/invoices/schedule-runs.json", string(data))
	if shouldDeliverRunOutcome(t.Context(), "Workflow/invoices", "daily", "current", "success") {
		t.Fatal("routine successful repeat would notify the team")
	}
	for _, status := range []string{"error", "partial", "blocked", "no_run"} {
		if !shouldDeliverRunOutcome(t.Context(), "Workflow/invoices", "daily", "current", status) {
			t.Fatalf("%s did not notify", status)
		}
	}
	runs[1].Status = "error"
	data, _ = json.Marshal(runs)
	f.write(t, "Workflow/invoices/schedule-runs.json", string(data))
	if !shouldDeliverRunOutcome(t.Context(), "Workflow/invoices", "daily", "current", "success") {
		t.Fatal("recovery was suppressed")
	}
}
