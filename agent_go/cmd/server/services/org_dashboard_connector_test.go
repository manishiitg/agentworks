package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOrgDashboardConnectorPersistsClassifiedSummary(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	connector := NewOrgDashboardConnector()
	dest := &NotificationDestination{
		WorkspacePath: "Workflow/demo",
		Content: &NotificationContent{Summary: &NotificationSummary{
			Kind:   "pulse_summary",
			Title:  "Autopilot complete",
			Status: "waiting_for_user",
			Route:  "publish-reddit",
			Fields: []NotificationSummaryField{{Label: "Fixed", Value: "2"}},
		}},
	}
	id, err := connector.SendUserNotification(context.Background(), "Two issues fixed; one decision remains.", "", dest)
	if err != nil {
		t.Fatalf("SendUserNotification: %v", err)
	}
	if id == "" {
		t.Fatal("expected a durable org-dashboard notification id")
	}
	got, err := ListOrgDashboardNotifications(context.Background(), "Workflow/demo", 10)
	if err != nil {
		t.Fatalf("ListOrgDashboardNotifications: %v", err)
	}
	if got.PulseSummary == nil || got.PulseSummary.Title != "Autopilot complete" || got.PulseSummary.Status != "waiting_for_user" {
		t.Fatalf("unexpected pulse summary: %#v", got.PulseSummary)
	}
	if got.PulseSummary.Route != "publish-reddit" {
		t.Fatalf("route = %q, want publish-reddit", got.PulseSummary.Route)
	}
	if len(got.PulseSummary.Fields) != 1 || got.PulseSummary.Fields[0].Value != "2" {
		t.Fatalf("unexpected fields: %#v", got.PulseSummary.Fields)
	}

}

func TestOrgDashboardConnectorIgnoresGeneralNotification(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	connector := NewOrgDashboardConnector()
	id, err := connector.SendUserNotification(context.Background(), "FYI", "", &NotificationDestination{
		WorkspacePath: "Workflow/demo",
		Content:       &NotificationContent{Summary: &NotificationSummary{Kind: "general"}},
	})
	if err != nil {
		t.Fatalf("SendUserNotification: %v", err)
	}
	if id != "" {
		t.Fatalf("general notification should be skipped, got id %q", id)
	}
}

func TestOrgDashboardSchemaKeepsLegacyNotificationTableUsable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	_, dbPath, err := orgDashboardDBPath("Workflow/demo")
	if err != nil {
		t.Fatalf("orgDashboardDBPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("create db directory: %v", err)
	}
	db, err := openOrgDashboardDB(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE org_dashboard_notifications (
		id TEXT PRIMARY KEY, notification_kind TEXT NOT NULL, title TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'neutral', message TEXT NOT NULL,
		fields_json TEXT NOT NULL DEFAULT '[]', sections_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := ensureOrgDashboardSchema(context.Background(), db); err != nil {
		t.Fatalf("initialize legacy table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO org_dashboard_notifications
		(id, notification_kind, title, status, message, fields_json, sections_json, created_at)
		VALUES ('legacy-upgraded', 'run_summary', '', 'neutral', 'done', '[]', '[]', '2026-09-04T00:00:00Z')`); err != nil {
		t.Fatalf("write legacy-compatible row: %v", err)
	}
}

func TestOrgDashboardLatestKindsDoNotDependOnRecentHistoryLimit(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	connector := NewOrgDashboardConnector()
	workspacePath := "Workflow/demo"
	send := func(kind, title string) {
		t.Helper()
		_, err := connector.SendUserNotification(context.Background(), title, "", &NotificationDestination{
			WorkspacePath: workspacePath,
			Content: &NotificationContent{Summary: &NotificationSummary{
				Kind: kind, Title: title, Status: "success",
			}},
		})
		if err != nil {
			t.Fatalf("send %s: %v", kind, err)
		}
	}
	send("pulse_summary", "Autopilot retained")
	for i := 0; i < 5; i++ {
		send("run_summary", fmt.Sprintf("Run %d", i))
	}

	got, err := ListOrgDashboardNotifications(context.Background(), workspacePath, 2)
	if err != nil {
		t.Fatalf("ListOrgDashboardNotifications: %v", err)
	}
	if got.PulseSummary == nil || got.PulseSummary.Title != "Autopilot retained" {
		t.Fatalf("pulse summary was hidden by recent limit: %#v", got.PulseSummary)
	}
	if got.RunSummary == nil || got.RunSummary.Title != "Run 4" {
		t.Fatalf("unexpected latest run: %#v", got.RunSummary)
	}
	if len(got.Recent) != 2 {
		t.Fatalf("recent length = %d, want 2", len(got.Recent))
	}
}
