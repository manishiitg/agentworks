package events

import (
	"context"
	pkgevents "github.com/manishiitg/mcpagent/events"
	"testing"
	"time"
)

func TestUsageMessagesUseExactIdentityAndSavedBoundaries(t *testing.T) {
	j := openArtifactTestJournal(t)
	add := func(session, id, kind, usage string, data map[string]interface{}) Event {
		t.Helper()
		e, _, err := j.Append(session, Event{ID: id, Type: kind, Timestamp: time.Now(), UsageExecutionID: usage, Data: pkgevents.NewAgentEvent(NewGenericEventData(kind, data))})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := add("chat", "old-user", "user_message", "", map[string]interface{}{"content": "wrapped text", "metadata": map[string]interface{}{"display_content": "What changed?"}})
	add("chat", "ref", "reply_file_changes", "", map[string]interface{}{"start_sequence": 0, "end_sequence": first.Sequence, "execution_id": "old-turn"})
	// Historical saved references carry the execution on the event envelope.
	if _, err := j.db.Exec(`UPDATE structured_chat_events SET payload=json_set(payload,'$.execution_id','old-turn') WHERE session_id='chat' AND event_id='ref'`); err != nil {
		t.Fatal(err)
	}
	add("chat", "new-user", "user_message", "new-turn", map[string]interface{}{"content": "Run the checks"})
	add("other-chat", "other-user", "user_message", "new-turn", map[string]interface{}{"content": "private other text"})
	got, err := j.UsageTurnMessages(context.Background(), "chat", []string{"old-turn", "new-turn", "missing", "main:chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["old-turn"] != "What changed?" || got["new-turn"] != "Run the checks" {
		t.Fatalf("wrong exact messages: %#v", got)
	}
	// The new identity survives normal JSON persistence and restoration.
	rows, err := j.LoadTail("chat", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == "new-user" {
			found = row.UsageExecutionID == "new-turn"
		}
	}
	if !found {
		t.Fatal("usage identity lost")
	}
}
