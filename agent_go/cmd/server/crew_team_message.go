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
	Operation      string `json:"operation,omitempty"` // empty/send, or read
	After          int    `json:"after,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	FromTab        string `json:"from_tab"`        // the Mac agent sending: "main" or a side tab id
	ConversationID string `json:"conversation_id"` // its Mac conversation, for its session here
	Agent          string `json:"agent,omitempty"` // its name, for the other side's view
	AgentID        string `json:"agent_id"`        // server-issued Team agent ID, or a reply inbox
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
	if strings.HasPrefix(in.AgentID, "inbox-") {
		inbox = in.AgentID
	}
	// The sending agent's conversation key is the one its Mac is listed under, so the answer comes back to that Mac: the
	// owner's Mac by the project key, a member's Mac by their own key (crewMacTabRow).
	key := macSharedAgentChatKey(project, in.FromTab)
	if claims.UserID != ownerOfCrewProjectID(project) {
		key = crewMacTabRow(project, crewMacTeamTab{Person: claims.UserID, Tab: in.FromTab}).ConversationKey
	}
	ctx := internalBotRequestContext(r.Context(), claims.UserID)
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: project, ProfileID: crewProfileID}, Label: firstNonEmptyTrimmed(in.Agent, "Main"),
		Chat: &codeChat{OwnerID: claims.UserID, IsMain: in.FromTab == macMainTab, Key: key, ID: in.FromTab, Name: firstNonEmptyTrimmed(in.Agent, "Main"), SessionID: crewChatSessionID(project, in.ConversationID)}}
	if in.Operation == "read" {
		out, err := api.readAgentMessages(ctx, claims.UserID, caller, in.AgentID, in.After, in.Limit, 0)
		if err != nil {
			externalError(w, 404, "conversation_unavailable", "Conversation unavailable or access denied.")
			return
		}
		externalJSON(w, crewSyncReply{TeamMessages: out})
		return
	}
	if in.Operation != "" && in.Operation != "send" {
		externalError(w, 400, "invalid_operation", "Choose send or read.")
		return
	}
	var target triggerTarget
	member := ""
	if inbox == "" {
		var ok bool
		if target, member, ok = api.crewTeamMessageTarget(w, r, project, in.AgentID, key, claims); !ok {
			return
		}
	}
	out, err := api.sendAgentMessage(ctx, claims.UserID, caller, target, in.Message, inbox, in.SubmissionID)
	if err != nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_message_failed from=%s to_member=%s reason=%q", project, in.FromTab, logUsernameForUserID(member), err)
		externalError(w, 409, "message_failed", err.Error())
		return
	}
	log.Printf("[CREW_TEAM] project=%s event=agent_message from=%s to_member=%s chat=%q", project, in.FromTab, logUsernameForUserID(member), in.AgentID)
	externalJSON(w, out)
}

// crewTeamMessageTarget is the Team agent a Mac's new message goes to; a refusal is already written to w. fromKey is the
// sending agent's own conversation key: one of your agents may message your other In team agents (owner, 12 Nov: an
// agent and its online copy talk through Team messaging), never itself.
func (api *StreamingAPI) crewTeamMessageTarget(w http.ResponseWriter, r *http.Request, project, chatKey, fromKey string, claims *UserClaims) (triggerTarget, string, bool) {
	if !strings.HasPrefix(chatKey, crewTeamAgentIDPrefix) && !crewTeamShortMessageName(chatKey) {
		shape := "other"
		if strings.Contains(chatKey, codeChatSideMarker) {
			shape = "chat_key"
		}
		log.Printf("[CREW_TEAM] project=%s event=agent_address_stale id_shape=%s", project, shape)
		externalError(w, 409, "stale_team_address", "This Team address is out of date. Run messages action=list_agents and use the agent's new id.")
		return triggerTarget{}, "", false
	}
	agent, member, lookupErr := crewTeamMessageAgent(r.Context(), project, claims.UserID, chatKey)
	if lookupErr != nil {
		shape, id := "name", ""
		if strings.HasPrefix(chatKey, crewTeamAgentIDPrefix) {
			shape, id = "registry_id", chatKey
		}
		log.Printf("[CREW_TEAM] project=%s event=agent_not_in_team id_shape=%s agent_id=%q", project, shape, id)
		externalError(w, 409, "team_target_unresolved", lookupErr.Error())
		return triggerTarget{}, "", false
	}
	chatKey = agent.ID
	if member == claims.UserID && agent.ChatKey == fromKey {
		log.Printf("[CREW_TEAM] project=%s event=agent_message_refused reason=self agent_id=%q", project, chatKey)
		externalError(w, 409, "message_self", "An agent can't message itself. Pick another agent from list_agents.")
		return triggerTarget{}, "", false
	}
	ctx := internalBotRequestContext(r.Context(), claims.UserID)
	target, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:"+crewProfileID+":"+project+"/agent/"+chatKey)
	if err != nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_target_failed key=%q member=%s reason=%q", project, strings.TrimPrefix(chatKey, project+codeChatSideMarker), logUsernameForUserID(member), err)
		// Membership was admitted above; a target-resolution failure is not a Team removal.
		externalError(w, 409, "agent_target_failed", crewTeamTargetFailureMessage(err))
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

// sendTeamMessageFromMac is send_message on the Mac for a target "team:<local Crew>:<agent ID>" (list_agents
// names it): the message goes to the Crew's online copy through this Mac's pair.
func (api *StreamingAPI) sendTeamMessageFromMac(ctx context.Context, owner string, caller triggerLinkCaller, raw, message, submission string) (map[string]any, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(raw), "team:")
	project, chatKey, ok := strings.Cut(rest, ":")
	if !ok || project == "" || chatKey == "" {
		return nil, fmt.Errorf("use the team agent's id from list_agents")
	}
	reply, err := api.callTeamMessageFromMac(ctx, owner, caller, project, crewTeamMessageRequest{AgentID: chatKey, Message: message, SubmissionID: submission})
	if err != nil {
		return nil, err
	}
	return map[string]any{"sent": true, "to": chatKey, "inbox_id": reply.InboxID, "note": "Their reply comes back to this chat."}, nil
}

// Reuse this Crew's pair and the exact sender identity saved at send time.
// Neither an inbox prefix nor a named-agent identity is involved.
func (api *StreamingAPI) callTeamMessageFromMac(ctx context.Context, owner string, caller triggerLinkCaller, project string, input crewTeamMessageRequest) (crewSyncReply, error) {
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" || b.Mode != "relay" {
		log.Printf("[CREW_TEAM] project=%s event=team_message_refused reason=no_team_pairing", project)
		return crewSyncReply{}, fmt.Errorf("turn on the Team for this Crew first")
	}
	connection, err := desktopConnectionFor(owner, b.ConnectionID)
	if err != nil {
		return crewSyncReply{}, err
	}
	if caller.Chat == nil || caller.Stamp.ID != project {
		log.Printf("[CREW_TEAM] project=%s event=team_message_refused reason=other_crew from_crew=%s", project, caller.Stamp.ID)
		return crewSyncReply{}, fmt.Errorf("send to a Team member's agent from an agent of the same Crew")
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
	input.FromTab, input.ConversationID, input.Agent = tab, conversation, caller.Chat.Name
	reply, err := desktopCrewSyncCall(ctx, owner, &connection, b.PeerProjectID, crewSyncRequest{Action: "team_message", PairID: b.PairID, TeamMessage: &input})
	if err == nil && (input.Operation == "read" && reply.TeamMessages == nil || input.Operation != "read" && reply.InboxID == "") {
		err = fmt.Errorf("the server did not confirm this Team message; retry after updating the app")
	}
	if err == nil {
		inbox := reply.InboxID
		if input.Operation == "read" {
			inbox = input.AgentID
		}
		log.Printf("[CREW_TEAM] project=%s event=team_message_%s from=%s inbox=%s", project, firstNonEmptyTrimmed(input.Operation, "send"), tab, inbox)
	}
	return reply, err
}

func crewTeamMailboxLocalMiss(s agentMessageStore, inbox string, caller triggerLinkCaller) bool {
	if !IsLocalMode() || caller.Stamp.Type != triggerCallerCrew || caller.Chat == nil || !strings.HasPrefix(inbox, "inbox-") {
		return false
	}
	for _, c := range s.Conversations {
		if c.ID == inbox {
			return false
		}
	}
	for _, c := range s.Expired {
		if c.ID == inbox {
			return false
		}
	}
	b, err := crewroot.ReadSync(caller.Stamp.ID)
	return err == nil && b != nil && b.Side == "local" && b.Mode == "relay" && b.PeerProjectID != ""
}

// crewTeamAddressOnMac is this Mac's send_message target for a Team agent addressed the server's way
// (workspace:work:<online copy>/agent/<agent ID>): "team:<the Crew here paired with that copy>:<agent ID>", or "".
func crewTeamAddressOnMac(raw string) string {
	raw = strings.TrimSpace(raw)
	var project, chatKey string
	var ok bool
	if rest, team := strings.CutPrefix(raw, "team:"); team {
		project, chatKey, ok = strings.Cut(rest, ":")
	} else if rest, workspace := strings.CutPrefix(raw, "workspace:"+crewProfileID+":"); workspace {
		project, chatKey, ok = strings.Cut(rest, "/agent/")
	}
	if !ok || project == "" || chatKey == "" {
		return ""
	}
	if local := crewTeamMemberCopy(project); local != "" {
		return "team:" + local + ":" + chatKey
	}
	if local := crewLocalCopyOf(project); local != "" {
		return "team:" + local + ":" + chatKey
	}
	return ""
}

// teamMemberAgentRows lists, on the Mac, each Team member's own agents of the given Mac Crews, read from the
// Crew's online copy through the Mac's sign-in (the server decides what the owner may see). Their id is the
// send_message target "team:<Crew>:<agent ID>".
func teamMemberAgentRows(ctx context.Context, owner string, crews []map[string]interface{}) []map[string]interface{} {
	rows := []map[string]interface{}{}
	for _, crew := range crews {
		id, _ := crew["id"].(string)
		name, _ := crew["name"].(string)
		var team struct {
			People []crewTeamAgentsPerson `json:"people"`
		}
		if IsLocalMode() {
			b, err := crewroot.ReadSync(id)
			if err != nil || b == nil || b.Side != "local" || b.Mode != "relay" || b.PeerProjectID == "" {
				continue
			}
			c, err := desktopConnectionFor(owner, b.ConnectionID)
			if err != nil {
				continue
			}
			read, done := context.WithTimeout(ctx, 6*time.Second)
			err = crewTeamRemoteJSON(read, owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID)+"/agents", nil, &team)
			done()
			if err != nil {
				continue
			}
		} else {
			t, err := readCrewTeam(id)
			if err != nil || !crewTeamAgentsVisible(t, owner) {
				continue
			}
			team.People = teamAgentsByPerson(ctx, t, owner)
		}

		for _, p := range team.People {
			for _, agent := range p.Agents {
				// Your own agents on this Mac are its sibling chats; your own server agents (an agent's online copy, owner
				// 12 Nov) are reached through the Team like a teammate's.
				if !agent.InTeam || agent.ID == "" || p.You && agent.Place != "server" {
					continue
				}
				kind, title := "Team member's agent", p.Name+"'s "+firstNonEmptyTrimmed(agent.Name, "Main")
				if p.You {
					kind, title = "Your agent on the server", "Your "+firstNonEmptyTrimmed(agent.Name, "Main")
				}
				target := agent.Address
				if IsLocalMode() {
					target = "team:" + id + ":" + agent.ID
				}
				if target == "" {
					continue
				}
				rows = append(rows, map[string]interface{}{"kind": "team_agent", "type": kind, "id": target,
					"name": title, "owner": p.Name, "crew_name": name, "purpose": agent.Purpose,
					"presence": firstNonEmptyTrimmed(agent.PlaceLabel, agent.StatusLabel, "Runs when messaged"),
					"reach":    "messages action=send(target=<id>, message=…) from an agent of the same Crew; their reply comes back to that chat"})
			}
		}
	}
	return rows
}

// Only fixed, person-facing resolver errors cross the HTTP boundary. File and
// registry errors remain in the server log, not in another member's response.
func crewTeamTargetFailureMessage(err error) string {
	switch err.Error() {
	case "Choose a chat_id from list_agents for this Crew", "Choose this Team agent's chat_id from list_agents":
		return "Could not open that Team agent. Refresh the agent list and use its chat_id."
	case "This Crew's Team is not available to you", "That agent isn't in this Crew's Team now", "shared agent unavailable or access denied":
		return "That Team agent is no longer available to you. Refresh the agent list."
	case "Crew chat runtime unavailable":
		return "That Team agent's chat is temporarily unavailable. Try again shortly."
	default:
		return "Could not open that Team agent. Try again; if it still fails, ask the Crew owner to check the server log."
	}
}
