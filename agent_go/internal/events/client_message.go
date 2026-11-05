package events

import (
	"strings"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// A fresh turn's user_message is emitted by the agent library, which never
// sees the HTTP submission. The query handler registers the submission's
// client message id just before starting the turn; the next main-agent
// user_message for that session carries it, so the browser's provisional
// bubble and the durable row share one identity.
type expectedClientMessage struct {
	id      string
	display string
	expires time.Time
}

const expectedClientMessageTTL = 10 * time.Minute

func ClientUserMessageEventID(clientMessageID string) string {
	if clientMessageID == "" {
		return ""
	}
	return "user:" + clientMessageID
}

// ExpectClientUserMessage makes the next main-agent user_message recorded for
// sessionID carry clientMessageID. display is the text the user typed; it is
// kept separately when the recorded content differs (a continuity-wrapped
// prompt), so rendering never depends on the wrapper.
func (es *EventStore) ExpectClientUserMessage(sessionID, clientMessageID, display string) {
	if es == nil || sessionID == "" || clientMessageID == "" {
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.expectedClientMessages == nil {
		es.expectedClientMessages = make(map[string]expectedClientMessage)
	}
	es.expectedClientMessages[sessionID] = expectedClientMessage{
		id:      clientMessageID,
		display: strings.TrimSpace(display),
		expires: time.Now().Add(expectedClientMessageTTL),
	}
}

// stampExpectedClientUserMessage runs with es.mu held, on an already-cloned
// event.
func (es *EventStore) stampExpectedClientUserMessage(sessionID string, event *Event) {
	if event.Type != string(pkgevents.UserMessage) {
		return
	}
	switch strings.ToLower(strings.TrimSpace(event.ExecutionKind)) {
	case "", "main", "main_agent", "chat":
	default:
		return
	}
	message := userMessageEventData(event)
	if message == nil {
		return
	}
	// Live input already carries the submission id. Its observer event id is
	// only a transport identity; using it leaves the provisional bubble behind
	// and collides with another steer in the same turn (PLAT-746-331).
	if existing, _ := message.Metadata["client_message_id"].(string); strings.TrimSpace(existing) != "" {
		event.ID = ClientUserMessageEventID(strings.TrimSpace(existing))
		return
	}
	expected, ok := es.expectedClientMessages[sessionID]
	if !ok {
		return
	}
	if time.Now().After(expected.expires) {
		delete(es.expectedClientMessages, sessionID)
		return
	}
	if message.Metadata == nil {
		message.Metadata = make(map[string]interface{})
	}
	message.Metadata["client_message_id"] = expected.id
	if expected.display != "" && strings.TrimSpace(message.Content) != expected.display {
		message.Metadata["display_content"] = expected.display
	}
	event.ID = ClientUserMessageEventID(expected.id)
	delete(es.expectedClientMessages, sessionID)
}

func userMessageEventData(event *Event) *pkgevents.UserMessageEvent {
	if event == nil || event.Data == nil {
		return nil
	}
	message, _ := event.Data.Data.(*pkgevents.UserMessageEvent)
	return message
}
