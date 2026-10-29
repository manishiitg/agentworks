package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// An agent may ping the person behind an agent that has not answered, once per
// message it sent (owner, 2026-10-15). The ping is a server-authored line for
// the target agent: its person's computer shows it as a desktop notification;
// it is never handed to the chat. Repeated pings from one sender to one agent
// that are still unread collapse into one line ("waiting for 3 replies"), so a
// looping sender cannot flood the person.

const agentPingKind = "ping"

// Person pings use their own server-owned conversation.
const agentStatusConversationPrefix = "status-inbox-"

func shortMessageID(id string) string {
	id = strings.TrimPrefix(id, "msg-")
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

// serverAuthoredKind is true for lines the server writes itself: no peer sender to check.
func serverAuthoredKind(kind string) bool { return kind == agentPingKind }

func firstLineClip(text string, limit int) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if r := []rune(line); len(r) > limit {
		line = string(r[:limit]) + "…"
	}
	return line
}

func (api *StreamingAPI) externalAgentPing(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !namedAgentOperationAllowed(claims, true) {
		externalError(w, 403, "insufficient_scope", "Pinging a person needs a run-enabled connection.")
		return
	}
	caller, err := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil || caller.NamedAgentID == "" {
		externalError(w, 404, "not_found", "Name your own attached agent (agent_name).")
		return
	}
	messageID := strings.TrimSpace(externalArg(args, "message_id"))
	if messageID == "" {
		externalError(w, 400, "invalid_arguments", "message_id is required: the message you sent that is waiting for a reply.")
		return
	}
	now := time.Now().UTC()
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalError(w, 500, "store_error", "Messages could not be read; try again.")
		return
	}
	ci, mi := -1, -1
	for i := range s.Conversations {
		c := &s.Conversations[i]
		for j := range c.Messages {
			m := c.Messages[j]
			if m.ID == messageID && m.Kind == "" && m.From >= 0 && m.From < 2 && c.Endpoints[m.From].NamedAgentID == caller.NamedAgentID {
				ci, mi = i, j
			}
		}
	}
	if ci < 0 {
		externalError(w, 404, "not_found", "No message you sent has that ID.")
		return
	}
	c := &s.Conversations[ci]
	m := &c.Messages[mi]
	dest := c.Endpoints[1-m.From]
	switch {
	case !m.InboxRemovedAt.IsZero():
		externalError(w, 409, "message_unavailable", "That message is no longer waiting in the agent's inbox.")
		return
	case !m.PingedAt.IsZero():
		externalError(w, 409, "already_pinged", "You already pinged once for this message. Wait for the reply.")
		return
	case dest.NamedAgentID == "":
		externalError(w, 400, "invalid_arguments", "Only a message to an agent can ping its person.")
		return
	case !s.shareSenderAllowed(*c, m.From, dest.UserID, now):
		externalError(w, 403, "not_allowed", "You can no longer message that agent, so its person cannot be pinged.")
		return
	}
	sender := c.Endpoints[m.From]
	senderName, targetName := endpointDisplayLabel(sender, dest.UserID), endpointDisplayLabel(dest, dest.UserID)
	m.PingedAt = now
	collapsed := false
	pingConversation := -1
	statusID := agentStatusConversationPrefix + dest.NamedAgentID
	for i := range s.Conversations {
		if s.Conversations[i].ID != statusID {
			continue
		}
		pingConversation = i
		for j := range s.Conversations[i].Messages {
			p := &s.Conversations[i].Messages[j]
			if p.Kind == agentPingKind && p.Delivery == "stored" && p.PingSender == sender.NamedAgentID {
				p.PingCount++
				p.Text = fmt.Sprintf("agent:%s is waiting for %d replies from agent:%s", senderName, p.PingCount, targetName)
				collapsed = true
			}
		}
	}
	if !collapsed {
		dest.ShareID, dest.Guest = "", "" // the ping belongs to the target agent itself
		if pingConversation < 0 {
			s.Conversations = append(s.Conversations, agentConversation{ID: statusID, Endpoints: [2]agentMessageEndpoint{dest, dest}})
			pingConversation = len(s.Conversations) - 1
		}
		pings := &s.Conversations[pingConversation]
		if len(pings.Messages) >= 100 {
			pings.Messages = pings.Messages[len(pings.Messages)-99:]
		}
		pings.Next++
		s.NextAgentSequence++
		pings.Messages = append(pings.Messages, agentMessage{ID: "msg-status-" + fmt.Sprint(s.NextAgentSequence) + "-" + shortMessageID(dest.NamedAgentID), Sequence: pings.Next, From: 0, Text: fmt.Sprintf("agent:%s is waiting for a reply from agent:%s", senderName, targetName), SentAt: now, Delivery: "stored", Kind: agentPingKind, AgentSequence: s.NextAgentSequence, PingSender: sender.NamedAgentID, PingCount: 1})
	}
	if err = saveAgentMessageStore(r.Context(), s); err != nil {
		externalError(w, 500, "store_error", "The ping could not be saved; try again.")
		return
	}
	logfWithContext(api.httpRequestLogContext(r), "[AGENT_PING] sender=%s target=%s message=%s collapsed=%t", sender.NamedAgentID, dest.NamedAgentID, shortMessageID(m.ID), collapsed)
	externalJSON(w, map[string]any{"pinged": true, "collapsed": collapsed, "note": "Pinged once. The person sees a notification on their computer when AgentWorks is running there; do not ping again for this message."})
}
