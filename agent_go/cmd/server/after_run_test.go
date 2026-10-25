package server

import (
	"context"
	"encoding/json"
	"strings"
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

// PLAT-825-112: both full-run finalizers use the Goal selection. Only the
// provider turn is replaced; workspace reads, configuration, freshness and
// housekeeping dispatch are real. The replacement writes an actual artifact.
func TestGoalAfterRunsDispatchesConfiguredBackupForScheduledAndManualRuns(t *testing.T) {
	server, store := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	t.Setenv("MULTI_USER_MODE", "false")
	const path = "Workflow/after-runs"
	api := &StreamingAPI{}
	svc := NewSchedulerService(api)
	api.scheduler = svc
	marker := path + "/backup/qa-result.txt"
	svc.housekeepingTurn = func(ctx context.Context, request map[string]interface{}, session, user string) error {
		if !strings.Contains(request["query"].(string), "backup-strategy.md") {
			t.Fatal("housekeeping lost the configured backup action")
		}
		return writeFileToWorkspace(ctx, marker, "saved current Goal data")
	}
	manifest := NewWorkflowManifest("After runs")
	manifest.Backup = &WorkflowBackupConfig{Enabled: true, Destinations: []WorkflowBackupDestination{{ID: "local", Type: "local_zip", Covers: []string{"workflow"}}}}
	for _, enabled := range []bool{true, false} {
		manifest.Backup.AfterRuns = enabled
		raw, _ := json.Marshal(manifest)
		store.files[manifestPath(path)] = string(raw)
		for _, manual := range []bool{false, true} {
			delete(store.files, marker)
			if manual {
				svc.runManualAfterRunOptions(path, "iteration-1/default", "success", "builder-after-runs")
			} else {
				sctx := buildScheduleContext(path, manifest, WorkflowSchedule{ID: "daily", ScheduleType: "cron"})
				svc.runAfterRunOptions(context.Background(), sctx, manifest, "success", "", "iteration-1/default", "scheduled-after-runs", "run-1", time.Second)
			}
			if _, exists := store.files[marker]; exists != enabled {
				t.Fatalf("automatic Backup enabled=%v manual=%v: artifact present=%v", enabled, manual, exists)
			}
		}
	}
	// Selecting an unconfigured destination must never start an agent to create one.
	manifest.Backup.Enabled = false
	manifest.Backup.AfterRuns = true
	if work := svc.afterRunWorkDue(context.Background(), &ScheduleContext{WorkspacePath: path}, manifest, manifest.afterRunOptions()); work.Backup {
		t.Fatal("unconfigured automatic Backup was dispatched")
	}
}
