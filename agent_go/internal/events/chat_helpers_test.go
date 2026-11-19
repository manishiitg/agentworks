package events

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3245: the helper list reads every helper in the saved chat, not only the
// events the live window still holds, and nothing else.
func TestChatHelperEventsReadsWholeSavedChat(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "helpers.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := NewEventStore(5)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat", "owner")
	if err = store.SetSessionPersistenceClass("chat", SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	add := func(event Event) {
		t.Helper()
		event.Timestamp = time.Now()
		if err := store.AddEventChecked("chat", event); err != nil {
			t.Fatal(err)
		}
	}
	native := func(id, kind string) Event {
		record := llmtypes.NativeRecord{ID: id, Provider: "claude-code", Kind: kind, Timestamp: time.Now()}
		return Event{ID: id, Type: string(agentevents.CodingAgentNativeActivity), Data: agentevents.NewAgentEvent(agentevents.NewCodingAgentNativeActivity(record))}
	}
	tool := func(id, name string) Event {
		return Event{ID: id, Type: string(agentevents.ToolCallStart), Data: agentevents.NewAgentEvent(&agentevents.ToolCallStartEvent{ToolName: name, ToolCallID: id})}
	}
	add(native("helper-1", "helper"))
	add(tool("spawn-1", "spawnAgent"))
	add(native("reply-1", "reply"))
	add(tool("read-1", "read_file"))
	for i := 0; i < 20; i++ { // pushes the helpers out of the 5-event live window
		add(journalTestEvent(string(rune('a'+i)), "user_message"))
	}
	add(tool("job-1", "mcp__llm-provider__delegate_coding_agent"))
	add(tool("lookalike-1", "delegateXcodingXagent"))

	found, err := store.ChatHelperEvents(context.Background(), "chat")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, event := range found {
		ids = append(ids, event.ID)
	}
	if len(ids) != 3 || ids[0] != "helper-1" || ids[1] != "spawn-1" || ids[2] != "job-1" {
		t.Fatalf("helper events = %v, want [helper-1 spawn-1 job-1]", ids)
	}
	if other, err := store.ChatHelperEvents(context.Background(), "another-chat"); err != nil || len(other) != 0 {
		t.Fatalf("helpers leaked across chats: %d %v", len(other), err)
	}
}
