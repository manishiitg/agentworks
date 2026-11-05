package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team is a shared room (owner, 5 Nov, the decisions log): every member sees the agents each person put in the team,
// grouped by person, with whether that person's Mac is on. Agents stay their maker's; others only watch them and reach
// them from their own agents.

type crewTeamAgent struct {
	Tab     string `json:"tab"`
	Name    string `json:"name"`
	Purpose string `json:"purpose,omitempty"`
	// Place is where it runs: "mac" (its maker's Mac) or "server" (published).
	Place       string `json:"place"`
	ChatKey     string `json:"chat_key"`
	StatusLabel string `json:"status_label,omitempty"`
}

type crewTeamAgentsPerson struct {
	UserID string          `json:"user_id"`
	Name   string          `json:"name"`
	You    bool            `json:"you,omitempty"`
	Owner  bool            `json:"owner,omitempty"`
	MacOn  bool            `json:"mac_on"`
	Agents []crewTeamAgent `json:"agents"`
}

// crewTeamInTeam: an agent is in the team when its owner chose In team (audience "team").
func crewTeamInTeam(agent macSharedAgent) bool { return !agent.Off && agent.Audience == "team" }

// teamAgentsByPerson lists the team's in-team agents, the owner first: the owner's from this Crew's Mac, each member's
// from their own tabs in the server copy (they run on the server; Only me ones are left out for everyone else).
func teamAgentsByPerson(ctx context.Context, t crewTeam, caller string) []crewTeamAgentsPerson {
	owner := crewTeamAgentsPerson{UserID: t.OwnerID, Name: crewPersonShortName(t.OwnerID), Owner: true, You: t.OwnerID == caller, Agents: []crewTeamAgent{}}
	onMac := crewRelayTwin(t.ProjectID)
	// Website tabs use the Main conversation's physical key, while AgentBridge
	// addresses it by project. Return the actual tab identity to the browser.
	mainKey := t.ProjectID
	if records, err := defaultProductConversationRegistryStore().projectChats(ctx, t.OwnerID, crewProfileID, t.ProjectID); err == nil {
		for _, record := range records {
			if record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(t.ProjectID, t.OwnerID) {
				mainKey = record.ConversationKey
				break
			}
		}
	}
	if b, err := crewroot.ReadSync(t.ProjectID); err == nil && b != nil && b.Mode == "relay" && b.Side == "server" {
		owner.MacOn = crewMacSocketFor(t.OwnerID, t.ProjectID, b.PairID) != nil
	}
	for _, agent := range readMacSharedAgents(t.ProjectID) {
		if !crewTeamInTeam(agent) {
			continue
		}
		place := "server"
		if onMac && !crewPublishedTarget(t.ProjectID, agent.Tab) {
			place = "mac"
		}
		row := crewTeamAgent{Tab: agent.Tab, Name: agent.Name, Purpose: agent.Purpose, Place: place, ChatKey: macSharedAgentChatKey(t.ProjectID, agent.Tab)}
		if agent.Tab == macMainTab {
			row.ChatKey = mainKey
		}
		if place == "mac" && !owner.MacOn {
			row.StatusLabel = "Mac off"
		}
		owner.Agents = append(owner.Agents, row)
	}
	out := []crewTeamAgentsPerson{owner}
	for _, p := range t.People {
		if p.Invited || !crewTeamAccountActive(p.UserID) {
			continue
		}
		person := crewTeamAgentsPerson{UserID: p.UserID, Name: crewPersonShortName(p.UserID), You: p.UserID == caller, MacOn: time.Since(teamMacSeen(t.ProjectID, p.UserID)) <= teamMacSeenWithin}
		if pair, onMac := t.memberMac(p.UserID); onMac {
			// Their agents here work on their Mac (crew_team_where.go): listed from it, marked when it is off.
			person.MacOn = crewMacSocketFor(p.UserID, t.ProjectID, pair) != nil
			person.Agents = []crewTeamAgent{}
			for _, tab := range personMacTabs(ctx, p.UserID, t.ProjectID) {
				row := crewTeamAgent{Tab: tab.Tab, Name: tab.Name, Place: "mac", ChatKey: crewMacTabRow(t.ProjectID, tab).ConversationKey}
				if !person.MacOn {
					row.StatusLabel = "Mac off"
				}
				person.Agents = append(person.Agents, row)
			}
		} else {
			person.Agents = memberTeamAgents(ctx, t.ProjectID, p.UserID, caller)
		}
		out = append(out, person)
	}
	return out
}

// memberTeamAgents are one member's own tabs in this Crew's server copy.
func memberTeamAgents(ctx context.Context, project, member, caller string) []crewTeamAgent {
	out := []crewTeamAgent{}
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(ctx, member, crewProfileID, project)
	if err != nil {
		return out
	}
	identities, _ := store.agentIdentities(ctx, member, crewProfileID, project)
	for _, record := range records {
		identity := identities[record.ConversationKey]
		if identity.OnlyMe && member != caller || record.SessionID == "" || hiddenFromTabStrip(record.ConversationKey) {
			continue
		}
		_, tab, side := strings.Cut(record.ConversationKey, codeChatSideMarker)
		if !side {
			continue // the Crew's own Main record and Builder chats are not this member's agents
		}
		// Named like the left menu (sideChatsFor, owner 5 Nov): their default chat is their Main, a tab its own name;
		// never a raw chat title or an email.
		name := firstNonEmptyTrimmed(identity.Name, "Agent")
		if record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(project, member) || strings.HasPrefix(tab, "root-") {
			name = "Main"
		}
		out = append(out, crewTeamAgent{Tab: tab, Name: name, Purpose: identity.Purpose, Place: "server", ChatKey: record.ConversationKey})
	}
	return out
}

// handleCrewTeamAgents is the team's agents for one of its members (invited people and others get nothing).
func (api *StreamingAPI) handleCrewTeamAgents(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, false) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_forbidden", "This team is not shared with you. Ask its owner to add you.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"project_id": project, "name": t.Name, "people": teamAgentsByPerson(r.Context(), t, claims.UserID)})
}

// crewPersonShortName is how a person is named in a Team: their short name (tester-b), never their email.
func crewPersonShortName(userID string) string {
	return strings.SplitN(strings.TrimSpace(shareOwnerLabel(userID)), "@", 2)[0]
}
