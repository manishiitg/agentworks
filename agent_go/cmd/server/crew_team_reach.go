package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewTeamReachableAgent is one agent the viewer may discover and message as a Team member (PLAT-746-1034, owner
// 15 Nov: In team is an AgentBridge audience, the owner plus the Team's current members). The same rows feed bridge
// discovery, message admission and the right panel's Agents list, so they cannot disagree.
type crewTeamReachableAgent struct {
	Purpose  string   `json:"purpose,omitempty"`
	Examples []string `json:"examples,omitempty"`
	userAvatarFields
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
	// Audience is set on the Team agent row only: only_me (its owner and their own agents), chosen or whole_team.
	Audience string `json:"audience,omitempty"`
	Address  string `json:"address"`
	// Availability is public only after source admission. Execution bindings
	// are server-only capabilities; discovery never exposes copy identities.
	TaskState           string `json:"task_state,omitempty"`
	OfflineCopyReady    bool   `json:"offline_copy_ready,omitempty"`
	OfflineCopyID       string `json:"-"`
	OfflineCopyRevision uint64 `json:"-"`
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
		if err != nil || !crewTeamReachOpen(t, viewer) && !(t.OnlineHome != "" && crewTeamOwnerReach(t, viewer)) {
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

// crewTeamOwnerReach: the viewer is the current, active owner of this real Team record (not a roster copy). The
// owner always reaches their own online copy, whether or not the Team is on.
// The viewer is always the server-derived caller (publishedAsker of the sending endpoint): the person's own agents
// carry that person's ID, while another person's agent, or a guest asking through a published agent, does not.
func crewTeamOwnerReach(t crewTeam, viewer string) bool {
	return viewer != "" && viewer == t.OwnerID && t.RosterSource == "" && ownerOfCrewProjectID(t.ProjectID) == t.OwnerID && crewTeamAccountActive(t.OwnerID)
}

// crewTeamRowAudience is who a registered agent row answers besides its own person (owner, 15 Nov: "only me, chosen
// people or the whole team"): only_me while the Team is off or the row is not In team, otherwise the row's
// whole_team or chosen choice. Only me is In team off, not a third stored value.
func crewTeamRowAudience(t crewTeam, a crewTeamRegisteredAgent) string {
	if !t.On || !a.InTeam {
		return "only_me"
	}
	return a.Rule.audienceName()
}

// crewTeamOnlineCopyAdmits: the owner always; anyone else only while the Team is open to them and the row's audience
// includes them. This is the one audience check for the online copy; resolvers and routing call reach, never a copy.
func crewTeamOnlineCopyAdmits(t crewTeam, viewer string, a crewTeamRegisteredAgent) bool {
	if crewTeamOwnerReach(t, viewer) {
		return true
	}
	return crewTeamReachOpen(t, viewer) && crewTeamRowAudience(t, a) != "only_me" && a.Rule.chosen(viewer)
}

// crewTeamSourceRowAdmits is the one admission for a person's own source agent and its offline copy (nonrecursive):
// the row's own person whatever the Team or audience (Team off and only_me included) while they are the primary owner
// or a saved person on the Team; anyone else exactly as crewTeamReachRow. viewer is always the server-derived caller, never a request field. Access comes from this row
// only, never from another home's Main.
func crewTeamSourceRowAdmits(t crewTeam, viewer string, row crewTeamRegisteredAgent) bool {
	// Team off keeps people saved; removing the maker from the Team ends their own row's copy.
	saved := viewer == t.OwnerID || slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == viewer && !p.Invited })
	if viewer != "" && viewer == row.OwnerID && t.RosterSource == "" && saved && crewTeamAccountActive(viewer) {
		return true
	}
	return crewTeamReachRow(t, viewer, row)
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
	return crewTeamReachableFrom(ctx, t, viewer, nil)
}

// crewTeamReachableFrom is crewTeamReachableIn over an already-read discovery (people), so a reader that has it (the
// Team tab, the header's live view) applies the same rows without a second discovery. nil reads it here.
func crewTeamReachableFrom(ctx context.Context, t crewTeam, viewer string, people []crewTeamAgentsPerson) []crewTeamReachableAgent {
	out := []crewTeamReachableAgent{}
	open := crewTeamReachOpen(t, viewer)
	if !crewTeamAccountActive(viewer) || !open && !crewTeamOwnerReach(t, viewer) {
		return out // every caller gets the gate, not only crewTeamReachableAgents
	}
	if !open {
		return append(out, crewTeamOnlineCopyRow(ctx, t, viewer)...) // Team off: the owner's own online copy only
	}
	if people == nil {
		people = teamAgentsByPersonWithMain(ctx, t, viewer, crewOwnerMainChatKey(t.ProjectID))
	}
	for _, p := range people {
		if p.UserID == viewer || !t.member(p.UserID) || !crewTeamAccountActive(p.UserID) {
			continue
		}
		for _, a := range p.Agents {
			registered, ok := t.Agents[a.ID]
			if a.ID == "" || !ok || registered.OwnerID != p.UserID || !crewTeamReachRow(t, viewer, registered) {
				continue
			}
			online, address := !crewTeamAgentOnMac(t, p.UserID) || p.MacOn || a.PlaceServer, crewTeamAgentBridgeAddress(registered.OwnerID, a.ID)
			if registered.Kind == "terminal" {
				named, err := currentCrewTeamTerminal(ctx, t, registered)
				if err != nil {
					continue
				} // rebound is refused, never ordinary offline
				online = namedAgentOnline(named)
				address = "agent:" + shareOwnerLabel(registered.OwnerID) + "/" + named.Name
			}
			row := crewTeamReachableAgent{userAvatarFields: avatarFieldsFor(p.UserID, t.ProjectID), Team: t.ProjectID, TeamName: t.Name, ID: a.ID, Kind: "agent", Purpose: a.Purpose, Examples: a.Examples, Name: firstNonEmptyTrimmed(a.Name, "Agent"), PersonID: p.UserID, Person: p.Name, Place: a.Place,
				Online: online, Activity: a.Activity, ChatShare: registered.Rule.ChatShare, Audience: crewTeamRowAudience(t, registered), Address: address, TaskState: crewAgentRequestState(ctx, registered)}
			if !online {
				// The resolver rechecks source membership/rules without calling
				// reach recursively. Do not project history or copy routing IDs.
				_, ready, err := crewTeamOfflineCopyTarget(ctx, t, row, viewer)
				row.OfflineCopyReady = err == nil && ready
			}
			out = append(out, row)
		}
	}
	return append(out, crewTeamOnlineCopyRow(ctx, t, viewer)...)
}

// crewTeamOwnSourceRows are the viewer's own source agents in one Team, admitted by crewTeamSourceRowAdmits (Only me
// and Team off included, while the viewer is a saved maker). They are never part of discovery (a person's own agents
// are listed as their own elsewhere); admission uses them so the maker's own source address
// reaches its own copy. No Home or Main access comes from these rows.
func crewTeamOwnSourceRows(ctx context.Context, t crewTeam, viewer string) []crewTeamReachableAgent {
	if viewer == "" || t.RosterSource != "" || !crewTeamAccountActive(viewer) {
		return nil
	}
	ids := []string{}
	for id, registered := range t.Agents {
		if registered.OwnerID == viewer && crewTeamSourceRowAdmits(t, viewer, registered) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	listed, macOn := map[string]crewTeamAgent{}, false
	for _, p := range teamAgentsByPersonWithMain(ctx, t, viewer, crewOwnerMainChatKey(t.ProjectID)) {
		if p.UserID == viewer {
			macOn = p.MacOn
			for _, a := range p.Agents {
				listed[a.ID] = a
			}
		}
	}
	onMac := crewTeamAgentOnMac(t, viewer)
	out := []crewTeamReachableAgent{}
	for _, id := range ids {
		registered := t.Agents[id]
		// Not listed now (Team off for a member, Mac metadata missing): treat the source as away so the copy answers.
		a, isListed := listed[id]
		online, address := isListed && (!onMac || macOn || a.PlaceServer), crewTeamAgentBridgeAddress(viewer, id)
		if registered.Kind == "terminal" {
			named, err := currentCrewTeamTerminal(ctx, t, registered)
			if err != nil {
				continue
			}
			online = namedAgentOnline(named)
			address = "agent:" + shareOwnerLabel(viewer) + "/" + named.Name
		}
		row := crewTeamReachableAgent{userAvatarFields: avatarFieldsFor(viewer, t.ProjectID), Team: t.ProjectID, TeamName: t.Name, ID: id, Kind: "agent", Purpose: a.Purpose, Examples: a.Examples, Name: firstNonEmptyTrimmed(a.Name, registered.Name, "Agent"), PersonID: viewer, Place: registered.Place,
			Online: online, Activity: a.Activity, ChatShare: registered.Rule.ChatShare, Audience: crewTeamRowAudience(t, registered), Address: address, TaskState: crewAgentRequestState(ctx, registered)}
		if !online {
			_, ready, err := crewTeamOfflineCopyTarget(ctx, t, row, viewer)
			row.OfflineCopyReady = err == nil && ready
		}
		out = append(out, row)
	}
	return out
}

// crewTeamOnlineCopyRow is the Team agent (the online home's Main) when this viewer is in its audience.
func crewTeamOnlineCopyRow(ctx context.Context, t crewTeam, viewer string) []crewTeamReachableAgent {
	if t.OnlineHome == "" {
		return nil
	}
	owner, key := "team-"+t.ProjectID, crewOwnerMainChatKey(t.OnlineHome)
	home, err := readCrewTeam(t.OnlineHome)
	if err != nil {
		return nil
	}
	// Use the ordinary registry projection, also before anybody opens the
	// new home's tab. Provisioning creates Main, not its Team registry row.
	teamAgentsByPersonWithMain(ctx, home, viewer, key)
	if home, err = readCrewTeam(t.OnlineHome); err != nil {
		return nil
	}
	a, ok := home.registeredAgent(owner, key)
	if !ok || !crewTeamOnlineCopyAdmits(t, viewer, a) {
		return nil
	}
	// The Team agent is a teammate: its own person (the Team's account, named "Team agent", with that account's picture).
	return []crewTeamReachableAgent{{userAvatarFields: avatarFieldsFor(owner, t.ProjectID), PersonID: owner, Person: crewTeamAgentPersonName, Team: t.ProjectID, TeamName: t.Name, ID: a.ID, Kind: "team_agent", Purpose: a.Purpose, Examples: a.Examples, Name: firstNonEmptyTrimmed(a.Name, "Team agent"), Place: "server", Online: true, ChatShare: crewChatShareAgent, Audience: crewTeamRowAudience(t, a), Address: crewTeamAgentBridgeAddress(a.OwnerID, a.ID)}}
}

// crewTeamAgentPersonName is how the Team's own account shows among the Team's people.
const crewTeamAgentPersonName = "Team agent"

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
