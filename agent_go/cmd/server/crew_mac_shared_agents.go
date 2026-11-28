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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Sharing a Mac Crew's agents (PLAT-856-509, owner 3 Nov): the owner ticks side tabs that other people's agents may
// message while the Mac is on, each with its own audience. The Mac keeps its choice and pushes the same list to its
// online copy; the server decides who is in the audience, the Mac decides which of its tabs are shared. A message
// runs on the Mac in that tab, in Run mode, framed as someone else's words, and its sender reads back only their own
// exchange. The owner decides which tabs, Main included; unshared tabs are never reached.

type macSharedAgent struct {
	Tab      string   `json:"tab"`
	Name     string   `json:"name"`
	Purpose  string   `json:"purpose"`
	Examples []string `json:"examples,omitempty"`
	Audience string   `json:"audience"` // team: shared through the Team (the Team record decides In team)
	Since    string   `json:"since,omitempty"`
	// Address is how other people's agents message it: agent:<owner>/<address>. The server gives it once and keeps it
	// across renames; Off keeps a turned-off entry only to keep its address for this owner.
	Address string `json:"address,omitempty"`
	Off     bool   `json:"off,omitempty"`
	// The agent's "When others ask" rule lives in the server's Team record (crew_when_others_ask.go), sent with
	// every message to the Mac.
}

const macSharedAgentsMax = maxProjectChatsPerRequest

func macSharedAgentsPath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid project identity")
	}
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", project+"-agents.json"), err
}

// readMacSharedAgents is the Crew's shared agents now (turned-off entries left out).
func readMacSharedAgents(project string) []macSharedAgent {
	out := []macSharedAgent{}
	for _, agent := range readAllMacSharedAgents(project) {
		if !agent.Off {
			out = append(out, agent)
		}
	}
	return out
}

func readAllMacSharedAgents(project string) []macSharedAgent {
	path, err := macSharedAgentsPath(project)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var agents []macSharedAgent
	if json.Unmarshal(raw, &agents) != nil {
		return nil
	}
	return agents
}

// cleanMacSharedAgents validates a list the same way on both sides and keeps each tab's first shared time.
func cleanMacSharedAgents(in, previous []macSharedAgent) ([]macSharedAgent, error) {
	if len(in) > macSharedAgentsMax {
		return nil, fmt.Errorf("share at most %d agents", macSharedAgentsMax)
	}
	since := map[string]string{}
	for _, agent := range previous {
		since[agent.Tab] = agent.Since
	}
	out := []macSharedAgent{}
	for _, agent := range in {
		agent.Name, agent.Purpose = crewMacTabName(agent.Name), strings.Join(strings.Fields(agent.Purpose), " ")
		switch {
		case !crewMacTabID(agent.Tab):
			return nil, fmt.Errorf("choose one of this project's tabs")
		case agent.Name == "":
			return nil, fmt.Errorf("an agent needs a name")
		case len([]rune(agent.Purpose)) > productAgentPurposeMaxRunes:
			return nil, fmt.Errorf("%s's purpose is longer than one line (at most %d characters)", agent.Name, productAgentPurposeMaxRunes)
		case agent.Audience == "":
			return nil, fmt.Errorf("choose who can message %s", agent.Name)
		case agent.Audience != "" && agent.Audience != "team":
			return nil, fmt.Errorf("choose Shared with team or Not shared for %s", agent.Name)
		}
		if slices.ContainsFunc(out, func(a macSharedAgent) bool { return a.Tab == agent.Tab }) {
			continue
		}
		agent.Since = since[agent.Tab]
		if agent.Since == "" {
			agent.Since = time.Now().UTC().Format(time.RFC3339Nano)
		}
		out = append(out, agent)
	}
	return out, nil
}

var macSharedAgentsMu sync.Map // path -> *sync.Mutex

func macSharedAgentsMutex(path string) *sync.Mutex {
	mu, _ := macSharedAgentsMu.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func writeMacSharedAgents(project string, agents []macSharedAgent) (result error) {
	defer func() {
		if result == nil {
			publishCrewPanelChanged(project)
		}
	}()
	path, err := macSharedAgentsPath(project)
	if err != nil {
		return err
	}
	mu := macSharedAgentsMutex(path)
	mu.Lock()
	defer mu.Unlock()
	if len(agents) == 0 {
		if err = os.Remove(path); err == nil || os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, agents, 0600)
}

// shareMacCrewAgents stores the Mac's list on its online copy: only the main owner, from the Mac that holds the pair,
// while the Crew lives on the Mac.
func (api *StreamingAPI) shareMacCrewAgents(w http.ResponseWriter, r *http.Request, project string, input crewSyncRequest) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, project)
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	b, err := crewroot.ReadSync(roots.ID)
	if err != nil || b == nil || b.Mode != "relay" || b.Side != "server" || b.Editing != "local" || input.PairID == "" || b.PairID != input.PairID {
		externalError(w, 409, "wrong_pair", "Share this project's agents from the Mac that holds it.")
		return
	}
	previous := readAllMacSharedAgents(roots.ID)
	agents, err := cleanMacSharedAgents(input.Agents, previous)
	if err != nil {
		externalError(w, 400, "invalid_agents", err.Error())
		return
	}
	agents = assignMacAgentAddresses(r.Context(), claims.UserID, roots.ID, agents, previous)
	if err = writeMacSharedAgents(roots.ID, agents); err != nil {
		externalError(w, 500, "save_failed", "The shared agents could not be saved. Try again.")
		return
	}
	log.Printf("[CREW_SHARED_AGENT] project=%s owner=%s shared=%d action=set", roots.ID, logUsernameForUserID(claims.UserID), len(agents))
	externalJSON(w, map[string]any{"project_id": roots.ID, "agents": readMacSharedAgents(roots.ID), "agent_owner": shareOwnerLabel(claims.UserID)})
}

func directoryUsers(dir *userDirectory) []string {
	if dir == nil {
		return nil
	}
	ids := make([]string, 0, len(dir.Users))
	for _, user := range dir.Users {
		if !user.Disabled {
			ids = append(ids, user.ID)
		}
	}
	return ids
}

