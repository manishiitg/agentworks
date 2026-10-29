package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// PLAT-871-28: a Goal message schedule runs in the owner's picked tab while it
// exists, else in a new conversation (the Goal default); a plan run never does,
// and another owner's tab is never the schedule owner's to type in.
func TestGoalMessageScheduleRunsInOwnersTab(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","can_edit":true,"can_create":true},{"id":"bob","username":"bob","role":"creator","can_edit":true,"can_create":true}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{"Workflow/demo/workflow.json": `{"id":"wf","created_by":"alice","access":{"owners":["alice","bob"]}}`}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	scope := workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}
	as := func(user string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
	}
	aliceTab, err := storeGoalOwnerTab(as("alice"), scope, "Review", false, "")
	if err != nil {
		t.Fatal(err)
	}
	bobTab, err := storeGoalOwnerTab(as("bob"), scope, "Bob's", false, "")
	if err != nil {
		t.Fatal(err)
	}
	fire := func(record projectactions.Schedule) string {
		sched, err := workflowScheduleFromAction(record)
		if err != nil {
			t.Fatal(err)
		}
		session, _, _ := goalScheduleConversation(context.Background(), &ScheduleContext{WorkspacePath: "Workflow/demo", WorkflowID: "wf", OwnerUserID: "alice", Schedule: sched})
		return session
	}
	message := projectactions.Schedule{ID: "s1", Input: []byte(`{"messages":["check the inbox"]}`), Action: projectactions.Action{Type: "message", Tab: aliceTab.ID}}
	if got := fire(message); got != aliceTab.SessionID {
		t.Fatalf("picked tab: session %q, want %q", got, aliceTab.SessionID)
	}
	run := message
	run.Input, run.Action.Type = []byte(`{}`), "run"
	if got := fire(run); got != "" {
		t.Fatalf("plan run went to a chat: %q", got)
	}
	gone := message
	gone.Action.Tab = "wf" + codeChatSideMarker + "closed"
	if got := fire(gone); got != "" {
		t.Fatalf("closed tab did not fall back to a new conversation: %q", got)
	}
	other := message
	other.Action.Tab = bobTab.ID
	if got := fire(other); got != "" {
		t.Fatalf("schedule typed into another owner's tab: %q", got)
	}
}
