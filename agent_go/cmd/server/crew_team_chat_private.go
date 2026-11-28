package server

import (
	"context"
	"log"
	"net/http"
	"os"
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

// crewChatShareOptions name the two chat shares, as the share choice shows them.
type crewChatShareOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var crewChatShareOptions = []crewChatShareOption{{crewChatShareLive, "Full chat"}, {crewChatShareAgent, "Agent only"}}

// An agent's sharing is one choice (owner, 28 Nov, approved mockup-team-share-modes): Only me, Agent only (teammates
// and their agents can ask it and hand it work; the chat stays its person's and is not copied to the server) or Full
// chat (they can also read the chat live, and its server copy while the Mac is off). Saved as the two existing fields:
// shared with the Team (InTeam) and what teammates see of its chat (Rule.ChatShare).
const (
	crewShareOnlyMe    = "only_me"
	crewShareAgentOnly = "agent_only"
	crewShareFullChat  = "full_chat"
)

// crewShareMode is the one choice an agent's two saved fields make.
func crewShareMode(inTeam bool, chatShare string) string {
	switch {
	case !inTeam:
		return crewShareOnlyMe
	case chatShare == crewChatShareAgent:
		return crewShareAgentOnly
	default:
		return crewShareFullChat
	}
}

// crewShareModeFields is what saving a choice writes; ok is false for anything but the three choices.
func crewShareModeFields(mode string) (inTeam bool, chatShare string, ok bool) {
	switch strings.TrimSpace(mode) {
	case crewShareOnlyMe:
		return false, "", true
	case crewShareAgentOnly:
		return true, crewChatShareAgent, true
	case crewShareFullChat:
		return true, crewChatShareLive, true
	}
	return false, "", false
}

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
	return crewPersonShortName(person) + " shares this agent, not the chat. Ask it what it's working on, how to do something, or hand it a task."
}

// agentChatShare is what other people in the Team see of this agent's chat. The key is any name of the agent
// (crewTeamRuleKey); there is one rule per registered agent.
func (t crewTeam) agentChatShare(ctx context.Context, person, key string) string {
	return t.agentRule(person, crewTeamRuleKey(ctx, t, person, strings.TrimSpace(key))).ChatShare
}

func (t crewTeam) anyChatLimit() bool {
	for _, a := range t.Agents {
		if a.Rule.ChatShare != crewChatShareLive || a.Rule.Audience == crewTeamAudienceChosen {
			return true
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
	if os.IsNotExist(err) || err == nil && !t.anyChatLimit() {
		return nil
	}
	if err != nil {
		return &crewChatRefusal{project: project, person: ownerOfCrewProjectID(project), agent: conversation}
	}
	person, tab := crewMacTeamConversation(r.Context(), project, conversation)
	if person == "" || person == GetUserIDFromContext(r.Context()) {
		return nil
	}
	if t.agentChatShareFor(r.Context(), GetUserIDFromContext(r.Context()), person, tab) != crewChatShareLive {
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
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return &crewChatRefusal{project: project, person: owner, agent: record.ConversationKey}
		}
		if !t.anyChatLimit() {
			continue
		}
		key := record.ConversationKey
		if crewTeamAgentOnMac(t, owner) {
			key = firstNonEmptyTrimmed(crewMacAgentTabForKey(project, key), key)
		}
		if t.agentChatShareFor(r.Context(), caller, owner, key) != crewChatShareLive {
			return &crewChatRefusal{project: project, person: owner, agent: key}
		}
	}
	return nil
}
