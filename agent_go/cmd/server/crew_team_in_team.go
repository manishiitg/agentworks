package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Every Team agent has one server-issued ID. Visibility and rules live on that
// agent's registry row. Native chat keys are resolved only at transport boundaries.

// Main is an optional role on the owner's saved physical agent. A bare
// project address or a deterministic member name must not fabricate that role.
func crewTeamMainAgent(t crewTeam, person, key string) bool {
	if person != t.OwnerID {
		return false
	}
	return crewTeamMainAgentWithKey(t, person, key, crewOwnerMainChatKey(t.ProjectID))
}

// The request resolves Main once; comparisons inside row loops perform no reads.
func crewTeamMainAgentWithKey(t crewTeam, person, key, main string) bool {
	if person != t.OwnerID {
		return false
	}
	return main != "" && (key == main || key == macMainTab || key == t.ProjectID)
}

// agentInTeam: the person's own choice for the agent, else In team (new agents are shared).
func (t crewTeam) agentInTeam(person, key string) bool {
	if crewFunctionTabKey(key) {
		return false
	}
	if a, ok := t.registeredAgent(person, key); ok {
		return a.InTeam
	}
	if strings.HasPrefix(key, crewTeamAgentIDPrefix) {
		return false
	}
	return true
}

// errCrewTeamLastShared: a person's last shared agent cannot be turned off (owner, 6 Nov: "all agents cannot be off").
var errCrewTeamLastShared = errors.New("Keep at least one agent shared with the Team. Share another agent first.")

// crewTeamSharedKeys are the IDs of this person's currently shared agents.
func crewTeamSharedKeys(ctx context.Context, t crewTeam, person string) []string {
	var keys []string
	for _, a := range t.Agents {
		if a.OwnerID == person && a.InTeam {
			keys = append(keys, a.ID)
		}
	}
	return keys
}

// seesAgent: the viewer may see, watch and hear about this person's agent. Their own always; someone else's only
// while both are in the Team (Team on) and the agent is In team.
func (t crewTeam) seesAgent(viewer, person, key string) bool {
	if viewer == "" || person == "" {
		return false
	}
	return viewer == person || t.member(viewer) && t.member(person) && t.agentInTeam(person, key)
}

// crewAgentInTeam is the same answer for a server agent by its chat key, from the Crew's Team record. A Crew with no
// Team record shares nothing by choice, so nothing of anyone's is hidden by it.
func crewAgentInTeam(project, person, chatKey string) bool {
	t, err := readCrewTeam(project)
	if err != nil {
		return os.IsNotExist(err)
	}
	return t.agentInTeam(person, strings.TrimSpace(chatKey))
}

// crewTeamAgentOnMac: this person's agents in the Crew work on a Mac (the owner's Mac Crew, or a member who chose
// "Add to my Mac").
func crewTeamAgentOnMac(t crewTeam, person string) bool {
	if person == t.OwnerID {
		return crewRelayTwin(t.ProjectID)
	}
	_, onMac := t.memberMac(person)
	return onMac
}

// crewTeamOwnerMainKey is the chat key the website uses for the Crew owner's Main.
func crewTeamOwnerMainKey(ctx context.Context, t crewTeam) string {
	return crewOwnerMainChatKey(t.ProjectID)
}

