package events

import (
	"path/filepath"
	"testing"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

func rewindTestUser(id, text, display string) Event {
	message := &pkgevents.UserMessageEvent{Content: text}
	if display != "" {
		message.Metadata = map[string]interface{}{"display_content": display}
	}
	return Event{ID: id, Type: string(pkgevents.UserMessage), Timestamp: time.Now(), Data: pkgevents.NewAgentEvent(message)}
}

func rewindTestReply(id string) Event {
	return Event{ID: id, Type: "agent_reply", Timestamp: time.Now(), Data: pkgevents.NewAgentEvent(&pkgevents.UserMessageEvent{Content: "reply"})}
}

func TestRewindDurableChatDropsMessageAndLaterRows(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	session := "chat-rewind"
	for _, event := range []Event{
		rewindTestUser("user:a", "first ask", ""),
		rewindTestReply("r1"),
		rewindTestUser("user:b", "[handoff notice]\nsecond ask", "second ask"),
		rewindTestReply("r2"),
		rewindTestUser("user:c", "third ask", ""),
	} {
		if _, _, err := journal.Append(session, event); err != nil {
			t.Fatal(err)
		}
	}
	sequence, found, err := store.DurableChatEventSequence(session, "user:b")
	if err != nil || !found || sequence != 3 {
		t.Fatalf("sequence=%d found=%t err=%v", sequence, found, err)
	}
	users, err := store.DurableUserMessagesThrough(session, sequence)
	if err != nil || len(users) != 2 || users[1].ID != "user:b" {
		t.Fatalf("users=%v err=%v", users, err)
	}
	if text := UserMessageText(users[1]); text != "second ask" {
		t.Fatalf("display text = %q", text)
	}
	if text := UserMessageText(users[0]); text != "first ask" {
		t.Fatalf("content text = %q", text)
	}
	removed, err := store.RewindDurableChat(session, sequence)
	if err != nil || removed != 3 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	page, err := journal.ReadPage(session, DurableEventPageOptions{FromStart: true, Limit: 100})
	if err != nil || len(page.Events) != 2 || page.Events[1].ID != "r1" {
		t.Fatalf("remaining=%v err=%v", page.Events, err)
	}
	// The counter is not rewound: the next row numbers after the old tip.
	next, _, err := journal.Append(session, rewindTestUser("user:d", "edited ask", ""))
	if err != nil || next.Sequence != 6 {
		t.Fatalf("next sequence=%d err=%v", next.Sequence, err)
	}
	if _, found, _ := store.DurableChatEventSequence(session, "user:c"); found {
		t.Fatal("a later message survived the rewind")
	}
}

func TestRewindDurableChatNeedsAPoint(t *testing.T) {
	store := NewEventStore(10)
	defer store.Stop()
	if _, err := store.RewindDurableChat("chat", 0); err == nil {
		t.Fatal("expected refusal without a message")
	}
	if _, err := store.RewindDurableChat("chat", 3); err == nil {
		t.Fatal("expected refusal without a durable journal")
	}
}