// macSharedAgentFor is the server's decision for a person or their agent (the website, their AI app's workspace ask,
// Shared with me, an agent message): the agent the caller may message on this Mac Crew, if any. Other people reach a
// Mac agent only through its Team (owner, 28 Nov: Discovery on AgentBridge removed).
func macSharedAgentFor(project, tab, caller, owner string) (macSharedAgent, string) {
	// A joined online project has no account of its own: its main owner's decides (crewTeamAskerActive).
	if _, project := crewTeamMemberProjectID(caller); project && !crewTeamAskerActive(caller) {
		return macSharedAgent{}, "no_access"
	} else if claims := (&UserClaims{UserID: caller}); !project && (userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, crewProfileID)) {
		return macSharedAgent{}, "no_access"
	}
	agents := readMacSharedAgents(project)
	// The relay advertises native bindings, never Team permission. Only the
	// server's Team record decides whether a teammate may use this agent.
	if t, err := liveCrewTeam(project); err == nil && t.OwnerID == owner && caller != owner && crewTeamMemberCurrent(t, caller) && t.agentInTeam(owner, tab) {
		for _, a := range agents {
			if a.Tab == tab {
				return a, ""
			}
		}
	}
	return macSharedAudience(agents, tab, caller, owner, nil)
}

// macSharedAudience: whether the shared list lets this caller message the tab. A Mac agent reaches another person
// only through Team (owner, 5 Nov): Team on, the caller a joined member, the agent In team.
func macSharedAudience(agents []macSharedAgent, tab, caller, owner string, members []string) (macSharedAgent, string) {
	if caller == "" || caller == owner {
		return macSharedAgent{}, "owner"
	}
	for _, agent := range agents {
		if agent.Tab != tab {
			continue
		}
		if agent.Audience == "team" && slices.Contains(members, caller) {
			return agent, ""
		}
		return macSharedAgent{}, "not_in_audience"
	}
	return macSharedAgent{}, "not_shared"
}

// macMainTab is how the shared list names a Crew's Main chat (owner, 3 Nov: Main can be shared too).
const macMainTab = "main"

// macSharedAgentChatKey is the conversation key of a shared tab: the project itself for Main.
func macSharedAgentChatKey(project, tab string) string {
	if tab == macMainTab {
		return project
	}
	return project + codeChatSideMarker + tab
}

// macCrewAddressTab is the agent a message to the Crew itself reaches: Main while it is In team, else the owner's first shared agent (owner, 6 Nov: Main can be off).
func macCrewAddressTab(project string) string {
	if IsLocalMode() {
		return macMainTab
	}
	t, err := liveCrewTeam(project)
	if err != nil || !t.On {
		return macMainTab
	}
	first := ""
	for _, agent := range readMacSharedAgents(project) {
		inTeam := t.agentInTeam(t.OwnerID, agent.Tab)
		if agent.Tab == macMainTab && inTeam {
			return macMainTab
		}
		if inTeam && first == "" {
			first = agent.Tab
		}
	}
	return firstNonEmptyTrimmed(first, macMainTab)
}

// macSharedAgentTab reads the tab id from a chat_id: "<project>:chat:<id>", the bare id, or the project (the Crew
// itself, macCrewAddressTab). A key that names Main itself stays Main, so an unshared Main is refused as such.
func macSharedAgentTab(project, chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == project {
		return macCrewAddressTab(project)
	}
	if base, id, ok := strings.Cut(chatID, codeChatSideMarker); ok {
		if base != project {
			return ""
		}
		chatID = id
	}
	if !crewMacTabID(chatID) {
		return ""
	}
	// Main follows the saved role pointer, including a migrated Builder chat.
	if project+codeChatSideMarker+chatID == crewOwnerMainChatKey(project) {
		return macMainTab
	}
	return chatID
}

// crewOwnerMainChatKey returns only the manifest's Main backed by the owner's own registry.
// A registered side chat or another person's Main is never enough.
func crewOwnerMainChatKey(project string) string {
	owner, root := ownerOfCrewProjectID(project), crewProjectRootByID(project)
	if owner == "" || root == "" {
		return ""
	}
	ctx := context.Background()
	manifest, err := readCrewProjectMetadata(ctx, crewProfileID, root)
	if err != nil || manifest.ID != project || manifest.MainChatKey == "" {
		return ""
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
	if err != nil {
		return ""
	}
	key := manifest.MainChatKey
	record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)]
	if !ok || record.ConversationKey != key || !validProjectMainRecord(owner, crewProfileID, project, root, record) {
		return ""
	}
	return key
}

// externalMacSharedAgent answers another person's ask or read for an agent shared from a Crew on its owner's Mac.
func (api *StreamingAPI) externalMacSharedAgent(w http.ResponseWriter, r *http.Request, action string, args map[string]any, project crewProjectBinding, manifest productProjectManifest) bool {
	if project.OwnerID == GetUserFromContext(r.Context()).UserID && !project.CoOwner {
		return false
	}
	return api.macSharedAgentCall(w, r, action, args, project.OwnerID, manifest.ID)
}

// externalMacSharedAgentByID serves a person who has no access to the Crew itself, only to agents shared from it:
// their ask and read reach the same audience check (PLAT-856-509). Anything else stays "not found".
func (api *StreamingAPI) externalMacSharedAgentByID(w http.ResponseWriter, r *http.Request, action string, args map[string]any) bool {
	if IsLocalMode() || (action != "ask" && action != "read_chat" && action != "reply_input") || externalArg(args, "chat_id") == "" {
		return false
	}
	id := workspaceTokenProjectID(strings.TrimSpace(externalArg(args, "workspace_id")))
	claims := GetUserFromContext(r.Context())
	if id == "" || !crewRelayTwin(id) || !workspaceOriginAllowed(claims, crewProfileID) {
		return false
	}
	// A connection limited to some Crews keeps that limit here too.
	if claims.AccessToken != nil && !workspaceDashboardTokenAllows(claims, crewProfileID, id) {
		return false
	}
	owner := ownerOfCrewProjectID(id)
	if owner == "" || owner == claims.UserID {
		return false
	}
	return api.macSharedAgentCall(w, r, action, args, owner, id)
}