// crewTeamAgentKey resolves an internal native binding to its registry ID.
func crewTeamAgentKey(ctx context.Context, t crewTeam, person, key string) string {
	if a, ok := t.registeredAgent(person, key); ok {
		return a.ID
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if !crewTeamAgentOnMac(t, person) {
		return key
	}
	if key == t.ProjectID || person == t.OwnerID && key == crewTeamOwnerMainKey(ctx, t) {
		return macMainTab
	}
	rest, ok := strings.CutPrefix(key, t.ProjectID+codeChatSideMarker)
	if !ok {
		return ""
	}
	if person != t.OwnerID {
		rest = strings.TrimPrefix(rest, "mac-"+sanitizeUserIDForPath(person)+"-") // the Team tab's row key for it
	}
	if rest == "mac-main" || rest == macMainTab {
		return macMainTab
	}
	if crewMacTabID(rest) {
		return rest
	}
	return ""
}

// errCrewTeamAgentNotYours: the agent is not one of the caller's own in this Team (or they are not in it).
var errCrewTeamAgentNotYours = errors.New("choose one of your own agents in this Team")

// crewTeamRuleKey is the registry identity for a native binding.
func crewTeamRuleKey(ctx context.Context, t crewTeam, person, rowKey string) string {
	if stored := crewTeamAgentKey(ctx, t, person, rowKey); stored != "" {
		return stored
	}
	return rowKey
}

// crewTeamOwnRows uses the registry's ownership, independent of whether the Mac
// has pushed its agent list since Team setup. Discovery is not an access check.
func crewTeamOwnRows(ctx context.Context, t crewTeam, person string) []crewTeamAgent {
	var rows []crewTeamAgent
	for _, a := range t.Agents {
		if a.OwnerID == person {
			rows = append(rows, crewTeamAgent{ID: a.ID, ChatKey: a.ChatKey, Name: a.Name, Tab: a.Tab})
		}
	}
	return rows
}

// crewTeamOwnAgent admits settings only for the caller's own registered agent.
// Chat tools may use a unique name; screens send the ID supplied by the server.
func crewTeamOwnAgent(ctx context.Context, project, person, key string) (crewTeam, string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !t.member(person) || ownerOfCrewProjectID(project) != t.OwnerID {
		return t, "", errCrewTeamAgentNotYours
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return t, "", errCrewTeamAgentNotYours
	}
	rows := crewTeamOwnRows(ctx, t, person)
	main := crewTeamMainAgent(t, person, key)
	for _, row := range rows {
		if row.ID == key || row.ChatKey == key || row.Tab == key || main && row.Tab == macMainTab {
			return t, row.ID, nil
		}
	}
	var named []crewTeamAgent
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Name), key) {
			named = append(named, row)
		}
	}
	if len(named) == 1 {
		return t, named[0].ID, nil
	}
	log.Printf("[CREW_TEAM] project=%s event=agent_rule_refused user=%s agent=%q on_mac=%t own_agents=%d same_name=%d", project, logUsernameForUserID(person), key, crewTeamAgentOnMac(t, person), len(rows), len(named))
	return t, "", errCrewTeamAgentNotYours
}

// setCrewTeamAgentChoice saves the person's own In team / Only me for one of their agents. Only the agent's own
// person chooses (owner, 5 Nov: Only me is per person); a server agent must be one of their chats in this Crew; their
// last shared agent stays In team (owner, 6 Nov), for every caller (Team tab, chat, MCP).
func setCrewTeamAgentChoice(ctx context.Context, project, person, key string, inTeam bool) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	unlock, err := wf.LockWorkspace(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	t, err := readCrewTeam(project)
	if err != nil || !t.member(person) || ownerOfCrewProjectID(project) != t.OwnerID {
		return errCrewTeamAgentNotYours
	}
	_, stored, err := crewTeamOwnAgent(ctx, project, person, key)
	if err != nil {
		return err
	}
	if !inTeam && !slices.ContainsFunc(crewTeamSharedKeys(ctx, t, person), func(k string) bool { return k != stored }) {
		return errCrewTeamLastShared
	}
	if err = updateCrewTeamRegisteredAgent(project, person, stored, func(a *crewTeamRegisteredAgent) { a.InTeam = inTeam }); err != nil {
		return err
	}
	pruneCrewStandby(project)
	crewMacTeamTabsCache.Delete(project + "\x00" + person)
	log.Printf("[CREW_TEAM] project=%s event=agent_visibility user=%s agent=%s in_team=%t revision=%d", project, logUsernameForUserID(person), stored, inTeam, t.Revision)
	return nil
}

// setOwnAgentInTeam is an agent's own In team / Only me from its chat (set_my_agent_identity in_team). On a Mac the
// Team record is on the company server, reached through this Crew's Mac online connection; the agent is its Mac tab.
func (api *StreamingAPI) setOwnAgentInTeam(ctx context.Context, user, chatKey string, inTeam bool) error {
	project := crewResourceProjectID(chatKey)
	if !IsLocalMode() {
		return setCrewTeamAgentChoice(ctx, project, user, chatKey, inTeam)
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" {
		return fmt.Errorf("turn on this Crew's Team first; it works through Mac online")
	}
	c, err := desktopConnectionFor(user, b.ConnectionID)
	if err != nil {
		return err
	}
	tab := macSharedAgentTab(project, chatKey)
	if tab == "" {
		return fmt.Errorf("choose one of this Crew's agents")
	}
	body := map[string]any{"agent_key": macSharedAgentChatKey(b.PeerProjectID, tab), "in_team": inTeam}
	return crewTeamRemoteJSON(ctx, user, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID)+"/agent-visibility", body, nil)
}
