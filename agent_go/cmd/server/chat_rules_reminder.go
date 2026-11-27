package server

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// Chat rules reminders (owner, 27 Nov). The card and the unrelated-work question live in the chat's instructions
// (chat_reply_style.go). In a long chat a rule can fade, so the person's message carries one short reminder line only
// when there is a reason:
//   - compaction: the chat was compacted since the last reminder (the summary may have dropped the rules);
//   - not_followed: the card is on, yet the last chatRulesIgnoredReplies replies that ended with a question had no card;
//   - long_break: chatRulesLongBreak since the person's last message;
//   - every_20: a fallback after chatRulesEveryTurns of the person's messages with no other reminder.
// The count is in memory: after a server restart the coding app starts with the current instructions anyway.

const (
	chatRulesEveryTurns     = 20
	chatRulesLongBreak      = 6 * time.Hour
	chatRulesIgnoredReplies = 3
)

type chatRulesState struct {
	// lastKey/lastReason: the same message resolved again (a live-input policy check, then the turn) gets the same
	// answer and is counted once.
	lastKey, lastReason string
	turns               int
	lastTurn            time.Time
	lastReminder        time.Time
}

var chatRulesReminders = struct {
	sync.Mutex
	bySession map[string]*chatRulesState
}{bySession: map[string]*chatRulesState{}}

// chatRulesReminderReason is why this person turn gets a reminder, or "". It counts the turn once per message (key).
func chatRulesReminderReason(sessionID, key string, now time.Time, card bool, events []storeevents.Event) string {
	chatRulesReminders.Lock()
	defer chatRulesReminders.Unlock()
	st := chatRulesReminders.bySession[sessionID]
	if st == nil {
		// First turn this server sees: the app runs with the current instructions.
		chatRulesReminders.bySession[sessionID] = &chatRulesState{lastKey: key, lastTurn: now, lastReminder: now}
		return ""
	}
	if key == st.lastKey && now.Sub(st.lastTurn) < 2*time.Minute {
		return st.lastReason
	}
	st.turns++
	reason := ""
	switch {
	case chatRulesCompactedSince(events, st.lastReminder):
		reason = "compaction"
	case card && chatRulesCardIgnored(events, st.lastReminder):
		reason = "not_followed"
	case now.Sub(st.lastTurn) >= chatRulesLongBreak:
		reason = "long_break"
	case st.turns >= chatRulesEveryTurns:
		reason = "every_20"
	}
	st.lastTurn, st.lastKey, st.lastReason = now, key, reason
	if reason != "" {
		st.turns, st.lastReminder = 0, now
	}
	return reason
}

func chatRulesCompactedSince(events []storeevents.Event, since time.Time) bool {
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if !event.Timestamp.After(since) {
			break
		}
		if event.Type != string(agentevents.ContextCompaction) || event.Data == nil {
			continue
		}
		if chatRulesEventField(event, "phase") == "end" {
			return true
		}
	}
	return false
}

// chatRulesCardIgnored: the newest replies since the last reminder that end with a question, up to
// chatRulesIgnoredReplies of them, all lack the card.
func chatRulesCardIgnored(events []storeevents.Event, since time.Time) bool {
	asked := 0
	for i := len(events) - 1; i >= 0 && asked < chatRulesIgnoredReplies; i-- {
		event := events[i]
		if !event.Timestamp.After(since) {
			break
		}
		text := chatRulesReplyText(event)
		if strings.Contains(text, "```next") {
			return false
		}
		if strings.HasSuffix(strings.TrimSpace(text), "?") {
			asked++
		}
	}
	return asked >= chatRulesIgnoredReplies
}

func chatRulesReplyText(event storeevents.Event) string {
	if event.Type != string(agentevents.EventTypeUnifiedCompletion) {
		return ""
	}
	return chatRulesEventField(event, "final_result")
}

// chatRulesEventField reads one field of an event's payload, typed (live) or generic (read back from disk).
func chatRulesEventField(event storeevents.Event, field string) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return ""
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	value, _ := fields[field].(string)
	return value
}
