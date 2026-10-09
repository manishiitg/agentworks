package server

import (
	"strings"
	"testing"
)

// manage_schedules follows the Schedules page: a reader cannot create one, and
// a schedule must belong to the workflow named in the call.
func TestExternalManageSchedulesFollowsRoleAndTarget(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.scheduler = &SchedulerService{api: f.api}
	create := map[string]any{"workflow_id": "invoices", "action": "create",
		"schedule": map[string]any{"name": "Morning", "cron_expression": "0 9 * * *", "timezone": "UTC", "enabled": true, "messages": []any{"Run the invoices"}}}
	if w := f.call(t, "reader", "manage_schedules", create); w.Code != 403 {
		t.Fatalf("reader created a schedule: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "manage_schedules", map[string]any{"workflow_id": "invoices", "action": "delete", "schedule_id": "not-on-this-workflow"}); w.Code != 404 || !strings.Contains(w.Body.String(), "schedule_not_found") {
		t.Fatalf("foreign schedule id: %d %s", w.Code, w.Body)
	}
}

// manage_triggers follows the trigger pages: a reader can list a workflow's
// triggers but not create one.
func TestExternalManageTriggersNeedsWriteAccess(t *testing.T) {
	f := newExternalToolsFixture(t)
	w := f.call(t, "reader", "manage_triggers", map[string]any{"workflow_id": "invoices", "action": "create",
		"trigger": map[string]any{"name": "Inbound", "enabled": true, "auth_mode": "bearer", "route_selections": map[string]any{}, "group_names": []any{}}})
	if w.Code != 403 {
		t.Fatalf("reader created a trigger: %d %s", w.Code, w.Body)
	}
}

// manage_project: delete is permanent, so it needs the ID repeated; access
// changes follow the share dialog (owners only); an owner can rename.
func TestExternalManageProjectGuards(t *testing.T) {
	f := newExternalToolsFixture(t)
	if w := f.call(t, "owner", "manage_project", map[string]any{"workflow_id": "invoices", "action": "delete"}); w.Code != 400 || !strings.Contains(w.Body.String(), "confirm_required") {
		t.Fatalf("delete without confirm: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "reader", "manage_project", map[string]any{"workflow_id": "invoices", "action": "set_access", "readers": []any{"outsider"}}); w.Code < 400 {
		t.Fatalf("reader changed access: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "manage_project", map[string]any{"workflow_id": "invoices", "action": "rename", "label": "Invoices v2"}); w.Code != 200 {
		t.Fatalf("owner rename: %d %s", w.Code, w.Body)
	}
	if manifest, _, _ := ReadWorkflowManifest(t.Context(), "Workflow/invoices"); manifest == nil || manifest.Label != "Invoices v2" {
		t.Fatalf("label not saved: %+v", manifest)
	}
}
