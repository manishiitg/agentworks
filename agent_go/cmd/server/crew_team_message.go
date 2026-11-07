package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Agent to agent in a Team (owner, 5 Nov, "A Team is a shared room"; step 3): an agent of a Crew on its owner's Mac
// messages a Team member's own agent, which lives on the server. The Mac sends through its pair with this Crew's online
// copy; here the message goes out as that Mac agent (the same identity people's agents already reach it by), to the
// member's existing tab, with the normal message checks. The member's reply comes back to the Mac agent by the path
// every message to a Mac agent takes (deliverToMacSharedAgent), which needs the sending agent to be In team.

type crewTeamMessageRequest struct {
	FromTab        string `json:"from_tab"`        // the Mac agent sending: "main" or a side tab id
	ConversationID string `json:"conversation_id"` // its Mac conversation, for its session here
	Agent          string `json:"agent,omitempty"` // its name, for the other side's view
	ChatKey        string `json:"chat_key"`        // the member's tab, as the side-chats list names it
	Message        string `json:"message"`
	SubmissionID   string `json:"submission_id,omitempty"`
}

// crewTeamMessage handles a crew-sync "team_message" from the Mac that holds this Crew.
func (api *StreamingAPI) crewTeamMessage(w http.ResponseWriter, r *http.Request, project string, input crewSyncRequest) {
	claims := GetUserFromContext(r.Context())
	in := input.TeamMessage
	if in == nil || claims == nil || !crewTeamMessagePairAdmitted(project, claims.UserID, input.PairID) {
		externalError(w, 409, "wrong_pair", "Send this from the Mac that holds this Crew.")
		return
	}
	if !crewMacTabID(in.FromTab) && in.FromTab != macMainTab || strings.TrimSpace(in.ConversationID) == "" {
		externalError(w, 400, "invalid_sender", "Choose one of this Crew's agents to send from.")
		return
	}
	// A reply names the conversation's inbox; sendAgentMessage admits only that conversation's own agents.
	inbox := ""
	if strings.HasPrefix(in.ChatKey, "inbox-") {
		inbox = in.ChatKey
	}
	var target triggerTarget
	member := ""
	if inbox == "" {
		var ok bool
		if target, member, ok = api.crewTeamMessageTarget(w, r, project, in.ChatKey, claims); !ok {
			return
		}
	}
	ctx := internalBotRequestContext(r.Context(), claims.UserID)
	// The sending agent's conversation key is the one its Mac is listed under, so the answer comes back to that Mac: the
	// owner's Mac by the project key, a member's Mac by their own key (crewMacTabRow).
	key := macSharedAgentChatKey(project, in.FromTab)
	if claims.UserID != ownerOfCrewProjectID(project) {
		key = crewMacTabRow(project, crewMacTeamTab{Person: claims.UserID, Tab: in.FromTab}).ConversationKey
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: project, ProfileID: crewProfileID}, Label: firstNonEmptyTrimmed(in.Agent, "Main"),
		Chat: &codeChat{OwnerID: claims.UserID, IsMain: in.FromTab == macMainTab, Key: key, ID: in.FromTab, Name: firstNonEmptyTrimmed(in.Agent, "Main"), SessionID: crewChatSessionID(project, in.ConversationID)}}
	out, err := api.sendAgentMessage(ctx, claims.UserID, caller, target, in.Message, inbox, in.SubmissionID)
	if err != nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_message_failed from=%s to_member=%s reason=%q", project, in.FromTab, logUsernameForUserID(member), err)
		externalError(w, 409, "message_failed", err.Error())
		return
	}
	log.Printf("[CREW_TEAM] project=%s event=agent_message from=%s to_member=%s chat=%q", project, in.FromTab, logUsernameForUserID(member), in.ChatKey)
	externalJSON(w, out)
}

// crewTeamMessageTarget is the Team agent a Mac's new message goes to; a refusal is already written to w.
func (api *StreamingAPI) crewTeamMessageTarget(w http.ResponseWriter, r *http.Request, project, chatKey string, claims *UserClaims) (triggerTarget, string, bool) {
	member := crewTeamMemberTab(r.Context(), project, chatKey)
	if member == "" {
		// A member's agent on their Mac (crew_team_mac_watch.go), not one of their server tabs.
		if person, _, ok := crewMemberMacAgentFor(r.Context(), project, chatKey, claims.UserID); ok {
			member = person
		}
	}
	if member == "" {
		member = crewTeamOwnerAgent(r.Context(), project, chatKey, claims.UserID)
	}
	if member == "" || member == claims.UserID {
		externalError(w, 404, "not_in_team", "That agent isn't in this Crew's Team now.")
		return triggerTarget{}, "", false
	}
	ctx := internalBotRequestContext(r.Context(), claims.UserID)
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:"+crewProfileID+":"+project+"/agent/"+chatKey)
	if err != nil {
		externalError(w, 404, "not_in_team", "That agent isn't in this Crew's Team now.")
		return triggerTarget{}, "", false
	}
	return target, member, true
}