func (api *StreamingAPI) macSharedAgentCall(w http.ResponseWriter, r *http.Request, action string, args map[string]any, owner, projectID string) bool {
	claims := GetUserFromContext(r.Context())
	manifest := productProjectManifest{ID: projectID, Product: crewProfileID}
	project := crewProjectBinding{OwnerID: owner}
	tab := macSharedAgentTab(manifest.ID, externalArg(args, "chat_id"))
	agent, reason := macSharedAgentFor(manifest.ID, tab, claims.UserID, project.OwnerID)
	if reason != "" {
		log.Printf("[CREW_SHARED_AGENT] project=%s user=%s tab=%q action=%s refused reason=%s", manifest.ID, logUsernameForUserID(claims.UserID), tab, action, reason)
		if team, err := readCrewTeam(projectID); err == nil && crewTeamAgentsVisible(team, claims.UserID) && claims.UserID != owner {
			externalError(w, 403, "agent_unavailable", "This agent is no longer shared with the team.")
		} else {
			externalError(w, 403, "crew_on_mac", "This project lives on its owner's Mac and isn't shared with you.")
		}
		return true
	}
	if action != "ask" && action != "read_chat" && action != "reply_input" {
		externalError(w, 403, "tab_owner", "Send this agent a message; its owner controls its settings and confirmations.")
		return true
	}
	if token := claims.AccessToken; token != nil && !token.Allows(map[bool]string{true: "crews:run", false: "crews:read"}[action == "ask" || action == "reply_input"]) {
		externalError(w, 403, "insufficient_scope", "This connection cannot use this agent.")
		return true
	}
	// A Team is a shared room (owner, 5 Nov): members watch an in-team agent but never type into it from the website;
	// they reach it from their own agent (an agent message, or their own AI app over MCP).
	if (action == "ask" || action == "reply_input") && crewTeamMember(manifest.ID, claims.UserID) && claims.UserID != project.OwnerID && claims.AccessToken == nil {
		log.Printf("[CREW_SHARED_AGENT] project=%s user=%s tab=%q action=%s refused reason=team_watch_only", manifest.ID, logUsernameForUserID(claims.UserID), tab, action)
		externalError(w, 403, "team_watch_only", "You can watch this agent. To ask it something, ask from your own agent.")
		return true
	}
	if action == "ask" {
		message := strings.TrimSpace(externalArg(args, "message"))
		if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
			externalError(w, 400, "invalid_arguments", "Give a message within the chat size limit.")
			return true
		}
	}
	// MCP teammate asks use exactly the admitted registry/message route too;
	// they never reach the maker's native agent_ask/resume branch below.
	if action == "ask" && !IsLocalMode() {
		if team, e := readCrewTeam(projectID); e == nil && crewTeamAgentsVisible(team, claims.UserID) && claims.UserID != owner {
			registered, ok := team.registeredAgent(owner, tab)
			if !ok || !registered.InTeam {
				externalError(w, 403, "agent_unavailable", "This agent is no longer shared with the team.")
				return true
			}
			profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
			if err != nil {
				externalCommunicationError(w, 403, "agent_unavailable", err)
				return true
			}
			binding, err := resolveCrewProjectBinding(r.Context(), owner, profile, projectID, "")
			if err != nil || binding.OwnerID != owner {
				externalError(w, 403, "agent_unavailable", "This agent's project is unavailable.")
				return true
			}
			// Preserve the validated Crew root in the mailbox endpoint. Delivery
			// rechecks it even though the answer never runs in the maker's folder.
			target, err := api.resolveTeamAgentTarget(r.Context(), triggerTarget{Kind: triggerCallerCrew, Path: binding.Binding.WorkspacePath, CrewID: projectID, CrewProfile: crewProfileID, CrewOwner: owner}, registered.ID)
			if err != nil {
				externalCommunicationError(w, 403, "agent_unavailable", err)
				return true
			}
			caller, err := api.externalMessageCaller(r.Context(), claims, args)
			if err != nil {
				externalCommunicationError(w, 400, "message_refused", err)
				return true
			}
			if err = api.externalAuthorizeMessagePeer(r.Context(), claims, targetMessageEndpoint(claims.UserID, target), true); err != nil {
				externalCommunicationError(w, 403, "access_denied", err)
				return true
			}
			files, err := prepareMessageFiles(args)
			if err != nil {
				externalError(w, 400, "message_refused", err.Error())
				return true
			}
			receipt, err := api.sendAgentMessage(context.WithoutCancel(r.Context()), claims.UserID, caller, target, externalArg(args, "message"), externalArg(args, "inbox_id"), externalArg(args, "submission_id"), files...)
			if err != nil {
				externalCommunicationError(w, 400, "message_refused", err)
				return true
			}
			externalJSON(w, receipt)
			return true
		}
	}
	operation := map[bool]string{true: "agent_ask", false: "agent_read"}[action == "ask"]
	if action == "reply_input" {
		operation = "agent_answer"
	}
	events, _ := args["events"].(bool) // the chat's own events for those turns, so the person sees the owner's chat view
	// How it came, for the receiving chat's "From …" line: the person's own AI app over MCP, or the website.
	channel := "website"
	if claims.AccessToken != nil {
		channel = "MCP"
	}
	raw, _ := json.Marshal(withAgentRuleArgs(r.Context(), map[string]any{"sender_id": claims.UserID, "after": externalInt(args, "after", 0), "wait_seconds": externalInt(args, "wait_seconds", 0), "events": events, "sender_channel": channel},
		manifest.ID, project.OwnerID, macSharedAgentChatKey(manifest.ID, agent.Tab)))
	call := crewChatCall{Operation: operation, Tab: agent.Tab, Message: externalArg(args, "message"), Caller: crewOwnerDisplayName(sanitizeUserIDForPath(claims.UserID)), Args: raw,
		SubmissionID: externalArg(args, "submission_id")}
	if operation == "agent_ask" && call.SubmissionID == "" {
		// Every ask carries an id the Mac runs once: a timed-out ask retried (or held and delivered later) with it is
		// not run twice. The reply names it for such a retry.
		call.SubmissionID = uuid.NewString()
	}
	if action == "reply_input" {
		call.Answer, _ = json.Marshal(map[string]any{"provider": args["provider"], "prompt_id": args["request_id"], "answers": args["answers"], "auto": args["auto"]})
	}
	result := callCrewMacFor(r.Context(), project.OwnerID, manifest.ID, call, externalCrewWait(args)+40*time.Second)
	log.Printf("[CREW_SHARED_AGENT] project=%s user=%s tab=%s action=%s mac_status=%d", manifest.ID, logUsernameForUserID(claims.UserID), agent.Tab, action, result.Status)
	// An Only me agent (or one with no server copy yet) waits for its Mac.
	if result.Status == http.StatusServiceUnavailable && !IsLocalMode() && action == "ask" {
		if err := holdForMac(manifest.ID, "mac_off", call, "", 0); err != nil {
			if errors.Is(err, errCrewMacHeldFull) {
				externalError(w, 429, "too_many_waiting", "Too many of your messages are already waiting for this Mac. Try again when it is back.")
				return true
			}
			externalError(w, 503, "message_not_saved", "Your message could not be saved. Please try again.")
			return true
		}
		externalJSON(w, map[string]any{"status": "waiting", "workspace_id": manifest.Product + ":" + manifest.ID, "chat_id": macSharedAgentChatKey(manifest.ID, agent.Tab), "name": agent.Name,
			"message": shareOwnerLabel(project.OwnerID) + "'s Mac is off; your message is delivered when it's back. Read this chat for its answer."})
		return true
	}
	// A held ask of this person that could not be delivered is said once, on their next read of this chat.
	if action == "read_chat" {
		if notices := takeHeldAskRefusals(manifest.ID, agent.Tab, claims.UserID); len(notices) > 0 {
			if result.Status != http.StatusOK {
				externalJSON(w, map[string]any{"chat_id": macSharedAgentChatKey(manifest.ID, agent.Tab), "name": agent.Name, "notices": notices})
				return true
			}
			log.Printf("[CREW_MAC_WAITING] project=%s tab=%s action=refusal_shown count=%d", manifest.ID, agent.Tab, len(notices))
			var withNotices map[string]any
			if json.Unmarshal(result.Body, &withNotices) == nil {
				withNotices["notices"] = notices
				result.Body, _ = json.Marshal(withNotices)
			}
		}
	}
	if result.Status == http.StatusServiceUnavailable {
		externalError(w, 503, "owner_mac_off", "Its owner's Mac is off. Try again later.")
		return true
	}
	if result.Status == http.StatusGatewayTimeout && action == "ask" {
		body, _ := json.Marshal(map[string]any{"submission_id": call.SubmissionID, "chat_id": macSharedAgentChatKey(manifest.ID, agent.Tab),
			"error": "The Mac did not answer in time; it may still be working on it. Read this chat for its answer, or ask again with the same submission_id so it runs once."})
		writeCrewChatResult(w, crewChatResult{Status: http.StatusGatewayTimeout, Body: body})
		return true
	}
	var out map[string]any
	if result.Status == http.StatusOK && json.Unmarshal(result.Body, &out) == nil {
		out["workspace_id"], out["chat_id"], out["name"], out["purpose"], out["can_write"], out["shared"] = manifest.Product+":"+manifest.ID, macSharedAgentChatKey(manifest.ID, agent.Tab), agent.Name, agent.Purpose, false, true
		if action == "ask" {
			out["submission_id"] = call.SubmissionID
		}
		externalJSON(w, out)
		return true
	}
	writeCrewChatResult(w, result)
	return true
}

