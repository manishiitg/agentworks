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
		// The receiver may still read it from its mailbox: what it reads is only that it was withdrawn, with no files.
		message.Delivery, message.Text = "withdrawn", "Withdrawn by the sender."
		files := message.Attachments
		message.Attachments = nil
		err = saveAgentMessageStore(r.Context(), s)
		agentMessageMu.Unlock()
		if err == nil {
			dropMessageFiles(r.Context(), files, "the ask was withdrawn")
		}
		if err != nil {
			externalError(w, 500, "stop_failed", "Could not stop your ask. Try again.")
			return
		}
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s outcome=withdrawn", claims.UserID, inbox, messageID)
		externalJSON(w, map[string]any{"stopped": true, "outcome": "withdrawn", "line": "Your ask was withdrawn before it started."})
		return
	}
	delivery, answered := message.Delivery, false
	for _, later := range conversation.Messages {
		if later.From == 1-message.From && later.Sequence > message.Sequence {
			answered = true
		}
	}
	agentMessageMu.Unlock()
	sessionValue, running := agentMessageTurns.Load(messageID)
	if !running {
		outcome, line := "finished", "Your ask isn't running anymore."
		switch {
		case delivery == "dispatching":
			outcome, line = "dispatching", "Your ask is being handed to the agent right now. Try Stop again in a moment."
		case !answered:
			// Delivered but not running here: it runs on the receiver's own computer, which this server can't stop yet.
			outcome, line = "elsewhere", "It's running on "+shareOwnerLabel(dest.UserID)+"'s computer; stopping it from here isn't available yet."
		}
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s outcome=%s", claims.UserID, inbox, messageID, outcome)
		externalJSON(w, map[string]any{"stopped": false, "outcome": outcome, "line": line})
		return
	}
	session := sessionValue.(string)
	if api.withdrawQueuedAgentMessageTurn(r.Context(), session, messageID) {
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s session=%s outcome=dequeued", claims.UserID, inbox, messageID, session)
		externalJSON(w, map[string]any{"stopped": true, "outcome": "withdrawn", "line": "Your ask was taken out of the queue before it started."})
		return
	}
	// Stop only when the session's running turn is this message's own turn, so a race or a message that steered into
	// a running turn can never stop the receiver's other work.
	if !api.agentMessageTurnRunning(r.Context(), session, messageID) {
		log.Printf("[AGENT_MESSAGE_STOP] user=%s inbox=%s message=%s session=%s outcome=not_running reason=turn_mismatch", claims.UserID, inbox, messageID, session)
		externalError(w, 409, "not_running", "Your ask isn't the turn running right now, so nothing was stopped.")
		return
	}
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
	return api.withdrawQueuedAgentMessageTurns(ctx, session, map[string]bool{messageID: true}) > 0
}

// withdrawQueuedAgentMessageTurns removes the session's not-yet-started queued turns that deliver one of these
// messages: server mailbox turns carry the id on the request, AgentBridge network turns on the delivery stamp.
func (api *StreamingAPI) withdrawQueuedAgentMessageTurns(ctx context.Context, session string, ids map[string]bool) int {
	removed := 0
	if session == "" || len(ids) == 0 {
		return 0
	}
	for _, owner := range api.conversationQueueOwners(session) {
		lock := productConversationRegistryMutex(conversationTurnQueuePath(owner))
		lock.Lock()
		turns, err := api.readConversationTurnQueue(ctx, owner)
		if err != nil {
			lock.Unlock()
			continue
		}
		kept := turns[:0]
		for _, turn := range turns {
			stamped, _ := turn.Principal.DeliveredMessage["message_id"].(string)
			if turn.SessionID == session && turn.StartedAt == nil && !turn.Steering && (ids[turn.Request.AgentMessageID] || ids[stamped]) {
				log.Printf("[AGENT-MESSAGE] action=queued_turn_withdrawn session=%s turn=%s message=%s", session, turn.ID, turn.Request.AgentMessageID+stamped)
				removed++
				continue
			}
			kept = append(kept, turn)
		}
		if len(kept) != len(turns) {
			if err := api.writeConversationTurnQueue(ctx, owner, kept); err != nil {
				removed -= len(turns) - len(kept)
			}
		}
		lock.Unlock()
	}
	return removed
}

// agentMessageTurnRunning reports whether the session's running conversation turn is the one answering this message.
func (api *StreamingAPI) agentMessageTurnRunning(_ context.Context, session, messageID string) bool {
	api.trackedWorkflowExecutionsMux.RLock()
	defer api.trackedWorkflowExecutionsMux.RUnlock()
	var turn *TrackedWorkflowExecution
	for _, exec := range api.trackedWorkflowExecutions {
		if exec != nil && exec.Status == trackedExecutionStatusRunning && exec.SessionID == session && exec.Source == trackedExecutionSourceConversationTurn && (turn == nil || exec.StartedAt.After(turn.StartedAt)) {
			turn = exec
		}
	}
	return turn != nil && turn.Metadata["agent_message_id"] == messageID
}
