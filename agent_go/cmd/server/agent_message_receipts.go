package server

import (
	"time"
)

// What a sender can learn about its own messages, in plain words: Sent (stored on
// the server), Delivered (the receiving chat accepted it), Handled (it was
// processed), or a reason it has not arrived. Times and states only, never
// bodies or session ids.

// receiptState names the state of one message and a one-line explanation.
func receiptState(m agentMessage, destAgent *namedMessageAgent, destNamed bool) (string, string) {
	switch m.Delivery {
	case "processed":
		return "handled", "Handled: the receiving agent processed it."
	case "admitted", "typed":
		return "delivered", "Delivered: the receiving chat accepted it; it has not said it finished."
	case "failed", "interrupted":
		return "not_delivered", "Not delivered: the receiving chat could not take it. Send it again if it still matters."
	case "suppressed":
		return "not_delivered", "Not delivered: access ended (the share, group or login changed) before the receiving chat took it."
	}
	if destNamed && (destAgent == nil || !namedAgentOnline(*destAgent)) {
		return "waiting_offline", "Not delivered yet: the receiving agent is offline. It will see the message when it is connected again."
	}
	return "sent", "Sent: stored on the server, waiting for the receiving chat to take it."
}

func (s agentMessageStore) destAgentOf(e agentMessageEndpoint) (*namedMessageAgent, bool) {
	if e.NamedAgentID == "" {
		return nil, false
	}
	for i := range s.Agents {
		if s.Agents[i].ID == e.NamedAgentID && s.Agents[i].UserID == e.UserID {
			return &s.Agents[i], true
		}
	}
	return nil, true
}

// receiptFields adds the plain-language state to a send result or a sent-message row.
func (s agentMessageStore) receiptFields(m agentMessage, dest agentMessageEndpoint) map[string]interface{} {
	agent, named := s.destAgentOf(dest)
	state, text := receiptState(m, agent, named)
	out := map[string]interface{}{"state": state, "state_text": text, "sent_at": m.SentAt}
	if !m.AdmittedAt.IsZero() {
		out["delivered_at"] = m.AdmittedAt
	}
	if !m.ProcessedAt.IsZero() {
		out["handled_at"] = m.ProcessedAt
	}
	return out
}

// sentReceipts lists the newest messages this side sent in a conversation.
func (s agentMessageStore) sentReceipts(c agentConversation, side, limit int) []map[string]interface{} {
	rows := []map[string]interface{}{}
	for i := len(c.Messages) - 1; i >= 0 && len(rows) < limit; i-- {
		m := c.Messages[i]
		if m.From != side || serverAuthoredKind(m.Kind) {
			continue
		}
		row := s.receiptFields(m, c.Endpoints[1-side])
		row["message_id"], row["inbox_id"], row["to"] = m.ID, c.ID, endpointDisplayLabel(c.Endpoints[1-side], c.Endpoints[side].UserID)
		if m.Group != "" {
			row["group"] = m.Group
		}
		rows = append(rows, row)
	}
	return rows
}

func stampReceipt(m *agentMessage, delivery string, now time.Time) {
	switch delivery {
	case "admitted", "typed":
		if m.AdmittedAt.IsZero() {
			m.AdmittedAt = now
		}
	case "processed":
		if m.AdmittedAt.IsZero() {
			m.AdmittedAt = now
		}
		if m.ProcessedAt.IsZero() {
			m.ProcessedAt = now
		}
	}
}
