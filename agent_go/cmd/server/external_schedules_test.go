package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
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

// A connection approved only for Crews may not act on the account's
// workflows through manage_project, nor see their questions in the inbox
// (2026-10-09 review of PLAT-738).
func TestExternalCrewOnlyConnectionCannotReachWorkflows(t *testing.T) {
	f := newExternalToolsFixture(t)
	crewOnly := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "crew-only", Name: "crews", Scopes: []string{"crews:read", "crews:run", "crews:write"}, AllWorkflows: true, AllCrews: true}}
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(data), crewOnly, nil))
		return w
	}
	for _, args := range []map[string]any{
		{"workflow_id": "invoices", "action": "get_access"},
		{"workflow_id": "invoices", "action": "rename", "label": "Taken over"},
	} {
		if w := call("manage_project", args); w.Code != 403 {
			t.Fatalf("Crew-only connection %v: %d %s", args["action"], w.Code, w.Body)
		}
	}
	if manifest, _, _ := ReadWorkflowManifest(t.Context(), "Workflow/invoices"); manifest == nil || manifest.Label == "Taken over" {
		t.Fatal("workflow changed through a Crew-only connection")
	}
	const session = "owner-invoices"
	f.api.activeSessions = map[string]*ActiveSessionInfo{session: {SessionID: session, UserID: "owner", WorkspacePath: "Workflow/invoices"}}
	id := "hf-" + uuid.NewString()
	if err := virtualtools.GetHumanFeedbackStore().CreatePendingRequest(id, "Send?", "", session, []string{"Yes"}, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	if w := call("list_needs_you", map[string]any{}); strings.Contains(w.Body.String(), id) {
		t.Fatalf("Crew-only connection saw a workflow question: %s", w.Body)
	}
}

// manage_messaging: routing a Slack channel is the owner's, as in the Slack
// tab; WhatsApp pairing is only pointed to, never done over MCP.
func TestExternalManageMessagingGuards(t *testing.T) {
	f := newExternalToolsFixture(t)
	if w := f.call(t, "reader", "manage_messaging", map[string]any{"workflow_id": "invoices", "action": "add_channel", "channel_id": "C0123ABCD"}); w.Code < 400 {
		t.Fatalf("reader routed a channel: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "manage_messaging", map[string]any{"workflow_id": "invoices", "action": "status"}); w.Code != 200 || !strings.Contains(w.Body.String(), `"slack_bot"`) {
		t.Fatalf("owner status: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, "owner", "manage_messaging", map[string]any{"workflow_id": "invoices", "action": "whatsapp_link"}); w.Code != 200 || !strings.Contains(w.Body.String(), "QR code") {
		t.Fatalf("whatsapp_link: %d %s", w.Code, w.Body)
	}
}
