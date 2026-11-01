package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// PLAT-871-28: a Goal message schedule runs in the owner's picked tab while it
// exists, else in a new conversation (the Goal default); a plan run never does,
// and another owner's tab is never the schedule owner's to type in.
func TestGoalMessageScheduleRunsInOwnersTab(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","can_edit":true,"can_create":true},{"id":"bob","username":"bob","role":"creator","can_edit":true,"can_create":true}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{"Workflow/demo/workflow.json": `{"id":"wf","created_by":"alice","access":{"owners":["alice","bob"]}}`}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	scope := workflowBuilderScope{PresetID: "wf", Workspace: "Workflow/demo"}
	as := func(user string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user, Username: user})
	}
	aliceTab, err := storeGoalOwnerTab(as("alice"), scope, "Builder", false, "")
	if err != nil {
		t.Fatal(err)
	}
	bobTab, err := storeGoalOwnerTab(as("bob"), scope, "Bob's", false, "")
	if err != nil {
		t.Fatal(err)
	}
	// The scheduled turn updates its saved transcript, not the registry row.
	// Lists and schedule choices must show that activity and its first message.
	updated := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	conversationPath := filepath.Join(root, "Workflow/demo/builder/conversation/users/alice/2026-10-30", "session-"+aliceTab.SessionID+"-conversation.json")
	if err := os.MkdirAll(filepath.Dir(conversationPath), 0755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"session_id": aliceTab.SessionID, "user_id": "alice", "phase_id": "workflow-builder", "updated_at": updated,
		"conversation_history": []builderConversationMessage{{Role: "human", Parts: []builderConversationPart{{Text: "Review the scheduled reply"}}}},
	})
	if err := os.WriteFile(conversationPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	tabs, err := api.goalOwnerTabs(as("alice"), scope)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tab := range tabs {
		if tab.SessionID == aliceTab.SessionID {
			found = true
			if tab.Name != "Review the scheduled reply" || tab.UpdatedAt != updated {
				t.Fatalf("stale tab metadata: %+v", tab)
			}
		}
	}
	if !found {
		t.Fatal("saved tab missing")
	}
	chats, err := goalScheduleChats(as("alice"), "alice", "wf", "Workflow/demo")
	if err != nil || len(chats) != 1 || chats[0].Name != "Review the scheduled reply" {
		t.Fatalf("schedule destinations differ from tab list: %+v, %v", chats, err)
	}
	fire := func(record projectactions.Schedule) string {
		sched, err := workflowScheduleFromAction(record)
		if err != nil {
			t.Fatal(err)
		}
		session, _, _ := goalScheduleConversation(context.Background(), &ScheduleContext{WorkspacePath: "Workflow/demo", WorkflowID: "wf", OwnerUserID: "alice", Schedule: sched})
		return session
	}
	message := projectactions.Schedule{ID: "s1", Name: "Check messages", When: projectactions.When{Cron: "0 9 * * *", Timezone: "UTC"}, Input: []byte(`{"messages":["check the inbox"]}`), Action: projectactions.Action{Type: "message", Tab: aliceTab.ID}}
	if got := fire(message); got != aliceTab.SessionID {
		t.Fatalf("picked tab: session %q, want %q", got, aliceTab.SessionID)
	}
	// A decoded message carries ChatID and ChatKey. Changing its destination
	// must persist the new selection, including clearing the old chat.
	for _, destination := range []string{runDestinationNew, runDestinationChat, runDestinationIsolated} {
		sched, err := workflowScheduleFromAction(message)
		if err != nil {
			t.Fatal(err)
		}
		main := "main"
		if err := applyGoalScheduleDestination(as("alice"), &WorkflowManifest{ID: "wf", CreatedBy: "alice"}, "Workflow/demo", &sched, destination, &main); err != nil {
			t.Fatal(err)
		}
		encoded, err := actionFromWorkflowSchedule(projectactions.Project{Kind: "goal", ID: "wf"}, "alice", sched)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if destination == runDestinationChat {
			want = crewOwnerDefaultChatKey("wf", "alice")
		}
		if encoded.Action.Tab != want {
			t.Fatalf("destination=%s kept old chat %q, want %q", destination, encoded.Action.Tab, want)
		}
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
