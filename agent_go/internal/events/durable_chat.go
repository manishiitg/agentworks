package events

import (
	"encoding/json"
	"strings"
)

const maxDurableChatEventBytes = 64 * 1024

// durableChatEventTypes is intentionally an allowlist. Adding a producer does
// not make it part of conversation history until its bounded, user-visible
// meaning is reviewed here.
var durableChatEventTypes = map[string]bool{
	"agent_end":                    true,
	"agent_error":                  true,
	"background_agent_completed":   true,
	"background_agent_started":     true,
	"background_agent_terminated":  true,
	"batch_execution_canceled":     true,
	"blocking_human_feedback":      true,
	"context_cancelled":            true, //nolint:misspell // Wire contract.
	"coding_agent_background_task": true,
	"coding_agent_question":        true,
	"conversation_end":             true,
	"conversation_error":           true,
	"conversation_resumed":         true,
	"human_feedback_resolved":      true,
	"live_input_confirmed":         true,
	"orchestrator_end":             true,
	"plan_approval":                true,
	"pre_validation_completed":     true,
	"product_interaction":          true,
	"request_human_feedback":       true,
	"tool_call_end":                true,
	"tool_call_error":              true,
	"tool_call_start":              true,
	"unified_completion":           true,
	"user_message":                 true,
}

var durableChildSummaryTypes = map[string]bool{
	"background_agent_completed":  true,
	"background_agent_started":    true,
	"background_agent_terminated": true,
}

// IsDurableChatEvent mirrors the journal projector for transport filtering.
// AddEventChecked publishes the projected row only after SQLite accepts it.
func IsDurableChatEvent(event Event) bool {
	if IsTranscriptMessage(event) {
		return true
	}
	if !durableChatEventTypes[event.Type] {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(event.ExecutionKind))
	return kind == "" || kind == "main" || kind == "main_agent" || kind == "chat" || durableChildSummaryTypes[event.Type]
}

// projectDurableChatEvent selects the rows that belong to chat history. Size
// bounding happens in the journal itself, which moves an oversized row into a
// private artifact and stores its summary (see spillOversizedEvent).
func projectDurableChatEvent(event Event) (Event, bool) {
	if !IsDurableChatEvent(event) {
		return Event{}, false
	}
	return event, true
}

func isDurableScalar(value interface{}) bool {
	switch value.(type) {
	case bool, float32, float64, int, int32, int64, json.Number:
		return true
	default:
		return false
	}
}

// ReadDurableChatPage reads the canonical SQLite conversation log directly.
// It never falls back to the in-memory transport window.
func (es *EventStore) ReadDurableChatPage(sessionID string, opts DurableEventPageOptions) (DurableEventPage, error) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return DurableEventPage{Events: []Event{}}, nil
	}
	es.adoptJournaledSession(sessionID)
	es.mu.RLock()
	journal, ok := es.durableJournal.(DurableEventJournalPageReader)
	class := es.persistenceClasses[sessionID]
	es.mu.RUnlock()
	if !ok || journal == nil || class != SessionPersistenceInteractiveChat {
		return DurableEventPage{Events: []Event{}}, nil
	}
	return journal.ReadPage(sessionID, opts)
}

func (es *EventStore) DeleteDurableChatSession(sessionID string) error {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	es.mu.RLock()
	journal, ok := es.durableJournal.(DurableEventJournalPageReader)
	es.mu.RUnlock()
	if !ok || journal == nil {
		return nil
	}
	// Hold the session's append lock so an in-flight turn cannot write a row
	// after the delete, then drop the in-memory class and buffer: a still
	// running turn continues live-only instead of re-creating ownerless rows.
	defer es.lockSessionAppend(sessionID)()
	if err := journal.DeleteSession(sessionID); err != nil {
		return err
	}
	es.RemoveSession(sessionID)
	es.mu.Lock()
	es.journalProbed[sessionID] = true
	es.mu.Unlock()
	return nil
}

// ImportDurableChatEvents seeds the canonical journal without publishing old
// events to live subscribers. It is used only by the one-time JSON migration.
func (es *EventStore) ImportDurableChatEvents(sessionID string, imported []Event) error {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	es.mu.RLock()
	journal := es.durableJournal
	es.mu.RUnlock()
	if journal == nil {
		return nil
	}
	for _, event := range imported {
		projected, keep := projectDurableChatEvent(event)
		if !keep {
			continue
		}
		if _, _, err := journal.Append(sessionID, projected); err != nil {
			return err
		}
	}
	if migrator, ok := journal.(DurableEventJournalMigrator); ok {
		return migrator.MarkMigrationComplete(sessionID)
	}
	return nil
}

func (es *EventStore) DurableChatMigrationComplete(sessionID string) (bool, error) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return false, nil
	}
	es.mu.RLock()
	journal := es.durableJournal
	es.mu.RUnlock()
	migrator, ok := journal.(DurableEventJournalMigrator)
	if !ok {
		return true, nil
	}
	return migrator.MigrationComplete(sessionID)
}

func (es *EventStore) MarkDurableChatMigrationComplete(sessionID string) error {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	es.mu.RLock()
	journal := es.durableJournal
	es.mu.RUnlock()
	if migrator, ok := journal.(DurableEventJournalMigrator); ok {
		return migrator.MarkMigrationComplete(sessionID)
	}
	return nil
}

func (es *EventStore) DurableChatOwner(sessionID string) (string, error) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return "", nil
	}
	es.mu.RLock()
	journal := es.durableJournal
	es.mu.RUnlock()
	if ownership, ok := journal.(DurableEventJournalOwnership); ok {
		return ownership.Owner(sessionID)
	}
	return "", nil
}
