package services

import (
	"context"
	"fmt"
	"testing"
)

func TestRouteSummariesKeepIndependentHistoryAndIdentity(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	c := NewOrgDashboardConnector()
	send := func(kind string, routes ...NotificationRouteSummary) {
		t.Helper()
		_, err := c.SendUserNotification(ctx, "Shared work and rendered route details", "", &NotificationDestination{
			WorkspacePath: "Workflow/routes", Content: &NotificationContent{Text: "Shared work", Summary: &NotificationSummary{
				Kind: kind, Status: "completed", Routes: routes,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	quiet := NotificationRouteSummary{RoutingStepID: "router-a", RouteID: "publish", Label: "Publishing A", Title: "Needs access", Status: "blocked", Message: "No publishing happened."}
	busy := NotificationRouteSummary{RoutingStepID: "router-b", RouteID: "publish", Label: "Publishing B", Title: "Published", Status: "completed", Message: "One article published."}
	send("run_summary", quiet, busy)
	send("pulse_summary", quiet)
	for i := 0; i < 55; i++ {
		busy.Title = fmt.Sprintf("Busy run %d", i)
		send("run_summary", busy)
	}
	got, err := ListOrgDashboardNotifications(ctx, "Workflow/routes", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Recent) != 1 || len(got.ByRoute) != 2 {
		t.Fatalf("lost per-route history: %+v", got)
	}
	for _, route := range got.ByRoute {
		switch route.RoutingStepID {
		case "router-a":
			if route.RunSummary == nil || route.PulseSummary == nil || route.RunSummary.Status != "blocked" || route.RunSummary.Title != "Needs access" {
				t.Fatalf("busy route evicted quiet route or changed its status: %+v", route)
			}
		case "router-b":
			if route.RunSummary == nil || route.RunSummary.Title != "Busy run 54" || route.PulseSummary != nil {
				t.Fatalf("invented Autopilot coverage or lost latest run: %+v", route)
			}
		default:
			t.Fatalf("unexpected route: %+v", route)
		}
	}
	if got.RunSummary.Message != "Shared work" || len(got.RunSummary.Routes) != 1 {
		t.Fatalf("route facts should remain structured: %+v", got.RunSummary)
	}
	_, dbPath, _ := orgDashboardDBPath("Workflow/routes")
	db, err := openOrgDashboardDB(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM org_dashboard_notifications`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 52 {
		t.Fatalf("retention should keep 50 busy runs, the quiet digest, and quiet Autopilot; got %d", count)
	}
	other, err := ListOrgDashboardNotifications(ctx, "Workflow/other", 10)
	if err != nil || len(other.ByRoute) != 0 {
		t.Fatalf("cross-workflow route leakage: %+v %v", other, err)
	}
}

func TestLegacyRouteLabelsAreNotGuessedIntoCanonicalRoutes(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	c := NewOrgDashboardConnector()
	for _, summary := range []*NotificationSummary{
		{Kind: "run_summary", Route: "publish", Status: "completed"},
		{Kind: "pulse_summary", Status: "monitoring"},
		{Kind: "run_summary", Routes: []NotificationRouteSummary{{RoutingStepID: "router", RouteID: "publish", Status: "blocked"}}},
	} {
		if _, err := c.SendUserNotification(ctx, "Recorded evidence", "", &NotificationDestination{WorkspacePath: "Workflow/routes", Content: &NotificationContent{Summary: summary}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListOrgDashboardNotifications(ctx, "Workflow/routes", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ByRoute) != 2 {
		t.Fatalf("legacy label must remain distinct: %+v", got.ByRoute)
	}
	for _, group := range got.ByRoute {
		if group.PulseSummary != nil {
			t.Fatalf("workflow-wide Autopilot must not imply route coverage: %+v", group)
		}
	}
}
