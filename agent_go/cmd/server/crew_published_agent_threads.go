package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Guest is stamped from the originating runtime, never supplied by message text.
func publishedAsker(e agentMessageEndpoint) string { return firstNonEmptyTrimmed(e.Guest, e.UserID) }

func publishedConversation(c agentConversation) bool {
	return c.Endpoints[0].PublishedAgent != "" || c.Endpoints[1].PublishedAgent != ""
}

// This server-issued endpoint grants exactly one inbox, never the asker's files,
// native key or project tools. The ordinary publication audience remains live.
func recheckAgentConversationContact(ctx context.Context, c agentConversation) error {
	if !publishedConversation(c) {
		return recheckSharedCrewAgentContact(ctx, c.Endpoints[0], c.Endpoints[1])
	}
	for i, agent := range c.Endpoints {
		if agent.PublishedAgent == "" {
			continue
		}
		refuse := func(reason string) error {
			log.Printf("[TEAM_ONLINE] event=offline_refused home=%s asker=%s thread=%s reason=%s", agent.ID, agent.PublishedAsker, agent.ChatKey, reason)
			return admissionRefused("This published agent or its audience is no longer available.")
		}
		if agent.ReplyInbox != c.ID || agent.PublishedAsker != publishedAsker(c.Endpoints[1-i]) || c.Endpoints[1-i].PublishedAgent != "" {
			return refuse("inbox_binding")
		}
		if agent.Kind != triggerCallerCrew || agent.External || agent.UserID != ownerOfCrewProjectID(agent.ID) {
			return refuse("agent_identity")
		}
		if agent.ChatKey != sharedThreadKey(agent.ID, strings.TrimPrefix(agent.PublishedAgent, agent.ID+codeChatSideMarker), agent.PublishedAsker) {
			return refuse("thread_binding")
		}
		if _, err := publishedAgentAuthorizationPeer(ctx, agent); err != nil {
			return refuse("audience_or_publication")
		}
	}
	return nil
}

// Authorization uses the published agent, not the private sender thread. This
// does not strip a connection's scopes or invent access to any other product.
func publishedAgentAuthorizationPeer(ctx context.Context, peer agentMessageEndpoint) (agentMessageEndpoint, error) {
	if peer.PublishedAgent == "" {
		return peer, nil
	}
	if peer.TeamAgentID != "" {
		a, owner, err := crewTeamAgentByID(ctx, peer.ID, peer.PublishedAsker, peer.TeamAgentID)
		if err != nil || owner != peer.UserID || a.ChatKey != peer.PublishedAgent {
			return peer, admissionRefused("The published agent is no longer available to this asker.")
		}
	}
	chat, ok := sharedCrewAgentChat(ctx, peer.PublishedAsker, peer.Path, peer.PublishedAgent)
	if !ok || chat.OwnerID != peer.UserID {
		return peer, admissionRefused("The agent is no longer shared with this asker.")
	}
	peer.ChatKey, peer.Session = chat.Key, chat.SessionID
	return peer, nil
}

