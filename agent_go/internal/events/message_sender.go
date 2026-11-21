package events

import (
	"maps"
	"strings"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// The server binds attribution to one admitted input. The model's event echo
// gets the marker before journal/SSE publication; text alone grants no identity.
type expectedSender struct {
	content  string
	metadata map[string]interface{}
	expires  time.Time
}

// ExpectAgentMessageSender is called only from the server's delivery context
// after admission. Clear its expectation before releasing the turn's input lane.
func (es *EventStore) ExpectAgentMessageSender(sessionID, content string, metadata map[string]interface{}) func() {
	if es == nil || sessionID == "" || strings.TrimSpace(content) == "" {
		return func() {}
	}
	es.mu.Lock()
	if es.expectedSenders == nil {
		es.expectedSenders = make(map[string]*expectedSender)
	}
	expected := &expectedSender{content: strings.TrimSpace(content), metadata: maps.Clone(metadata), expires: time.Now().Add(expectedClientMessageTTL)}
	es.expectedSenders[sessionID] = expected
	es.mu.Unlock()
	return func() {
		es.mu.Lock()
		defer es.mu.Unlock()
		if es.expectedSenders[sessionID] == expected {
			delete(es.expectedSenders, sessionID)
		}
	}
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
	if time.Now().After(expected.expires) {
		delete(es.expectedSenders, sessionID)
		return
	}
	switch strings.ToLower(strings.TrimSpace(event.ExecutionKind)) {
	case "", "main", "main_agent", "chat":
	default:
		return
	}
	message := userMessageEventData(event)
	if message == nil || strings.TrimSpace(message.Content) != expected.content {
		return
	}
	// A browser-typed copy carries its own client id; the delivered input may carry only the id the server gave it.
	if id := message.Metadata["client_message_id"]; message.Metadata["source"] == "coding_agent_live_input" || message.Metadata["source"] == "execution_prompt" || (id != nil && id != expected.metadata["client_message_id"]) {
		return
	}
	delete(es.expectedSenders, sessionID)
	if message.Metadata == nil {
		message.Metadata = make(map[string]interface{})
	}
	for key, value := range expected.metadata {
		message.Metadata[key] = value
	}
}

// stampAgentReply requires the provider's exact turn identity and a server-attributed
// delivered input. A queued card or merely adjacent message never identifies a reply.
// Called with es.mu held before durable projection, so restore retains the same link.
func (es *EventStore) stampAgentReply(sessionID string, event *Event) {
	event.ReplyToAgentMessageID = ""
	if event.Data == nil || event.Type == string(pkgevents.UserMessage) || event.Data.TurnID == "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(event.ExecutionKind)) {
	case "", "main", "main_agent", "chat":
	default:
		return
	}
	rows := es.events[sessionID]
	for i := len(rows) - 1; i >= 0; i-- {
		row := &rows[i]
		if row.Data == nil || row.Data.TurnID != event.Data.TurnID || row.Type != string(pkgevents.UserMessage) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(row.ExecutionKind)) {
		case "", "main", "main_agent", "chat":
		default:
			continue
		}
		message := userMessageEventData(row)
		if message == nil {
			return
		}
		metadata := message.Metadata
		if metadata["delivery_status"] == "queued_for_turn" || metadata["delivery_status"] == "failed" || metadata["provisional"] == true {
			continue
		}
		// A person's steering message in the same turn clears the agent association.
		if metadata["source"] == "agent_message" {
			event.ReplyToAgentMessageID, _ = metadata["message_id"].(string)
		}
		return
	}
}
