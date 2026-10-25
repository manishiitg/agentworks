package services

import (
	"context"
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
			Kind:   "run_summary",
			Title:  "Run finished",
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
	if got.RunSummary == nil || got.RunSummary.Title != "Run finished" || got.RunSummary.Status != "waiting_for_user" {
		t.Fatalf("unexpected run summary: %#v", got.RunSummary)
	}
	if got.RunSummary.Route != "publish-reddit" {
		t.Fatalf("route = %q, want publish-reddit", got.RunSummary.Route)
	}
	if len(got.RunSummary.Fields) != 1 || got.RunSummary.Fields[0].Value != "2" {
		t.Fatalf("unexpected fields: %#v", got.RunSummary.Fields)
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
