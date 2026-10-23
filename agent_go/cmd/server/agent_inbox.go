package server

import (
	"fmt"
	"maps"
	"net/http"
	"time"
)

const hostedInboxInstructions = "This agent uses an inbox. Nothing is pushed into this app. Check your inbox when the person asks or at the start of a conversation. If you can install the AgentWorks CLI, it can connect this chat for live delivery instead."

// OAuthClientID is populated from the authenticated server grant, not the
// application's name, user agent, request arguments or token display name.
func hostedAgentMailbox(claims *UserClaims) bool {
	if claims == nil || claims.AccessToken == nil || claims.ExecutionPrincipal != nil || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "" || claims.Provider == "bot_route" || claims.Provider == "bot_owner" {
		return false
	}
	t := claims.AccessToken
	return t.OAuthClientID != "" && t.OAuthClientID != cliOAuthClientID && t.BoundAgentID == "" &&
		t.AllWorkflows && len(t.WorkflowIDs) == 0 && personSignInScopes(t.Scopes) && t.FileGuard == nil && t.KnowledgebaseIdentityID == ""
}

func cliAgentMailRequest(r *http.Request) bool {
	claims := GetUserFromContext(r.Context())
	return r.URL.Path != externalMCPPath && claims != nil &&
		(claims.AccessToken == nil || claims.AccessToken.OAuthClientID == cliOAuthClientID)
}

func authorizeAgentMailRequest(r *http.Request, args map[string]any) error {
	if hostedAgentMailbox(GetUserFromContext(r.Context())) || cliAgentMailRequest(r) {
		return nil
	}
	mail := agentMailCall(args)
	// A conversation reply without agent_name must not evade the same check.
	if !mail && externalArg(args, "inbox_id") != "" {
		s, err := readAgentMessageSnapshot(r.Context())
		if err != nil {
			return fmt.Errorf("Your inbox is unavailable. Please try again.")
		}
		for _, c := range s.Conversations {
			if c.ID == externalArg(args, "inbox_id") {
				mail = c.Endpoints[0].NamedAgentID != "" || c.Endpoints[1].NamedAgentID != ""
				break
			}
		}
	}
	if mail {
		return fmt.Errorf("For this connection, use the AgentWorks CLI to message agents. Apps without a terminal can sign in through Connect AI agents and use an inbox over MCP.")
	}
	return nil
}

// Project only copies, never mutate the process-wide catalog. The REST bridge
// remains available to CLI agents; those credentials get no inbox agent over MCP.
func externalMessageToolForRequest(r *http.Request, tool externalTool) externalTool {
	if tool.Name != "messages" {
		return tool
	}
	claims := GetUserFromContext(r.Context())
	tool.InputSchema = maps.Clone(tool.InputSchema)
	props := maps.Clone(tool.InputSchema["properties"].(map[string]any))
	tool.InputSchema["properties"] = props
	if hostedAgentMailbox(claims) {
		tool.Description = "Message agents through your inbox: register your agent_name, list_agents to find recipients, send, read, or reply with send in the same inbox_id. The directory includes Crews, Goals, Relays, coding chats and hosted inboxes. Show name, type, provider, presence, last_seen_at and available receipt times; Builder/Run is access, not online status. Acknowledge your incoming message with ack and delivery=delivered. Count shows total/unread/pending and matching filters without paging; delete removes one incoming message_id, and clear removes matching incoming messages from your agent inbox only. Count with the same filters before clear to show the count. Other participants retain their history/receipts. " + hostedInboxInstructions + " Connected agents are private to their person and that person's agents. Published agents can be found and messaged by everyone on this server. Existing account and connection access still applies. Send uses the same message size and rate limits as the CLI. Publishing an agent still needs the person's request."
		props["delivery"] = map[string]any{"type": "string", "enum": []any{"delivered"}, "description": "For ack: delivered means this chat received the message. It does not mean the requested work is finished."}
		delete(props, "attachment_id")
		delete(props, "transport")
	} else if !cliAgentMailRequest(r) {
		tool.Description = "Send messages to an accessible Crew or workflow, or read that product conversation. For agent-to-agent messaging with this connection, use the AgentWorks CLI. Apps without a terminal can use a normal MCP sign-in and an inbox."
		action := maps.Clone(props["action"].(map[string]any))
		action["enum"] = []any{"send", "read", "state"}
		props["action"] = action
	}
	return tool
}

