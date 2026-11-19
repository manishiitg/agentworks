package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectinstructions"
)

// Terminal is a transport kind, not a second sharing or sync system. The native
// session is the chat key; the named-agent and lease identify its actual route.
type crewTeamTerminal struct {
	NamedAgentID string `json:"named_agent_id"`
	AttachmentID string `json:"attachment_id"`
	Provider     string `json:"provider"`
	MacPair      string `json:"mac_pair"`
}

type crewTeamTerminalCopy struct {
	Path    string   `json:"path"`
	Folders []string `json:"folders"`
}

// Only called through the signed-in exact attachment endpoint, never a browser
// supplied session, owner, device or local filesystem path.
func (api *StreamingAPI) externalTeamTerminalSync(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	project, action := externalArg(args, "project"), externalArg(args, "team_action")
	if externalArg(args, "action") == "crew_join" {
		action = "join"
	}
	if externalArg(args, "action") == "crew_leave" {
		action = "leave"
	}
	named, _, err := currentAgentAttachment(r.Context(), claims, externalArg(args, "agent_name"), externalArg(args, "attachment_id"))
	if err != nil || named.Attachment == nil || (named.Attachment.Provider != "claude" && named.Attachment.Provider != "codex") || strings.TrimSpace(named.Attachment.SessionID) == "" {
		crewTeamTerminalRefused(w, 403, project, named, "attachment_unavailable", "Connect this Claude Code or Codex session to AgentWorks first.")
		return
	}
	team, err := terminalCrewAdmission(project, claims.UserID)
	if err != nil {
		crewTeamTerminalRefused(w, 403, project, named, "membership_unavailable", "Choose a Crew you own or whose active Team you have joined.")
		return
	}
	if externalArg(args, "action") == "team_sync" && !slices.Contains(crewWorkActions, action) {
		crewTeamTerminalRefused(w, 400, project, named, "unsupported_action", "Use join or leave for this Crew, or a work_ action for the work log.")
		return
	}
	if action != "join" && action != "leave" {
		api.terminalTeamAction(w, r, args, team, named, action)
		return
	}
	pair, paired := crewMacPairFor(claims.UserID, project)
	// A Mac-source Crew always needs its current pair. A server Crew may be
	// joined without a downloaded Mac copy, but never returns a server path.
	if crewRelayTwin(project) && claims.UserID == team.OwnerID && !paired {
		crewTeamTerminalRefused(w, 409, project, named, "pair_unavailable", "This Mac Crew's current connection is unavailable.")
		return
	}
	var copy *crewTeamTerminalCopy
	if action == "join" && IsLocalMode() && claims.UserID == team.OwnerID {
		if !terminalSessionLiveHere(r.Context(), named.Attachment.Provider, named.Attachment.SessionID, named.Attachment.ID, named.ID) {
			crewTeamTerminalRefused(w, 409, project, named, "copy_unavailable", crewTerminalCopyUnavailable(team))
			return
		}
		roots, found, e := crewStorageRoots(crewProjectRootByID(project))
		if e != nil || !found {
			crewTeamTerminalRefused(w, 409, project, named, "copy_unavailable", "This Crew's local folder is unavailable.")
			return
		}
		root, e := crewTeamTerminalCopyPath(roots.Files, roots.Files)
		if e != nil {
			crewTeamTerminalRefused(w, 409, project, named, "copy_unavailable", "This Crew's local folder is unavailable.")
			return
		}
		copy = &crewTeamTerminalCopy{Path: root}
	}
	if action == "join" && paired && copy == nil {
		payload, _ := json.Marshal(map[string]string{"provider": named.Attachment.Provider, "session": named.Attachment.SessionID, "attachment": named.Attachment.ID, "agent": named.ID, "folder": team.Folder.ID})
		result := callCrewMacPair(r.Context(), claims.UserID, project, pair, crewChatCall{Operation: "team_terminal_copy", Args: payload}, 15*time.Second)
		var accepted crewTeamTerminalCopy
		if result.Status == 200 && json.Unmarshal(result.Body, &accepted) == nil && accepted.Path != "" {
			copy = &accepted
		} else if crewRelayTwin(project) && claims.UserID == team.OwnerID {
			crewTeamTerminalRefused(w, 409, project, named, "copy_unavailable", crewTerminalCopyUnavailable(team))
			return
		} else {
			pair = ""
		}
	}

	instructions := ""
	if action == "join" {
		setup, workspace, ownerCtx, ok := api.crewSetupOf(r.Context(), team.OwnerID, project)
		if !ok {
			crewTeamTerminalRefused(w, 503, project, named, "instructions_unavailable", "The Crew's instructions could not be read; try again.")
			return
		}
		instructions = setup.ChatInstructions
		// The owner's PROJECT_INSTRUCTIONS.md is the owner's own; a teammate gets the Team's instructions below, not a
		// copy of the owner's file (#2942).
		if claims.UserID == team.OwnerID {
			text, found, e := readFileFromWorkspace(ownerCtx, agentProfileRuntimeWorkspace(team.OwnerID, workspace)+"/PROJECT_INSTRUCTIONS.md")
			if e != nil {
				crewTeamTerminalRefused(w, 503, project, named, "instructions_unavailable", "The Crew's instructions could not be read; try again.")
				return
			}
			if found {
				instructions = strings.TrimSpace(instructions + "\n\n" + text)
			}
		}
		// Everyone who joins gets the Team's instructions as they are now (#3041). Live updates for joined coding chats
		// come with #2943.
		if text, found := committedTeamInstructions(project); found {
			if section, _ := projectinstructions.TeamSection(text); section != "" {
				instructions = strings.TrimSpace(instructions + "\n\n" + section)
			}
		}
	}

	crewTerminalJoinMu.Lock()
	defer crewTerminalJoinMu.Unlock()
	if action == "join" {
		other, _, joined, e := terminalCrewForNamed(named.ID)
		if e != nil {
			crewTeamTerminalRefused(w, 409, project, named, "membership_unavailable", "This agent's Crew membership could not be checked.")
			return
		}
		if joined && other.ProjectID != project {
			crewTeamTerminalRefused(w, 409, project, named, "already_joined", fmt.Sprintf("This agent is already in %s. Leave it first.", firstNonEmptyTrimmed(other.Name, "another Crew")))
			return
		}
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		crewTeamTerminalRefused(w, 503, project, named, "record_unavailable", "The Team is unavailable.")
		return
	}
	// Ordinary Team writes create this private directory before their atomic
	// write. A new Off registry must use the same fence, not mkdir's 0755.
	if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		crewTeamTerminalRefused(w, 503, project, named, "record_unavailable", "The Crew's private agent registry is unavailable.")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	// Membership and attachment are live authority, not the preflight snapshot.
	team, err = terminalCrewAdmission(project, claims.UserID)
	current, _, attachErr := currentAgentAttachment(r.Context(), claims, named.Name, named.Attachment.ID)
	currentPair, pairOK := crewMacPairFor(claims.UserID, project)
	if err != nil || attachErr != nil || current.ID != named.ID || (pair != "" && (!pairOK || currentPair != pair)) {
		crewTeamTerminalRefused(w, 403, project, named, "binding_changed", "This session or Team membership changed; try again.")
		return
	}
	id := ""
	newJoin := false
	if action == "leave" {
		for key, a := range team.Agents {
			if a.OwnerID == claims.UserID && a.Terminal != nil && a.Terminal.NamedAgentID == named.ID {
				id = key
				delete(team.Agents, key)
				break
			}
		}
	} else {
		known := false
		for _, a := range team.Agents {
			if a.OwnerID == claims.UserID && a.Terminal != nil && a.Terminal.NamedAgentID == named.ID {
				known = true
				break
			}
		}
		newJoin = !known
		record, joinErr := upsertCrewTeamTerminal(&team, claims.UserID, named, pair)
		if joinErr != nil {
			crewTeamTerminalRefused(w, 409, project, named, "registry_changed", joinErr.Error())
			return
		}
		id = record.ID
	}
	if action == "leave" && id == "" {
		crewTeamTerminalRefused(w, 409, project, named, "not_joined", "This session has not joined this Crew.")
		return
	}
	if copy != nil {
		copy.Folders = append([]string{}, team.Folder.Folders...)
	}
	createdRegistry := team.RegistryVersion == 0
	team.RegistryVersion = 1
	team.Revision++
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), team, 0600); err != nil {
		crewTeamTerminalRefused(w, 503, project, named, "write_failed", "The Team could not save this session.")
		return
	}
	if createdRegistry {
		log.Printf("[CREW_TEAM] project=%s event=registry_created on=false", project)
	}
	outcome := action
	if action == "join" && args["automatic"] == true {
		outcome = "auto_join" // a new chat started in this Crew's folder on the person's Mac (#2820)
	}
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s agent=%s session=%s outcome=%s", project, id, named.Attachment.SessionID, outcome)
	go pushCrewTeamRecordChanged(team)
	registered := team.Agents[id]
	if action == "join" {
		api.queueCrewOfflineNativeHome(r.Context(), team, registered)
	}
	if action == "join" && newJoin {
		go api.crewTeamNativeJoinSetup(r.Context(), claims, team, named.ID)
	}
	notice := "Rejoining keeps this agent's existing sharing and chat choices."
	if newJoin {
		notice = "Joined this Crew. Only you and your own agents can reach this session while its Team is off."
		if team.On {
			notice = "Teammates can now ask this agent; answers only. Switch to Only me in AgentWorks to hide it."
		}
	}
	if action == "leave" {
		notice = "Left this Crew; files and the AgentBridge connection are unchanged."
	}
	if action == "join" {
		notice += " Its online copy syncs automatically in the background and answers while this session is away."
	}
	if action == "join" && copy == nil {
		notice += " This Crew's files are on the server, not this Mac. Ask its agents, or add it to this Mac in AgentWorks to work on its files here."
	}
	folders := []string{}
	if copy != nil {
		folders = copy.Folders
	}
	if action == "join" && copy != nil {
		notice += " Work in the returned Crew folder."
		if team.On {
			notice += " Only the selected Team folders sync; keep AgentWorks running."
		}
	}
	if team.On {
		notice += " Team keys stay on the server."
	}
	externalJSON(w, map[string]any{"project": project, "agent_id": id, "copy": copy, "folders": folders, "team_on": team.On, "instructions": instructions, "outcome": action, "in_team": registered.InTeam && team.On, "chat_share": registered.Rule.ChatShare, "chat_share_label": crewChatShareName(registered.Rule.ChatShare), "notice": notice})
}

