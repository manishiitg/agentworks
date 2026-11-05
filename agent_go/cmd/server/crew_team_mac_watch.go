package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team member watches the owner's In team agents of a Crew that lives on the owner's Mac (owner, 5 Nov, "A Team is a
// shared room": every in-team agent and its chat, live, watch only). The member's list of this Crew's tabs adds those
// agents, and the relay streams only their conversations to the member, read-only. Agents the owner keeps to Only me
// are never listed or streamed.

type crewMacTeamTab struct {
	Tab            string // "main" or the Mac tab id
	ConversationID string // the Mac conversation, as the relay addresses it
	Name           string
}

type crewMacTeamTabsRead struct {
	at   time.Time
	tabs []crewMacTeamTab
}

// crewMacTeamTabsCache keeps the last read per Crew, so a member still sees the agents (and their history from the
// relay cache) while the Mac is off.
var crewMacTeamTabsCache sync.Map

// ownerMacTeamTabs are the owner's In team agents of this Mac Crew with their Mac conversations.
func (api *StreamingAPI) ownerMacTeamTabs(ctx context.Context, owner, project string) []crewMacTeamTab {
	inTeam := map[string]string{}
	for _, agent := range readMacSharedAgents(project) {
		if crewTeamInTeam(agent) {
			inTeam[agent.Tab] = firstNonEmptyTrimmed(agent.Name, agent.Tab)
		}
	}
	cached, _ := crewMacTeamTabsCache.Load(project)
	keep := func(tabs []crewMacTeamTab) []crewMacTeamTab {
		return slices.DeleteFunc(slices.Clone(tabs), func(t crewMacTeamTab) bool { _, ok := inTeam[t.Tab]; return !ok })
	}
	if v, ok := cached.(crewMacTeamTabsRead); ok && time.Since(v.at) < 15*time.Second {
		return keep(v.tabs)
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || crewMacSocketFor(owner, project, b.PairID) == nil {
		if v, ok := cached.(crewMacTeamTabsRead); ok {
			return keep(v.tabs)
		}
		return nil
	}
	tabs := []crewMacTeamTab{}
	result := callCrewMacFor(ctx, owner, project, crewChatCall{Operation: "tabs"}, 10*time.Second)
	var rows []AgentProfileSideChat
	if result.Status == http.StatusOK && json.Unmarshal(result.Body, &rows) == nil {
		for _, row := range rows {
			_, id, ok := strings.Cut(row.ConversationKey, codeChatSideMarker)
			if ok && crewMacTabID(id) && row.ConversationID != "" {
				tabs = append(tabs, crewMacTeamTab{Tab: id, ConversationID: row.ConversationID, Name: crewMacTabName(row.Name)})
			}
		}
	}
	if _, ok := inTeam[macMainTab]; ok {
		main := callCrewMacFor(ctx, owner, project, crewChatCall{Operation: "resolve"}, 10*time.Second)
		var out struct {
			ConversationID string `json:"conversation_id"`
		}
		if main.Status == http.StatusOK && json.Unmarshal(main.Body, &out) == nil && out.ConversationID != "" {
			tabs = append(tabs, crewMacTeamTab{Tab: macMainTab, ConversationID: out.ConversationID, Name: "Main"})
		}
	}
	crewMacTeamTabsCache.Store(project, crewMacTeamTabsRead{at: time.Now(), tabs: tabs})
	return keep(tabs)
}

// crewMacTeamConversation is the owner's In team agent this Mac conversation belongs to ("" when none), so a member
// may watch it.
func (api *StreamingAPI) crewMacTeamConversation(ctx context.Context, owner, project, conversation string) string {
	for _, t := range api.ownerMacTeamTabs(ctx, owner, project) {
		if t.ConversationID == conversation {
			return t.Tab
		}
	}
	return ""
}

// crewMacTeamMember is the caller as a joined member (not the owner) of this Mac Crew's Team, with its owner.
func crewMacTeamMember(project, caller string) (string, bool) {
	if IsLocalMode() || !crewRelayTwin(project) {
		return "", false
	}
	t, err := readCrewTeam(project)
	owner := ownerOfCrewProjectID(project)
	if err != nil || owner == "" || t.OwnerID != owner || caller == owner || !t.member(caller) {
		return "", false
	}
	return owner, true
}

// routeCrewMemberMacTabs answers a member's tab list of a Mac Crew's online copy: their own and other members' tabs as
// usual, without the owner's server records (the owner's agents live on the Mac), plus the owner's In team Mac agents.
func (api *StreamingAPI) routeCrewMemberMacTabs(w http.ResponseWriter, r *http.Request, project string) bool {
	caller := GetUserIDFromContext(r.Context())
	owner, ok := crewMacTeamMember(project, caller)
	if !ok {
		return false
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, project)
	if !ok {
		return true
	}
	rows, err := api.sideChatsFor(r, userID, slot, project)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return true
	}
	out := slices.DeleteFunc(rows, func(row AgentProfileSideChat) bool { return row.TabOwnerID == owner })
	name := crewOwnerDisplayName(owner)
	for _, tab := range api.ownerMacTeamTabs(r.Context(), owner, project) {
		key := project + codeChatSideMarker + tab.Tab
		if tab.Tab == macMainTab {
			key = project + codeChatSideMarker + "mac-main"
		}
		out = append(out, AgentProfileSideChat{ConversationKey: key, ConversationID: tab.ConversationID, SessionID: crewChatSessionID(project, tab.ConversationID),
			ChatLocation: "mac", Name: tab.Name, Pinned: true, TabOwnerID: owner, TabOwnerName: name})
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
	return true
}

// crewMacTeamWatchContext lets a member read (only read) the relay stream of the owner's In team Mac agent: the read
// runs as the owner, whose Mac holds the chat. Anything else, or any other conversation, keeps the caller's own context.
func (api *StreamingAPI) crewMacTeamWatchContext(r *http.Request, project, conversation string) (*http.Request, bool) {
	member := GetUserIDFromContext(r.Context())
	owner, ok := crewMacTeamMember(project, member)
	tab := ""
	if ok {
		tab = api.crewMacTeamConversation(r.Context(), owner, project, conversation)
	}
	if tab == "" {
		return r, false
	}
	log.Printf("[CREW_TEAM] project=%s event=member_watch user=%s tab=%s read=%s", project, logUsernameForUserID(member), tab, strings.TrimPrefix(r.URL.Path, "/api/sessions/"))
	return r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: owner, Username: owner})), true
}
