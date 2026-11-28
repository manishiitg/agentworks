package events

import (
	"fmt"
	"strings"
)

// SessionPersistenceClass declares whether a session belongs in the durable
// conversation journal. Unknown sessions deliberately remain live-only.
type SessionPersistenceClass string

const (
	SessionPersistenceInteractiveChat SessionPersistenceClass = "interactive_chat"
	SessionPersistenceExecution       SessionPersistenceClass = "execution"
	SessionPersistenceEphemeral       SessionPersistenceClass = "ephemeral"
)

func (class SessionPersistenceClass) valid() bool {
	switch class {
	case SessionPersistenceInteractiveChat, SessionPersistenceExecution, SessionPersistenceEphemeral:
		return true
	default:
		return false
	}
}

// SetSessionPersistenceClass explicitly classifies a session. An interactive
// chat must be classified before its first event so no live-only prefix can be
// mistaken for complete durable history, unless the journal already holds the
// session (restart/eviction). Classification never changes once set: a later
// automated turn (cron, webhook, queue) into an interactive chat keeps the
// chat's class, while promoting any other class to interactive is refused.
func (es *EventStore) SetSessionPersistenceClass(sessionID string, class SessionPersistenceClass) error {
	if es == nil {
		return fmt.Errorf("event store is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session persistence class requires session_id")
	}
	if !class.valid() {
		return fmt.Errorf("invalid session persistence class %q", class)
	}

	es.mu.RLock()
	if existing, ok := es.persistenceClasses[sessionID]; ok {
		es.mu.RUnlock()
		if existing != class && class == SessionPersistenceInteractiveChat {
			return fmt.Errorf("session %s persistence class is already %q, cannot change to %q", sessionID, existing, class)
		}
		return nil
	}
	es.mu.RUnlock()

	es.adoptJournaledSession(sessionID)
	es.mu.Lock()
	if existing, ok := es.persistenceClasses[sessionID]; ok {
		es.mu.Unlock()
		if existing != class && class == SessionPersistenceInteractiveChat {
			return fmt.Errorf("session %s persistence class is already %q, cannot change to %q", sessionID, existing, class)
		}
		return nil
	}
	if class == SessionPersistenceInteractiveChat && len(es.events[sessionID]) > 0 {
		es.mu.Unlock()
		return fmt.Errorf("session %s already has events and cannot be classified", sessionID)
	}
	es.persistenceClasses[sessionID] = class
	ownerID := es.sessionOwners[sessionID]
	journal := es.durableJournal
	es.mu.Unlock()
	if class == SessionPersistenceInteractiveChat && ownerID != "" {
		if ownership, ok := journal.(DurableEventJournalOwnership); ok {
			return ownership.RegisterOwner(sessionID, ownerID)
		}
	}
	return nil
}

func (es *EventStore) sessionUsesDurableChatJournal(sessionID string) bool {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.persistenceClasses[sessionID] == SessionPersistenceInteractiveChat
}

// IsDurableChatSession reports the server-owned classification. Transport
// requests may inspect it, but must never promote an execution into a chat.
func (es *EventStore) IsDurableChatSession(sessionID string) bool {
	if es == nil {
		return false
	}
	es.adoptJournaledSession(sessionID)
	return es.sessionUsesDurableChatJournal(sessionID)
}