// Reasons are fixed codes; neither provider text nor a caller's post enters logs.
func crewTeamTerminalRefused(w http.ResponseWriter, status int, project string, named namedMessageAgent, reason, line string) {
	session := ""
	if named.Attachment != nil {
		session = named.Attachment.SessionID
	}
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s agent=%s session=%s outcome=refused reason=%s", project, named.ID, session, reason)
	externalError(w, status, "team_terminal_refused", line)
}

// runDesktopCrewChat has already admitted the exact local pair. Inspect the
// existing helper's lease and liveness; do not attach or choose a native chat.
func (api *StreamingAPI) desktopTeamTerminalCopy(ctx context.Context, project string, call crewChatCall) crewChatResult {
	fail := func(reason string) crewChatResult {
		logCrewTerminalCopyRequest(project, call.ID, "refused", reason)
		return crewChatResult{ID: call.ID, Status: 403, Body: json.RawMessage(`{"error":"The attached session or Team copy is unavailable on this Mac."}`)}
	}
	claims, b, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil {
		return fail("binding_unavailable")
	}
	var in struct {
		Provider   string `json:"provider"`
		Session    string `json:"session"`
		Attachment string `json:"attachment"`
		Agent      string `json:"agent"`
		Folder     string `json:"folder"`
	}
	if json.Unmarshal(call.Args, &in) != nil || (in.Provider != "claude" && in.Provider != "codex") || in.Session == "" || in.Attachment == "" || in.Agent == "" {
		return fail("invalid_request")
	}
	if !terminalSessionLiveHere(ctx, in.Provider, in.Session, in.Attachment, in.Agent) {
		return fail("session_not_here")
	}
	if in.Folder == "" && crewProjectOwnedByCallerStrict(claims.UserID, crewProjectRootByID(project)) {
		roots, e := api.crewSyncRoots(ctx, claims, project)
		if e != nil {
			return fail("local_roots_unavailable")
		}
		root, e := crewTeamTerminalCopyPath(roots.Files, roots.Files)
		if e != nil {
			return fail("path_unavailable")
		}
		raw, _ := json.Marshal(crewTeamTerminalCopy{Path: root})
		logCrewTerminalCopyRequest(project, call.ID, "accepted", "none")
		return crewChatResult{ID: call.ID, Status: 200, Body: raw}
	}

	dir, err := crewTeamLocalDir(claims.UserID, connection, b.PeerProjectID, in.Folder)
	if err != nil {
		return fail("copy_binding_unavailable")
	}
	local, err := readCrewTeamLocal(dir)
	if err != nil || local.Project != b.PeerProjectID || local.Connection != connection.ID || local.RemoteUser != connection.User.ID || local.LocalProject != project || !local.Downloaded {
		return fail("copy_unavailable")
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return fail("local_roots_unavailable")
	}
	root, err := crewTeamTerminalCopyPath(roots.Files, local.Root)
	if err != nil {
		return fail("path_unavailable")
	}
	// Return the actual accepted working root, not private team-working state.
	raw, err := json.Marshal(crewTeamTerminalCopy{Path: root})
	if err != nil {
		return fail("response_unavailable")
	}
	logCrewTerminalCopyRequest(project, call.ID, "accepted", "none")
	return crewChatResult{ID: call.ID, Status: 200, Body: raw}
}

