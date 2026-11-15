package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
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
	named, _, err := currentAgentAttachment(r.Context(), claims, externalArg(args, "agent_name"), externalArg(args, "attachment_id"))
	if err != nil || named.Attachment == nil || named.Attachment.Provider != "claude" || strings.TrimSpace(named.Attachment.SessionID) == "" {
		externalError(w, 403, "team_terminal_refused", "Connect this Claude Code session to AgentWorks first.")
		return
	}
	project, action := externalArg(args, "project"), externalArg(args, "team_action")
	team, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(team, claims.UserID) {
		externalError(w, 403, "team_terminal_refused", "Join this Team in AgentWorks first, then try again.")
		return
	}
	if action != "join" && action != "leave" {
		api.terminalTeamAction(w, r, args, team, named, action)
		return
	}
	pair, ok := crewMacPairFor(claims.UserID, project)
	if !ok {
		externalError(w, 409, "team_terminal_refused", "Add this Team to your Mac in AgentWorks first.")
		return
	}
	var copy crewTeamTerminalCopy
	if action == "join" {
		// The Mac proves this lease exists on that Mac, then returns its admitted
		// working copy. Neither a caller path nor a guessed docs path is accepted.
		payload, _ := json.Marshal(map[string]string{"provider": named.Attachment.Provider, "session": named.Attachment.SessionID, "attachment": named.Attachment.ID, "agent": named.ID, "folder": team.Folder.ID})
		result := callCrewMacPair(r.Context(), claims.UserID, project, pair, crewChatCall{Operation: "team_terminal_copy", Args: payload}, 15*time.Second)
		if result.Status != 200 || json.Unmarshal(result.Body, &copy) != nil || copy.Path == "" {
			externalError(w, 409, "team_terminal_refused", "Keep AgentWorks and this terminal session running on the Mac where you joined the Team.")
			return
		}
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		externalError(w, 503, "team_terminal_refused", "The Team is unavailable.")
		return
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	// Membership and attachment are live authority, not the preflight snapshot.
	team, err = readCrewTeam(project)
	current, _, attachErr := currentAgentAttachment(r.Context(), claims, named.Name, named.Attachment.ID)
	currentPair, pairOK := crewMacPairFor(claims.UserID, project)
	if err != nil || attachErr != nil || !crewTeamAgentsVisible(team, claims.UserID) || current.ID != named.ID || !pairOK || currentPair != pair {
		externalError(w, 403, "team_terminal_refused", "This session or Team membership changed; try again.")
		return
	}
	id := ""
	if action == "leave" {
		for key, a := range team.Agents {
			if a.OwnerID == claims.UserID && a.Terminal != nil && a.Terminal.NamedAgentID == named.ID {
				id = key
				delete(team.Agents, key)
				break
			}
		}
	} else {
		record, joinErr := upsertCrewTeamTerminal(&team, claims.UserID, named, pair)
		if joinErr != nil {
			externalError(w, 409, "team_terminal_refused", joinErr.Error())
			return
		}
		id = record.ID
	}
	copy.Folders = append([]string(nil), team.Folder.Folders...)
	team.Revision++
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), team, 0600); err != nil {
		externalError(w, 503, "team_terminal_refused", "The Team could not save this session.")
		return
	}
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s agent=%s session=%s outcome=%s", project, id, named.Attachment.SessionID, action)
	go pushCrewTeamRecordChanged(team)
	externalJSON(w, map[string]any{"project": project, "agent_id": id, "copy": copy, "outcome": action, "notice": "New sessions start Only me. Choose In team in AgentWorks to let teammates ask; Answers only is the default. Keep AgentWorks running for sync. Work only in the selected Team folders; Team keys stay on the server."})
}