// Ordinary publications use this same sender-thread path. Offline Team routing
// only chooses the destination; it adds no exception to message admission.
func (api *StreamingAPI) routePublishedAgentMessage(ctx context.Context, c agentConversation, m agentMessage, source, dest agentMessageEndpoint, route string) (agentConversation, bool, error) {
	offline := false
	if dest.TeamAgentID != "" && (route == "owner_mac" || route == "member_mac") {
		team, err := readCrewTeam(dest.ID)
		if err != nil {
			return c, false, err
		}
		pair := ""
		if route == "owner_mac" {
			b, err := crewroot.ReadSync(dest.ID)
			if err != nil {
				return c, false, err
			}
			if b != nil {
				pair = b.PairID
			}
		} else {
			for _, p := range team.People {
				if p.UserID == dest.UserID {
					pair = p.MacPair
				}
			}
		}
		on, _ := crewMacSocketOnline(dest.UserID, dest.ID, pair)
		if on || crewPublishedTarget(dest.ID, macSharedAgentTab(dest.ID, dest.ChatKey)) {
			return c, false, nil
		}
		offline = true
		home := team.OnlineHome
		if home == "" {
			return c, false, nil
		} // only an explicit hand-off creates it
		key := crewOwnerMainChatKey(home)
		root := crewProjectRootByID(home)
		chat, ok := sharedCrewAgentChat(ctx, publishedAsker(source), root, key)
		if !ok {
			return c, false, fmt.Errorf("The Team agent isn't ready. Choose its agent and account in the Team Crew settings.")
		}
		main, err := crewTeamSendingAgent(ctx, home, chat.OwnerID, key)
		if err != nil || main.ID == "" {
			return c, false, fmt.Errorf("The Team agent isn't shared with this Team.")
		}
		dest = agentMessageEndpoint{TeamAgentID: main.ID, Kind: triggerCallerCrew, ID: home, Profile: crewProfileID, UserID: chat.OwnerID, Path: root, ChatKey: key, Label: chat.Name}
	} else {
		if crewRelayTwin(dest.ID) || !ordinaryProjectChatKey(dest.ID, dest.ChatKey) {
			return c, false, nil
		}
		chat, ok := sharedCrewAgentChat(ctx, publishedAsker(source), dest.Path, dest.ChatKey)
		if !ok || chat.OwnerID != dest.UserID {
			return c, false, nil
		}
		dest.Label = chat.Name
	}
	dest.PublishedAgent = dest.ChatKey
	dest.PublishedAsker = publishedAsker(source)
	dest.ReplyInbox = c.ID
	dest.ChatKey = sharedThreadKey(dest.ID, strings.TrimPrefix(dest.PublishedAgent, dest.ID+codeChatSideMarker), publishedAsker(source))
	dest.Session = ""
	c.Endpoints[1-m.From] = dest
	if err := recheckAgentConversationContact(ctx, c); err != nil {
		return c, false, err
	}
	// The original ask was admitted before any redirect. Persist its unchanged
	// asker and new home identity together before starting the receiving turn.
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err == nil {
		found := false
		for i := range s.Conversations {
			if s.Conversations[i].ID == c.ID {
				if publishedConversation(s.Conversations[i]) {
					c = s.Conversations[i]
				} else {
					s.Conversations[i].Endpoints[1-m.From] = dest
				}
				found = true
				break
			}
		}
		if !found {
			err = fmt.Errorf("The reply inbox is no longer available.")
		} else {
			err = saveAgentMessageStore(ctx, s)
		}
	}
	agentMessageMu.Unlock()
	if err == nil && offline {
		log.Printf("[TEAM_ONLINE] event=offline_ask home=%s asker=%s thread=%s", dest.ID, publishedAsker(source), dest.ChatKey)
	}
	return c, err == nil, err
}