// Transport proof only; not an audience grant. The shared Team reach function
// admits the sender/In-team row before using this per-row liveness adapter.
func currentCrewTeamTerminal(ctx context.Context, t crewTeam, a crewTeamRegisteredAgent) (namedMessageAgent, error) {
	return currentCrewTeamTerminalWithPair(ctx, t, a, func() (string, bool) { return crewMacPairFor(a.OwnerID, t.ProjectID) })
}

// Offline source uploads reuse the exact attachment proof but read the saved
// maker pair after canonical owned-row admission, including Team Off.
func currentCrewTeamTerminalWithPair(ctx context.Context, t crewTeam, a crewTeamRegisteredAgent, pairFor func() (string, bool)) (namedMessageAgent, error) {
	if a.Kind != "terminal" || a.Terminal == nil {
		return namedMessageAgent{}, fmt.Errorf("terminal route unavailable")
	}
	// The same named agent reattaching the same native session (a CLI update or reconnect) gets a new attachment
	// ID; the row follows it (owner, 16 Nov: a reattach must never cut Team reach). A different session, provider,
	// owner connection or Mac pair still refuses below.
	named, err := namedAgentByID(ctx, a.OwnerID, a.Terminal.NamedAgentID)
	if err != nil || named.Attachment == nil || named.Attachment.SessionID != a.ChatKey || named.Attachment.Provider != a.Terminal.Provider {
		return namedMessageAgent{}, fmt.Errorf("terminal attachment changed")
	}
	claims, err := agentMessageConnectionClaims(ctx, named.Attachment.ConnectionID)
	if err != nil || claims == nil || claims.UserID != a.OwnerID {
		return namedMessageAgent{}, fmt.Errorf("terminal connection unavailable")
	}
	current, _, err := currentAgentAttachment(ctx, claims, named.Name, named.Attachment.ID)
	if err != nil || current.ID != named.ID {
		return namedMessageAgent{}, fmt.Errorf("terminal attachment changed")
	}
	pair, ok := pairFor()
	if a.Terminal.MacPair != "" && (!ok || pair != a.Terminal.MacPair) {
		return namedMessageAgent{}, fmt.Errorf("terminal Mac pair changed")
	}
	if previous := a.Terminal.AttachmentID; previous != named.Attachment.ID {
		go crewTeamTerminalRebind(t.ProjectID, a, previous, named.Attachment.ID)
	}
	return current, nil
}

