package server

import (
	"context"
	"net/http"
	"strings"

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
	// PlaceLabel is where an agent of the owner's Mac answers now and where its server copy stands (Always on,
	// crewAlwaysOnPlaceLabel); PlaceTone colours it ("ok", "wait", "server" or ""); PlaceServer is set when the
	// server answers for it.
	PlaceLabel  string `json:"place_label,omitempty"`
	PlaceTone   string `json:"place_tone,omitempty"`
	PlaceServer bool   `json:"place_server,omitempty"`
	// InTeam: everyone in the Team sees it (its maker chose In team); false only on the viewer's own Only me agents.
	InTeam bool `json:"in_team"`
	// Activity is "running" while a turn is going in it (crew_team_activity.go), "" otherwise.
	Activity string `json:"activity,omitempty"`
	// Address is how another agent messages it (send_message target), written here so no screen builds it.
	Address string `json:"address,omitempty"`
}

// crewTeamAgentAddress is the send_message target of one of the Team's agents in this Crew's online copy; on a Mac
// send_message maps it to the Crew paired with that copy (crew_team_message.go).
func crewTeamAgentAddress(project, chatKey string) string {
	if project == "" || chatKey == "" {
		return ""
	}
	return "workspace:" + crewProfileID + ":" + project + "/agent/" + chatKey
}

type crewTeamAgentsPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	You    bool   `json:"you,omitempty"`
	Owner  bool   `json:"owner,omitempty"`
	MacOn  bool   `json:"mac_on"`
	// Active is the people dot, ActiveLabel its words ("Active now on the web", "Mac on", "Last seen 2 h ago").
	Active      bool            `json:"active"`
	ActiveLabel string          `json:"active_label"`
	Agents      []crewTeamAgent `json:"agents"`
}

// crewTeamInTeam: the Mac Crew's agent is on its online copy's list for the Team (the owner's Mac lists every agent
// while the Team is on). Whether it is In team is the Team record's answer (crewTeam.agentInTeam).
func crewTeamInTeam(agent macSharedAgent) bool { return !agent.Off && agent.Audience == "team" }

