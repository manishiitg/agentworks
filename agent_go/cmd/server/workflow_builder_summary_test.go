package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkflowOwnerTabsShareProjectAndKeepControl(t *testing.T) {
	for _, kind := range []string{"workflow", "relay"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("MULTI_USER_MODE", "true")
			t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
			withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","can_edit":true,"can_create":true},{"id":"bob","username":"bob","role":"creator","can_edit":true,"can_create":true},{"id":"runner","username":"runner","role":"viewer"}]}`)
			manifest := `{"id":"wf","kind":"` + kind + `","created_by":"alice","access":{"owners":["alice","bob"],"readers":["runner"]}}`
			workspace := &mockWorkspaceAPI{files: map[string]string{"Workflow/demo/workflow.json": manifest}}
			srv := httptest.NewServer(workspace)
			defer srv.Close()
			t.Setenv("WORKSPACE_API_URL", srv.URL)
			api := lifecycleTestAPI()
			scope := workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}
			ctx := func(user string) context.Context {
				return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
			}
			main, err := api.goalOwnerTabSummary(ctx("alice"), scope, false)
			if err != nil {
				t.Fatal(err)
			}
			side, err := api.createGoalOwnerTab(ctx("alice"), scope, "Review", false, "")
			if err != nil {
				t.Fatal(err)
			}
			bob, err := api.goalOwnerTabSummary(ctx("bob"), scope, false)
			if err != nil || len(bob.Tabs) != 3 {
				t.Fatalf("shared tabs: %+v %v", bob, err)
			}
			if bob.SessionID == main.SessionID {
				t.Fatal("co-owner adopted another person's main")
			}
			request := func(user, action, id, name string) *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"preset_query_id": "wf", "workspace_path": "Workflow/demo", "action": action, "chat_id": id, "name": name})
				req := httptest.NewRequest("POST", "/api/workflow/chats", strings.NewReader(string(body))).WithContext(ctx(user))
				w := httptest.NewRecorder()
				api.handleGoalOwnerTabs(w, req)
				return w
			}
			if w := request("bob", "read", side.ID, ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, action := range []string{"rename", "close"} {
				if w := request("bob", action, side.ID, "Taken"); w.Code != 403 {
					t.Fatal("other owner control", action, w.Code)
				}
			}
			if api.crewSessionCanControl(httptest.NewRequest("POST", "/", nil).WithContext(ctx("bob")), side.SessionID) {
				t.Fatal("human control granted to co-owner")
			}
			if _, _, err := api.externalBuilderSession(httptest.NewRequest("GET", "/", nil).WithContext(ctx("bob")), scope.Workspace, side.SessionID); err == nil {
				t.Fatal("MCP admitted other owner's tab")
			}
			if _, _, err := api.externalBuilderSession(httptest.NewRequest("GET", "/", nil).WithContext(ctx("alice")), scope.Workspace, side.SessionID); err != nil {
				t.Fatal("empty owned tab rejected", err)
			}
			if w := request("runner", "list", "", ""); w.Code != 403 {
				t.Fatal("Run person saw Builder tabs", w.Code)
			}
			if w := request("alice", "rename", side.ID, "Review changes"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			if w := request("alice", "close", side.ID, ""); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			after, err := api.goalOwnerTabSummary(ctx("alice"), scope, false)
			if err != nil || after.SessionID != main.SessionID || len(after.Tabs) != 2 {
				t.Fatalf("side tab replaced main: %+v %v", after, err)
			}
			workspace.mu.Lock()
			workspace.files["Workflow/demo/workflow.json"] = strings.Replace(manifest, `"alice","bob"`, `"alice"`, 1)
			workspace.mu.Unlock()
			if w := request("bob", "read", main.Tabs[0].ID, ""); w.Code != 403 {
				t.Fatal("removed owner kept access", w.Code)
			}
		})
	}
}