// macSharedAgentRows lists the Mac-shared agents this caller (a person or their agent) may message: Shared with me,
// the agent directory and the agents their agents can message.
func macSharedAgentRows(caller string) []map[string]any {
	state, err := crewroot.StateRoot()
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(state, "crew-sync", "*-agents.json"))
	rows := []map[string]any{}
	for _, path := range matches {
		project := strings.TrimSuffix(filepath.Base(path), "-agents.json")
		b, err := crewroot.ReadSync(project)
		if err != nil || b == nil || b.Mode != "relay" || b.Side != "server" || b.Editing != "local" {
			continue
		}
		owner, title := crewOwnerAndTitle(project)
		if owner == "" {
			continue
		}
		online := crewMacSocketFor(owner, project, b.PairID) != nil
		ownerShort := strings.SplitN(logUsernameForUserID(owner), "@", 2)[0]
		for _, agent := range readMacSharedAgents(project) {
			if _, reason := macSharedAgentFor(project, agent.Tab, caller, owner); reason != "" {
				continue
			}
			presence, available := "Its owner's Mac is off; messages wait for it", online
			if online {
				presence = "Answers now, on its owner's Mac"
			}
			target := "workspace:" + crewProfileID + ":" + project
			rows = append(rows, map[string]any{"kind": "crew_agent", "type": "Project agent", "id": target + "/agent/" + agent.Tab, "target": target, "crew_id": project,
				"chat_id": macSharedAgentChatKey(project, agent.Tab), "name": agent.Name, "crew_name": ownerShort + "'s Mac", "purpose": agent.Purpose, "examples": agent.Examples, "presence": presence, "your_access": "run",
				"reach":       "workspace action=ask with workspace_id=" + crewProfileID + ":" + project + " and this chat_id, then read_chat for its answer",
				"owner_short": ownerShort, "crew_title": title, "available": available, "watch_only": crewTeamMember(project, caller)})
		}
	}
	return rows
}

// crewProjectRootByID is a Crew's folder by its project id, from the owner registry.
func crewProjectRootByID(project string) string {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return ""
	}
	for _, record := range records {
		if record.Product == crewProfileID && record.ProjectID == project {
			return firstNonEmptyTrimmed(record.CanonicalRoot, workspaceref.SharedProjectPath(record.Folder))
		}
	}
	return ""
}

// ownerOfCrewProjectID is the main owner of a Crew by its project id, from the owner registry.
// crewOwnerAndTitle is a Crew's owner and its name as its owner sees it (product.json's title), else its folder's name
// without the id ("test" for "test-d9edf837").
func crewOwnerAndTitle(project string) (string, string) {
	record, ok := crewOwnerRecord(project)
	if !ok {
		return "", ""
	}
	if manifest, err := readCrewProjectManifests(context.Background(), crewProfileID, crewProjectRootByID(project)); err == nil && strings.TrimSpace(manifest.Title) != "" {
		return record.OwnerID, strings.TrimSpace(manifest.Title)
	}
	title := record.Folder
	if at := strings.LastIndex(title, "-"); at > 0 {
		title = title[:at]
	}
	return record.OwnerID, strings.ReplaceAll(title, "-", " ")
}

func crewOwnerRecord(project string) (projectOwnerRecord, bool) {
	return defaultProjectOwners().FindByProjectID(crewProfileID, project)
}

func ownerOfCrewProjectID(project string) string {
	record, _ := crewOwnerRecord(project)
	return record.OwnerID
}

// macAgentRow is one agent on the Mac app's Access page: Main or a side tab, with its purpose from the tab itself.
type macAgentRow struct {
	Tab      string   `json:"tab"`
	Name     string   `json:"name"`
	Purpose  string   `json:"purpose"`
	Examples []string `json:"examples,omitempty"`
}

func (api *StreamingAPI) macAgentRows(ctx context.Context, claims *UserClaims, project string) ([]macAgentRow, error) {
	tabs, main, err := api.macCrewAgents(ctx, claims, project)
	if err != nil {
		return nil, err
	}
	// The owner decides which agents to share, the default one (Main) included (owner, 3 Nov). A purpose is optional.
	rows := []macAgentRow{{Tab: macMainTab, Name: defaultAgentName(main.Name, false), Examples: main.Examples, Purpose: strings.TrimSpace(main.Purpose)}}
	for _, tab := range tabs {
		if _, id, ok := strings.Cut(tab.ConversationKey, codeChatSideMarker); ok {
			rows = append(rows, macAgentRow{Tab: id, Name: tab.Name, Purpose: tab.Purpose, Examples: tab.Examples})
		}
	}
	return rows, nil
}

