package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"reflect"
	"strings"
	"testing"
)

func TestUserAccessToolsPermissionsAndRevocation(t *testing.T) {
	f := newExternalToolsFixture(t)
	api := f.api
	f.write(t, sharedSecretsTestWorkflow+"/workflow.json", `{"id":"wf_shared","label":"Renewals","created_by":"a1","access":{"owners":["a1"],"readers":["c3"]}}`)
	registerWorkflowOwner(sharedSecretsTestWorkflow, "a1")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","role":"admin"},{"id":"a1","username":"owner","email":"owner@example.com","role":"creator"},{"id":"c3","username":"reader","email":"reader@example.com","role":"viewer"}]}`)
	policy := workflowChatPolicy{Capabilities: map[string]bool{"user_management": true}}
	register := func(id string) recordedTool {
		t.Helper()
		r := &recordingRegistrar{}
		if err := api.registerUserAccessTools(r, id, sharedSecretsTestWorkflow, policy); err != nil {
			t.Fatal(err)
		}
		return r.tools["manage_user_access"]
	}
	owner, reader, admin := register("a1"), register("c3"), register("admin")
	ctx := context.Background()
	for _, action := range []string{"get_workflow_access", "set_workflow_access", "list_users", "update_user", "delete_user"} {
		if _, err := reader.exec(ctx, map[string]interface{}{"action": action}); err == nil {
			t.Fatalf("reader allowed %s", action)
		}
	}
	// Accounts are added by DevOps on the server: the tool offers no create action to anyone.
	if _, err := admin.exec(ctx, map[string]interface{}{"action": "create_user", "username": "unexpected"}); err == nil {
		t.Fatal("create_user is still offered")
	}
	if _, err := owner.exec(ctx, map[string]interface{}{"action": "list_users"}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.exec(ctx, map[string]interface{}{"action": "get_workflow_access"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Workflow/missing", "Workflow/renewals/../other", ""} {
		if _, err := admin.exec(ctx, map[string]interface{}{"action": "get_workflow_access", "workspace_path": path}); err == nil {
			t.Fatalf("invalid path %q accepted", path)
		}
	}
	// The current main-owner rule keeps the owner even when omitted from the list.
	if _, err := owner.exec(ctx, map[string]interface{}{"action": "set_workflow_access", "owners": []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, manifest := workflowAccessForWorkspacePath(ctx, &UserClaims{UserID: "a1"}, sharedSecretsTestWorkflow); manifest == nil || !containsID(manifest.effectiveOwners(), "a1") {
		t.Fatal("main owner was removed")
	}
	if _, err := owner.exec(ctx, map[string]interface{}{"action": "set_workflow_access", "owners": []string{"owner@example.com"}, "readers": []string{"reader@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.exec(ctx, map[string]interface{}{"action": "set_workflow_access", "owners": []string{"missing@example.com"}}); err == nil || !strings.Contains(err.Error(), "email address of someone in your company") {
		t.Fatalf("unknown email did not explain the next step: %v", err)
	}

	dir, err := readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := addDirectoryUser(dir, "newreader@example.com", "newreader", "viewer", nil); err != nil {
		t.Fatal(err)
	}
	if err := saveUserDirectory(dir); err != nil {
		t.Fatal(err)
	}
	output, err := admin.exec(ctx, map[string]interface{}{"action": "update_user", "user_id": userIDForUsername("newreader"), "can_create": true})
	if err != nil || !strings.Contains(output, `"can_create":true`) {
		t.Fatalf("account update: %v", err)
	}
	if output, err := admin.exec(ctx, map[string]interface{}{"action": "delete_user", "user_id": userIDForUsername("newreader")}); err != nil || !strings.Contains(output, `"status":"deleted"`) {
		t.Fatalf("native account delete: %v", err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"admin","can_create":true,"products":[]},{"id":"a1","username":"owner","can_create":true,"products":[]}]}`)
	if _, err := admin.exec(ctx, map[string]interface{}{"action": "list_users"}); err == nil {
		t.Fatal("demoted non-owner retained directory authority")
	}
	if _, err := admin.exec(ctx, map[string]interface{}{"action": "update_user", "user_id": userIDForUsername("newreader"), "can_create": false}); err == nil {
		t.Fatal("demoted admin retained mutation authority")
	}
}

func TestUserAccessToolsAbsentOutsideBuilder(t *testing.T) {
	api := &StreamingAPI{}
	for _, origin := range []string{"interactive", "scheduled", "bot", "child"} {
		req := QueryRequest{}
		switch origin {
		case "scheduled":
			req.TriggeredBy = "cron"
		case "bot":
			req.BotPlatform = "slack"
		case "child":
			req.ParentSessionID = "parent"
		}
		policy := resolveWorkflowChatPolicy("session", req, nil, false)
		reg := &recordingRegistrar{}
		if err := api.registerUserAccessTools(reg, "user", "Workflow/example", policy); err != nil {
			t.Fatal(err)
		}
		_, exists := reg.tools["manage_user_access"]
		// The person's own scheduled and background turns get what typed turns get (owner, 5 Nov).
		want := true
		if exists != want {
			t.Fatalf("admission %s=%v", origin, exists)
		}
	}
}

// PLAT-746-504: access chat omitted the title when creating its first shared dashboard.
func TestAccessToolSharesSameDefaultDashboardAsShare(t *testing.T) {
	var standardFiles map[string]string
	for _, origin := range []string{"share", "access_tool"} {
		t.Run(origin, func(t *testing.T) {
			t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
			f := newExternalToolsFixture(t)
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","email":"owner@example.com","role":"creator","products":["agentworks"]},{"id":"reader","username":"reader","email":"reader@example.com","role":"viewer","products":["agentworks"]}]}`)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			request := dashboards.Request{Root: "Workflow/invoices", Action: "list", IncludeDraft: true}
			before, err := dashboardWorkspaceRequest(ctx, request)
			if err != nil || len(before.Dashboards) != 0 {
				t.Fatalf("fixture already has dashboard: %+v %v", before, err)
			}
			if origin == "share" {
				externalTestBody(t, f.call(t, "owner", "share", map[string]any{"action": "set", "workflow_id": "invoices", "mode": "people", "emails": []string{"reader@example.com"}}), 200)
			} else {
				registrar := &recordingRegistrar{}
				if err := f.api.registerUserAccessTools(registrar, "owner", "Workflow/invoices", workflowChatPolicy{Capabilities: map[string]bool{"user_management": true}}); err != nil {
					t.Fatal(err)
				}
				if _, err := registrar.tools["manage_user_access"].exec(ctx, map[string]any{"action": "set_workflow_access", "readers": []string{"reader@example.com"}}); err != nil {
					t.Fatal(err)
				}
			}
			after, err := dashboardWorkspaceRequest(ctx, request)
			if err != nil || len(after.Dashboards) != 1 || after.Dashboards[0].Title != "Invoice processing" || after.Dashboards[0].Published == "" {
				t.Fatalf("shared dashboard missing canonical title/publication: %+v %v", after, err)
			}
			page, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: request.Root, Action: "get", ID: after.Dashboards[0].ID, IncludeDraft: true})
			if err != nil || page.Snapshot == nil {
				t.Fatalf("missing published default: %+v %v", page, err)
			}
			if origin == "share" {
				standardFiles = page.Snapshot.Files
			} else if !reflect.DeepEqual(standardFiles, page.Snapshot.Files) {
				t.Fatal("access tool created a different standard dashboard")
			}
			_, manifest := workflowAccessForWorkspacePath(ctx, &UserClaims{UserID: "owner"}, request.Root)
			if manifest == nil || projectAccessKind(manifest) != "workflow" || workflowAccessForManifest(&UserClaims{UserID: "reader"}, manifest) != WorkflowAccessRead {
				t.Fatalf("shared workflow kind/access lost: %+v", manifest)
			}
		})
	}
}
