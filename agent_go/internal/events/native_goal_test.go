package events

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-988: a goal older than the retained tail still restores after
// reopening the journal, including a native clear. Children never replace it.
func TestNativeGoalSnapshotSurvivesTailAndJournalReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goals.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(10)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat", "owner")
	if err = store.SetSessionPersistenceClass("chat", SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	appendGoal := func(id, parent string, goal *llmtypes.NativeGoal) {
		t.Helper()
		executionID, kind := "main:chat", "main_agent"
		if parent != "" && parent != "session:chat" {
			executionID, kind = "child", "background_agent"
		}
		err := store.AddEventChecked("chat", Event{ID: id, Type: "native_goal_updated", ExecutionID: executionID, ExecutionKind: kind, ParentExecutionID: parent, Timestamp: time.Now(), Data: agentevents.NewAgentEvent(&agentevents.NativeGoalEvent{Update: llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native", Goal: goal}})})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendGoal("goal", "session:chat", &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"})
	for i := 0; i < 20; i++ {
		if err = store.AddEventChecked("chat", journalTestEvent(string(rune('a'+i)), "user_message")); err != nil {
			t.Fatal(err)
		}
	}
	if err = journal.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err = OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store.SetDurableJournal(journal)
	event, err := journal.LatestNativeGoal(context.Background(), "chat")
	if err != nil || event == nil || event.ID != "goal" {
		t.Fatalf("goal lost on reconnect: %+v %v", event, err)
	}
	appendGoal("clear", "", nil)
	appendGoal("child", "parent", &llmtypes.NativeGoal{Objective: "Child task", Status: "active"})
	event, err = journal.LatestNativeGoal(context.Background(), "chat")
	if err != nil || event == nil || event.ID != "clear" {
		t.Fatalf("clear lost or replaced by child: %+v %v", event, err)
	}
	if event, err = journal.LatestNativeGoal(context.Background(), "another-chat"); err != nil || event != nil {
		t.Fatal("goal leaked across sessions", err)
	}
}
