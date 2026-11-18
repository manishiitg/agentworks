package server

import (
	"context"
	"net/http"
	"strings"
)

// crewTeamAgentChat is one chat of the Team agent, as every current Team member sees it (#3059): the Team agent is a
// teammate, so its chats are the Team's. Each person who asks it gets their own chat in the Team's online home
// (sharedThreadKey); only that person writes in it (through the usual Ask) and the others read it. The home Main is
// the Team agent's own chat (its schedules, Watchers and setup work); it has no person.
type crewTeamAgentChat struct {
	SessionID string `json:"session_id"`
	PersonID  string `json:"person_id,omitempty"` // empty for the Team agent's own chat
	Person    string `json:"person"`
	You       bool   `json:"you,omitempty"`
	Activity  string `json:"activity"` // working or idle
	crewTeamMessagePreview
	workspace string
}

// crewTeamAgentChatAskers maps each per-person chat key of the home's Team agent to the person who asked in it. The
// asker is only recorded on the AgentBridge conversation that delivered the question, never in the chat key.
func crewTeamAgentChatAskers(ctx context.Context, home string) map[string]string {
	out := map[string]string{}
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		return out
	}
	main := crewOwnerMainChatKey(home)
	for _, c := range s.Conversations {
		for _, e := range c.Endpoints {
			if e.ID == home && e.OfflineCopyTeam == "" && e.PublishedAgent == main && e.PublishedAsker != "" && isSharedThreadKey(e.ChatKey) {
				out[e.ChatKey] = e.PublishedAsker
			}
		}
	}
	return out
}

// crewTeamAgentChatRecords are the Team agent's chats a viewer may read: only for a current member of a Team that is
// on, its own chat and those of people still on the Team. This is the one access rule for the list and the reader.
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
	askers := crewTeamAgentChatAskers(ctx, t.OnlineHome)
	// A chat opened from the chat screen ("My chat") has no AgentBridge conversation: its key names its person.
	people := []string{t.OwnerID}
	for _, p := range t.People {
		if !p.Invited {
			people = append(people, p.UserID)
		}
	}
	for _, person := range people {
		if key := crewTeamAgentThreadKey(t.OnlineHome, person); key != "" {
			askers[key] = person
		}
	}
	out := []crewTeamAgentChat{}
	for _, record := range doc.Entries {
		if record.ProfileID != crewProfileID || record.SessionID == "" || crewResourceProjectID(record.ConversationKey) != t.OnlineHome {
			continue
		}
		row := crewTeamAgentChat{SessionID: record.SessionID, Person: "Team schedules and setup", workspace: record.WorkspacePath}
		if record.ConversationKey != main {
			who := askers[record.ConversationKey]
			if !isSharedThreadKey(record.ConversationKey) || !t.member(who) {
				continue
			}
			row.PersonID, row.Person, row.You = who, crewPersonShortName(who), who == viewer
		}
		out = append(out, row)
	}
	return home, out
}

// crewTeamAgentThreadKey is a person's chat key with the Team agent: sharedThreadKey under the home's Main, exactly as
// the AgentBridge route builds it.
func crewTeamAgentThreadKey(home, person string) string {
	main := crewOwnerMainChatKey(home)
	if main == "" || person == "" {
		return ""
	}
	return sharedThreadKey(home, strings.TrimPrefix(main, home+codeChatSideMarker), person)
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