func (api *StreamingAPI) deliverPublishedAgentMessage(ctx context.Context, c agentConversation, m agentMessage) (string, error) {
	if err := api.recheckAgentMessageAdmission(ctx, c, m); err != nil {
		return "", err
	}
	source, dest := c.Endpoints[m.From], c.Endpoints[1-m.From]
	if source.PublishedAgent != "" {
		// The original asker receives a new message in their own chat. The
		// admitted inbox authorizes this delivery only; the publisher acquires
		// no ability to read that Crew or contact a different private endpoint.
		ctx = internalBotRequestContext(context.WithValue(ctx, UserContextKey, (*UserClaims)(nil)), dest.UserID)
		return api.deliverNativeAgentMessage(ctx, c, m, true)
	}
	// A redirect changes the receiving user. Keep all provider/account and
	// workspace preparation in that normal user's namespace, not the old Mac
	// agent owner's. Sender authority is still rechecked from the saved message.
	// The normal helper deliberately preserves an existing sign-in. Mask only
	// that old identity before asking it to load the receiving user's directory
	// record; keep cancellation, admission and sender-attribution context.
	ctx = internalBotRequestContext(context.WithValue(ctx, UserContextKey, (*UserClaims)(nil)), dest.UserID)
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, dest.UserID)
	if err != nil {
		return "", err
	}
	store := defaultProductConversationRegistryStore()
	mainBinding, err := resolveProductProjectBinding(ctx, dest.UserID, profile, dest.PublishedAgent)
	if err != nil {
		return "", err
	}
	main, err := store.resolveOrCreate(ctx, dest.UserID, profile, mainBinding, "")
	if err != nil {
		return "", err
	}
	binding, err := resolveProductProjectBinding(ctx, dest.UserID, profile, dest.ChatKey)
	if err != nil {
		return "", err
	}
	conversation, err := store.resolveOrCreate(ctx, dest.UserID, profile, binding, "")
	if err != nil {
		return "", err
	}
	if api.agentMessageSessionClosed(main.SessionID) || api.agentMessageSessionClosed(conversation.SessionID) {
		return "", fmt.Errorf("The receiving conversation was stopped or replaced.")
	}
	ctx = costAgentMessageContext(ctx, source)
	ctx = context.WithValue(ctx, crewMacSharedAskKey{}, &crewMacSharedAsk{Sender: publishedAsker(source), SenderName: source.Label, Owner: main})
	text := agentMessageText(c, m)
	query, err := prepareProductConversationTurn(ctx, dest.UserID, profile, AgentProfileChatRequest{Message: text}, conversation)
	if err != nil {
		return "", err
	}
	req, err := queryRequestToMap(query)
	if err != nil {
		return "", err
	}
	// Admit the normal home binding, then preserve the published agent's normal
	// Builder/Run settings rather than inventing a Team-specific delegation.
	_, err = resolveCrewProjectBinding(ctx, publishedAsker(source), profile, dest.ID, "")
	if err != nil {
		return "", err
	}
	role := dest
	role.ChatKey, role.Session = main.ConversationKey, main.SessionID
	role = api.agentMessageCallerRole(role)
	run := crewMessageRunMode(role, source, firstNonEmptyTrimmed(role.Guest, publishedAsker(source)))
	if run {
		applyCrewGuestCaller(req, publishedAsker(source))
		req["crew_ask_read_only"] = true
	}
	req["pin_run_mode"] = run
	req["agent_message_inbox"] = c.ID
	req["triggered_by"] = "agent_message"
	req["triggered_by_label"] = "From " + source.Label
	req["asked_by"] = publishedAsker(source)
	req["delivery_mode"] = "queue"
	req["agent_message_from_other_person"] = true
	req["is_auto_notification"] = true
	req["disable_live_input_delivery"] = true
	label, agent, channel := agentMessageSender(ctx, source, dest.UserID)
	ctx = context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "agent_message", "sender_label": label, "sender_agent": agent, "sender_channel": channel, "sender_user_id": publishedAsker(source), "agent_message_inbox": c.ID, "message_id": m.ID, "display_content": m.Text, "agent_message_instructions": agentMessageInstructions(c.ID)})
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err == nil {
		found := false
		for i := range s.Conversations {
			if s.Conversations[i].ID == c.ID && s.Conversations[i].Endpoints[1-m.From].ReplyInbox == c.ID {
				s.Conversations[i].Endpoints[1-m.From].Session = conversation.SessionID
				found = true
				break
			}
		}
		if !found {
			err = fmt.Errorf("The reply inbox is no longer available.")
		} else {
			err = saveAgentMessageStore(ctx, s)
		}
	}
	agentMessageMu.Unlock()
	if err != nil {
		return "", err
	}
	if err = api.recheckAgentMessageAdmission(ctx, c, m); err != nil {
		return "", err
	}
	if agentMessageTurn != nil {
		_, err = agentMessageTurn(api, ctx, req, conversation.SessionID, dest.UserID)
	} else {
		_, err = api.startSessionInternalWithResult(ctx, req, conversation.SessionID, dest.UserID, nil)
	}
	return conversation.SessionID, err
}

// Use the already-existing paired owner-side ask operation, bound to the exact
// original asker/tab. No read operation or new Mac protocol is introduced.
func (api *StreamingAPI) deliverPublishedReplyToMac(ctx context.Context, c agentConversation, m agentMessage, dest agentMessageEndpoint) (bool, error) {
	pair, paired := crewMacPairFor(dest.UserID, dest.ID)
	if !paired {
		return false, nil
	}
	if err := api.recheckAgentMessageAdmission(ctx, c, m); err != nil {
		return true, err
	}
	tab := macSharedAgentTab(dest.ID, dest.ChatKey)
	if person, memberTab, member := crewMemberMacTarget(dest.ID, dest.ChatKey); member {
		if person != dest.UserID {
			return true, admissionRefused("The reply endpoint changed.")
		}
		tab = memberTab
	}
	if !crewMacTabID(tab) {
		return true, admissionRefused("The original reply chat is unavailable.")
	}
	args, _ := json.Marshal(map[string]any{"chat_id": tab, "message": agentMessageText(c, m), "submission_id": "published-reply:" + m.ID})
	result := callCrewMacPair(ctx, dest.UserID, dest.ID, pair, crewChatCall{Operation: "workspace", Action: "ask", Caller: c.Endpoints[m.From].Label, Args: args, SubmissionID: m.ID}, 60*time.Second)
	if result.Status == http.StatusServiceUnavailable {
		return true, conditiontrigger.HoldError{Err: fmt.Errorf("The asker's computer is offline.")}
	}
	if result.Status < 200 || result.Status >= 300 {
		return true, fmt.Errorf("The original chat could not take the reply.")
	}
	return true, nil
}
