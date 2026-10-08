package events

import (
	"strings"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// An agent can send a message into a chat (Pulse messaging the Builder chat).
// The agent library emits that turn's user_message without knowing who sent
// it; the server names the sender just before starting the turn, and the next
// user_message of that session carries it as metadata.source / sender_label.
type expectedSender struct {
	source  string
	label   string
	expires time.Time
}

// ExpectUserMessageSender marks the next user_message of sessionID as sent by
// source (for example "pulse") with a display label.
func (es *EventStore) ExpectUserMessageSender(sessionID, source, label string) {
	if es == nil || sessionID == "" || strings.TrimSpace(source) == "" {
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.expectedSenders == nil {
		es.expectedSenders = make(map[string]expectedSender)
	}
	es.expectedSenders[sessionID] = expectedSender{source: strings.TrimSpace(source), label: strings.TrimSpace(label), expires: time.Now().Add(expectedClientMessageTTL)}
}

// stampExpectedSender runs with es.mu held, on an already-cloned event.
func (es *EventStore) stampExpectedSender(sessionID string, event *Event) {
	if event.Type != string(pkgevents.UserMessage) || len(es.expectedSenders) == 0 {
		return
	}
	expected, ok := es.expectedSenders[sessionID]
	if !ok {
		return
	}
	delete(es.expectedSenders, sessionID)
	if time.Now().After(expected.expires) {
		return
	}
	message := userMessageEventData(event)
	if message == nil {
		return
	}
	if message.Metadata == nil {
		message.Metadata = make(map[string]interface{})
	}
	message.Metadata["source"] = expected.source
	if expected.label != "" {
		message.Metadata["sender_label"] = expected.label
	}
}