// crewTeamTerminalRebind saves the proven reattachment, off the caller's path (callers may hold the Team lock).
// It changes nothing if the row moved on meanwhile.
func crewTeamTerminalRebind(project string, a crewTeamRegisteredAgent, previous, next string) {
	err := updateCrewTeamRegisteredAgent(project, a.OwnerID, a.ID, func(row *crewTeamRegisteredAgent) {
		if row.Terminal != nil && row.Terminal.NamedAgentID == a.Terminal.NamedAgentID && row.ChatKey == a.ChatKey && row.Terminal.AttachmentID == previous {
			row.Terminal.AttachmentID = next
		}
	})
	outcome := "rebound"
	if err != nil {
		outcome = "rebind_failed"
	}
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s agent=%s session=%s event=%s old=%s new=%s", project, a.ID, a.ChatKey, outcome, previous, next)
}

// A new join is In team, with answers only and no change permission.
func newCrewTeamTerminalAgent(id, owner string, named namedMessageAgent) crewTeamRegisteredAgent {
	return crewTeamRegisteredAgent{ID: id, OwnerID: owner, Tab: "terminal-" + uuid.NewString(), ChatKey: named.Attachment.SessionID, Name: firstNonEmptyTrimmed(named.Label, named.Name), Place: "mac", Kind: "terminal", InTeam: crewTeamDefaultInTeam(nil), Rule: crewTeamDefaultRule()}
}

