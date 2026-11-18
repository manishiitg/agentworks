package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// The Team agent's chat with one person, opened in the normal chat screen (#3084): "My chat". Each current member has
// one chat with the Team agent in the Team's online home, the same per-person thread an AgentBridge ask already lands
// in (routePublishedAgentMessage), so there is one chat per person whichever way they ask. Only that person writes in
// it; every current member reads it (crewTeamAgentChatRecords, crewTeamAgentWatchesSession).

// crewTeamAgentThreadKey is a person's chat key with the Team agent: sharedThreadKey under the home's Main, exactly as
// the AgentBridge route builds it.
func crewTeamAgentThreadKey(home, person string) string {
	main := crewOwnerMainChatKey(home)
	if main == "" || person == "" {
		return ""
	}
	return sharedThreadKey(home, strings.TrimPrefix(main, home+codeChatSideMarker), person)
}

// crewTeamAgentMyChat resolves (creating on first use) the viewer's own chat with the Team agent. Only a current
// member of a Team that is on, with a working Team agent home, gets one.
func (api *StreamingAPI) crewTeamAgentMyChat(ctx context.Context, t crewTeam, viewer string) (crewTeam, ProductConversationRecord, ProductConversationRecord, error) {
	if !t.On || !t.member(viewer) || !crewTeamAccountActive(viewer) {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, fmt.Errorf("Join this Team to talk to its Team agent.")
	}
	home, ok := crewTeamAgentHome(t)
	if !ok {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, fmt.Errorf("The Team agent isn't set up yet. Try again in a minute.")
	}
	key := crewTeamAgentThreadKey(t.OnlineHome, viewer)
	if key == "" {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, fmt.Errorf("The Team agent isn't set up yet. Try again in a minute.")
	}
	owner := home.OwnerID
	ctx = internalBotRequestContext(context.WithValue(ctx, UserContextKey, (*UserClaims)(nil)), owner)
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if err != nil {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, err
	}
	store := defaultProductConversationRegistryStore()
	mainBinding, err := resolveProductProjectBinding(ctx, owner, profile, crewOwnerMainChatKey(t.OnlineHome))
	if err != nil {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, err
	}
	main, err := store.resolveOrCreate(ctx, owner, profile, mainBinding, "")
	if err != nil {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, err
	}
	binding, err := resolveProductProjectBinding(ctx, owner, profile, key)
	if err != nil {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, err
	}
	thread, err := store.resolveOrCreate(ctx, owner, profile, binding, "")
	if err != nil {
		return crewTeam{}, ProductConversationRecord{}, ProductConversationRecord{}, err
	}
	// The person's chat runs on the provider and model the Team owner chose for the Team agent (#3059).
	if identity, err := store.agentIdentity(ctx, owner, crewProfileID, main.ConversationKey); err == nil {
		syncCrewTeamAgentThreadModel(ctx, owner, thread.ConversationKey, identity)
	}
	return home, main, thread, nil
}