func agentDeliveryMode(a namedMessageAgent) string {
	if !namedAgentOnline(a) {
		if a.Attachment != nil || a.EverAttached {
			return "offline"
		}
		return "inbox"
	}
	switch a.Attachment.DeliveryRoute {
	case "codex_desktop", "codex_queue", "claude_session", "claude_channel", "muse_monitor", "pi_extension":
		return "live"
	case "codex_hook", "claude_hook", "cursor_hook", "muse_hook", "agy_hook":
		return "next_step"
	case "inbox":
		return "inbox"
	default:
		return "unknown"
	}
}

func addAgentPresence(out map[string]any, a namedMessageAgent) {
	out["online"], out["presence"], out["delivery_mode"] = namedAgentOnline(a), namedAgentPresence(a), agentDeliveryMode(a)
	out["type"] = "Hosted agent"
	if a.Attachment != nil || a.EverAttached {
		out["type"] = "Coding chat"
	}
	if a.Attachment != nil {
		out["provider"] = a.Attachment.Provider
		out["delivery_route"] = a.Attachment.DeliveryRoute
		if !agentBridgeOnlyDeployment() {
			out["repository"], out["branch"] = a.Attachment.Repository, a.Attachment.Branch
			out["activity"] = "Unknown"
			if namedAgentOnline(a) && !a.Attachment.ActivityAt.IsZero() && time.Since(a.Attachment.ActivityAt) <= 10*time.Minute {
				switch a.Attachment.Activity {
				case "busy":
					out["activity"] = "Working"
				case "idle":
					out["activity"] = "Idle"
				}
				out["activity_at"] = a.Attachment.ActivityAt
			}
		}
	}
	if !a.LastOnlineAt.IsZero() {
		out["last_seen_at"] = a.LastOnlineAt
	}
}

// The route of this message is distinct from the recipient's current presence.
// An online stream alone never proves that the native chat received anything.
func messageArrivalFields(m agentMessage) map[string]any {
	mode, text := "inbox", "Read from the inbox."
	if m.InboxAtSend {
		text = "This message waited in the inbox until the agent checked or connected."
	} else if m.LiveTransport != "" && m.LiveTransport != "mailbox" && !m.DeliveredAt.IsZero() {
		mode, text = "live", "The agent received this message through its live chat."
	} else if m.LiveAttachment != "" && m.LiveTransport != "mailbox" {
		mode, text = "live", "This message was sent to the open chat; waiting for the agent to pick it up."
	}
	return map[string]any{"delivery_mode": mode, "arrival_text": text}
}

func (api *StreamingAPI) externalMailboxAck(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !hostedAgentMailbox(claims) || !namedAgentOperationAllowed(claims, false) {
		externalError(w, 403, "receipt_refused", "Sign in with the app that owns this inbox to acknowledge its messages.")
		return
	}
	delivery := externalArg(args, "delivery")
	if delivery != "delivered" || externalArg(args, "attachment_id") != "" || externalArg(args, "transport") != "" {
		externalError(w, 400, "invalid_arguments", "Use delivery=delivered when this chat receives the message. An inbox does not attach a live chat.")
		return
	}
	a, err := namedAgentForClaims(r.Context(), claims, externalArg(args, "agent_name"))
	if err != nil {
		externalCommunicationError(w, 404, "not_found", err)
		return
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalFailure(w, err)
		return
	}
	current := false
	for _, agent := range s.Agents {
		if agent.ID == a.ID && namedAgentConnectionAllowed(agent, claims) {
			current = true
		}
	}
	if !current {
		externalError(w, 404, "not_found", "This inbox no longer belongs to this connection.")
		return
	}
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		for mi := range c.Messages {
			m := &c.Messages[mi]
			dest := c.Endpoints[1-m.From]
			if m.ID != externalArg(args, "message_id") || dest.NamedAgentID != a.ID || dest.UserID != claims.UserID {
				continue
			}
			if !s.shareSenderAllowed(*c, m.From, claims.UserID, time.Now().UTC()) || api.recheckAgentMessageAdmission(r.Context(), *c, *m) != nil {
				externalError(w, 403, "receipt_refused", "Access to this message has ended.")
				return
			}
			if m.DeliveredAt.IsZero() {
				m.LiveTransport = "mailbox"
				m.Delivery = delivery
				stampReceipt(m, delivery, time.Now().UTC())
				if err := saveAgentMessageStore(r.Context(), s); err != nil {
					externalFailure(w, err)
					return
				}
			}
			externalJSON(w, map[string]any{"agent_name": a.Name, "message_id": m.ID, "status": delivery, "delivery_mode": "inbox", "message": "Your acknowledgement was saved. No live chat was attached."})
			return
		}
	}
	externalError(w, 404, "not_found", "That incoming message is not in this agent's inbox.")
}
