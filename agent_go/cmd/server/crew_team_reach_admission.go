package server

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// A Team row is the grant. No cached/persisted duplicate audience is consulted.
// Every bridge operation resolves the same current rows used by discovery.
func crewTeamAdmitAddress(ctx context.Context, viewer, address string) (crewTeamReachableAgent, crewTeam, error) {
	address = strings.TrimSpace(address)
	var candidates []crewTeamReachableAgent
	if rest, ok := strings.CutPrefix(address, "workspace:"+crewProfileID+":"); ok {
		project, _, valid := strings.Cut(rest, "/agent/")
		if valid {
			if t, err := readCrewTeam(project); err == nil {
				if t.RosterSource != "" {
					t, err = readCrewTeam(t.RosterSource)
				}
				if err == nil {
					candidates = crewTeamReachableIn(ctx, t, viewer)
				}
			}
		}
	} else if strings.HasPrefix(address, "agent:") {
		candidates = crewTeamReachableAgents(ctx, viewer)
	}
	for _, row := range candidates {
		if row.Address != address {
			continue
		}
		t, err := readCrewTeam(row.Team)
		if err != nil {
			break
		}
		// Re-read after discovery: no stale membership, rule or transport snapshot.
		for _, current := range crewTeamReachableIn(ctx, t, viewer) {
			if current.ID == row.ID && current.Address == address {
				return current, t, nil
			}
		}
	}
	log.Printf("[TEAM_AGENT_REACH] caller=%s event=admit outcome=refused reason=not_reachable", viewer)
	return crewTeamReachableAgent{}, crewTeam{}, admissionRefused("This agent is not In team for you now. Refresh the agent list.")
}

// Routing is not a second grant: the original target was admitted above and
// the Team agent is selected from that same viewer's current reachable rows.
func crewTeamRouteAsk(ctx context.Context, t crewTeam, row crewTeamReachableAgent) (crewTeamReachableAgent, error) {
	if row.Online {
		return row, nil
	}
	viewer := GetUserIDFromContext(ctx)
	for _, candidate := range crewTeamReachableIn(ctx, t, viewer) {
		if candidate.Kind == "team_agent" {
			return candidate, nil
		}
	}
	log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask outcome=refused reason=offline_no_team_agent", t.ProjectID, row.ID, viewer)
	return crewTeamReachableAgent{}, fmt.Errorf("This agent is offline. Try later, or set up the Team agent in AgentWorks.")
}

// The bridge directory consumes exactly the same reachable rows as admission.
func crewTeamBridgeDirectoryItems(ctx context.Context, viewer string) []map[string]interface{} {
	items := []map[string]interface{}{}
	for _, row := range crewTeamReachableAgents(ctx, viewer) {
		presence := "Offline"
		if row.Online {
			presence = "Online"
		}
		items = append(items, map[string]interface{}{"agent_name": row.Address, "name": row.Name, "label": row.Name, "id": row.Address, "target": row.Address, "agent_id": row.ID, "team_project_id": row.Team, "team_name": row.TeamName, "owner": row.Person, "kind": "team_agent", "type": "In-team agent", "shared": true, "online": row.Online, "presence": presence, "reach": "Ask or send through AgentBridge; replies stay in the original inbox."})
	}
	return items
}

// Existing endpoint identity pins the Team row; the registry remains the only
// audience record. A renamed/rebound mailbox never inherits another row.
func crewTeamEndpointAddress(ctx context.Context, e agentMessageEndpoint) (string, error) {
	if e.NamedAgentID != "" {
		named, err := namedAgentByID(ctx, e.UserID, e.NamedAgentID)
		if err != nil {
			return "", err
		}
		return "agent:" + shareOwnerLabel(e.UserID) + "/" + named.Name, nil
	}
	if e.Kind == triggerCallerCrew && e.TeamAgentID != "" {
		return crewTeamAgentAddress(e.ID, e.TeamAgentID), nil
	}
	return "", fmt.Errorf("Team endpoint unavailable")
}

func crewTeamAdmitEndpoint(ctx context.Context, viewer string, e agentMessageEndpoint) error {
	address, err := crewTeamEndpointAddress(ctx, e)
	if err != nil {
		return err
	}
	row, t, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil || row.ID != e.TeamAgentID {
		return admissionRefused("This Team agent is no longer reachable.")
	}
	if row.Kind == "team_agent" {
		if e.ID != t.OnlineHome || e.UserID != ownerOfCrewProjectID(t.OnlineHome) {
			return admissionRefused("Team agent identity changed.")
		}
		return nil
	}
	registered, ok := t.Agents[row.ID]
	if !ok || registered.OwnerID != e.UserID {
		return admissionRefused("Team agent identity changed.")
	}
	if e.NamedAgentID != "" {
		if registered.Terminal == nil || registered.Terminal.NamedAgentID != e.NamedAgentID {
			return admissionRefused("Terminal binding changed.")
		}
	} else {
		key := e.ChatKey
		if e.PublishedAgent != "" {
			key = e.PublishedAgent
		}
		if registered.ChatKey != key && !(crewRelayTwin(t.ProjectID) && registered.OwnerID == t.OwnerID && macSharedAgentChatKey(t.ProjectID, registered.Tab) == key) {
			return admissionRefused("Team chat binding changed.")
		}
	}
	return nil
}