// Same Team operations as the screen/chat, with actor stamped from the exact
// attachment. In particular no model-chosen agent key is used for a post.
func (api *StreamingAPI) terminalTeamAction(w http.ResponseWriter, r *http.Request, args map[string]any, team crewTeam, named namedMessageAgent, action string) {
	claims := GetUserFromContext(r.Context())
	var actor crewTeamRegisteredAgent
	for _, a := range team.Agents {
		if a.Terminal != nil && a.OwnerID == claims.UserID && a.ChatKey == named.Attachment.SessionID && a.Terminal.NamedAgentID == named.ID {
			actor = a
			break
		}
	}
	if _, err := currentCrewTeamTerminal(r.Context(), team, actor); actor.ID == "" || err != nil {
		crewTeamTerminalRefused(w, 403, team.ProjectID, named, "binding_changed", "Join this Crew from this session first.")
		return
	}
	if !slices.Contains(crewWorkActions, action) {
		crewTeamTerminalRefused(w, 400, team.ProjectID, named, "unsupported_action", "Choose join, leave, work_brief or work_answer. Find and message Team agents through AgentBridge. Use AgentWorks for clashes and conversation copies.")
		return
	}
	scope := crewWorkScope{Project: team.ProjectID, Team: &team}
	if !team.On {
		scope.Team = nil // Team off: the person's own entries only
	}
	out, err := crewWorkDo(r.Context(), scope, claims.UserID, actor, crewWorkInputFromArgs(args))
	if err != nil {
		crewTeamTerminalRefused(w, 400, team.ProjectID, named, "work_refused", err.Error())
		return
	}
	externalJSON(w, out)
}

