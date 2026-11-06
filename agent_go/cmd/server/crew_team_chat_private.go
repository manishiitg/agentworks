package server

import (
	"context"
	"log"
	"net/http"
	"strings"
)

// Teammates can read this chat (owner, 6 Nov, the decisions log): an agent can stay shared while its own chat is not
// readable by the Team. Everything is visible unless the agent's own person turns it off. With it off, teammates still
// see the agent listed with its status and still ask it through their own thread; the server refuses every read of the
// agent's chat by anyone but its person. Every reader asks agentChatVisible.

// crewTeamChatVisibleLabel is the line of the switch on the viewer's own agent.
const crewTeamChatVisibleLabel = "Teammates can read this chat"

// crewChatPrivateLine is what a teammate sees in place of the chat, and what a refused read says.
func crewChatPrivateLine(person string) string {
	return crewPersonShortName(person) + " keeps this chat private. You can still ask this agent."
}

// chatVisible: unset is visible; only "off" is ever stored.
func (r crewTeamAgentRule) chatVisible() bool { return r.ChatVisible == nil || *r.ChatVisible }

// agentChatVisible: other people in the Team may read this agent's chat. The key is any name of the agent
// (crewTeamRuleKey); a Main is one agent under all its names, like In team / Only me.
func (t crewTeam) agentChatVisible(ctx context.Context, person, key string) bool {
	rules := t.AgentRules[person]
	if len(rules) == 0 {
		return true
	}
	stored := crewTeamRuleKey(ctx, t, person, strings.TrimSpace(key))
	main := crewTeamMainAgent(t, person, stored)
	for name, rule := range rules {
		if !rule.chatVisible() && (name == stored || main && crewTeamMainAgent(t, person, name)) {
			return false
		}
	}
	return true
}

// anyChatPrivate: someone in this Team keeps a chat private, so a read needs the fuller look.
func (t crewTeam) anyChatPrivate() bool {
	for _, rules := range t.AgentRules {
		for _, rule := range rules {
			if !rule.chatVisible() {
				return true
			}
		}
	}
	return false
}

// crewChatRefusal is one read of a private chat: whose agent, and which.
type crewChatRefusal struct {
	project, person, agent string
}

// refuse logs the read and answers with the line the screen shows.
func (c *crewChatRefusal) refuse(w http.ResponseWriter, viewer string) {
	log.Printf("[CREW_TEAM] project=%s event=member_watch_refused reason=chat_private user=%s of=%s agent=%s", c.project, logUsernameForUserID(viewer), logUsernameForUserID(c.person), c.agent)
	externalError(w, http.StatusForbidden, "chat_private", crewChatPrivateLine(c.person))
}

// crewMacChatPrivate is a refusal when the caller is not the person of the Mac agent this relay conversation belongs to
// and that person keeps its chat private. It stands before the relay's reads and the Mac-off reads (crewMacOffRead).
func (api *StreamingAPI) crewMacChatPrivate(r *http.Request, project, conversation string) *crewChatRefusal {
	t, err := readCrewTeam(project)
	if err != nil || !t.anyChatPrivate() {
		return nil
	}
	person, tab := crewMacTeamConversation(r.Context(), project, conversation)
	if person == "" || person == GetUserIDFromContext(r.Context()) || t.agentChatVisible(r.Context(), person, tab) {
		return nil
	}
	return &crewChatRefusal{project: project, person: person, agent: tab}
}

// crewSessionChatPrivate is the same for a chat kept on this server: an agent of a Crew on the server, a member's
// server agent, or the server's own chat of a Mac agent it answers for. A teammate's own thread with the agent stays
// theirs; another person's thread with it is the agent's conversation.
func (api *StreamingAPI) crewSessionChatPrivate(r *http.Request, session string) *crewChatRefusal {
	caller := GetUserIDFromContext(r.Context())
	if IsLocalMode() || caller == "" || strings.TrimSpace(session) == "" {
		return nil
	}
	owner := api.crewSessionRecordedOwner(session)
	if owner == "" || owner == caller {
		return nil
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(r.Context(), productConversationRegistryPath(owner))
	if err != nil {
		return nil // the read's own check cannot place this chat either and refuses it
	}
	for _, record := range doc.Entries {
		if record.SessionID != session || record.ProfileID != crewProfileID {
			continue
		}
		project := crewResourceProjectID(record.ConversationKey)
		t, err := readCrewTeam(project)
		if err != nil || !t.anyChatPrivate() {
			continue
		}
		key := record.ConversationKey
		if crewTeamAgentOnMac(t, owner) {
			tab := crewStandbyTabForKey(project, key)
			if key == sharedThreadKey(project, tab, caller) {
				return nil
			}
			key = firstNonEmptyTrimmed(tab, key)
		}
		if !t.agentChatVisible(r.Context(), owner, key) {
			return &crewChatRefusal{project: project, person: owner, agent: key}
		}
	}
	return nil
}
