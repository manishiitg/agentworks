package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// Edit and resend (#3134) takes a chat back to just before one of the
// person's messages. The journal drops that message and every later row; the
// sequence counter is not rewound, so cursors held by open screens stay
// monotonic and a reload shows the shorter chat.

// DurableEventJournalRewinder finds a row and removes the journal's tail.
type DurableEventJournalRewinder interface {
	EventSequence(sessionID, eventID string) (int64, bool, error)
	DeleteFromSequence(sessionID string, fromSequence int64) (int64, error)
}

func (j *SQLiteEventJournal) EventSequence(sessionID, eventID string) (int64, bool, error) {
	if j == nil || j.db == nil || sessionID == "" || eventID == "" {
		return 0, false, nil
	}
	var sequence int64
	err := j.reader().QueryRow(`SELECT sequence FROM structured_chat_events WHERE session_id = ? AND event_id = ?`, sessionID, eventID).Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return sequence, true, nil
}

func (j *SQLiteEventJournal) DeleteFromSequence(sessionID string, fromSequence int64) (int64, error) {
	if j == nil || j.db == nil || sessionID == "" || fromSequence <= 0 {
		return 0, nil
	}
	tx, err := j.db.BeginTx(context.Background(), &sql.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM structured_chat_events WHERE session_id = ? AND sequence >= ?`, sessionID, fromSequence)
	if err != nil {
		return 0, err
	}
	deleted, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// DurableChatEventSequence returns the journal position of one saved row.
func (es *EventStore) DurableChatEventSequence(sessionID, eventID string) (int64, bool, error) {
	if es == nil {
		return 0, false, nil
	}
	es.mu.RLock()
	journal, ok := es.durableJournal.(DurableEventJournalRewinder)
	es.mu.RUnlock()
	if !ok || journal == nil {
		return 0, false, fmt.Errorf("this chat's saved history cannot be rewound")
	}
	return journal.EventSequence(sessionID, eventID)
}

// RewindDurableChat removes rows at fromSequence and later from the journal
// and from the in-memory buffer. It holds the session's append lock so no turn
// writes in between.
func (es *EventStore) RewindDurableChat(sessionID string, fromSequence int64) (int64, error) {
	if es == nil || strings.TrimSpace(sessionID) == "" || fromSequence <= 0 {
		return 0, fmt.Errorf("choose a saved message to go back to")
	}
	es.mu.RLock()
	journal, ok := es.durableJournal.(DurableEventJournalRewinder)
	es.mu.RUnlock()
	if !ok || journal == nil {
		return 0, fmt.Errorf("this chat's saved history cannot be rewound")
	}
	defer es.lockSessionAppend(sessionID)()
	deleted, err := journal.DeleteFromSequence(sessionID, fromSequence)
	if err != nil {
		return 0, err
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	if current, exists := es.events[sessionID]; exists {
		kept := current[:0:0]
		for _, event := range current {
			if event.Sequence > 0 && event.Sequence >= fromSequence {
				continue
			}
			kept = append(kept, event)
		}
		es.events[sessionID] = kept
		es.recomputeSessionBytesLocked(sessionID)
		es.rebuildTerminalEventsLocked(sessionID)
	}
	delete(es.generationPositions, sessionID)
	delete(es.thinkingRuns, sessionID)
	delete(es.expectedClientMessages, sessionID)
	return deleted, nil
}

// DurableUserMessagesThrough lists the person's saved messages from the start
// of the chat through toSequence, oldest first.
func (es *EventStore) DurableUserMessagesThrough(sessionID string, toSequence int64) ([]Event, error) {
	if es == nil {
		return nil, nil
	}
	es.mu.RLock()
	journal, ok := es.durableJournal.(DurableEventJournalPageReader)
	es.mu.RUnlock()
	if !ok || journal == nil {
		return nil, fmt.Errorf("this chat's saved history cannot be read")
	}
	out := []Event{}
	opts := DurableEventPageOptions{FromStart: true, Limit: 1000}
	for {
		page, err := journal.ReadPage(sessionID, opts)
		if err != nil {
			return nil, err
		}
		for _, event := range page.Events {
			if event.Sequence > toSequence {
				return out, nil
			}
			if event.Type == string(pkgevents.UserMessage) && isMainUserMessage(event) {
				out = append(out, event)
			}
		}
		if !page.HasNewer || len(page.Events) == 0 {
			return out, nil
		}
		opts = DurableEventPageOptions{AfterSequence: page.LatestSequence, Limit: 1000}
	}
}

func isMainUserMessage(event Event) bool {
	switch strings.ToLower(strings.TrimSpace(event.ExecutionKind)) {
	case "", "main", "main_agent", "chat":
		return true
	}
	return false
}

// UserMessageText returns what the person typed: the display text when the
// saved content carries a wrapper, else the content.
func UserMessageText(event Event) string {
	if message := userMessageEventData(&event); message != nil {
		if display, _ := message.Metadata["display_content"].(string); strings.TrimSpace(display) != "" {
			return strings.TrimSpace(display)
		}
		return strings.TrimSpace(message.Content)
	}
	if event.Data == nil {
		return ""
	}
	// A row read back from the journal carries its payload as generic fields.
	generic, ok := event.Data.Data.(*GenericEventData)
	if !ok || generic == nil {
		return ""
	}
	if metadata, ok := generic.Fields["metadata"].(map[string]interface{}); ok {
		if display, _ := metadata["display_content"].(string); strings.TrimSpace(display) != "" {
			return strings.TrimSpace(display)
		}
	}
	content, _ := generic.Fields["content"].(string)
	return strings.TrimSpace(content)
}