// A terminal agent has one Team identity across native session rotations.
// Privacy choices belong to that stable identity, not to a provider journal.
func upsertCrewTeamTerminal(team *crewTeam, person string, named namedMessageAgent, pair string) (crewTeamRegisteredAgent, error) {
	if named.ID == "" || named.UserID != person || named.Attachment == nil || strings.TrimSpace(named.Attachment.SessionID) == "" {
		return crewTeamRegisteredAgent{}, fmt.Errorf("Connect this exact session before joining the Team.")
	}
	var record crewTeamRegisteredAgent
	for _, a := range team.Agents {
		if a.OwnerID == person && a.Terminal != nil && a.Terminal.NamedAgentID == named.ID {
			if record.ID != "" {
				return crewTeamRegisteredAgent{}, fmt.Errorf("This terminal agent has duplicate Team entries; refresh the Team before joining.")
			}
			record = a
		}
	}
	if record.ID == "" {
		record = newCrewTeamTerminalAgent(crewTeamAgentIDPrefix+uuid.NewString(), person, named)
	}
	for _, a := range team.Agents {
		if a.ID != record.ID && a.OwnerID == person && (a.ChatKey == named.Attachment.SessionID || a.Tab == record.Tab) {
			return crewTeamRegisteredAgent{}, fmt.Errorf("This session is already registered as another Team agent.")
		}
	}
	record.ChatKey = named.Attachment.SessionID
	record.Name = firstNonEmptyTrimmed(named.Label, named.Name)
	record.Kind = "terminal"
	record.Terminal = &crewTeamTerminal{NamedAgentID: named.ID, AttachmentID: named.Attachment.ID, Provider: named.Attachment.Provider, MacPair: pair}
	if team.Agents == nil {
		team.Agents = map[string]crewTeamRegisteredAgent{}
	}
	team.Agents[record.ID] = record
	return record, nil
}

// Only server-read accepted roots reach here; a symlink must not expand them.
func crewTeamTerminalCopyPath(files, copyRoot string) (string, error) {
	base, err := filepath.EvalSymlinks(files)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(copyRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("the Team copy is outside the accepted project")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("the Team copy is unavailable")
	}
	return root, nil
}

// posts has already been filtered for this viewer by crewTeamActivityPostsFor.
// The Team preview is never a native transcript or a mailbox answer excerpt.
func crewTeamTerminalPostPreview(a crewTeamRegisteredAgent, posts crewTeamActivityPosts) crewTeamMessagePreview {
	latest := posts.Doing[a.ID]
	for _, post := range posts.Decisions {
		if post.AgentID == a.ID && post.OwnerID == a.OwnerID && post.At.After(latest.At) {
			latest = post
		}
	}
	if latest.AgentID != a.ID || latest.OwnerID != a.OwnerID || latest.Line == "" {
		return crewTeamMessagePreview{}
	}
	return crewTeamMessagePreview{LastMessage: crewTeamMessageLine(latest.Line), LastMessageAt: latest.At.UTC().Format(time.RFC3339Nano)}
}

// The owner can join an ordinary Crew before Team setup. Dashboard Run sharing
// is never membership, and an Off Team admits no other person.
func terminalCrewAdmission(project, person string) (crewTeam, error) {
	t, err := readCrewTeamOrNew(project, person)
	if err != nil || t.OwnerID == "" || ownerOfCrewProjectID(project) != t.OwnerID || !t.member(person) || !crewTeamAccountActive(person) || !crewTeamAccountActive(t.OwnerID) {
		return crewTeam{}, fmt.Errorf("Crew unavailable or not a current member")
	}
	return t, nil
}

// A native identity is proved by this Mac's existing helper; never select or
// attach a session, and never trust a path from the request.
func terminalSessionLiveHere(ctx context.Context, provider, session, lease, agent string) bool {
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return false
	}
	state, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
	if err != nil {
		return false
	}
	for _, s := range state.AgentStates {
		if s.Online && s.HostAlive && !s.ServerClosed && s.Provider == provider && s.SessionID == session && s.ID == lease && s.AgentID == agent {
			return true
		}
	}
	return false
}

// Turn hooks already report busy/idle over the exact attachment's label channel.
// Notify the existing Team projection without writing another activity registry.
func crewTeamTerminalActivityChanged(ctx context.Context, named namedMessageAgent) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t, row, joined, err := terminalCrewForNamed(named.ID)
	if err != nil || !joined || named.Attachment == nil {
		return
	}
	current, err := currentCrewTeamTerminal(ctx, t, row)
	if err != nil || current.Attachment.ID != named.Attachment.ID {
		return
	}
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s agent=%s session=%s attachment=%s outcome=activity activity=%s", t.ProjectID, row.ID, row.ChatKey, named.Attachment.ID, current.Attachment.Activity)
	pushCrewTeamBusy(t.ProjectID)
}
