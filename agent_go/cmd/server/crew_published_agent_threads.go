package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
)

// Guest is stamped from the originating runtime, never supplied by message text.
func publishedAsker(e agentMessageEndpoint) string { return firstNonEmptyTrimmed(e.Guest, e.UserID) }

func publishedConversation(c agentConversation) bool {
	return c.Endpoints[0].PublishedAgent != "" || c.Endpoints[1].PublishedAgent != ""
}

// This server-issued endpoint grants exactly one inbox, never the asker's files,
// native key or project tools. The ordinary publication audience remains live.
func recheckAgentConversationContact(ctx context.Context, c agentConversation) error {
	if handled, err := crewTeamConversationAdmission(ctx, c); handled {
		if err != nil {
			return err
		}
		if !publishedConversation(c) {
			return nil
		}
	}
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
		// The published chat may belong to the Crew's owner or to a Team member
		// whose own chat lives in that Crew. Which person owns it is pinned
		// below by publishedAgentAuthorizationPeer (chat.OwnerID == UserID).
		if agent.Kind != triggerCallerCrew || agent.External || agent.UserID == "" || ownerOfCrewProjectID(agent.ID) == "" {
			return refuse("agent_identity")
		}
		// One agent, one chat (#3469): the ask runs in the published agent's own chat.
		if agent.ChatKey != agent.PublishedAgent {
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
		if err := crewTeamAdmitEndpoint(ctx, peer.PublishedAsker, peer); err != nil {
			return peer, err
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
	// Offline ASK routing happened at submission through crewTeamRouteAsk.
	// A direct message never chooses a different receiver while queued.
	if dest.External || dest.Kind != triggerCallerCrew || route == "owner_mac" || route == "member_mac" {
		return c, false, nil
	}
	if dest.TeamAgentID == "" && (crewRelayTwin(dest.ID) || !ordinaryProjectChatKey(dest.ID, dest.ChatKey)) {
		return c, false, nil
	}
	chat, ok := sharedCrewAgentChat(ctx, publishedAsker(source), dest.Path, dest.ChatKey)
	if !ok || chat.OwnerID != dest.UserID {
		return c, false, nil
	}
	dest.Label = chat.Name
	dest.PublishedAgent = dest.ChatKey
	dest.PublishedAsker = publishedAsker(source)
	dest.ReplyInbox = c.ID
	// One agent, one chat (owner, 20 Nov, #3469): the ask goes into the agent's own chat, marked with who sent it.
	dest.ChatKey = dest.PublishedAgent
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
	// The receiving chat may be a Team member's own chat in the owner's Crew:
	// bind it the way that person's own turns are bound, not owner-only.
	mainBinding, _, err := resolveConversationBindingForUser(ctx, dest.UserID, profile, dest.PublishedAgent)
	if err != nil {
		return "", err
	}
	main, err := store.resolveOrCreate(ctx, dest.UserID, profile, mainBinding, "")
	if err != nil {
		return "", err
	}
	binding, _, err := resolveConversationBindingForUser(ctx, dest.UserID, profile, dest.ChatKey)
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
	// A person's chat with the Team agent runs on the provider and model its owner chose for the Team agent (#3059).
	teamNote := ""
	if main.ConversationKey == crewOwnerMainChatKey(dest.ID) {
		if home, err := readCrewTeam(dest.ID); err == nil && home.RosterSource != "" {
			teamNote = crewTeamAgentAskNote(home.RosterSource, publishedAsker(source))
			log.Printf("[TEAM_AGENT_ASK] team=%s home=%s asker=%s inbox=%s message=%s target=agent_chat session=%s", home.RosterSource, dest.ID, publishedAsker(source), c.ID, m.ID, conversation.SessionID)
		}
	}
	ctx = costAgentMessageContext(ctx, source)
	ctx = context.WithValue(ctx, crewMacSharedAskKey{}, &crewMacSharedAsk{Sender: publishedAsker(source), SenderName: source.Label, Owner: main})
	text := agentMessageText(c, m)
	if teamNote != "" {
		text = teamNote + agentMessageTextFromTeamMember(c, m)
	}
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
	if _, err = resolveCrewProjectBinding(ctx, publishedAsker(source), profile, dest.ID, ""); err != nil {
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
	if err == nil {
		log.Printf("[AGENT-MESSAGE] action=delivered kind=%s message=%s session=%s woke=true", m.MessageType, m.ID, conversation.SessionID)
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

// crewTeamAgentAskNote tells the Team agent who is asking: a joined member's agent is a teammate asking for Team work,
// not a stranger's offer (#2993). Admission is still the source Team's live roster (resolveCrewProjectBinding below);
// this only words it. Anyone else gets no note.
func crewTeamAgentAskNote(sourceProject, asker string) string {
	source, err := readCrewTeam(sourceProject)
	if err != nil || !source.On || !source.member(asker) || !crewTeamAccountActive(asker) {
		return ""
	}
	name := crewTeamPersonLabels(source)[asker]
	return "[This request is from " + name + ", a member of the Team \"" + source.Name + "\", sent through their own agent. It is not an offer from an outsider: as the Team agent you work for every member, and a member's request is your normal work with the same authority as the Team's own. Do the Team work they ask, including installing, setting up or changing things in this home, schedules, Watchers, skills and how the Team works, without waiting for the owner. Files they hand off are in the Team's shared folder: use team_sync files, read_file and save_file. Never reveal secret values.]\n\n"
}
