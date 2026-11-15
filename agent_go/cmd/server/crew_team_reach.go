package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewTeamReachableAgent is one agent the viewer may discover and message as a Team member (PLAT-746-1034, owner
// 15 Nov: In team is an AgentBridge audience, the owner plus the Team's current members). The same rows feed bridge
// discovery, message admission and the right panel's Agents list, so they cannot disagree.
type crewTeamReachableAgent struct {
	Team     string `json:"team_project_id"`
	TeamName string `json:"team_name"`
	ID       string `json:"agent_id"`
	Kind     string `json:"kind"` // "agent" or "team_agent" (the Team's online home Main)
	Name     string `json:"name"`
	PersonID string `json:"person_id,omitempty"`
	Person   string `json:"person,omitempty"`
	Place    string `json:"place,omitempty"` // "mac" or "server"
	Online   bool   `json:"online"`
	Activity string `json:"activity,omitempty"` // "running" or "waiting", as the server reads it
	// ChatShare is the agent's own "what teammates see" choice (agent = answers only, live = its chat); the panel's
	// live line is gated on it server-side.
	ChatShare string `json:"chat_share,omitempty"`
	Address   string `json:"address"`
}

// crewTeamReachableAgents is the one answer to "which agents can this person reach in their Teams": for every Team
// that is on, whose owner is current, and where the viewer is the owner or a joined member (never only invited), the
// other people's registered agents that are In team, plus the Team agent. Only-me agents are never included. Every
// discovery and every send rechecks this at that moment, so leaving a Team ends access at once.
func crewTeamReachableAgents(ctx context.Context, viewer string) []crewTeamReachableAgent {
	out := []crewTeamReachableAgent{}
	if viewer == "" || !crewTeamAccountActive(viewer) {
		return out
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return out
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, err := readCrewTeam(entry.Name())
		if err != nil || !crewTeamReachOpen(t, viewer) {
			continue
		}
		out = append(out, crewTeamReachableIn(ctx, t, viewer)...)
	}
	return out
}

// crewTeamReachOpen: the Team is on, its owner current and active, and the viewer is in it (owner or joined member).
func crewTeamReachOpen(t crewTeam, viewer string) bool {
	return t.On && t.RosterSource == "" && t.member(viewer) && t.OwnerID != "" && ownerOfCrewProjectID(t.ProjectID) == t.OwnerID && crewTeamAccountActive(t.OwnerID)
}

// crewTeamReachRow is the one per-row admission check (nonrecursive): the Team is open to the viewer, and the row is
// someone else's, In team (for this viewer: the whole Team or chosen people including them), owned by a current active
// member. Discovery and every send use it. A terminal row's
// transport check is applied separately: a changed binding refuses, while a valid disconnected stream is offline.
func crewTeamReachRow(t crewTeam, viewer string, row crewTeamRegisteredAgent) bool {
	return crewTeamReachOpen(t, viewer) && row.InTeam && row.Rule.chosen(viewer) && row.OwnerID != "" && row.OwnerID != viewer && t.member(row.OwnerID) && crewTeamAccountActive(row.OwnerID)
}

// crewTeamReachableIn is one Team's reachable agents for the viewer.
func crewTeamReachableIn(ctx context.Context, t crewTeam, viewer string) []crewTeamReachableAgent {
	out := []crewTeamReachableAgent{}
	if !crewTeamReachOpen(t, viewer) || !crewTeamAccountActive(viewer) {
		return out // every caller gets the gate, not only crewTeamReachableAgents
	}
	for _, p := range teamAgentsByPersonWithMain(ctx, t, viewer, crewOwnerMainChatKey(t.ProjectID)) {
		if p.UserID == viewer || !t.member(p.UserID) || !crewTeamAccountActive(p.UserID) {
			continue
		}
		for _, a := range p.Agents {
			registered, ok := t.Agents[a.ID]
			if a.ID == "" || !ok || registered.OwnerID != p.UserID || !crewTeamReachRow(t, viewer, registered) {
				continue
			}
			online, address := !crewTeamAgentOnMac(t, p.UserID) || p.MacOn || a.PlaceServer || crewPublishedTarget(t.ProjectID, a.Tab), crewTeamAgentBridgeAddress(registered.OwnerID, a.ID)
			if registered.Kind == "terminal" {
				named, err := currentCrewTeamTerminal(ctx, t, registered)
				if err != nil {
					continue
				} // rebound is refused, never ordinary offline
				online = namedAgentOnline(named)
				address = "agent:" + shareOwnerLabel(registered.OwnerID) + "/" + named.Name
			}
			out = append(out, crewTeamReachableAgent{Team: t.ProjectID, TeamName: t.Name, ID: a.ID, Kind: "agent", Name: firstNonEmptyTrimmed(a.Name, "Agent"), PersonID: p.UserID, Person: p.Name, Place: a.Place,
				Online: online, Activity: a.Activity, ChatShare: registered.Rule.ChatShare, Address: address})
		}
	}
	if t.OnlineHome != "" {
		owner, key := "team-"+t.ProjectID, crewOwnerMainChatKey(t.OnlineHome)
		if home, err := readCrewTeam(t.OnlineHome); err == nil {
			// Use the ordinary registry projection, also before anybody opens the
			// new home's tab. Provisioning creates Main, not its Team registry row.
			teamAgentsByPersonWithMain(ctx, home, viewer, key)
			home, err = readCrewTeam(t.OnlineHome)
			if err != nil {
				return out
			}
			if a, ok := home.registeredAgent(owner, key); ok && a.InTeam {
				out = append(out, crewTeamReachableAgent{Team: t.ProjectID, TeamName: t.Name, ID: a.ID, Kind: "team_agent", Name: "Team agent", Place: "server", Online: true, Address: crewTeamAgentBridgeAddress(a.OwnerID, a.ID)})
			}
		}
	}
	return out
}

// GET /api/crew-team-reach: the signed-in person's reachable Team agents (draft read endpoint for the panel and the
// bridge directory; PLAT-746-1034).
func (api *StreamingAPI) handleCrewTeamReach(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.Scope != "" {
		externalError(w, 401, "unauthenticated", "Sign in to see your Team's agents.")
		return
	}
	agents := crewTeamReachableAgents(r.Context(), claims.UserID)
	if previous, ok := crewTeamReachLogged.Swap(claims.UserID, len(agents)); !ok || previous != len(agents) {
		log.Printf("[TEAM_AGENT_REACH] caller=%s event=discover via=read team_agents=%d", claims.UserID, len(agents))
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"agents": agents})
}

var crewTeamReachLogged sync.Map