// pushMacSharedAgents saves the owner's list here and on the online copy. Name and purpose always come from the tab
// (one purpose, owner 3 Nov), never from the screen's copy.
func (api *StreamingAPI) pushMacSharedAgents(ctx context.Context, claims *UserClaims, project string, binding *crewroot.SyncBinding, connection desktopConnection, rows []macAgentRow, agents []macSharedAgent) (int, string, error) {
	if binding == nil || binding.Mode != "relay" || binding.Editing != "local" {
		return 409, "reach_off", fmt.Errorf("Turn on Use from phone and web first.")
	}
	previous := readMacSharedAgents(project)
	for i := range agents {
		at := slices.IndexFunc(rows, func(row macAgentRow) bool { return row.Tab == agents[i].Tab })
		if at < 0 {
			return 400, "invalid_agents", fmt.Errorf("Choose one of this project's agents.")
		}
		agents[i].Name, agents[i].Purpose = rows[at].Name, rows[at].Purpose
		agents[i].Examples = rows[at].Examples
	}
	clean, err := cleanMacSharedAgents(agents, previous)
	if err != nil {
		return 400, "invalid_agents", err
	}
	reply, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "share_agents", PairID: binding.PairID, Agents: clean})
	if err != nil {
		return 409, crewSyncErrorCode(err), err
	}
	// The server names each agent's address; the Mac shows it on the card.
	for i := range clean {
		for _, named := range reply.Agents {
			if named.Tab == clean[i].Tab && named.Address != "" && reply.AgentOwner != "" {
				clean[i].Address = reply.AgentOwner + "/" + named.Address
			}
		}
	}
	if err = writeMacSharedAgents(project, clean); err != nil {
		return 500, "save_failed", fmt.Errorf("The shared agents could not be saved. Try again.")
	}
	log.Printf("[CREW_SHARED_AGENT] local project=%s server_project=%s shared=%d action=set", project, binding.PeerProjectID, len(clean))
	// A newly shared agent's chat goes to the server now, not after its next turn, so a copy can be sent at once
	// (#3218).
	go func(user string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := api.syncCrewOfflineAppProjectNow(ctx, user, project); err != nil {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s event=share_sync_failed error_type=%T", project, err)
		}
	}(claims.UserID)
	return 200, "", nil
}

// desktopShareMacCrewAgents is the Mac app's Access page for shared agents: the Crew's agents with their sharing.
func (api *StreamingAPI) desktopShareMacCrewAgents(w http.ResponseWriter, r *http.Request, claims *UserClaims, project string, binding *crewroot.SyncBinding, connection desktopConnection, action string, agents []macSharedAgent) {
	rows, err := api.macAgentRows(r.Context(), claims, project)
	if err != nil {
		externalError(w, 409, "tabs_unavailable", err.Error())
		return
	}
	if action == "share_agents" {
		if status, code, err := api.pushMacSharedAgents(r.Context(), claims, project, binding, connection, rows, agents); err != nil {
			externalError(w, status, code, err.Error())
			return
		}
	} else if binding != nil && binding.Mode == "relay" {
		// A new agent joins the Team's list, or a purpose changed in its tab since: the online copy follows.
		if next, changed := crewTeamRoster(rows, readMacSharedAgents(project), readCrewTeamState(project).TeamOn); changed || macSharedPurposesStale(rows, next) {
			if _, _, err := api.pushMacSharedAgents(r.Context(), claims, project, binding, connection, rows, next); err != nil {
				log.Printf("[CREW_SHARED_AGENT] local project=%s refresh failed: %v", project, err)
			}
		}
	}
	externalJSON(w, map[string]any{"tabs": rows, "agents": readMacSharedAgents(project)})
}

// macSharedPurposesStale: a shared agent's saved name or purpose differs from its tab's now. A purpose cleared in the
// tab counts too (a purpose is optional), so the online copy's list drops it as well.
func macSharedPurposesStale(rows []macAgentRow, agents []macSharedAgent) bool {
	for _, agent := range agents {
		for _, row := range rows {
			if row.Tab == agent.Tab && (row.Purpose != agent.Purpose || row.Name != agent.Name) {
				return true
			}
		}
	}
	return false
}

// refreshMacSharedPurposes follows a purpose change of a shared agent on this Mac to its online copy.
func (api *StreamingAPI) refreshMacSharedPurposes(ctx context.Context, project string) {
	state := readCrewTeamState(project)
	teamOn := state.TeamOn
	// A member's working copy has no list of its own on the owner's Crew (the server reads its agents from this Mac):
	// pushing one was refused with a 403 on every change.
	if !IsLocalMode() || state.MemberOf != "" || len(readMacSharedAgents(project)) == 0 && !teamOn {
		return
	}
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || binding.Mode != "relay" {
		return
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil {
		return
	}
	// While the Team is on every agent is on the Team's list, new ones too (crew_team_all.go); off, the list is empty.
	next, changed := crewTeamRoster(rows, readMacSharedAgents(project), teamOn)
	if !changed && !macSharedPurposesStale(rows, next) {
		return
	}
	if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, next); err != nil {
		log.Printf("[CREW_SHARED_AGENT] local project=%s purpose refresh failed: %v", project, err)
		return
	}
	// #2827: a previously registered empty chat can gain its Team row here.
	// Rebind its ordinary metadata once after that row exists; the receiver's
	// unchanged-registration cache must not leave it unconnected to the Team.
	records, err := api.desktopAppAgentRecords(ctx, claims.UserID)
	if err != nil {
		return
	}
	for _, record := range records {
		if record.ResourceID != project {
			continue
		}
		if _, err := registerDesktopAppAgent(ctx, claims.UserID, connection, record); err != nil {
			log.Printf("[AGENTBRIDGE_APP] event=team_register_refused project=%s reason=registration_unavailable", project)
		}
	}
}

// assignMacAgentAddresses gives each shared agent its address for other people's agents (agent:<owner>/<address>): kept
// when it already has one (also after a rename or being turned off and on), otherwise made from its name and unique
// among this owner's agents. Agents turned off stay as reserved entries so their address is never reused.
func assignMacAgentAddresses(ctx context.Context, owner, project string, agents, previous []macSharedAgent) []macSharedAgent {
	taken := map[string]bool{}
	if s, err := readAgentMessageSnapshot(ctx); err == nil {
		for _, a := range s.Agents {
			if a.UserID == owner {
				taken[a.Name] = true
			}
		}
	}
	state, _ := crewroot.StateRoot()
	files, _ := filepath.Glob(filepath.Join(state, "crew-sync", "*-agents.json"))
	for _, file := range files {
		other := strings.TrimSuffix(filepath.Base(file), "-agents.json")
		if other == project || ownerOfCrewProjectID(other) != owner {
			continue
		}
		for _, agent := range readAllMacSharedAgents(other) {
			taken[agent.Address] = true
		}
	}
	kept := map[string]string{}
	for _, agent := range previous {
		if agent.Address != "" {
			kept[agent.Tab] = agent.Address
			taken[agent.Address] = true
		}
	}
	out := []macSharedAgent{}
	for _, agent := range agents {
		if address := kept[agent.Tab]; address != "" {
			agent.Address = address
		} else {
			base := macAgentSlug(agent.Name)
			agent.Address = base
			for n := 2; taken[agent.Address]; n++ {
				agent.Address = fmt.Sprintf("%s-%d", base, n)
			}
			taken[agent.Address] = true
		}
		out = append(out, agent)
	}
	for _, agent := range previous {
		if agent.Address != "" && !slices.ContainsFunc(out, func(a macSharedAgent) bool { return a.Tab == agent.Tab }) {
			out = append(out, macSharedAgent{Tab: agent.Tab, Name: agent.Name, Address: agent.Address, Off: true})
		}
	}
	return out
}