// crewTeamMessagePairAdmitted: a Team message comes from the Mac that holds the sender's working copy of this Crew. The
// owner's Mac is the Crew's own sync binding; a member's is the pair the server recorded for them when they put their
// agents on their Mac (owner, 6 Nov: any two people in a Team can message each other's agents, owner or not). A person
// who is only invited, or whose agents work on the website, has no pair and is refused.
func crewTeamMessagePairAdmitted(project, user, pair string) bool {
	if user == "" || pair == "" {
		return false
	}
	if ownerOfCrewProjectID(project) == user {
		b, err := crewroot.ReadSync(project)
		return err == nil && b != nil && b.Mode == "relay" && b.Side == "server" && b.Editing == "local" && b.PairID == pair
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID {
		return false
	}
	recorded, ok := t.memberMac(user)
	return ok && recorded == pair
}

// crewTeamOwnerAgent is the Crew owner when key is one of the owner's agents that are In team, read from the same rows the
// Team tab lists (teamAgentsByPerson), else "". caller is the one asking: others' Only me agents are never in the rows.
func crewTeamOwnerAgent(ctx context.Context, project, key, caller string) string {
	t, err := readCrewTeam(project)
	if err != nil || !t.On || t.OwnerID == "" || ownerOfCrewProjectID(project) != t.OwnerID {
		return ""
	}
	short := strings.TrimPrefix(strings.TrimSpace(key), project+codeChatSideMarker)
	for _, person := range teamAgentsByPerson(ctx, t, caller) {
		if !person.Owner {
			continue
		}
		for _, agent := range person.Agents {
			if agent.InTeam && (agent.ChatKey == key || strings.TrimPrefix(agent.ChatKey, project+codeChatSideMarker) == short || key == project && agent.Tab == macMainTab) {
				return t.OwnerID
			}
		}
	}
	return ""
}

// sendTeamMessageFromMac is send_message on the Mac for a target "team:<local Crew>:<member's chat key>" (list_agents
// names it): the message goes to the Crew's online copy through this Mac's pair.
func (api *StreamingAPI) sendTeamMessageFromMac(ctx context.Context, owner string, caller triggerLinkCaller, raw, message, submission string) (map[string]any, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(raw), "team:")
	project, chatKey, ok := strings.Cut(rest, ":")
	if !ok || project == "" || chatKey == "" {
		return nil, fmt.Errorf("use the team agent's id from list_agents")
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" || b.Mode != "relay" {
		return nil, fmt.Errorf("turn on the Team for this Crew first")
	}
	connection, err := desktopConnectionFor(owner, b.ConnectionID)
	if err != nil {
		return nil, err
	}
	if caller.Chat == nil || caller.Stamp.ID != project {
		return nil, fmt.Errorf("send to a Team member's agent from an agent of the same Crew")
	}
	tab := macMainTab
	if _, id, side := strings.Cut(caller.Chat.Key, codeChatSideMarker); side {
		tab = id
	}
	conversation := ""
	if records, err := defaultProductConversationRegistryStore().projectChats(ctx, owner, crewProfileID, project); err == nil {
		for _, record := range records {
			if record.ConversationKey == caller.Chat.Key || record.SessionID == caller.Chat.SessionID {
				conversation = record.ConversationID
				// The Team lists a Mac's main chat as Main, so that is the name a reply must come back to.
				if record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(project, owner) {
					tab = macMainTab
				}
				break
			}
		}
	}
	reply, err := desktopCrewSyncCall(ctx, owner, &connection, b.PeerProjectID, crewSyncRequest{Action: "team_message", PairID: b.PairID,
		TeamMessage: &crewTeamMessageRequest{FromTab: tab, ConversationID: conversation, Agent: caller.Chat.Name, ChatKey: chatKey, Message: message, SubmissionID: submission}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"sent": true, "to": chatKey, "inbox_id": reply.InboxID, "note": "Their reply comes back to this chat."}, nil
}

// crewTeamAddressOnMac is this Mac's send_message target for a Team agent addressed the server's way
// (workspace:work:<online copy>/agent/<chat key>): "team:<the Crew here paired with that copy>:<chat key>", or "".
func crewTeamAddressOnMac(raw string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(raw), "workspace:"+crewProfileID+":")
	if !ok {
		return ""
	}
	project, chatKey, ok := strings.Cut(rest, "/agent/")
	if !ok || project == "" || chatKey == "" {
		return ""
	}
	if local := crewLocalCopyOf(project); local != "" {
		return "team:" + local + ":" + chatKey
	}
	return ""
}

// teamMemberAgentRows lists, on the Mac, each Team member's own agents of this person's Mac Crews, read from the
// Crew's online copy through the Mac's sign-in (the server decides what the owner may see). Their id is the
// send_message target "team:<Crew>:<their chat>".
func teamMemberAgentRows(ctx context.Context, owner string, crews []map[string]interface{}) []map[string]interface{} {
	rows := []map[string]interface{}{}
	for _, crew := range crews {
		id, _ := crew["id"].(string)
		name, _ := crew["name"].(string)
		b, err := crewroot.ReadSync(id)
		if err != nil || b == nil || b.Side != "local" || b.Mode != "relay" || b.PeerProjectID == "" {
			continue
		}
		c, err := desktopConnectionFor(owner, b.ConnectionID)
		if err != nil {
			continue
		}
		var tabs []AgentProfileSideChat
		read, done := context.WithTimeout(ctx, 6*time.Second)
		err = crewTeamRemoteJSON(read, owner, c, http.MethodGet, "/api/agent-profiles/"+crewProfileID+"/side-chats?project_id="+url.QueryEscape(b.PeerProjectID), nil, &tabs)
		done()
		if err != nil {
			continue
		}
		for _, tab := range tabs {
			if tab.CanWrite || tab.TabOwnerID == "" || tab.TabOwnerID == owner || tab.ConversationKey == "" {
				continue
			}
			person := strings.SplitN(firstNonEmptyTrimmed(tab.TabOwnerName, "Teammate"), "@", 2)[0]
			rows = append(rows, map[string]interface{}{"kind": "team_agent", "type": "Team member's agent", "id": "team:" + id + ":" + tab.ConversationKey,
				"name": person + "'s " + firstNonEmptyTrimmed(tab.Name, "Main"), "owner": person, "crew_name": name, "purpose": tab.Purpose, "presence": "Runs when messaged",
				"reach": "messages action=send(target=<id>, message=…) from an agent of the same Crew; their reply comes back to that chat"})
		}
	}
	return rows
}