// runDesktopCrewChat has already admitted the exact local pair. Inspect the
// existing helper's lease and liveness; do not attach or choose a native chat.
func (api *StreamingAPI) desktopTeamTerminalCopy(ctx context.Context, project string, call crewChatCall) crewChatResult {
	fail := func() crewChatResult {
		return crewChatResult{ID: call.ID, Status: 403, Body: json.RawMessage(`{"error":"The attached session or Team copy is unavailable on this Mac."}`)}
	}
	claims, b, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil {
		return fail()
	}
	var in struct {
		Provider   string `json:"provider"`
		Session    string `json:"session"`
		Attachment string `json:"attachment"`
		Agent      string `json:"agent"`
		Folder     string `json:"folder"`
	}
	if json.Unmarshal(call.Args, &in) != nil || in.Provider != "claude" || in.Session == "" || in.Attachment == "" || in.Agent == "" {
		return fail()
	}
	bridge, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return fail()
	}
	state, err := agentworksclient.CallLocalBridge(ctx, bridge, agentworksclient.LocalBridgeRequest{Action: "list"})
	if err != nil {
		return fail()
	}
	exact := false
	for _, s := range state.AgentStates {
		if s.Online && s.HostAlive && !s.ServerClosed && s.Provider == in.Provider && s.SessionID == in.Session && s.ID == in.Attachment && s.AgentID == in.Agent {
			exact = true
			break
		}
	}
	if !exact {
		return fail()
	}
	dir, err := crewTeamLocalDir(claims.UserID, connection, b.PeerProjectID, in.Folder)
	if err != nil {
		return fail()
	}
	local, err := readCrewTeamLocal(dir)
	if err != nil || local.Project != b.PeerProjectID || local.Connection != connection.ID || local.RemoteUser != connection.User.ID || local.LocalProject != project || !local.Downloaded {
		return fail()
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return fail()
	}
	root, err := crewTeamTerminalCopyPath(roots.Files, local.Root)
	if err != nil {
		return fail()
	}
	// Return the actual accepted working root, not private team-working state.
	raw, err := json.Marshal(crewTeamTerminalCopy{Path: root})
	if err != nil {
		return fail()
	}
	return crewChatResult{ID: call.ID, Status: 200, Body: raw}
}

// Transport proof only; not an audience grant. The shared Team reach function
// admits the sender/In-team row before using this per-row liveness adapter.
func currentCrewTeamTerminal(ctx context.Context, t crewTeam, a crewTeamRegisteredAgent) (namedMessageAgent, error) {
	if a.Kind != "terminal" || a.Terminal == nil {
		return namedMessageAgent{}, fmt.Errorf("terminal route unavailable")
	}
	named, err := namedAgentByID(ctx, a.OwnerID, a.Terminal.NamedAgentID)
	if err != nil || named.Attachment == nil || named.Attachment.ID != a.Terminal.AttachmentID || named.Attachment.SessionID != a.ChatKey || named.Attachment.Provider != a.Terminal.Provider {
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
	pair, ok := crewMacPairFor(a.OwnerID, t.ProjectID)
	if !ok || pair != a.Terminal.MacPair {
		return namedMessageAgent{}, fmt.Errorf("terminal Mac pair changed")
	}
	return current, nil
}

// Same defaults as an app agent's explicit Only-me/Answers-only choices.
func newCrewTeamTerminalAgent(id, owner string, named namedMessageAgent) crewTeamRegisteredAgent {
	return crewTeamRegisteredAgent{ID: id, OwnerID: owner, Tab: "terminal-" + uuid.NewString(), ChatKey: named.Attachment.SessionID, Name: firstNonEmptyTrimmed(named.Label, named.Name), Place: "mac", Kind: "terminal", Rule: crewTeamAgentRule{ChatShare: crewChatShareAgent}}
}

// Same Team operations as the screen/chat, with actor stamped from the exact
// attachment. In particular no model-chosen agent key is used for a post.
func (api *StreamingAPI) terminalTeamAction(w http.ResponseWriter, r *http.Request, args map[string]any, team crewTeam, named namedMessageAgent, action string) {
	claims := GetUserFromContext(r.Context())
	var actor crewTeamRegisteredAgent
	for _, a := range team.Agents {
		if a.Terminal != nil && a.OwnerID == claims.UserID && a.ChatKey == named.Attachment.SessionID && a.Terminal.NamedAgentID == named.ID && a.Terminal.AttachmentID == named.Attachment.ID {
			actor = a
			break
		}
	}
	if _, err := currentCrewTeamTerminal(r.Context(), team, actor); actor.ID == "" || err != nil {
		externalError(w, 403, "team_terminal_refused", "Join this Team from this session first.")
		return
	}
	switch action {
	case "doing", "decision":
		post, err := writeCrewTeamActivityPostAs(r.Context(), team.ProjectID, claims.UserID, actor, action, externalArg(args, "line"), externalArg(args, "why"), "")
		if err != nil {
			externalError(w, 400, "team_terminal_refused", err.Error())
			return
		}
		externalJSON(w, post)
	default:
		externalError(w, 400, "team_terminal_refused", "Choose join, leave, doing or decision. Find and message Team agents through AgentBridge. Use AgentWorks for clashes and conversation copies.")
	}
}

// A terminal agent has one Team identity across native session rotations.
// Privacy choices belong to that stable identity, not to a provider journal.
func upsertCrewTeamTerminal(team *crewTeam, person string, named namedMessageAgent, pair string) (crewTeamRegisteredAgent, error) {
	if named.ID == "" || named.UserID != person || named.Attachment == nil || strings.TrimSpace(named.Attachment.SessionID) == "" || pair == "" {
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
