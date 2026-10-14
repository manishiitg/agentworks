package virtualtools

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

type routeDeliveryRecorder struct {
	services.OrgDashboardConnector
	message string
}

func (r *routeDeliveryRecorder) Name() string { return "route_delivery_test" }
func (r *routeDeliveryRecorder) SendUserNotification(ctx context.Context, message, contextMsg string, dest *services.NotificationDestination) (string, error) {
	r.message = message
	return r.OrgDashboardConnector.SendUserNotification(ctx, message, contextMsg, dest)
}

func TestNotifyRouteDigestAcrossSessionAndDurableProvider(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	c := &routeDeliveryRecorder{}
	m := services.GetNotificationManager()
	m.RegisterConnector(c)
	t.Cleanup(func() { m.UnregisterConnector(c.Name()) })
	const session = "route-digest-bridge"
	RegisterSessionNotificationDestination(session, &services.NotificationDestination{WorkspacePath: "Workflow/routes", RouteSelections: map[string]string{"router": "publish"}})
	t.Cleanup(func() { DeleteSessionNotificationDestination(session) })
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, session)
	_, err := DeliverWorkflowNotification(ctx, map[string]interface{}{
		"message_for_user": "Shared work checked.", "notification_kind": "pulse_summary", "summary_status": "blocked",
		"exclude_channels": []interface{}{"gmail", "slack", "whatsapp"},
		"summary_routes": []interface{}{
			map[string]interface{}{"routing_step_id": "router", "route_id": "research", "title": "Access blocked", "status": "blocked", "message": "Waiting for access."},
			map[string]interface{}{"routing_step_id": "router", "route_id": "publish", "title": "Review complete", "status": "completed", "message": "Output verified."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.message, "Waiting for access.") || !strings.Contains(c.message, "Output verified.") {
		t.Fatalf("delivery omitted route evidence: %s", c.message)
	}
	got, err := services.ListOrgDashboardNotifications(ctx, "Workflow/routes", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.PulseSummary == nil || got.PulseSummary.Route != "" || len(got.PulseSummary.Routes) != 2 || len(got.ByRoute) != 2 || got.PulseSummary.Message != "Shared work checked." {
		t.Fatalf("digest lost route-specific evidence across bridge/provider: %+v", got)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "Workflow/routes/db/db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var legacyMessage string
	if err := db.QueryRow(`SELECT message FROM org_dashboard_notifications ORDER BY created_at DESC LIMIT 1`).Scan(&legacyMessage); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(legacyMessage, "Waiting for access.") || !strings.Contains(legacyMessage, "Output verified.") {
		t.Fatalf("existing Daily Action reports would lose route facts: %s", legacyMessage)
	}
}

func TestNotificationRouteInputAndChannelRendering(t *testing.T) {
	raw := []interface{}{map[string]interface{}{"routing_step_id": "router", "route_id": "publish", "label": "Publishing", "title": "Awaiting approval", "status": "waiting_for_user", "message": "Review <draft> before publishing."}}
	routes, err := notificationRoutesFromArg(raw)
	if err != nil {
		t.Fatal(err)
	}
	gc := &services.GmailContent{HTMLBody: "<body>Shared work</body>"}
	text := appendNotificationRouteContent("Shared work", routes)
	if !strings.Contains(text, "Publishing — Awaiting approval") || !strings.Contains(text, "Review <draft>") || gc.HTMLBody != "<body>Shared work</body>" {
		t.Fatalf("channel-neutral route content missing or custom Gmail HTML was changed: %s / %s", text, gc.HTMLBody)
	}
	if _, err := notificationRoutesFromArg(append(raw, raw[0])); err == nil {
		t.Fatal("duplicate route identities accepted")
	}
	for _, bad := range []interface{}{map[string]interface{}{}, []interface{}{map[string]interface{}{"route_id": "publish"}}, []interface{}{map[string]interface{}{"routing_step_id": "router", "route_id": "publish", "title": "Done", "message": "done", "status": "clean"}}} {
		if _, err := notificationRoutesFromArg(bad); err == nil {
			t.Fatalf("invalid route summary accepted: %+v", bad)
		}
	}
}

func TestPulseNotificationDoesNotInheritExecutionRoute(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ch := make(chan *services.NotificationDestination, 2)
	c := &testUserNotificationConnector{name: "route_contract", ch: ch}
	m := services.GetNotificationManager()
	m.RegisterConnector(c)
	t.Cleanup(func() { m.UnregisterConnector(c.Name()) })
	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{
		WorkspacePath: "Workflow/routes", RouteSelections: map[string]string{"router": "publish"},
	})
	for _, kind := range []string{"run_summary", "pulse_summary"} {
		_, err := DeliverWorkflowNotification(ctx, map[string]interface{}{"message_for_user": "Current evidence", "notification_kind": kind, "summary_status": "informational", "exclude_channels": []interface{}{"gmail", "slack", "whatsapp"}})
		if err != nil {
			t.Fatal(err)
		}
		dest := <-ch
		want := ""
		if kind == "run_summary" {
			want = "publish"
		}
		if dest.Content.Summary.Route != want {
			t.Fatalf("%s scope = %q, want %q", kind, dest.Content.Summary.Route, want)
		}
	}
}
