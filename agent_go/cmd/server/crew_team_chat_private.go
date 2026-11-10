package server

import (
	"context"
	"log"
	"net/http"
	"strings"
)

// What teammates see of an agent's chat (owner, 6 Nov, the decisions log; the saved-only choice was removed 9 Nov): an
// agent can stay shared while its chat is not. The agent's own person chooses, per agent:
//
//	Live (default, unset)  history and live updates while the agent works
//	Hidden                 listed with its status and askable; the chat is refused
//
// Teammates still ask the agent through their own thread in both. Every reader asks agentChatShare.
const (
	crewChatShareLive  = ""
	crewChatShareAgent = "agent"
)

// crewTeamChatShareLabel and crewChatShareOptions are the choice on the viewer's own agent row.
const crewTeamChatShareLabel = "Teammates see"

type crewChatShareOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var crewChatShareOptions = []crewChatShareOption{{crewChatShareLive, "Live chat"}, {crewChatShareAgent, "Just the agent"}}

// crewChatShareName is a share's plain name, as the choice shows it; "" when it is not one of the two.
func crewChatShareName(share string) string {
	for _, option := range crewChatShareOptions {
		if option.Value == share {
			return option.Label
		}
	}
	return ""
}

// crewChatPrivateLine is what a teammate sees in place of the chat, and what a refused read says.
func crewChatPrivateLine(person string) string {
	return crewPersonShortName(person) + " keeps this chat private. You can still ask this agent."
}

// agentChatShare is what other people in the Team see of this agent's chat. The key is any name of the agent
// (crewTeamRuleKey); a Main is one agent under all its names, like In team / Only me, and the stricter name wins.
func (t crewTeam) agentChatShare(ctx context.Context, person, key string) string {
	rules := t.AgentRules[person]
	if len(rules) == 0 {
		return crewChatShareLive
	}
	stored := crewTeamRuleKey(ctx, t, person, strings.TrimSpace(key))
	main := crewTeamMainAgent(t, person, stored)
	for name, rule := range rules {
		if rule.ChatShare == crewChatShareAgent && (name == stored || main && crewTeamMainAgent(t, person, name)) {
			return crewChatShareAgent
		}
	}
	return crewChatShareLive
}

// anyChatLimit: someone in this Team limits a chat, so a read needs the fuller look.
func (t crewTeam) anyChatLimit() bool {
	for _, rules := range t.AgentRules {
		for _, rule := range rules {
			if rule.ChatShare != crewChatShareLive {
				return true
			}
		}
	}
	return false
}

// crewChatRefusal is a read of a Hidden chat: whose agent and which.
type crewChatRefusal struct {
	project, person, agent string
}

// refuse logs the read and answers with the line the screen shows.
func (c *crewChatRefusal) refuse(w http.ResponseWriter, viewer string) {
	code, line := "chat_private", crewChatPrivateLine(c.person)
	log.Printf("[CREW_TEAM] project=%s event=member_watch_refused reason=%s user=%s of=%s agent=%s", c.project, code, logUsernameForUserID(viewer), logUsernameForUserID(c.person), c.agent)
	externalError(w, http.StatusForbidden, code, line)
}

// crewMacChatLimit is set when the caller is not the person of the Mac agent this relay conversation belongs to and that
// person limits its chat. It stands before the relay's reads and the Mac-off reads (crewMacOffRead).
func (api *StreamingAPI) crewMacChatLimit(r *http.Request, project, conversation string) *crewChatRefusal {
	t, err := readCrewTeam(project)
	if err != nil || !t.anyChatLimit() {
		return nil
	}
	person, tab := crewMacTeamConversation(r.Context(), project, conversation)
	if person == "" || person == GetUserIDFromContext(r.Context()) {
		return nil
	}
	if t.agentChatShare(r.Context(), person, tab) != crewChatShareLive {
		return &crewChatRefusal{project: project, person: person, agent: tab}
	}
	return nil
}

// crewSessionChatPrivate is a refusal when this server chat is Hidden for the caller.
func (api *StreamingAPI) crewSessionChatPrivate(r *http.Request, session string) *crewChatRefusal {
	return api.crewSessionChatLimit(r, session)
}

// crewSessionChatLimit is the same as crewMacChatLimit for a chat kept on this server: an agent of a Crew on the server, a
// member's server agent, or the server's own chat of a Mac agent it answers for. A teammate's own thread with the agent
// stays theirs; another person's thread with it is the agent's conversation.
func (api *StreamingAPI) crewSessionChatLimit(r *http.Request, session string) *crewChatRefusal {
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
		if err != nil || !t.anyChatLimit() {
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
		if t.agentChatShare(r.Context(), owner, key) != crewChatShareLive {
			return &crewChatRefusal{project: project, person: owner, agent: key}
		}
	}
	return nil
}
