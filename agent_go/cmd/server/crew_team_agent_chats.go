package server

import (
	"context"
	"net/http"
)

// crewTeamAgentChat is the Team agent's one chat (one agent, one chat, #3469): its home Main, where everyone's
// messages and its own schedules and setup work go. Every current Team member reads it.
type crewTeamAgentChat struct {
	SessionID string `json:"session_id"`
}

// crewTeamAgentChatRecords is the Team agent's chat a viewer may read: only for a current member of a Team that is
// on. This is the one access rule for the reader.
func crewTeamAgentChatRecords(ctx context.Context, t crewTeam, viewer string) (crewTeam, []crewTeamAgentChat) {
	if !t.On || !t.member(viewer) || !crewTeamAccountActive(viewer) {
		return crewTeam{}, nil
	}
	home, ok := crewTeamAgentHome(t)
	if !ok {
		return crewTeam{}, nil
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(home.OwnerID))
	if err != nil {
		return crewTeam{}, nil
	}
	main := crewOwnerMainChatKey(t.OnlineHome)
	for _, record := range doc.Entries {
		if record.ProfileID == crewProfileID && record.SessionID != "" && record.ConversationKey == main {
			return home, []crewTeamAgentChat{{SessionID: record.SessionID}}
		}
	}
	return home, nil
}

// crewTeamAgentWatchesSession: a current Team member reads the Team agent's one chat in the normal chat screen
// (crewTeamAgentChatRecords). doc is the session owner's
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
