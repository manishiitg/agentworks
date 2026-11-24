package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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

// crewTeamAwayGrace: a source heard from this recently is only Away; its asks wait for it and never go to a copy
// (owner, #3194: lids close and Wi-Fi drops; the real agent must not be replaced for that).
const crewTeamAwayGrace = 10 * time.Minute

// The caller's choice for an agent offline longer than crewTeamAwayGrace (#3194). Empty means "not chosen yet".
// Instead of waiting, the caller's agent may read the agent's chat itself (messages action=history); nothing on the
// server answers for an offline agent.
const crewTeamOfflineWait = "wait" // keep the ask for the real agent until it is back

// crewTeamOfflineChoiceNeeded is the answer to an ask for an agent that has been offline past the grace: nothing was
// sent, and the caller (an agent, for its person) decides.
type crewTeamOfflineChoiceNeeded struct {
	Name, Person, Address string
	Since                 time.Time
	// Readable: its chat is on the server and its person shows it to teammates (Live chat).
	Readable bool
}

func (e crewTeamOfflineChoiceNeeded) Error() string {
	who := e.Name
	if e.Person != "" {
		who = e.Person + "'s agent " + e.Name
	}
	line := fmt.Sprintf("%s has been offline for %s, so nothing was sent. Ask again with offline=\"wait\" and it answers when it is back", who, crewTeamOfflineFor(time.Since(e.Since)))
	if !e.Readable {
		return line + "."
	}
	return line + fmt.Sprintf(", or read its chat now with messages action=%s target=%s and query=\"words\" (it may not have the latest).", crewTeamChatReadAction, e.Address)
}

// writeCrewTeamOfflineChoice answers an external ask with the offline choice: the agent text in message, and for a
// screen (the Ask box) the person's line, how long, and whether its chat can be read, so it shows Wait without
// parsing text. Nothing was sent.
func writeCrewTeamOfflineChoice(w http.ResponseWriter, e crewTeamOfflineChoiceNeeded) {
	who := e.Name
	if e.Person != "" {
		who = e.Person + "'s " + e.Name
	}
	offFor := crewTeamOfflineFor(time.Since(e.Since))
	line := fmt.Sprintf("%s has been offline for %s, so nothing was sent yet.", who, offFor)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "offline_choice", "message": e.Error(), "line": line, "offline_for": offFor, "readable": e.Readable}})
}

func crewTeamOfflineFor(d time.Duration) string {
	switch m := int(d.Minutes()); {
	case m < 120:
		return fmt.Sprintf("%d minutes", m)
	case m < 48*60:
		return fmt.Sprintf("%d hours", m/60)
	default:
		return fmt.Sprintf("%d days", m/(24*60))
	}
}

// An offline source is never replaced (#3194): a long-offline agent's ask waits only when the caller chooses wait;
// otherwise the caller is told how long it has been offline and that it can wait or read the agent's chat.
func crewTeamRouteAsk(ctx context.Context, t crewTeam, row crewTeamReachableAgent, choice string) error {
	viewer := GetUserIDFromContext(ctx)
	if row.Online {
		log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask route=source reason=source_online", t.ProjectID, row.ID, viewer)
		return nil
	}
	offFor := time.Since(row.OfflineSince).Round(time.Second)
	if offFor < crewTeamAwayGrace || choice == crewTeamOfflineWait {
		reason := "away"
		if offFor >= crewTeamAwayGrace {
			reason = "caller_wait"
		}
		log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask route=source reason=%s offline_for=%s", t.ProjectID, row.ID, viewer, reason, offFor)
		return nil
	}
	log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask outcome=choice_needed offline_for=%s readable=%t", t.ProjectID, row.ID, viewer, offFor, row.ChatReadable)
	return crewTeamOfflineChoiceNeeded{Name: row.Name, Person: row.Person, Address: row.Address, Since: row.OfflineSince, Readable: row.ChatReadable}
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
	if e.Kind == triggerCallerCrew && e.TeamAgentID != "" {
		return crewTeamAgentAddress(e.ID, e.TeamAgentID), nil
	}
	return "", fmt.Errorf("Team endpoint unavailable")
}

func crewTeamAdmitEndpoint(ctx context.Context, viewer string, e agentMessageEndpoint) error {
	_, err := crewTeamAdmittedTeam(ctx, viewer, e)
	return err
}

