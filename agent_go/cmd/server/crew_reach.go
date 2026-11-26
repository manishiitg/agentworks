package server

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewReach is what an agent inside a Crew may list and start a message or function call to (owner, 19 Nov): its own
// Crew, that Crew's Team, and the Crews linked to it with their agents and Team agents. The person's other Crews and
// other Teams stay hidden and refused; other people's shared agents and Relays are unchanged.
type crewReach struct {
	roots    map[string]bool // this Crew and its linked Crews
	projects map[string]bool // their project IDs, each one's paired copy (Mac or server) and the Team a home belongs to
}

// crewReachNote is the one refusal line.
const crewReachNote = "This agent isn't in this project or its Team, and its project is not linked to this one. Link its project (ask this chat, or use Linked projects), or ask from that project."

// crewReachFrom is the reach of the Crew at here. nil means no limit: outside a Crew (MCP). A link read failure still returns this Crew's own reach, with the error.
func crewReachFrom(ctx context.Context, here string) (*crewReach, error) {
	here = strings.TrimSpace(here)
	if here == "" {
		return nil, nil
	}
	refs, err := readWorkWorkflowReferences(ctx, here)
	r := &crewReach{roots: map[string]bool{}, projects: map[string]bool{}}
	for _, root := range append([]string{here}, refs...) {
		root = canonicalCrewWorkspaceRoot(root)
		r.roots[root] = true
		id := crewProjectIDOfRoot(root)
		if id == "" {
			continue
		}
		r.projects[id] = true
		if pair, e := crewroot.ReadSync(id); e == nil && pair != nil {
			r.projects[pair.PeerProjectID] = true
		}
		// The Teams an online project joined are its Team too (#3664).
		for _, t := range crewTeamsJoinedBy(id) {
			r.projects[t.ProjectID] = true
		}
	}
	// Linked projects on the other machine (#3663): their agents are the person's own agents tagged with that project.
	links, linkErr := readRemoteProjectLinks(ctx, here)
	for _, link := range links {
		r.projects[link.ProjectID] = true
	}
	if err == nil {
		err = linkErr
	}
	return r, err
}

// crewReachCaller is the Crew a caller works in, or "" when the reach does not apply: watchers, workflow steps and
// connections from outside a Crew. A Crew tab that sends through AgentBridge keeps its Crew.
func crewReachCaller(caller triggerLinkCaller) string {
	if caller.ConditionJob != "" || caller.Stamp.Type != triggerCallerCrew && caller.BridgeHosted == nil {
		return ""
	}
	return strings.TrimSpace(caller.Path)
}

func (r *crewReach) crew(path, id string) bool {
	return r.roots[canonicalCrewWorkspaceRoot(path)] || id != "" && r.projects[id]
}

// places says whether any of ids names a Crew, and whether one of them is in reach.
func (r *crewReach) places(ids ...string) (placed, in bool) {
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			placed = true
			if r.projects[id] {
				return true, true
			}
		}
	}
	return placed, false
}

// namedAgentCrews are the Crews a registered agent works in: a Crew tab, an app chat (and its Mac copy), or a native
// chat joined to a Crew.
func namedAgentCrews(a namedMessageAgent, terminals map[string]string) []string {
	out := []string{terminals[a.ID]}
	if a.Hosted != nil {
		out = append(out, a.Hosted.Project)
	}
	if a.Attachment != nil && a.Attachment.RuntimeInfo != nil {
		out = append(out, a.Attachment.RuntimeInfo.CrewID, a.Attachment.RuntimeInfo.TeamProject)
	}
	return out
}

// keepsRow decides a directory row other than a Crew's own row. appCrews maps this Mac's app chat names to their Crew.
// The person's agents in no Crew (their own CLI chats) stay.
func (r *crewReach) keepsRow(row map[string]any, named map[string]namedMessageAgent, terminals, appCrews map[string]string) bool {
	switch stringField(row["kind"]) {
	case "crew", "crew_agent", "relay":
		return true // Crew rows are decided where they are built; crew_agent rows are other people's shared Crews
	}
	team := stringField(row["team_project_id"])
	if team != "" && (r.projects[team] || row["yours"] != true) {
		return r.projects[team]
	}
	if row["yours"] != true {
		return true
	}
	// The person's own agent in a Team out of reach still shows when its own project is linked (#3663).
	ids := []string{team, stringField(row["crew_id"]), appCrews[stringField(row["agent_name"])]}
	if a, ok := named[stringField(row["agent_id"])]; ok {
		ids = append(ids, namedAgentCrews(a, terminals)...)
	}
	placed, in := r.places(ids...)
	return in || !placed && stringField(row["crew_name"]) == ""
}

// allowsTarget is keepsRow for a resolved message or function target. team is the Team that admitted a Team agent.
func (r *crewReach) allowsTarget(ctx context.Context, userID string, target triggerTarget, team string) bool {
	if team != "" {
		return r.projects[team]
	}
	if target.NamedAgentID != "" {
		if target.ShareOwnerUserID != "" && target.ShareOwnerUserID != userID {
			return true // another person's shared agent follows its sharing
		}
		a, err := namedAgentByID(ctx, userID, target.NamedAgentID)
		if err != nil {
			return true // delivery admission refuses an unknown agent
		}
		terminals, _ := terminalCrewMemberships()
		placed, in := r.places(namedAgentCrews(a, terminals)...)
		return in || !placed
	}
	if target.Kind != triggerCallerCrew || strings.TrimSpace(target.Path) == "" || r.crew(target.Path, target.CrewID) {
		return true
	}
	if crewProjectOwnedByCaller(userID, target.Path) {
		return false
	}
	t, err := readCrewTeam(target.CrewID)
	return err != nil || !crewTeamAgentsVisible(t, userID) // another Team's Crew needs a link; shared Crews follow their sharing
}

// requireNetworkReach: on the Mac, a Crew agent starts a network message only to an agent its own list shows.
func (api *StreamingAPI) requireNetworkReach(ctx context.Context, owner string, caller triggerLinkCaller, address string) error {
	here := crewReachCaller(caller)
	if reach, _ := crewReachFrom(ctx, here); reach == nil {
		return nil
	}
	claims := GetUserFromContext(ctx)
	if claims == nil {
		claims = &UserClaims{UserID: owner}
	}
	directory, err := listAgentDirectory(withDirectoryThisCrew(ctx, here), claims, api)
	if err != nil {
		return err
	}
	rows, _ := directory["agents"].([]map[string]any)
	for _, row := range rows {
		if stringField(row["target"]) == address || stringField(row["id"]) == address || slices.Contains(agentDirectoryAddresses(row), address) {
			return nil
		}
	}
	log.Printf("[CREW_LINK] event=refused caller=%s target=%s reason=outside_reach", here, address)
	return fmt.Errorf("%s", crewReachNote)
}
