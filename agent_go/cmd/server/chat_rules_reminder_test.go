package server

import (
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

func chatRulesReply(at time.Time, text string) storeevents.Event {
	return storeevents.Event{Type: string(agentevents.EventTypeUnifiedCompletion), Timestamp: at, Data: &agentevents.AgentEvent{Data: &agentevents.UnifiedCompletionEvent{FinalResult: text}}}
}

func TestChatRulesReminderOnlyWhenRulesMayFade(t *testing.T) {
	session := "chat-rules-test"
	defer func() {
		chatRulesReminders.Lock()
		delete(chatRulesReminders.bySession, session)
		chatRulesReminders.Unlock()
	}()
	t0 := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	if r := chatRulesReminderReason(session, "m0", t0, true, nil); r != "" {
		t.Fatalf("first turn the server sees: the app has the instructions, got %q", r)
	}
	if r := chatRulesReminderReason(session, "m1", t0.Add(time.Minute), true, nil); r != "" {
		t.Fatalf("an ordinary turn gets nothing, got %q", r)
	}
	if r := chatRulesReminderReason(session, "m1", t0.Add(time.Minute), true, nil); r != "" {
		t.Fatalf("the same message resolved twice is one turn, got %q", r)
	}
	// Card on, three replies asked the person something with no card.
	at := t0.Add(2 * time.Minute)
	ignored := []storeevents.Event{chatRulesReply(at, "Build it?"), chatRulesReply(at, "Which one?"), chatRulesReply(at, "Merge now?")}
	if r := chatRulesReminderReason(session, "m2", t0.Add(3*time.Minute), true, ignored); r != "not_followed" {
		t.Fatalf("an ignored card is reminded, got %q", r)
	}
	followed := []storeevents.Event{chatRulesReply(t0.Add(4*time.Minute), "Done?\n\n```next\n{}\n```"), chatRulesReply(t0.Add(4*time.Minute), "Ok?"), chatRulesReply(t0.Add(4*time.Minute), "So?")}
	if r := chatRulesReminderReason(session, "m3", t0.Add(5*time.Minute), true, followed); r != "" {
		t.Fatalf("a followed card needs no reminder, got %q", r)
	}
	compacted := []storeevents.Event{{Type: string(agentevents.ContextCompaction), Timestamp: t0.Add(6 * time.Minute), Data: &agentevents.AgentEvent{Data: &agentevents.ContextCompactionEvent{Phase: "end"}}}}
	if r := chatRulesReminderReason(session, "m4", t0.Add(7*time.Minute), true, compacted); r != "compaction" {
		t.Fatalf("a compaction is followed by a reminder, got %q", r)
	}
	if r := chatRulesReminderReason(session, "m5", t0.Add(7*time.Hour), true, nil); r != "long_break" {
		t.Fatalf("a long break is followed by a reminder, got %q", r)
	}
	now := t0.Add(7 * time.Hour)
	for i := 1; i < chatRulesEveryTurns; i++ {
		now = now.Add(time.Minute)
		if r := chatRulesReminderReason(session, "n"+time.Duration(i).String(), now, true, nil); r != "" {
			t.Fatalf("turn %d: no reminder before the fallback, got %q", i, r)
		}
	}
	if r := chatRulesReminderReason(session, "n-last", now.Add(time.Minute), true, nil); r != "every_20" {
		t.Fatalf("the fallback reminds every %d messages, got %q", chatRulesEveryTurns, r)
	}
}
