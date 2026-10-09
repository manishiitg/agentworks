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