// macAgentSlug is a plain address from a name: lowercase letters, digits and single hyphens, at most 40 characters.
func macAgentSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "agent"
	}
	return slug
}

// macSharedAgentByAddress finds the shared agent at <owner>/<address> that this person may message from their own
// agents, as a Crew target. Sharing an agent opens it to its people and their agents together (owner, 3 Nov).
func macSharedAgentByAddress(owner, address, subject string) (triggerTarget, bool) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return triggerTarget{}, false
	}
	files, _ := filepath.Glob(filepath.Join(state, "crew-sync", "*-agents.json"))
	for _, file := range files {
		project := strings.TrimSuffix(filepath.Base(file), "-agents.json")
		for _, agent := range readMacSharedAgents(project) {
			if agent.Address != address || !crewRelayTwin(project) || ownerOfCrewProjectID(project) != owner {
				continue
			}
			// An address is reached by the subject's agent, so the Team rule applies: In team, for a member.
			if _, reason := macSharedAgentFor(project, agent.Tab, subject, owner); reason != "" {
				log.Printf("[CREW_SHARED_AGENT] project=%s tab=%s action=address refused reason=%s user=%s", project, agent.Tab, reason, logUsernameForUserID(subject))
				return triggerTarget{}, false
			}
			key := macSharedAgentChatKey(project, agent.Tab)
			// The Crew's own folder: message delivery checks the target is still this owner's Crew at this path.
			return triggerTarget{Kind: triggerCallerCrew, Path: crewProjectRootByID(project), CrewID: project, CrewProfile: crewProfileID, CrewOwner: owner, Label: agent.Name,
				Chat: &codeChat{OwnerID: owner, Key: key, ID: key, Name: agent.Name, Purpose: agent.Purpose}}, true
		}
	}
	return triggerTarget{}, false
}

// macAskActivity is who asked a shared agent on this Mac and when, kept in memory for the owner's Access row
// (owner, 3 Nov: "show here who is doing it.. so we can click on it and it opens the agent"). Never stored or sent on.
type macAskActivity struct {
	Tab     string    `json:"tab"`
	Agent   string    `json:"agent"`
	Sender  string    `json:"sender"`
	Channel string    `json:"channel"`          // how it came: website, MCP (their AI app) or AgentBridge (their agent); set by the server
	Key     string    `json:"conversation_key"` // this sender's own thread with the agent, opened for the owner to view
	Session string    `json:"session_id"`
	Started time.Time `json:"started_at"`
	Active  bool      `json:"active"`
}

const macAskRecent = 10 * time.Minute

var macAsks = struct {
	sync.Mutex
	byProject map[string][]macAskActivity
}{byProject: map[string][]macAskActivity{}}

func noteMacAsk(project string, ask macAskActivity) {
	defer publishCrewPanelChanged(project)
	macAsks.Lock()
	defer macAsks.Unlock()
	kept := []macAskActivity{}
	for _, old := range macAsks.byProject[project] {
		if time.Since(old.Started) < macAskRecent && !(old.Tab == ask.Tab && old.Sender == ask.Sender) {
			kept = append(kept, old)
		}
	}
	macAsks.byProject[project] = append(kept, ask)
}

// recentMacAsks is this Crew's asks in the last ten minutes, newest first, each marked active while its tab still runs.
func (api *StreamingAPI) recentMacAsks(project string) []macAskActivity {
	macAsks.Lock()
	defer macAsks.Unlock()
	out := []macAskActivity{}
	for _, ask := range macAsks.byProject[project] {
		if time.Since(ask.Started) < macAskRecent {
			ask.Active = api.sessionTurnRunning(ask.Session)
			out = append([]macAskActivity{ask}, out...)
		}
	}
	return out
}

// crewOwnTeamAgentMessage: the sender is one of the destination owner's own agents of the same Crew (not a Run person,
// not another Crew), the Team is on and the destination agent is In team.
// The person themself answering from their own Team panel counts the same (#2853): their own website may message
// their own agents; only their external chats need the AgentBridge Public switch.
func crewOwnTeamAgentMessage(project, tab string, source, dest agentMessageEndpoint) bool {
	if source.ID != project && !source.TeamPanel || !crewOwnAgentMessage(dest, source) {
		return false
	}
	t, err := liveCrewTeam(project)
	return err == nil && t.On && t.OwnerID == dest.UserID && t.agentInTeam(dest.UserID, tab)
}

// macAgentReplyWindow bounds how long the server waits for a Mac agent's answer to another person's message.
const macAgentReplyWindow = 30 * time.Minute

