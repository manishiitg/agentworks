package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team row is the grant. No cached/persisted duplicate audience is consulted.
// Every bridge operation resolves the same current rows used by discovery.
func crewTeamAdmitAddress(ctx context.Context, viewer, address string) (crewTeamReachableAgent, crewTeam, error) {
	address = strings.TrimSpace(address)
	var candidates []crewTeamReachableAgent
	if strings.HasPrefix(address, "agent:") {
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
	// The maker's own source address (Only me and Team off included) admits only through crewTeamSourceRowAdmits.
	if row, t, ok := crewTeamAdmitOwnSource(ctx, viewer, address); ok {
		return row, t, nil
	}
	reason := "not_reachable"
	if crewTeamBindingChangedForAddress(ctx, viewer, address) {
		reason = "binding_changed"
	} else if crewTeamNotChosenForAddress(ctx, viewer, address) {
		reason = "not_chosen"
	}
	log.Printf("[TEAM_AGENT_REACH] caller=%s event=admit outcome=refused reason=%s", viewer, reason)
	return crewTeamReachableAgent{}, crewTeam{}, agentAdmissionRefusal{err: crewTeamReachRefusal{reason: reason}}
}

func crewTeamAdmitOwnSource(ctx context.Context, viewer, address string) (crewTeamReachableAgent, crewTeam, bool) {
	if !strings.HasPrefix(address, "agent:"+shareOwnerLabel(viewer)+"/") {
		return crewTeamReachableAgent{}, crewTeam{}, false
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return crewTeamReachableAgent{}, crewTeam{}, false
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return crewTeamReachableAgent{}, crewTeam{}, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, err := readCrewTeam(entry.Name())
		if err != nil {
			continue
		}
		for _, row := range crewTeamOwnSourceRows(ctx, t, viewer) {
			if row.Address == address {
				log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=admit outcome=allowed reason=own_source audience=%s", t.ProjectID, row.ID, viewer, row.Audience)
				return row, t, true
			}
		}
	}
	return crewTeamReachableAgent{}, crewTeam{}, false
}

// Routing is not a second grant: the original target was admitted above and
// the Team agent is selected from that same viewer's current reachable rows.
func crewTeamRouteAsk(ctx context.Context, t crewTeam, row crewTeamReachableAgent) (crewTeamReachableAgent, error) {
	viewer := GetUserIDFromContext(ctx)
	// The admitted row already includes published/server placement and live
	// source presence. A copy is never a second target while either is online.
	if row.Online {
		log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask route=source reason=source_online", t.ProjectID, row.ID, viewer)
		return row, nil
	}
	// Explicit copy intent controls preparation only after the source is away.
	intent, err := crewOfflineCopyPutOnline(t, row)
	if err != nil {
		return crewTeamReachableAgent{}, admissionRefused("This agent's online copy is unavailable now.")
	}
	if intent {
		copy, ready, err := crewTeamOfflineCopyTarget(ctx, t, row, viewer)
		if err != nil {
			return crewTeamReachableAgent{}, err
		}
		if !ready {
			log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask outcome=refused reason=online_copy_preparing", t.ProjectID, row.ID, viewer)
			return crewTeamReachableAgent{}, admissionRefused("This agent's online copy is still being prepared. Try again in a few minutes.")
		}
		log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask route=offline_copy reason=put_online", t.ProjectID, row.ID, viewer)
		return copy, nil
	}
	copy, ready, err := crewTeamOfflineCopyTarget(ctx, t, row, viewer)
	if err != nil {
		return crewTeamReachableAgent{}, err // revocation/read failure is not no-copy fallback
	}
	if ready {
		log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask route=offline_copy", t.ProjectID, row.ID, viewer)
		return copy, nil
	}
	for _, candidate := range crewTeamReachableIn(ctx, t, viewer) {
		if candidate.Kind == "team_agent" {
			log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask route=team_agent reason=no_ready_copy", t.ProjectID, row.ID, viewer)
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
		items = append(items, map[string]interface{}{"agent_name": row.Address, "name": row.Name, "purpose": row.Purpose, "examples": row.Examples, "label": row.Name, "id": row.Address, "target": row.Address, "agent_id": row.ID, "team_project_id": row.Team, "team_name": row.TeamName, "owner": row.Person, "kind": "team_agent", "type": "In-team agent", "shared": true, "online": row.Online, "presence": presence, "reach": "Ask or send through AgentBridge; replies stay in the original inbox."})
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
	if e.Kind == triggerCallerCrew && e.TeamAgentID != "" && e.OfflineCopyTeam != "" {
		return crewTeamAgentAddress(e.OfflineCopyTeam, e.TeamAgentID), nil
	}
	if e.Kind == triggerCallerCrew && e.TeamAgentID != "" {
		return crewTeamAgentAddress(e.ID, e.TeamAgentID), nil
	}
	return "", fmt.Errorf("Team endpoint unavailable")
}

func crewTeamAdmitEndpoint(ctx context.Context, viewer string, e agentMessageEndpoint) error {
	// A stored native endpoint already pins its one Team and row. Admission
	// needs no people's tab discovery, Mac RPCs, presence or copy preparation.
	if e.NamedAgentID != "" && e.Hosted == nil && e.OfflineCopyTeam == "" {
		return crewTeamAdmitTerminalEndpoint(ctx, viewer, e)
	}
	address, err := crewTeamEndpointAddress(ctx, e)
	if err != nil {
		return err
	}
	row, t, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil {
		return err
	}
	if row.ID != e.TeamAgentID {
		return admissionRefused("This Team agent is no longer reachable.")
	}
	if e.OfflineCopyTeam != "" {
		if t.ProjectID != e.OfflineCopyTeam {
			return admissionRefused("This agent's offline copy changed.")
		}
		return crewOfflineCopyEndpointAdmits(t, viewer, e)
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

// The same source-row audience and terminal binding used by discovery, without
// materializing unrelated app rows. No result survives this operation.
func crewTeamAdmitTerminalEndpoint(ctx context.Context, viewer string, e agentMessageEndpoint) (retErr error) {
	start, reason := time.Now(), "allowed"
	defer func() {
		if ctx.Err() != nil {
			retErr = ctx.Err()
			reason = "interrupted"
		}
		outcome := "allowed"
		if retErr != nil {
			outcome = "refused"
		}
		if errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded) {
			outcome = "interrupted"
		}
		log.Printf("[TEAM_AGENT_REACH] event=terminal_admit caller=%s agent=%s named_agent=%s outcome=%s reason=%s elapsed_ms=%d", viewer, e.TeamAgentID, e.NamedAgentID, outcome, reason, time.Since(start).Milliseconds())
	}()
	refuse := func(why string) error {
		reason = why
		return agentAdmissionRefusal{err: crewTeamReachRefusal{reason: why}}
	}
	t, row, found, err := terminalCrewForNamed(e.NamedAgentID)
	if err != nil {
		reason = "terminal_registry"
		return err
	}
	if !found || row.ID != e.TeamAgentID || row.OwnerID != e.UserID || row.Terminal == nil || row.Terminal.NamedAgentID != e.NamedAgentID {
		return refuse("binding_changed")
	}
	if !crewTeamAccountActive(viewer) || !crewTeamSourceRowAdmits(t, viewer, row) {
		return refuse("not_reachable")
	}
	named, err := currentCrewTeamTerminal(ctx, t, row)
	if err != nil {
		reason = "terminal_binding"
		return err
	}
	if named.ID != e.NamedAgentID || named.UserID != e.UserID {
		return refuse("binding_changed")
	}
	return nil
}

func crewTeamConversationAdmission(ctx context.Context, c agentConversation) (bool, error) {
	if err := terminalCrewConversationFence(ctx, c); err != nil {
		log.Printf("[TEAM_AGENT_REACH] inbox=%s event=send outcome=refused reason=crew_joined", c.ID)
		return true, err
	}
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
			log.Printf("[TEAM_AGENT_REACH] caller=%s agent=%s inbox=%s event=send outcome=refused reason=%s", publishedAsker(c.Endpoints[1-i]), e.TeamAgentID, c.ID, crewTeamReachReason(err))
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
	if dest.NamedAgentID != "" && dest.Hosted == nil && dest.OfflineCopyTeam == "" {
		if err := crewTeamAdmitTerminalEndpoint(ctx, publishedAsker(source), dest); err != nil {
			return ""
		}
		_, row, found, err := terminalCrewForNamed(dest.NamedAgentID)
		if err != nil || !found || row.ID != dest.TeamAgentID || row.OwnerID != dest.UserID {
			return ""
		}
		return whenOthersAskNote(crewPersonShortName(publishedAsker(source)), crewPersonShortName(dest.UserID), row.Rule.WhenOthersAsk, row.Rule.CanMakeChanges)
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
	return whenOthersAskNote(crewPersonShortName(publishedAsker(source)), crewPersonShortName(dest.UserID), a.Rule.WhenOthersAsk, a.Rule.CanMakeChanges)
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

func crewTeamAskTarget(ctx context.Context, viewer string, target triggerTarget) (triggerTarget, error) {
	if target.TeamAgentID == "" {
		return target, nil
	}
	endpoint := targetMessageEndpoint(viewer, target)
	address, err := crewTeamEndpointAddress(ctx, endpoint)
	if err != nil {
		return target, err
	}
	row, t, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil {
		return target, err
	}
	askCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: viewer})
	routed, err := crewTeamRouteAsk(askCtx, t, row)
	if err != nil {
		return target, err
	}
	// A copy keeps the source address and sharing identity. Bind its private
	// execution capability before the ordinary same-address fast path.
	if routed.OfflineCopyID != "" {
		return crewTeamBindOfflineCopyAsk(askCtx, viewer, target, routed)
	}
	if routed.Address == row.Address {
		return target, nil
	}
	return crewTeamBridgeTarget(ctx, viewer, routed.Address)
}

// Per-asker conversations reuse the existing publication thread mechanism, but
// current In-team admission (not general Crew sharing) selects the native agent.
func crewTeamSharedChat(ctx context.Context, viewer, project, key string) (*codeChat, bool) {
	if chat, ok := crewOfflineCopySharedChat(viewer, project, key); ok {
		return chat, true
	}
	t, err := readCrewTeam(project)
	if err != nil {
		return nil, false
	}
	source := t
	if t.RosterSource != "" {
		source, err = readCrewTeam(t.RosterSource)
		if err != nil {
			return nil, false
		}
	}
	for _, row := range crewTeamReachableIn(ctx, source, viewer) {
		effective := t
		if row.Kind == "team_agent" && source.OnlineHome != project {
			continue
		}
		a, ok := effective.Agents[row.ID]
		if !ok || a.Kind == "terminal" {
			continue
		}
		native := crewTeamNativeChatKey(project, a.OwnerID, crewTeamAgent{Tab: a.Tab, ChatKey: a.ChatKey})
		if native != key {
			continue
		}
		session := ""
		for _, p := range teamAgentsByPerson(ctx, effective, viewer) {
			for _, agent := range p.Agents {
				if agent.ID == a.ID {
					session = agent.sessionID
				}
			}
		}
		return &codeChat{OwnerID: a.OwnerID, ID: key, Key: key, Name: a.Name, Purpose: a.Purpose, SessionID: session}, true
	}
	return nil, false
}

type crewTeamReachRefusal struct{ reason string }

func (e crewTeamReachRefusal) Error() string {
	if e.reason == "crew_joined" {
		return "This agent works inside a Crew now; only that Crew's people can reach it. Leave the Crew to use its network sharing."
	}
	return "This agent is not shared with you now, or its attached session changed. Refresh the agent list."
}
func crewTeamReachReason(err error) string {
	var refused crewTeamReachRefusal
	if errors.As(err, &refused) {
		return refused.reason
	}
	return "current_audience_or_binding"
}

// This classifies a refusal only; it never grants access. A current teammate
// must not bypass a rebound terminal binding through a broader Discovery share.
func crewTeamBindingChangedForAddress(ctx context.Context, viewer, address string) bool {
	if !strings.HasPrefix(address, "agent:") || !crewTeamAccountActive(viewer) {
		return false
	}
	root, err := crewroot.StateRoot()
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t, err := readCrewTeam(entry.Name())
		if err != nil {
			continue
		}
		for _, a := range t.Agents {
			if a.Kind != "terminal" || a.Terminal == nil || !crewTeamReachRow(t, viewer, a) {
				continue
			}
			named, err := namedAgentByID(ctx, a.OwnerID, a.Terminal.NamedAgentID)
			if err != nil || address != "agent:"+shareOwnerLabel(a.OwnerID)+"/"+named.Name {
				continue
			}
			if _, err = currentCrewTeamTerminal(ctx, t, a); err != nil {
				return true
			}
		}
	}
	return false
}
