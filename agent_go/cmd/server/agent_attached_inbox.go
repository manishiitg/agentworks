package server

import (
	"log"
	"net/http"
	"slices"
	"time"
)

// Attached chats and native wake adapters read the same server-owned mailbox.
// The helper has a read position, never another copy of the pending inbox.
func (api *StreamingAPI) readAttachedAgentInbox(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	name, id := externalArg(args, "agent_name"), externalArg(args, "attachment_id")
	agent, live, err := currentAgentAttachment(r.Context(), claims, name, id)
	if err != nil {
		externalError(w, 403, "agent_attachment_closed", "This chat is no longer connected. Connect it again to receive messages.")
		return
	}
	after, limit := externalInt(args, "after", 0), externalInt(args, "limit", 20)
	if after < 0 || limit < 1 || limit > 20 {
		externalError(w, 400, "invalid_arguments", "Read position must be non-negative; a batch contains at most 20 messages.")
		return
	}
	store, err := readAgentMessageSnapshot(r.Context())
	if err != nil {
		externalError(w, 503, "inbox_unavailable", "Messages are temporarily unavailable. Try again shortly.")
		return
	}
	type incoming struct {
		conversation agentConversation
		message      agentMessage
	}
	waiting := []incoming{}
	now := time.Now().UTC()
	for _, conversation := range store.Conversations {
		for _, message := range conversation.Messages {
			destination := conversation.Endpoints[1-message.From]
			if destination.NamedAgentID != agent.ID || destination.UserID != agent.UserID ||
				message.AgentSequence <= after || message.Delivery != "stored" || !message.InboxRemovedAt.IsZero() || agentMessagePendingExpired(message, now) {
				continue
			}
			waiting = append(waiting, incoming{conversation, message})
		}
	}
	slices.SortFunc(waiting, func(a, b incoming) int { return a.message.AgentSequence - b.message.AgentSequence })
	messages := []map[string]any{}
	held := map[string]int{}
	for _, pending := range waiting {
		conversation, message := pending.conversation, pending.message
		source := conversation.Endpoints[message.From]
		if !serverAuthoredKind(message.Kind) {
			if !store.shareSenderAllowed(conversation, message.From, agent.UserID, now) {
				held["share_check_failed"]++
				continue
			}
			if api.recheckAgentMessageAdmission(r.Context(), conversation, message) != nil {
				held["admission_recheck_failed"]++
				continue
			}
			if api.externalAuthorizeMessagePeer(r.Context(), live, source, false) != nil {
				held["sender_authorization_failed"]++
				continue
			}
		}
		row := map[string]any{"attachment_id": id, "message_id": message.ID, "inbox_id": conversation.ID,
			"sender": endpointDisplayLabel(source, agent.UserID), "sender_id": source.NamedAgentID,
			"message": message.Text, "sequence": message.AgentSequence, "delivery": message.Delivery, "sent_at": message.SentAt}
		if serverAuthoredKind(message.Kind) {
			row["sender"], row["sender_id"], row["kind"] = "AgentWorks", "", message.Kind
		}
		if message.Kind == agentPingKind {
			row["message"] = firstLineClip(message.Text, 160)
		}
		if person := crossPersonOwner(source, agent.UserID); person != "" && !serverAuthoredKind(message.Kind) {
			row["from_person"], row["untrusted"] = person, true
		}
		messages = append(messages, row)
		if len(messages) == limit {
			break
		}
	}
	// The same generation still owns the mailbox when the bodies are released.
	if _, _, err = currentAgentAttachment(r.Context(), claims, name, id); err != nil {
		externalError(w, 403, "agent_attachment_closed", "This chat's connection changed. Connect it again to receive messages.")
		return
	}
	next := after
	if len(messages) > 0 {
		next = messages[len(messages)-1]["sequence"].(int)
	}
	count := 0
	for _, n := range held {
		count += n
	}
	log.Printf("[AGENT-INBOX] agent=%s attachment=%s after=%d messages=%d held=%d next=%d", agent.ID, id, after, len(messages), count, next)
	externalJSON(w, map[string]any{"messages": messages, "next_cursor": next, "pending": len(waiting), "held": count, "held_reasons": held})
}
