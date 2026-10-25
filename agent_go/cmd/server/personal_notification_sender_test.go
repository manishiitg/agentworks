package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func TestPersonalNotificationSenderSave(t *testing.T) {
	api, ctx, _ := gmailConnectionFixture(t)
	svc := services.GetGmailService()
	cfg := svc.GetConfig()
	cfg.ManuallyDisabled = true
	cfg.Enabled = false
	cfg.Connections[0].Personal = true
	cfg.Connections[0].Workspaces = []string{"Workflow/mail"}
	if err := svc.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"notification_gmail_connection_id", "run_notification_gmail_connection_ids", "goal_update_notification_gmail_connection_ids"} {
		for _, id := range []string{"mail", "missing"} {
			body := map[string]interface{}{"workspace_path": "Workflow/mail"}
			if field == "notification_gmail_connection_id" {
				body[field] = id
			} else {
				body[field] = []string{id}
			}
			raw, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)).WithContext(ctx)
			rec := httptest.NewRecorder()
			api.handleUpdateWorkflowManifest(rec, req)
			want := http.StatusOK
			if id == "missing" {
				want = http.StatusBadRequest
			}
			if rec.Code != want {
				t.Fatalf("%s/%s: %d %s", field, id, rec.Code, rec.Body.String())
			}
		}
	}
	cfg.Connections[0].Workspaces = []string{"Workflow/other"}
	if err := svc.SaveConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"workspace_path":"Workflow/mail","notification_gmail_connection_id":"mail"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	api.handleUpdateWorkflowManifest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong item: %d %s", rec.Code, rec.Body.String())
	}

}

// The HTTP query path calls this same admission boundary before binding any
// notification destination. Knowing an item's path and sender ID grants no access.
func TestPersonalNotificationRejectsUnauthorizedItem(t *testing.T) {
	api, _, workspace := gmailConnectionFixture(t)
	manifest := NewWorkflowManifest("Private item")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	raw, _ := json.Marshal(manifest)
	workspace.files["Workflow/mail/workflow.json"] = string(raw)
	for _, user := range []string{"owner", "reader"} {
		ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
		for _, folder := range []string{"Workflow/mail", "/Workflow/mail", " Workflow/mail", "Workflow/missing"} {
			req := QueryRequest{Query: "Email this update", AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: folder, NotificationGmailConnectionID: "mail"}
			_, access, err := api.admitQueryTarget(ctx, &req, user, "notification-access-test")
			if folder == "Workflow/mail" && user == "reader" && (err == nil || access != WorkflowAccessNone) {
				t.Fatalf("unshared item admitted: %v %v", access, err)
			}
			if folder == "Workflow/mail" && user == "owner" && err != nil {
				t.Fatalf("owner refused: %v", err)
			}
			dest := notificationDestinationFromQuery(req, user)
			if folder == "Workflow/mail" && user == "owner" {
				if dest.Gmail.Workspace != "Workflow/mail" {
					t.Fatal("admitted item not bound")
				}
			} else if dest.Gmail.Workspace != "" {
				t.Fatalf("unadmitted sender item bound: user=%s folder=%q", user, folder)
			}
		}
	}
}
