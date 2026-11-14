package server

import (
	"context"
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
	case "undeliverable", "failed", "interrupted":
		return "not_delivered", "Not delivered: " + agentMessageFailureReason(m.Error)
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

// Stored delivery errors may contain internal paths or provider details. Only
// these fixed admission sentences become a sender-facing reason.
func agentMessageFailureReason(reason string) string {
	switch reason {
	case "Choose a chat_id from list_agents for this Crew", "shared agent unavailable or access denied":
		return "the agent's chat is no longer available. Refresh messages action=list_agents and choose a current agent."
	case "message Crew is unavailable or ownership changed":
		return "the agent's Crew is unavailable or its ownership changed."
	case "Team agent unavailable or access removed":
		return "this agent is no longer available to this Team."
	case "message participant account is disabled", "message participant account is no longer active":
		return "a participant's account is no longer active."
	default:
		return "the receiving chat could not take it. Check the conversation before trying again."
	}
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
func (s agentMessageStore) receiptFields(ctx context.Context, m agentMessage, dest agentMessageEndpoint) map[string]interface{} {
	return s.receiptFieldsWithLabel(m, dest, s.recipientLabel(ctx, dest))
}

func (s agentMessageStore) recipientLabel(ctx context.Context, dest agentMessageEndpoint) string {
	// The recipient's own name for itself (a Crew's title, an agent's current label), so the sender's chat can say
	// "Message to <label>" instead of a raw agent id such as app-<id>.
	label := s.endpointName(dest, dest.UserID)
	if dest.NamedAgentID == "" && dest.Kind == triggerCallerCrew {
		if name := defaultProductConversationRegistryStore().messageRecipientName(ctx, dest); name != "" {
			label = name
		}
	}
	return label
}

func (s agentMessageStore) receiptFieldsWithLabel(m agentMessage, dest agentMessageEndpoint, label string) map[string]interface{} {
	agent, named := s.destAgentOf(dest)
	state, text := receiptState(m, agent, named)
	out := map[string]interface{}{"state": state, "state_text": text, "message": text, "sent_at": m.SentAt}
	if label = strings.TrimSpace(label); label != "" {
		out["to_label"] = label
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
func (s agentMessageStore) sentReceipts(ctx context.Context, c agentConversation, side, limit int) []map[string]interface{} {
	rows := []map[string]interface{}{}
	label := s.recipientLabel(ctx, c.Endpoints[1-side])
	for i := len(c.Messages) - 1; i >= 0 && len(rows) < limit; i-- {
		m := c.Messages[i]
		if m.From != side || serverAuthoredKind(m.Kind) {
			continue
		}
		row := s.receiptFieldsWithLabel(m, c.Endpoints[1-side], label)
		row["message_id"], row["inbox_id"], row["to"] = m.ID, c.ID, row["to_label"]
		rows = append(rows, row)
	}
	return rows
}

func stampReceipt(m *agentMessage, delivery string, now time.Time) {
	if delivery == "delivered" && m.DeliveredAt.IsZero() {
		m.DeliveredAt = now
	}
}

// endpointName is how cards and inbox rows name an endpoint: a named agent by its current label (what the directory
// shows), not the registered address it was stored with (app-<id>). Other endpoints keep their stored label.
func (s agentMessageStore) endpointName(e agentMessageEndpoint, viewer string) string {
	if e.NamedAgentID != "" {
		for _, a := range s.Agents {
			if a.ID == e.NamedAgentID && a.UserID == e.UserID {
				if label := cleanAgentLabel(a.Label); label != "" {
					e.Label = label
				}
				break
			}
		}
	}
	return endpointDisplayLabel(e, viewer)
}

// PLAT-746: a Crew endpoint's stored Label is often only the Crew title. Resolve
// the exact admitted conversation, including inbox replies, never a guessed Main.
func (store productConversationRegistryStore) messageRecipientName(ctx context.Context, e agentMessageEndpoint) string {
	if e.UserID == "" || e.Session == "" || e.Path == "" {
		return ""
	}
	path := productConversationRegistryPath(e.UserID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		return ""
	}
	for key, record := range doc.Entries {
		if record.SessionID != e.Session || record.ProfileID != e.Profile || canonicalCrewWorkspaceRoot(record.WorkspacePath) != canonicalCrewWorkspaceRoot(e.Path) {
			continue
		}
		if e.ChatKey != "" && e.ChatKey != record.ConversationKey {
			continue
		}
		return firstNonEmptyTrimmed(doc.Agents[key].Name, record.Title)
	}
	return ""
}
