package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

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
		if agent.ReplyInbox != c.ID || agent.PublishedAsker != c.Endpoints[1-i].UserID || c.Endpoints[1-i].PublishedAgent != "" {
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
		home, err := api.ensureCrewTeamOnlineHome(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: source.UserID}), dest.ID)
		if err != nil {
			return c, false, err
		}
		key := crewOwnerMainChatKey(home)
		root := crewProjectRootByID(home)
		chat, ok := sharedCrewAgentChat(ctx, source.UserID, root, key)
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
		chat, ok := sharedCrewAgentChat(ctx, source.UserID, dest.Path, dest.ChatKey)
		if !ok || chat.OwnerID != dest.UserID {
			return c, false, nil
		}
		dest.Label = chat.Name
	}
	dest.PublishedAgent = dest.ChatKey
	dest.PublishedAsker = source.UserID
	dest.ReplyInbox = c.ID
	dest.ChatKey = sharedThreadKey(dest.ID, strings.TrimPrefix(dest.PublishedAgent, dest.ID+codeChatSideMarker), source.UserID)
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
		log.Printf("[TEAM_ONLINE] event=offline_ask home=%s asker=%s thread=%s", dest.ID, source.UserID, dest.ChatKey)
	}
	return c, err == nil, err
}

func (api *StreamingAPI) deliverPublishedAgentMessage(ctx context.Context, c agentConversation, m agentMessage) (string, error) {
	if err := api.recheckAgentMessageAdmission(ctx, c, m); err != nil {
		return "", err
	}
	source, dest := c.Endpoints[m.From], c.Endpoints[1-m.From]
	if source.PublishedAgent != "" {
		// An explicit reply settles only the admitted inbox. It never starts, reads
		// or resumes an agent in the asker's private Crew.
		log.Printf("[TEAM_ONLINE] event=offline_reply home=%s asker=%s thread=%s", source.ID, source.PublishedAsker, source.ChatKey)
		return "", nil
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
	ctx = context.WithValue(ctx, crewMacSharedAskKey{}, &crewMacSharedAsk{Sender: source.UserID, SenderName: source.Label, Owner: main})
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
	_, err = resolveCrewProjectBinding(ctx, source.UserID, profile, dest.ID, "")
	if err != nil {
		return "", err
	}
	role := dest
	role.ChatKey, role.Session = main.ConversationKey, main.SessionID
	role = api.agentMessageCallerRole(role)
	run := crewMessageRunMode(role, source, firstNonEmptyTrimmed(role.Guest, source.UserID))
	if run {
		applyCrewGuestCaller(req, source.UserID)
		req["crew_ask_read_only"] = true
	}
	req["pin_run_mode"] = run
	req["agent_message_inbox"] = c.ID
	req["triggered_by"] = "agent_message"
	req["triggered_by_label"] = "From " + source.Label
	req["asked_by"] = source.UserID
	req["delivery_mode"] = "queue"
	req["agent_message_from_other_person"] = true
	req["is_auto_notification"] = true
	req["disable_live_input_delivery"] = true
	label, agent, channel := agentMessageSender(ctx, source, dest.UserID)
	ctx = context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "agent_message", "sender_label": label, "sender_agent": agent, "sender_channel": channel, "sender_user_id": source.UserID, "agent_message_inbox": c.ID, "message_id": m.ID, "display_content": m.Text, "agent_message_instructions": agentMessageInstructions(c.ID)})
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
