package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// PLAT-746-772: network addresses retain the originating Crew. Never resolve
// a remote agent name against the local mailbox or choose a saved login by name.
type crewNetworkMessage struct {
	FromTab        string `json:"from_tab"`
	ConversationID string `json:"conversation_id"`
	Target         string `json:"target,omitempty"`
	Inbox          string `json:"inbox,omitempty"`
	Message        string `json:"message,omitempty"`
	SubmissionID   string `json:"submission_id,omitempty"`
	Read           bool   `json:"read,omitempty"`
	After          int    `json:"after,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}
type crewNetworkOrigin struct{ Pair, Conversation, Tab string }
type crewNetworkOriginKey struct{}

func splitCrewNetworkAddress(raw string) (project, address string, ok bool) {
	rest, ok := strings.CutPrefix(raw, "network:")
	if !ok {
		return "", "", false
	}
	project, address, ok = strings.Cut(rest, ":")
	return project, address, ok && project != "" && (strings.HasPrefix(address, "agent:") || strings.HasPrefix(address, "inbox-"))
}

// Keep every callable address on the same verified network route. Conversation
// IDs and message text remain server data, not local mailbox addresses.
func qualifyCrewNetworkReplyAddresses(out map[string]any, project string) {
	qualify := func(row map[string]any) {
		for _, key := range []string{"inbox_id", "reply_address"} {
			if address, _ := row[key].(string); strings.HasPrefix(address, "inbox-") {
				row[key] = "network:" + project + ":" + address
			}
		}
	}
	qualify(out)
	// The response has just been decoded from the selected network's JSON.
	messages, _ := out["messages"].([]any)
	for _, item := range messages {
		if row, ok := item.(map[string]any); ok {
			qualify(row)
		}
	}
}

// The saved pair authenticates the owner; the Mac's actual tab list authenticates
// the sender. Normal named-agent and inbox participant checks still decide access.
func (api *StreamingAPI) crewNetworkMessageOnServer(w http.ResponseWriter, r *http.Request, project string, input crewSyncRequest) {
	claims := GetUserFromContext(r.Context())
	in := input.NetworkMessage
	if claims == nil || in == nil || ownerOfCrewProjectID(project) != claims.UserID || !crewTeamMessagePairAdmitted(project, claims.UserID, input.PairID) {
		externalError(w, 403, "wrong_pair", "Send from the Mac that holds this Crew.")
		return
	}
	found := false
	name := ""
	for _, tab := range ownerMacTabsAll(r.Context(), project) {
		if tab.Tab == in.FromTab && tab.ConversationID == in.ConversationID && in.ConversationID != "" {
			found = true
			name = tab.Name
			break
		}
	}
	if !found {
		externalError(w, 409, "sender_changed", "This agent's conversation has changed; refresh and try again.")
		return
	}
	ctx := internalBotRequestContext(r.Context(), claims.UserID)
	profile, e := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if e != nil {
		externalError(w, 409, "crew_unavailable", "This Crew is unavailable.")
		return
	}
	binding, e := resolveCrewProjectBinding(ctx, claims.UserID, profile, project, "")
	if e != nil || binding.OwnerID != claims.UserID {
		externalError(w, 403, "crew_unavailable", "This Crew is unavailable.")
		return
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: project, ProfileID: crewProfileID}, Label: name, Path: binding.Binding.WorkspacePath,
		Chat: &codeChat{OwnerID: claims.UserID, IsMain: in.FromTab == macMainTab, Key: macSharedAgentChatKey(project, in.FromTab), ID: in.FromTab, Name: name, SessionID: crewChatSessionID(project, in.ConversationID)}}
	var out map[string]any
	var err error
	if in.Read {
		out, err = api.readAgentMessages(ctx, claims.UserID, caller, in.Inbox, in.After, in.Limit, 0)
	} else {
		var target triggerTarget
		if in.Inbox == "" {
			if !strings.HasPrefix(in.Target, "agent:") {
				externalError(w, 400, "invalid_target", "Choose a network agent from the directory.")
				return
			}
			target, err = resolveNamedMessageAgent(ctx, claims.UserID, strings.TrimPrefix(in.Target, "agent:"), "")
		}
		if err == nil {
			ctx = context.WithValue(ctx, crewNetworkOriginKey{}, crewNetworkOrigin{Pair: input.PairID, Conversation: in.ConversationID, Tab: in.FromTab})
			out, err = api.sendAgentMessage(ctx, claims.UserID, caller, target, in.Message, in.Inbox, in.SubmissionID)
		}
	}
	if err != nil {
		log.Printf("[CREW_NETWORK] event=message_failed project=%s read=%t", project, in.Read)
		externalError(w, 409, "network_message_failed", err.Error())
		return
	}
	log.Printf("[CREW_NETWORK] event=message project=%s read=%t", project, in.Read)
	externalJSON(w, crewSyncReply{NetworkMessage: out})
}

func (api *StreamingAPI) deliverCrewNetworkReply(ctx context.Context, c agentConversation, m agentMessage, source, dest agentMessageEndpoint) error {
	if !crewTeamMessagePairAdmitted(dest.ID, dest.UserID, dest.NetworkPairID) {
		return fmt.Errorf("the sending Mac is no longer paired with this Crew")
	}
	tab := dest.NetworkTab
	// Set only here, after pair admission, from endpoint identities: a reply from the person's own agent is theirs
	// (PLAT-746-852); anyone else's keeps the Team rule.
	own := crewOwnAgentMessage(dest, source)
	args, _ := json.Marshal(withAgentRuleArgs(ctx, map[string]any{"sender_id": source.UserID, "sender_agent": source.Label, "sender_channel": "AgentBridge", "network_inbox": c.ID, "own_agent_message": own}, dest.ID, dest.UserID, macSharedAgentChatKey(dest.ID, tab)))
	log.Printf("[CREW_NETWORK] event=reply_policy project=%s message=%s own_agent=%t source_mode=%q dest_mode=%q", dest.ID, m.ID, own, source.Mode, dest.Mode)
	result := callCrewMacFor(ctx, dest.UserID, dest.ID, crewChatCall{Operation: "network_reply", Tab: tab, ConversationID: dest.NetworkConversationID, Message: m.Text, Caller: source.Label, SubmissionID: m.ID, Args: args}, 30*time.Second)
	if result.Status != http.StatusOK {
		return fmt.Errorf("the originating Mac chat could not receive this reply")
	}
	return nil
}