// teamAgentsByPerson lists the team's in-team agents, the owner first: the owner's from this Crew's Mac, each member's
// from their own tabs in the server copy (they run on the server; Only me ones are left out for everyone else).
func teamAgentsByPerson(ctx context.Context, t crewTeam, caller string) []crewTeamAgentsPerson {
	owner := crewTeamAgentsPerson{UserID: t.OwnerID, Name: crewPersonShortName(t.OwnerID), Owner: true, You: t.OwnerID == caller, Agents: []crewTeamAgent{}}
	onMac := crewRelayTwin(t.ProjectID)
	// Website tabs use the Main conversation's physical key, while AgentBridge
	// addresses it by project. Return the actual tab identity to the browser.
	mainKey := crewTeamOwnerMainKey(ctx, t)
	if b, err := crewroot.ReadSync(t.ProjectID); err == nil && b != nil && b.Mode == "relay" && b.Side == "server" {
		owner.MacOn = crewMacSocketFor(t.OwnerID, t.ProjectID, b.PairID) != nil
	}
	if !onMac {
		// A Crew that lives on the server (owner, 5 Nov): its owner's agents are server tabs, In team unless Only me.
		owner.Agents = memberTeamAgents(ctx, t, t.OwnerID, caller)
	}
	// The Mac conversations of the owner's agents, by tab, for what is running in them.
	ownerConversations := map[string]string{}
	if onMac {
		for _, tab := range crewMacTabsFor(ctx, t.OwnerID, t.ProjectID, caller) {
			ownerConversations[tab.Tab] = tab.ConversationID
		}
	}
	for _, agent := range readMacSharedAgents(t.ProjectID) {
		if !onMac || !crewTeamInTeam(agent) || !t.seesAgent(caller, t.OwnerID, agent.Tab) {
			continue
		}
		place := "server"
		if onMac && !crewPublishedTarget(t.ProjectID, agent.Tab) {
			place = "mac"
		}
		// Named like the left menu: its own name, Main for the Crew's Main, never an email.
		name := firstNonEmptyTrimmed(agent.Name, "Agent")
		if agent.Tab == macMainTab {
			name = firstNonEmptyTrimmed(agent.Name, "Main")
		}
		row := crewTeamAgent{Tab: agent.Tab, Name: name, Purpose: agent.Purpose, Place: place, ChatKey: macSharedAgentChatKey(t.ProjectID, agent.Tab), InTeam: t.agentInTeam(t.OwnerID, agent.Tab)}
		if agent.Tab == macMainTab {
			row.ChatKey = mainKey
		}
		if place == "mac" {
			// Always on: an In team agent answers from the server while the Mac is off; an Only me one waits for it.
			row.PlaceLabel, row.PlaceTone = crewAlwaysOnPlaceLabel(t.ProjectID, t.OwnerID, agent.Tab, owner.MacOn, caller == t.OwnerID, row.InTeam)
			row.PlaceServer = row.PlaceTone == "server"
			if !owner.MacOn && !row.PlaceServer {
				row.StatusLabel = "Mac off"
			}
		}
		if place == "mac" {
			if owner.MacOn && crewMacConversationRunning(t.OwnerID, t.ProjectID, ownerConversations[agent.Tab]) || crewStandbyServedRunning(t.OwnerID, t.ProjectID, agent.Tab) {
				row.Activity = "running"
			}
		}
		owner.Agents = append(owner.Agents, row)
	}
	owner.Active, owner.ActiveLabel = teamActive(owner.UserID, owner.MacOn || (onMac && IsLocalMode()))
	out := []crewTeamAgentsPerson{owner}
	for _, p := range t.People {
		if p.Invited || !crewTeamAccountActive(p.UserID) {
			continue
		}
		// Mac on only from their Mac's own connection: the server copy's sync is not the person being there.
		person := crewTeamAgentsPerson{UserID: p.UserID, Name: crewPersonShortName(p.UserID), You: p.UserID == caller}
		if pair, onMac := t.memberMac(p.UserID); onMac {
			// Their agents here work on their Mac (crew_team_where.go): listed from it, marked when it is off.
			person.MacOn = crewMacSocketFor(p.UserID, t.ProjectID, pair) != nil
			person.Agents = []crewTeamAgent{}
			for _, tab := range crewMacTabsFor(ctx, p.UserID, t.ProjectID, caller) {
				name := firstNonEmptyTrimmed(tab.Name, "Agent")
				if tab.Tab == macMainTab {
					name = "Main"
				}
				row := crewTeamAgent{Tab: tab.Tab, Name: name, Place: "mac", ChatKey: crewMacTabRow(t.ProjectID, tab).ConversationKey, InTeam: !tab.OnlyMe}
				if !person.MacOn {
					row.StatusLabel = "Mac off"
				} else if crewMacConversationRunning(p.UserID, t.ProjectID, tab.ConversationID) {
					row.Activity = "running"
				}
				person.Agents = append(person.Agents, row)
			}
		} else {
			person.Agents = memberTeamAgents(ctx, t, p.UserID, caller)
		}
		person.Active, person.ActiveLabel = teamActive(p.UserID, person.MacOn)
		out = append(out, person)
	}
	for i := range out {
		for j := range out[i].Agents {
			out[i].Agents[j].Address = crewTeamAgentAddress(t.ProjectID, out[i].Agents[j].ChatKey)
		}
	}
	return out
}

// memberTeamAgents are one person's own tabs in this Crew's server copy that the caller may see.
func memberTeamAgents(ctx context.Context, t crewTeam, member, caller string) []crewTeamAgent {
	project := t.ProjectID
	out := []crewTeamAgent{}
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(ctx, member, crewProfileID, project)
	if err != nil {
		return out
	}
	identities, _ := store.agentIdentities(ctx, member, crewProfileID, project)
	for _, record := range records {
		identity := identities[record.ConversationKey]
		if !t.seesAgent(caller, member, record.ConversationKey) || record.SessionID == "" || hiddenFromTabStrip(record.ConversationKey) {
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
		activity := ""
		if crewServerChatRunning(record.SessionID) {
			activity = "running"
		}
		out = append(out, crewTeamAgent{Tab: tab, Name: name, Purpose: identity.Purpose, Place: "server", ChatKey: record.ConversationKey, InTeam: t.agentInTeam(member, record.ConversationKey), Activity: activity})
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