// handleCrewTeamAgentMyChat: GET /crew-teams/{project}/team-agent/my-chat returns the viewer's own chat with the Team
// agent (its session, to open in the chat screen); POST with {"message"} runs what they typed as a turn there.
func (api *StreamingAPI) handleCrewTeamAgentMyChat(w http.ResponseWriter, r *http.Request) {
	write := r.Method == http.MethodPost
	t, claims, ok := crewTeamForTab(w, r, write)
	if !ok {
		return
	}
	home, main, thread, err := api.crewTeamAgentMyChat(r.Context(), t, claims.UserID)
	if err != nil {
		log.Printf("[TEAM_AGENT_CHAT] team=%s person=%s event=%s outcome=refused reason=%q", t.ProjectID, claims.UserID, strings.ToLower(r.Method), err)
		externalError(w, http.StatusConflict, "team_agent_unavailable", err.Error())
		return
	}
	if !write {
		log.Printf("[TEAM_AGENT_CHAT] team=%s home=%s person=%s event=open session=%s", t.ProjectID, t.OnlineHome, claims.UserID, thread.SessionID)
		externalJSON(w, map[string]any{"session_id": thread.SessionID, "conversation_key": thread.ConversationKey, "workspace": thread.WorkspacePath, "note": "Your Team can see this chat."})
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		externalError(w, http.StatusBadRequest, "message_required", "Type a message for the Team agent.")
		return
	}
	if err := api.runCrewTeamAgentMyChat(r.Context(), t, home, main, thread, claims.UserID, body.Message); err != nil {
		log.Printf("[TEAM_AGENT_CHAT] team=%s home=%s person=%s event=send session=%s outcome=failed reason=%q", t.ProjectID, t.OnlineHome, claims.UserID, thread.SessionID, err)
		externalError(w, http.StatusConflict, "team_agent_busy", "The Team agent couldn't take that message. Try again.")
		return
	}
	log.Printf("[TEAM_AGENT_CHAT] team=%s home=%s person=%s event=send session=%s outcome=started", t.ProjectID, t.OnlineHome, claims.UserID, thread.SessionID)
	externalJSON(w, map[string]any{"session_id": thread.SessionID})
}

// runCrewTeamAgentMyChat runs the person's message in their own chat with the Team agent, as that person: the same
// turn settings as an AgentBridge ask to the Team agent (crew_published_agent_threads.go), without the mailbox. Team
// members are the home's people (derived roster), so admission is the home's normal binding check.
func (api *StreamingAPI) runCrewTeamAgentMyChat(ctx context.Context, t, home crewTeam, main, thread ProductConversationRecord, person, message string) error {
	owner := home.OwnerID
	ctx = internalBotRequestContext(context.WithValue(ctx, UserContextKey, (*UserClaims)(nil)), owner)
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if err != nil {
		return err
	}
	if _, err := resolveCrewProjectBinding(ctx, person, profile, t.OnlineHome, ""); err != nil {
		return err
	}
	name := crewTeamPersonLabels(t)[person]
	ctx = context.WithValue(ctx, crewMacSharedAskKey{}, &crewMacSharedAsk{Sender: person, SenderName: name, Owner: main})
	text := "[" + name + ", a member of the Team \"" + t.Name + "\", is writing in their own chat with you.]\n\n" + message
	query, err := prepareProductConversationTurn(ctx, owner, profile, AgentProfileChatRequest{Message: text}, thread)
	if err != nil {
		return err
	}
	req, err := queryRequestToMap(query)
	if err != nil {
		return err
	}
	req["pin_run_mode"] = false
	req["triggered_by"] = "team_agent_chat"
	req["triggered_by_label"] = "From " + name
	req["asked_by"] = person
	req["delivery_mode"] = "queue"
	ctx = context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "team_agent_chat", "sender_label": name, "sender_user_id": person, "display_content": message})
	_, err = api.startSessionInternalWithResult(ctx, req, thread.SessionID, owner, nil)
	return err
}

// crewTeamAgentWatchesSession: a current Team member reads any of the Team agent's chats (its own and each person's)
// in the normal chat screen, the same chats the list shows (crewTeamAgentChatRecords). doc is the session owner's
// registry; only a Team agent home's records qualify.
func crewTeamAgentWatchesSession(r *http.Request, doc productConversationRegistryDocument, viewer, session string) bool {
	for _, record := range doc.Entries {
		if record.SessionID != session || record.ProfileID != crewProfileID {
			continue
		}
		homeTeam, err := readCrewTeam(crewResourceProjectID(record.ConversationKey))
		if err != nil || homeTeam.RosterSource == "" {
			return false
		}
		source, err := readCrewTeam(homeTeam.RosterSource)
		if err != nil {
			return false
		}
		if token := GetUserFromContext(r.Context()).AccessToken; token != nil && (!token.AllowsCrew(source.ProjectID) || !token.Allows("crews:read")) {
			return false
		}
		_, rows := crewTeamAgentChatRecords(r.Context(), source, viewer)
		for _, row := range rows {
			if row.SessionID == session {
				return true
			}
		}
		return false
	}
	return false
}
