package server

import (
	"fmt"
	"strings"
	"time"
)

// What a sender can learn about its own messages, in plain words: Sent (stored on
// the server), Delivered (the receiving chat accepted it), or a reason it
// has not arrived. Delivery does not claim completion of the requested work. Never
// bodies or session ids.

// receiptState names the state of one message and a one-line explanation.
func receiptState(m agentMessage, destAgent *namedMessageAgent, destNamed bool) (string, string) {
	name := "The agent"
	if destAgent != nil {
		name = destAgent.Name
	}
	switch m.Delivery {
	case "expired":
		return "not_delivered", "Pending delivery cleared after 24 hours; message history kept."
	case "delivered":
		return "delivered", fmt.Sprintf("%s has it.", name)
	case "failed", "interrupted":
		return "not_delivered", "The receiving chat could not take it. Check the conversation before trying again."
	case "suppressed":
		return "not_delivered", "Access ended before the receiving chat took it."
	}
	if destNamed && (destAgent == nil || !namedAgentOnline(*destAgent)) {
		return "waiting_offline", fmt.Sprintf("Saved in %s's inbox; %s sees it when it next checks.", name, name)
	}
	if destNamed {
		switch agentDeliveryMode(*destAgent) {
		case "live":
			return "sent", fmt.Sprintf("Sent to %s's open chat; waiting for %s to pick it up.", name, name)
		case "next_step":
			return "sent", fmt.Sprintf("Stored; %s gets it at its next step.", name)
		default:
			return "sent", fmt.Sprintf("Stored; %s gets it when it next checks.", name)
		}
	}
	return "sent", "Sent to the product's chat; waiting for it to pick it up."
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
	out := map[string]interface{}{"state": state, "state_text": text, "message": text, "sent_at": m.SentAt}
	// The recipient's own name for itself (a Crew's title, an agent's label), so the sender's chat can say "Message to
	// <label>" instead of a raw agent id.
	if label := strings.TrimSpace(dest.Label); label != "" {
		out["to_label"] = label
	} else if agent != nil && strings.TrimSpace(agent.Label) != "" && agent.Label != agent.Name {
		out["to_label"] = strings.TrimSpace(agent.Label)
	}
	if named {
		out["delivery_mode"] = "inbox"
		if agent != nil {
			out["delivery_mode"] = agentDeliveryMode(*agent)
		}
	}
	if !m.DeliveredAt.IsZero() {
		out["delivered_at"] = m.DeliveredAt
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
		rows = append(rows, row)
	}
	return rows
}

func stampReceipt(m *agentMessage, delivery string, now time.Time) {
	if delivery == "delivered" && m.DeliveredAt.IsZero() {
		m.DeliveredAt = now
	}
}