// deliverToMacSharedAgent runs another person's agent message in the shared tab on its owner's Mac and sends the
// agent's final answer back to the same inbox. The person behind the sending agent must be in the audience.
func (api *StreamingAPI) deliverToMacSharedAgent(ctx context.Context, c agentConversation, m agentMessage, source, dest agentMessageEndpoint) error {
	if _, _, member := crewMemberMacTarget(dest.ID, dest.ChatKey); member {
		return api.deliverToMemberMacAgent(ctx, c, m, source, dest)
	}
	tab := macSharedAgentTab(dest.ID, dest.ChatKey)
	// The asker as the Team knows it: a joined online project's Builder turn asks as the project (#3664).
	asker := crewTeamAskerOf(source)
	// Your own agent of this Crew messaging your other In team agent on your Mac (owner, 12 Nov: an agent's online copy
	// and its Mac original talk through Team messaging). A teammate's agent keeps the Team audience check below.
	if crewOwnTeamAgentMessage(dest.ID, tab, source, dest) {
		log.Printf("[CREW_TEAM] project=%s event=own_agent_message tab=%q", dest.ID, tab)
	} else if _, reason := macSharedAgentFor(dest.ID, tab, asker, dest.UserID); reason != "" {
		log.Printf("[CREW_SHARED_AGENT] project=%s tab=%q action=message refused reason=%s sender_agent=%s user=%s", dest.ID, tab, reason, source.NamedAgentID, logUsernameForUserID(source.UserID))
		return fmt.Errorf("This agent isn't shared with you.")
	}
	sender := crewTeamAskerLabel(asker)
	// Read the answer from the Mac that took this message.
	run := func(call crewChatCall, timeout time.Duration) crewChatResult {
		return callCrewMacFor(context.Background(), dest.UserID, dest.ID, call, timeout)
	}

	read := func(after, wait int) (map[string]any, int) {
		raw, _ := json.Marshal(map[string]any{"sender_id": asker, "after": after, "wait_seconds": wait, "submission": m.ID})
		result := run(crewChatCall{Operation: "agent_read", Tab: tab, Caller: sender, Args: raw}, time.Duration(wait+40)*time.Second)
		var out map[string]any
		_ = json.Unmarshal(result.Body, &out)
		return out, result.Status
	}
	_, senderAgent, _ := agentMessageSender(ctx, source, source.UserID)
	raw, _ := json.Marshal(withAgentRuleArgs(ctx, map[string]any{"sender_id": asker, "sender_agent": senderAgent, "sender_channel": "AgentBridge", "queued_request": m.QueuedRequest, "own_agent_message": crewOwnAgentMessage(dest, source)}, dest.ID, dest.UserID, macSharedAgentChatKey(dest.ID, tab)))
	ask := crewChatCall{Operation: "agent_ask", Tab: tab, Message: m.Text, Caller: sender, Args: raw, SubmissionID: m.ID}
	from := 1 - m.From
	// The Mac must be reachable before the message is handed over; the answer is then read by this message's id.
	_, status := read(0, 0)
	if status == http.StatusOK {
		status = run(ask, 60*time.Second).Status
	}
	log.Printf("[CREW_SHARED_AGENT] project=%s tab=%s action=message sender_agent=%s user=%s mac_status=%d", dest.ID, tab, source.NamedAgentID, logUsernameForUserID(source.UserID), status)
	if status == http.StatusServiceUnavailable && !IsLocalMode() {
		if err := holdForMac(dest.ID, "mac_off", ask, c.ID, from); err != nil {
			if errors.Is(err, errCrewMacHeldFull) {
				return fmt.Errorf("Too many of your messages are already waiting for this Mac. Try again when it is back.")
			}
			return fmt.Errorf("Your message could not be saved. Please try again.")
		}
		go api.appendMacAgentReply(c.ID, from, shareOwnerLabel(dest.UserID)+"'s Mac is off; your message is delivered when it's back. The answer comes to this inbox.")
		return nil
	}
	if status != http.StatusOK {
		// Live round 3, 6 Nov: the asking agent was told "sent" and never heard of the refusal. It is told in its chat,
		// in plain words, as the Mac-off notice is.
		go api.appendMacAgentReply(c.ID, from, shareOwnerLabel(dest.UserID)+"'s agent could not take your message, so it was not delivered. Ask again in a moment; if it keeps failing, tell "+shareOwnerLabel(dest.UserID)+".")
		return fmt.Errorf("the agent could not take this message; try again shortly")
	}
	api.startMacAgentAnswer(macAgentAwaiting{Inbox: c.ID, Submission: m.ID, From: from, Project: dest.ID, Tab: tab, Mac: dest.UserID, SenderID: asker, Caller: sender})
	return nil
}

// macAgentAnswered remembers which messages already got their answer, so each is answered once.
var macAgentAnswered sync.Map

// macAgentAwaiting is one message a Mac agent took whose answer still goes back to its inbox. It is kept
// in the app's state while the answer is awaited, so a server restart resumes the wait instead of losing the reply.
type macAgentAwaiting struct {
	Inbox      string `json:"inbox"`
	Submission string `json:"submission"`
	From       int    `json:"from"`
	Project    string `json:"project"`
	Tab        string `json:"tab"`
	// Mac is whose Mac runs the agent (the Crew owner, or a Team member when Member);
	Mac      string    `json:"mac"`
	Member   bool      `json:"member,omitempty"`
	SenderID string    `json:"sender_id"`
	Caller   string    `json:"caller"`
	Started  time.Time `json:"started"`
}

func (w macAgentAwaiting) key() string { return w.Inbox + "\x00" + w.Submission }

var macAgentAwaitingMu sync.Mutex

func macAgentAwaitingPath() (string, error) {
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", "mac-agent-awaiting.json"), err
}

func readMacAgentAwaiting() ([]macAgentAwaiting, error) {
	p, err := macAgentAwaitingPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []macAgentAwaiting
	return out, json.Unmarshal(raw, &out)
}

func updateMacAgentAwaiting(change func([]macAgentAwaiting) []macAgentAwaiting) error {
	macAgentAwaitingMu.Lock()
	defer macAgentAwaitingMu.Unlock()
	list, err := readMacAgentAwaiting()
	if err != nil {
		return err
	}
	p, err := macAgentAwaitingPath()
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(p, change(list), 0o600)
}

// macAgentAnswerReadForTest stands in for the Mac in a test.
var macAgentAnswerReadForTest func(api *StreamingAPI, w macAgentAwaiting, after, wait int) (map[string]any, int)

// macAgentAnswerRead reads the agent's turn for this message from where it runs.
func macAgentAnswerRead(api *StreamingAPI, w macAgentAwaiting, after, wait int) (map[string]any, int) {
	if macAgentAnswerReadForTest != nil {
		return macAgentAnswerReadForTest(api, w, after, wait)
	}
	raw, _ := json.Marshal(map[string]any{"sender_id": w.SenderID, "after": after, "wait_seconds": wait, "submission": w.Submission})
	call := crewChatCall{Operation: "agent_read", Tab: w.Tab, Caller: w.Caller, Args: raw}
	timeout := time.Duration(wait+40) * time.Second
	var result crewChatResult
	switch {
	case w.Member:
		pair, _ := crewMacPairFor(w.Mac, w.Project)
		result = callCrewMacPair(context.Background(), w.Mac, w.Project, pair, call, timeout)
	default:
		result = callCrewMacFor(context.Background(), w.Mac, w.Project, call, timeout)
	}
	var out map[string]any
	_ = json.Unmarshal(result.Body, &out)
	return out, result.Status
}