func crewTeamConversationAdmission(ctx context.Context, c agentConversation) (bool, error) {
	if publishedAsker(c.Endpoints[0]) == publishedAsker(c.Endpoints[1]) {
		return false, nil
	}
	handled := false
	for i, e := range c.Endpoints {
		if e.TeamAgentID == "" {
			continue
		}
		handled = true
		if err := crewTeamAdmitEndpoint(ctx, publishedAsker(c.Endpoints[1-i]), e); err != nil {
			log.Printf("[TEAM_AGENT_REACH] caller=%s agent=%s inbox=%s event=send outcome=refused reason=current_audience_or_binding", publishedAsker(c.Endpoints[1-i]), e.TeamAgentID, c.ID)
			return true, err
		}
	}
	return handled, nil
}

// Authority is for this already-owned mailbox only, never Crew/file tools.
func (api *StreamingAPI) externalAuthorizeConversationPeer(ctx context.Context, claims *UserClaims, c agentConversation, side int, send bool) error {
	if handled, err := crewTeamConversationAdmission(ctx, c); handled {
		if err != nil {
			return err
		}
		if side < 0 || side > 1 || claims == nil || claims.UserID != publishedAsker(c.Endpoints[side]) || !namedAgentOperationAllowed(claims, send) {
			return admissionRefused("Team mailbox unavailable.")
		}
		if token := claims.AccessToken; token != nil && token.BoundAgentID != "" && token.BoundAgentID != c.Endpoints[side].NamedAgentID {
			return admissionRefused("Team mailbox belongs to another agent.")
		}
		return nil
	}
	return api.externalAuthorizeMessagePeer(ctx, claims, c.Endpoints[1-side], send)
}

// Server-owned per-delivery instructions, kept outside the sender's message.
func crewTeamMessageInstructions(ctx context.Context, c agentConversation, m agentMessage) string {
	dest, source := c.Endpoints[1-m.From], c.Endpoints[m.From]
	if dest.TeamAgentID == "" || publishedAsker(source) == dest.UserID {
		return ""
	}
	address, err := crewTeamEndpointAddress(ctx, dest)
	if err != nil {
		return ""
	}
	row, t, err := crewTeamAdmitAddress(ctx, publishedAsker(source), address)
	if err != nil {
		return ""
	}
	if row.Kind == "team_agent" {
		t, err = readCrewTeam(t.OnlineHome)
		if err != nil {
			return ""
		}
	}
	a, ok := t.Agents[row.ID]
	if !ok {
		return ""
	}
	return whenOthersAskNote(crewPersonShortName(publishedAsker(source)), crewPersonShortName(dest.UserID), a.Rule.WhenOthersAsk) + "\n\n" + crewAskModeLine(a.Rule.CanMakeChanges)
}

// Only message targets use this adapter. It grants no product/file catalog.
func crewTeamBridgeTarget(ctx context.Context, viewer, address string) (triggerTarget, error) {
	row, t, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil {
		return triggerTarget{}, err
	}
	if row.Kind == "team_agent" {
		t, err = readCrewTeam(t.OnlineHome)
		if err != nil {
			return triggerTarget{}, err
		}
	}
	a, ok := t.Agents[row.ID]
	if !ok {
		return triggerTarget{}, admissionRefused("Team agent unavailable.")
	}
	if a.Kind == "terminal" {
		named, err := currentCrewTeamTerminal(ctx, t, a)
		if err != nil {
			return triggerTarget{}, err
		}
		return triggerTarget{TeamAgentID: a.ID, NamedAgentID: named.ID, ShareOwnerUserID: a.OwnerID, Kind: triggerCallerUser, Label: a.Name}, nil
	}
	key := crewTeamNativeChatKey(t.ProjectID, a.OwnerID, crewTeamAgent{Tab: a.Tab, ChatKey: a.ChatKey})
	return triggerTarget{TeamAgentID: a.ID, Kind: triggerCallerCrew, CrewID: t.ProjectID, CrewProfile: crewProfileID, CrewOwner: a.OwnerID, Path: crewProjectRootByID(t.ProjectID), Label: a.Name, Chat: &codeChat{OwnerID: a.OwnerID, ID: key, Key: key, Name: a.Name}}, nil
}
