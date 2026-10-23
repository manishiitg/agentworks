package server

import (
	"fmt"
	"strings"
	"time"
)

// Explicit person pings use their own server-owned conversation. Ordinary
// message delivery never creates a reminder or asks for an acknowledgement.
const agentStatusConversationPrefix = "status-inbox-"

func shortMessageID(id string) string {
	id = strings.TrimPrefix(id, "msg-")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

// appendAgentStatusMessage adds a server-authored status line for one named agent to its status conversation.
func appendAgentStatusMessage(s *agentMessageStore, endpoint agentMessageEndpoint, text string, now time.Time) {
	endpoint.ShareID, endpoint.Guest = "", "" // the line is for this agent itself, whoever shared it
	id := agentStatusConversationPrefix + endpoint.NamedAgentID
	idx := -1
	for i := range s.Conversations {
		if s.Conversations[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.Conversations = append(s.Conversations, agentConversation{ID: id, Endpoints: [2]agentMessageEndpoint{endpoint, endpoint}, Next: 0})
		idx = len(s.Conversations) - 1
	}
	c := &s.Conversations[idx]
	if len(c.Messages) >= 100 {
		c.Messages = c.Messages[len(c.Messages)-99:] // a sender that never reads them does not grow the list
	}
	c.Next++
	s.NextAgentSequence++
	c.Messages = append(c.Messages, agentMessage{ID: "msg-status-" + fmt.Sprint(s.NextAgentSequence) + "-" + shortMessageID(endpoint.NamedAgentID), Sequence: c.Next, From: 0, Text: text, SentAt: now, Delivery: "stored", Kind: "status", AgentSequence: s.NextAgentSequence})
}
