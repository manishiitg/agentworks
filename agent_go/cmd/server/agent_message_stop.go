package server

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// agentMessageTurns holds, for each delivered message whose turn is starting or running on this server, the
// receiving session. It is what lets the asker's Stop reach only the turn working on their own ask (#3661).
var agentMessageTurns sync.Map // message ID -> session ID

// externalAgentMessageStop is messages action=stop: the asker stops their own ask. A message not yet delivered is
// withdrawn; one waiting in the receiver's queue is taken out of it; one being worked on stops only that turn (another
// person's message always runs as its own turn). Nobody else's message, and none of the receiver's other work, is touched.
func (api *StreamingAPI) externalAgentMessageStop(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	inbox, messageID := strings.TrimSpace(externalArg(args, "inbox_id")), strings.TrimSpace(externalArg(args, "message_id"))
	if inbox == "" || messageID == "" {
		externalError(w, 400, "invalid_arguments", "stop needs the inbox_id and message_id of your ask")
		return
	}
	if !namedAgentOperationAllowed(claims, true) {
		externalError(w, 403, "insufficient_scope", "Connection lacks messaging send access.")
		return
	}
	caller, err := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil {
		externalCommunicationError(w, 404, "not_found", err)
		return
	}
	notYours := func() {
		log.Printf("[AGENT_MESSAGE_STOP] refused user=%s inbox=%s message=%s reason=not_own_ask", claims.UserID, inbox, messageID)
		externalError(w, 404, "not_found", "You can stop only an ask you sent.")
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		agentMessageMu.Unlock()
		externalError(w, 500, "read_failed", "Could not read the conversation.")
		return
	}
	var conversation *agentConversation
	var message *agentMessage
	for i := range s.Conversations {
		if s.Conversations[i].ID != inbox {
			continue
		}
		for j := range s.Conversations[i].Messages {
			if s.Conversations[i].Messages[j].ID == messageID {
				conversation, message = &s.Conversations[i], &s.Conversations[i].Messages[j]
			}
		}
	}
	if message == nil || message.From < 0 || message.From > 1 || !messageEndpointMatches(conversation.Endpoints[message.From], claims.UserID, caller) {
		agentMessageMu.Unlock()
		notYours()
		return
	}
	dest := conversation.Endpoints[1-message.From]
	if message.Delivery == "pending" || message.Delivery == "stored" {
		message.Delivery = "withdrawn"
		err = saveAgentMessageStore(r.Context(), s)
		agentMessageMu.Unlock()
		if err != nil {
			externalError(w, 500, "stop_failed", "Could not stop your ask. Try again.")
			return
		}
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s outcome=withdrawn", claims.UserID, inbox, messageID)
		externalJSON(w, map[string]any{"stopped": true, "outcome": "withdrawn", "line": "Your ask was withdrawn before it started."})
		return
	}
	agentMessageMu.Unlock()
	sessionValue, running := agentMessageTurns.Load(messageID)
	if !running {
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s outcome=not_running", claims.UserID, inbox, messageID)
		externalJSON(w, map[string]any{"stopped": false, "outcome": "finished", "line": "Your ask isn't running anymore."})
		return
	}
	session := sessionValue.(string)
	if api.withdrawQueuedAgentMessageTurn(r.Context(), session, messageID) {
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s session=%s outcome=dequeued", claims.UserID, inbox, messageID, session)
		externalJSON(w, map[string]any{"stopped": true, "outcome": "withdrawn", "line": "Your ask was taken out of the queue before it started."})
		return
	}
	// The running turn is the one for this message: another person's ask never steers into a running turn.
	owner := &UserClaims{UserID: dest.UserID, Username: dest.UserID}
	stopRequest := r.WithContext(context.WithValue(r.Context(), UserContextKey, owner))
	stop := func(w2 http.ResponseWriter, r2 *http.Request) {
		r2.Header.Set("X-Session-ID", session)
		api.handleStopSession(w2, r2)
	}
	status, _ := scheduleHandler(stopRequest, stop, http.MethodPost, "/api/session/stop", nil, url.Values{"preserveConversation": {"true"}}, map[string]any{})
	if status >= 400 {
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s session=%s outcome=stop_failed status=%d", claims.UserID, inbox, messageID, session, status)
		externalError(w, 409, "stop_failed", "Your ask couldn't be stopped right now. Try again in a moment.")
		return
	}
	log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s session=%s outcome=stopped", claims.UserID, inbox, messageID, session)
	externalJSON(w, map[string]any{"stopped": true, "outcome": "stopped", "line": "You stopped your ask. Nothing else the agent was doing was touched."})
}

// withdrawQueuedAgentMessageTurn removes the not-yet-started queued turn for this message, if there is one.
func (api *StreamingAPI) withdrawQueuedAgentMessageTurn(ctx context.Context, session, messageID string) bool {
	for _, owner := range api.conversationQueueOwners(session) {
		turns, err := api.readConversationTurnQueue(ctx, owner)
		if err != nil {
			continue
		}
		for _, turn := range turns {
			if turn.SessionID == session && turn.StartedAt == nil && turn.Request.AgentMessageID == messageID {
				return api.removeConversationTurn(ctx, turn) == nil
			}
		}
	}
	return false
}