// startMacAgentAnswer records the awaited answer, then waits for it in the background.
func (api *StreamingAPI) startMacAgentAnswer(w macAgentAwaiting) {
	if w.Started.IsZero() {
		w.Started = time.Now().UTC()
	}
	if err := updateMacAgentAwaiting(func(list []macAgentAwaiting) []macAgentAwaiting {
		return append(slices.DeleteFunc(list, func(o macAgentAwaiting) bool { return o.key() == w.key() }), w)
	}); err != nil {
		log.Printf("[CREW_SHARED_AGENT] inbox=%s submission=%s action=awaiting_not_saved reason=%q", w.Inbox, w.Submission, err)
	}
	go api.awaitMacAgentAnswer(w)
}

// resumeMacAgentAnswers picks up, after a restart, every answer still awaited within its window.
func (api *StreamingAPI) resumeMacAgentAnswers() {
	list, err := readMacAgentAwaiting()
	if err != nil {
		log.Printf("[CREW_SHARED_AGENT] action=awaiting_unreadable reason=%q", err)
		return
	}
	for _, w := range list {
		if time.Since(w.Started) >= macAgentReplyWindow {
			api.doneMacAgentAnswer(w)
			continue
		}
		log.Printf("[CREW_SHARED_AGENT] inbox=%s submission=%s action=answer_wait_resumed", w.Inbox, w.Submission)
		go api.awaitMacAgentAnswer(w)
	}
}

func (api *StreamingAPI) doneMacAgentAnswer(w macAgentAwaiting) {
	if err := updateMacAgentAwaiting(func(list []macAgentAwaiting) []macAgentAwaiting {
		return slices.DeleteFunc(list, func(o macAgentAwaiting) bool { return o.key() == w.key() })
	}); err != nil {
		log.Printf("[CREW_SHARED_AGENT] inbox=%s submission=%s action=awaiting_not_cleared reason=%q", w.Inbox, w.Submission, err)
	}
}

// awaitMacAgentAnswer sends only the agent's final answer to this sender's turn back to the inbox: once "Still
// working" if it takes long, then the answer. Never its tool steps.
func (api *StreamingAPI) awaitMacAgentAnswer(w macAgentAwaiting) {
	defer api.doneMacAgentAnswer(w)
	inbox, submission, from := w.Inbox, w.Submission, w.From
	read := func(after, wait int) (map[string]any, int) { return macAgentAnswerRead(api, w, after, wait) }
	started, toldWorking := w.Started, time.Since(w.Started) > 2*time.Minute // a resumed wait already said it
	if started.IsZero() {
		started = time.Now()
	}
	answer := ""
	for time.Since(started) < macAgentReplyWindow {
		out, status := read(0, 25)
		if status != http.StatusOK {
			time.Sleep(10 * time.Second)
			continue
		}
		if items, ok := out["messages"].([]any); ok {
			for _, item := range items {
				if row, ok := item.(map[string]any); ok && row["role"] == "assistant" {
					answer, _ = row["text"].(string)
				}
			}
		}
		if working, _ := out["working"].(bool); !working && strings.TrimSpace(answer) != "" {
			if _, done := macAgentAnswered.LoadOrStore(submission, true); !done {
				time.AfterFunc(macAgentReplyWindow, func() { macAgentAnswered.Delete(submission) })
				api.appendMacAgentAnswer(inbox, from, answer)
				log.Printf("[CREW_SHARED_AGENT] inbox=%s submission=%s action=answer_sent", inbox, submission)
			}
			return
		}
		if !toldWorking && time.Since(started) > 2*time.Minute {
			api.appendMacAgentReply(inbox, from, "Still working; I'll reply when done.")
			toldWorking = true
		}
	}
	log.Printf("[CREW_SHARED_AGENT] inbox=%s submission=%s answer not ready within %s", inbox, submission, macAgentReplyWindow)
}

// appendMacAgentReply adds the Mac agent's words to the inbox as a message from the agent's side, like any reply.
func (api *StreamingAPI) appendMacAgentReply(inbox string, from int, text string) error {
	return api.appendMacAgentMessage(inbox, from, text, false)
}
func (api *StreamingAPI) appendMacAgentAnswer(inbox string, from int, text string) error {
	return api.appendMacAgentMessage(inbox, from, text, true)
}
func (api *StreamingAPI) appendMacAgentMessage(inbox string, from int, text string, answered bool) error {
	ctx := context.Background()
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return err
	}
	index := slices.IndexFunc(s.Conversations, func(c agentConversation) bool { return c.ID == inbox })
	if index < 0 {
		agentMessageMu.Unlock()
		return os.ErrNotExist
	}
	c := &s.Conversations[index]
	if len(c.Messages) >= agentMessageKeep {
		c.Messages = c.Messages[1:]
	}
	c.Next++
	reply := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: c.Next, From: from, Text: text, SentAt: time.Now().UTC(), Delivery: "pending"}
	if dest := c.Endpoints[1-from]; dest.External {
		reply.Delivery = "stored"
		if dest.NamedAgentID != "" {
			s.NextAgentSequence++
			reply.AgentSequence = s.NextAgentSequence
		}
	}
	c.Messages = append(c.Messages, reply)
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err == nil {
		api.kickAgentMessages(inbox)
	}
	return err
}

// macSharedAgentDirectoryItems lists, among the agents a person's agents can message, those shared from Mac Crews to
// that person, with their address and whether the owner's Mac is on.
func macSharedAgentDirectoryItems(user string) []map[string]interface{} {
	items := []map[string]interface{}{}
	if IsLocalMode() {
		return items
	}
	for _, row := range macSharedAgentRows(user) {
		project, _ := row["crew_id"].(string)
		owner := ownerOfCrewProjectID(project)
		tab := macSharedAgentTab(project, fmt.Sprint(row["chat_id"]))
		for _, agent := range readMacSharedAgents(project) {
			if agent.Tab != tab || agent.Address == "" {
				continue
			}
			label := shareOwnerLabel(owner)
			online, _ := row["available"].(bool)
			items = append(items, map[string]interface{}{"agent_name": label + "/" + agent.Address, "target": "agent:" + label + "/" + agent.Address, "owner": label, "shared": true,
				"label": agent.Name, "purpose": agent.Purpose, "examples": agent.Examples, "online": online, "presence": row["presence"], "delivery_mode": "answer"})
		}
	}
	return items
}

// crewMacAgentTabForKey maps the owner's saved Main pointer as well as side
// and shared-thread keys to the corresponding Mac agent, without a server copy.
func crewMacAgentTabForKey(project, key string) string {
	key = strings.TrimSpace(key)
	if project == "" || key == "" {
		return ""
	}
	owner := ownerOfCrewProjectID(project)
	if key == crewOwnerDefaultChatKey(project, owner) || key == crewOwnerMainChatKey(project) {
		return macMainTab
	}
	return crewAgentKeyTab(key)
}