// crewTeamAdmittedTeam is crewTeamAdmitEndpoint that also returns the Team that admitted the endpoint, so a Crew's reach
// can check it is that Crew's own or a linked Team (crew_reach.go).
func crewTeamAdmittedTeam(ctx context.Context, viewer string, e agentMessageEndpoint) (crewTeam, error) {
	// A stored native endpoint already pins its one Team and row. Admission
	// needs no people's tab discovery, Mac RPCs, presence or copy preparation.
	if e.NamedAgentID != "" && e.Hosted == nil {
		if err := crewTeamAdmitTerminalEndpoint(ctx, viewer, e); err != nil {
			return crewTeam{}, err
		}
		t, _, _, err := terminalCrewForNamed(e.NamedAgentID)
		return t, err
	}
	address, err := crewTeamEndpointAddress(ctx, e)
	if err != nil {
		return crewTeam{}, err
	}
	row, t, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil {
		return crewTeam{}, err
	}
	if row.ID != e.TeamAgentID {
		return crewTeam{}, admissionRefused("This Team agent is no longer reachable.")
	}
	registered, ok := t.Agents[row.ID]
	if !ok || registered.OwnerID != e.UserID {
		return crewTeam{}, admissionRefused("Team agent identity changed.")
	}
	if e.NamedAgentID != "" {
		if registered.Terminal == nil || registered.Terminal.NamedAgentID != e.NamedAgentID {
			return crewTeam{}, admissionRefused("Terminal binding changed.")
		}
	} else {
		key := e.ChatKey
		if e.PublishedAgent != "" {
			key = e.PublishedAgent
		}
		if registered.ChatKey != key && !(crewRelayTwin(t.ProjectID) && registered.OwnerID == t.OwnerID && macSharedAgentChatKey(t.ProjectID, registered.Tab) == key) {
			return crewTeam{}, admissionRefused("Team chat binding changed.")
		}
	}
	return t, nil
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
	if dest.NamedAgentID != "" && dest.Hosted == nil {
		if err := crewTeamAdmitTerminalEndpoint(ctx, publishedAsker(source), dest); err != nil {
			return ""
		}
		_, row, found, err := terminalCrewForNamed(dest.NamedAgentID)
		if err != nil || !found || row.ID != dest.TeamAgentID || row.OwnerID != dest.UserID {
			return ""
		}
		return whenOthersAskNote(crewPersonShortName(publishedAsker(source)), crewPersonShortName(dest.UserID), row.Rule.WhenOthersAsk)
	}
	address, err := crewTeamEndpointAddress(ctx, dest)
	if err != nil {
		return ""
	}
	row, t, err := crewTeamAdmitAddress(ctx, publishedAsker(source), address)
	if err != nil {
		return ""
	}
	a, ok := t.Agents[row.ID]
	if !ok {
		return ""
	}
	return whenOthersAskNote(crewPersonShortName(publishedAsker(source)), crewPersonShortName(dest.UserID), a.Rule.WhenOthersAsk)
}

// Only message targets use this adapter. It grants no product/file catalog.
func crewTeamBridgeTarget(ctx context.Context, viewer, address string) (triggerTarget, error) {
	row, t, err := crewTeamAdmitAddress(ctx, viewer, address)
	if err != nil {
		return triggerTarget{}, err
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

// crewTeamAskTarget routes a new ask; offline is the caller's choice for a long-offline agent ("" or "wait").
func crewTeamAskTarget(ctx context.Context, viewer string, target triggerTarget, offline string) (triggerTarget, error) {
	if target.TeamAgentID == "" {
		return target, nil
	}
	offline = strings.ToLower(strings.TrimSpace(offline))
	if offline != "" && offline != crewTeamOfflineWait {
		return target, admissionRefused(`offline can only be "wait". To read the agent's chat instead, use messages action=history.`)
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
	return target, crewTeamRouteAsk(askCtx, t, row, offline)
}

// Team asks reuse the publication mechanism (into the agent's one chat, #3469), but
// current In-team admission (not general Crew sharing) selects the native agent.
func crewTeamSharedChat(ctx context.Context, viewer, project, key string) (*codeChat, bool) {
	t, err := readCrewTeam(project)
	if err != nil {
		return nil, false
	}
	for _, row := range crewTeamReachableIn(ctx, t, viewer) {
		a, ok := t.Agents[row.ID]
		if !ok || a.Kind == "terminal" {
			continue
		}
		native := crewTeamNativeChatKey(project, a.OwnerID, crewTeamAgent{Tab: a.Tab, ChatKey: a.ChatKey})
		if native != key {
			continue
		}
		session := ""
		for _, p := range teamAgentsByPerson(ctx, t, viewer) {
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
		return "This agent works inside a project now; only that project's people can reach it. Leave the project to use its network sharing."
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
