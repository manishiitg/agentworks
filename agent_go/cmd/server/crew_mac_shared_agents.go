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
	"sync"
	"time"

	"github.com/google/uuid"
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
	Icon     string   `json:"icon,omitempty"`
	Audience string   `json:"audience"` // team | everyone | chosen
	People   []string `json:"people,omitempty"`
	Since    string   `json:"since,omitempty"`
	// Address is how other people's agents message it: agent:<owner>/<address>. The server gives it once and keeps it
	// across renames; Off keeps a turned-off entry only to keep its address for this owner.
	Address string `json:"address,omitempty"`
	Off     bool   `json:"off,omitempty"`
	// CanChange: the owner lets other people's messages make changes; off (the default) they get answers only, as one
	// line after each of their messages (owner, 3 Nov). The agent keeps its own tools either way.
	CanChange bool `json:"can_change,omitempty"`
}

const macSharedAgentsMax = externalCrewMaxSideChats

func macSharedAgentsPath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid Crew identity")
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
	before := map[string]macSharedAgent{}
	for _, agent := range previous {
		since[agent.Tab] = agent.Since
		before[agent.Tab] = agent
	}
	// The three-field rule applies when a share is turned on or its audience changes; a refresh of an existing
	// share (a new purpose or name) keeps it working, so people already using an agent are not cut off.
	opening := func(agent macSharedAgent) bool {
		old, ok := before[agent.Tab]
		return !ok || old.Off || old.Audience != agent.Audience || !slices.Equal(old.People, agent.People)
	}
	out := []macSharedAgent{}
	for _, agent := range in {
		agent.Name, agent.Purpose = crewMacTabName(agent.Name), strings.Join(strings.Fields(agent.Purpose), " ")
		switch {
		case !crewMacTabID(agent.Tab):
			return nil, fmt.Errorf("choose one of this Crew's tabs")
		case agent.Name == "":
			return nil, fmt.Errorf("an agent needs a name")
		case agent.Purpose == "" || len([]rune(agent.Purpose)) > productAgentPurposeMaxRunes:
			return nil, fmt.Errorf("set %s's purpose to share it (one line, at most %d characters)", agent.Name, productAgentPurposeMaxRunes)
		case strings.TrimSpace(agent.Icon) == "" && !agent.Off && opening(agent):
			// Owner, 4 Nov: sharing an agent needs its name, purpose and icon.
			return nil, fmt.Errorf("give %s an icon to share it: ask it to set its name, purpose and an emoji icon that fits it", agent.Name)
		case agent.Audience != "team" && agent.Audience != "everyone" && agent.Audience != "chosen":
			return nil, fmt.Errorf("choose My team, Chosen people or Everyone in the company for %s", agent.Name)
		case agent.Audience == "chosen" && (len(agent.People) == 0 || len(agent.People) > 100):
			return nil, fmt.Errorf("choose who can message %s", agent.Name)
		}
		if agent.Audience != "chosen" {
			agent.People = nil
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

func writeMacSharedAgents(project string, agents []macSharedAgent) error {
	path, err := macSharedAgentsPath(project)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		if err = os.Remove(path); err == nil || os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(agents, "", "  ")
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
		externalError(w, 409, "wrong_pair", "Share this Crew's agents from the Mac that holds it.")
		return
	}
	directory, _ := loadUserDirectory()
	for _, agent := range input.Agents {
		for _, person := range agent.People {
			if !slices.ContainsFunc(directoryUsers(directory), func(id string) bool { return id == person }) {
				externalError(w, 400, "unknown_person", "Choose people who have an account here.")
				return
			}
		}
	}
	previous := readAllMacSharedAgents(roots.ID)
	agents, err := cleanMacSharedAgents(input.Agents, previous)
	if err != nil {
		externalError(w, 400, "invalid_agents", err.Error())
		return
	}
	// Choosing an agent audience cannot add people to the Team or grant files.
	members := crewTeamPeople(roots.ID)
	for _, agent := range agents {
		for _, person := range agent.People {
			if !slices.Contains(members, person) {
				externalError(w, 400, "not_in_team", "Add that person to the Team first, then choose them for this agent.")
				return
			}
		}
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

// macSharedAgentFor is the server's audience decision: the agent the caller may message on this Mac Crew, if any.
func macSharedAgentFor(project, tab, caller, owner string) (macSharedAgent, string) {
	agent, reason := macSharedAudience(readMacSharedAgents(project), tab, caller, owner, crewTeamPeople(project))
	if reason != "" {
		return agent, reason
	}
	if claims := (&UserClaims{UserID: caller}); userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, crewProfileID) {
		return macSharedAgent{}, "no_access"
	}
	return agent, ""
}

// macSharedAudience: whether the shared list lets this caller message the tab.
func macSharedAudience(agents []macSharedAgent, tab, caller, owner string, members []string) (macSharedAgent, string) {
	if caller == "" || caller == owner {
		return macSharedAgent{}, "owner"
	}
	for _, agent := range agents {
		if agent.Tab != tab {
			continue
		}
		if agent.Audience == "everyone" || (slices.Contains(members, caller) && (agent.Audience == "team" || agent.Audience == "chosen" && slices.Contains(agent.People, caller))) {
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

// macSharedAgentTab reads the tab id from a chat_id: "<project>:chat:<id>", the bare id, or the project (Main).
func macSharedAgentTab(project, chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == project {
		return macMainTab
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
	return chatID
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
		externalError(w, 403, "crew_on_mac", "This Crew lives on its owner's Mac and isn't shared with you.")
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
	if action == "ask" {
		message := strings.TrimSpace(externalArg(args, "message"))
		if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
			externalError(w, 400, "invalid_arguments", "Give a message within the chat size limit.")
			return true
		}
	}
	operation := map[bool]string{true: "agent_ask", false: "agent_read"}[action == "ask"]
	if action == "reply_input" {
		operation = "agent_answer"
	}
	events, _ := args["events"].(bool) // the chat's own events for those turns, so the person sees the owner's chat view
	raw, _ := json.Marshal(map[string]any{"sender_id": claims.UserID, "after": externalInt(args, "after", 0), "wait_seconds": externalInt(args, "wait_seconds", 0), "events": events})
	call := crewChatCall{Operation: operation, Tab: agent.Tab, Message: externalArg(args, "message"), Caller: crewOwnerDisplayName(sanitizeUserIDForPath(claims.UserID)), Args: raw,
		SubmissionID: externalArg(args, "submission_id")}
	if action == "reply_input" {
		call.Answer, _ = json.Marshal(map[string]any{"provider": args["provider"], "prompt_id": args["request_id"], "answers": args["answers"], "auto": args["auto"]})
	}
	result := callCrewMacFor(r.Context(), project.OwnerID, manifest.ID, call, externalCrewWait(args)+40*time.Second)
	log.Printf("[CREW_SHARED_AGENT] project=%s user=%s tab=%s action=%s mac_status=%d", manifest.ID, logUsernameForUserID(claims.UserID), agent.Tab, action, result.Status)
	// The Mac is off: its team copy answers, or the message waits for the Mac, as the owner chose (crew_team_copy.go).
	if result.Status == http.StatusServiceUnavailable && !IsLocalMode() {
		if route, reason := teamCopyRoute(manifest.ID, agent.Tab); route == teamCopyAnswer {
			result = api.teamCopyCall(r.Context(), project.OwnerID, manifest.ID, call)
		} else if action == "ask" && holdForMac(manifest.ID, reason, call, "", 0) == nil {
			externalJSON(w, map[string]any{"status": "waiting", "workspace_id": manifest.Product + ":" + manifest.ID, "chat_id": macSharedAgentChatKey(manifest.ID, agent.Tab), "name": agent.Name,
				"message": "Its owner's Mac is off. Your message waits, and " + firstNonEmptyTrimmed(agent.Name, "the agent") + " answers it when the Mac is back; read this chat then."})
			return true
		}
	}
	if result.Status == http.StatusServiceUnavailable {
		externalError(w, 503, "owner_mac_off", "Its owner's Mac is off. Try again later.")
		return true
	}
	var out map[string]any
	if result.Status == http.StatusOK && json.Unmarshal(result.Body, &out) == nil {
		out["workspace_id"], out["chat_id"], out["name"], out["purpose"], out["icon"], out["can_write"], out["shared"] = manifest.Product+":"+manifest.ID, macSharedAgentChatKey(manifest.ID, agent.Tab), agent.Name, agent.Purpose, agent.Icon, false, true
		externalJSON(w, out)
		return true
	}
	writeCrewChatResult(w, result)
	return true
}

// macSharedAgentRows lists, for the agent directory, the Mac-shared agents this caller may message.
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
			} else if route, _ := teamCopyRoute(project, agent.Tab); route == teamCopyAnswer {
				presence, available = "Its owner's Mac is off; its team copy answers", true
			}
			target := "workspace:" + crewProfileID + ":" + project
			rows = append(rows, map[string]any{"kind": "crew_agent", "type": "Crew agent", "id": target + "/agent/" + agent.Tab, "target": target, "crew_id": project,
				"chat_id": macSharedAgentChatKey(project, agent.Tab), "name": agent.Name, "crew_name": ownerShort + "'s Mac", "purpose": agent.Purpose, "icon": agent.Icon, "presence": presence, "your_access": "run",
				"reach":       "workspace action=ask with workspace_id=" + crewProfileID + ":" + project + " and this chat_id, then read_chat for its answer",
				"owner_short": ownerShort, "crew_title": title, "available": available})
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
	records, err := defaultProjectOwners().All()
	if err != nil {
		return projectOwnerRecord{}, false
	}
	for _, record := range records {
		if record.Product == crewProfileID && record.ProjectID == project {
			return record, true
		}
	}
	return projectOwnerRecord{}, false
}

func ownerOfCrewProjectID(project string) string {
	record, _ := crewOwnerRecord(project)
	return record.OwnerID
}

// macAgentRow is one agent on the Mac app's Access page: Main or a side tab, with its purpose from the tab itself.
type macAgentRow struct {
	Tab     string `json:"tab"`
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Icon    string `json:"icon,omitempty"`
}

func (api *StreamingAPI) macAgentRows(ctx context.Context, claims *UserClaims, project string) ([]macAgentRow, error) {
	tabs, main, err := api.macCrewAgents(ctx, claims, project)
	if err != nil {
		return nil, err
	}
	// Main first: the owner decides which agents to share, Main included (owner, 3 Nov).
	rows := []macAgentRow{{Tab: macMainTab, Name: "Main", Purpose: main.Purpose, Icon: main.Icon}}
	for _, tab := range tabs {
		if _, id, ok := strings.Cut(tab.ConversationKey, codeChatSideMarker); ok {
			rows = append(rows, macAgentRow{Tab: id, Name: tab.Name, Purpose: tab.Purpose, Icon: tab.Icon})
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
	for i := range agents {
		at := slices.IndexFunc(rows, func(row macAgentRow) bool { return row.Tab == agents[i].Tab })
		if at < 0 {
			return 400, "invalid_agents", fmt.Errorf("Choose one of this Crew's agents.")
		}
		agents[i].Name, agents[i].Purpose, agents[i].Icon = rows[at].Name, rows[at].Purpose, rows[at].Icon
	}
	clean, err := cleanMacSharedAgents(agents, readMacSharedAgents(project))
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
	} else if macSharedPurposesStale(rows, readMacSharedAgents(project)) && binding != nil && binding.Mode == "relay" {
		// A purpose changed in its tab since it was shared: the online copy gets the new line too.
		if _, _, err := api.pushMacSharedAgents(r.Context(), claims, project, binding, connection, rows, readMacSharedAgents(project)); err != nil {
			log.Printf("[CREW_SHARED_AGENT] local project=%s purpose refresh failed: %v", project, err)
		}
	}
	externalJSON(w, map[string]any{"tabs": rows, "agents": readMacSharedAgents(project)})
}

// macSharedPurposesStale: a shared agent's saved name or purpose differs from its tab's now (and the tab still has one).
func macSharedPurposesStale(rows []macAgentRow, agents []macSharedAgent) bool {
	for _, agent := range agents {
		for _, row := range rows {
			if row.Tab == agent.Tab && row.Purpose != "" && (row.Purpose != agent.Purpose || row.Name != agent.Name || row.Icon != agent.Icon) {
				return true
			}
		}
	}
	return false
}

// refreshMacSharedPurposes follows a purpose change of a shared agent on this Mac to its online copy.
func (api *StreamingAPI) refreshMacSharedPurposes(ctx context.Context, project string) {
	if !IsLocalMode() || len(readMacSharedAgents(project)) == 0 {
		return
	}
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || binding.Mode != "relay" {
		return
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil || !macSharedPurposesStale(rows, readMacSharedAgents(project)) {
		return
	}
	if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, readMacSharedAgents(project)); err != nil {
		log.Printf("[CREW_SHARED_AGENT] local project=%s purpose refresh failed: %v", project, err)
	}
}

// handleSharedWithMe is the recipient's "Shared with me" list (PLAT-856-509): each agent someone shared with this
// person from a Crew on their Mac, decided by the server from each agent's audience. The Crew itself is not listed.
func (api *StreamingAPI) handleSharedWithMe(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, 401, "sign_in", "Sign in first.")
		return
	}
	agents := []map[string]any{}
	if !IsLocalMode() {
		for _, row := range macSharedAgentRows(claims.UserID) {
			agents = append(agents, map[string]any{"workspace_id": crewProfileID + ":" + fmt.Sprint(row["crew_id"]), "chat_id": row["chat_id"], "name": row["name"],
				"owner_name": row["owner_short"], "crew_name": row["crew_title"], "purpose": row["purpose"], "icon": row["icon"], "available": row["available"]})
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"agents": agents})
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

// macAgentReplyWindow bounds how long the server waits for a Mac agent's answer to another person's message.
const macAgentReplyWindow = 30 * time.Minute

// deliverToMacSharedAgent runs another person's agent message in the shared tab on its owner's Mac and sends the
// agent's final answer back to the same inbox. The person behind the sending agent must be in the audience.
func (api *StreamingAPI) deliverToMacSharedAgent(ctx context.Context, c agentConversation, m agentMessage, source, dest agentMessageEndpoint) error {
	tab := macSharedAgentTab(dest.ID, dest.ChatKey)
	if _, reason := macSharedAgentFor(dest.ID, tab, source.UserID, dest.UserID); reason != "" {
		log.Printf("[CREW_SHARED_AGENT] project=%s tab=%q action=message refused reason=%s sender_agent=%s user=%s", dest.ID, tab, reason, source.NamedAgentID, logUsernameForUserID(source.UserID))
		return fmt.Errorf("This agent isn't shared with you.")
	}
	sender := shareOwnerLabel(source.UserID)
	// Where this message is answered: the Mac, or its team copy while the Mac is off. The answer is read from the same place.
	teamCopy := false
	run := func(call crewChatCall, timeout time.Duration) crewChatResult {
		if teamCopy {
			return api.teamCopyCall(context.Background(), dest.UserID, dest.ID, call)
		}
		return callCrewMacFor(context.Background(), dest.UserID, dest.ID, call, timeout)
	}
	read := func(after, wait int) (map[string]any, int) {
		raw, _ := json.Marshal(map[string]any{"sender_id": source.UserID, "after": after, "wait_seconds": wait, "submission": m.ID})
		result := run(crewChatCall{Operation: "agent_read", Tab: tab, Caller: sender, Args: raw}, time.Duration(wait+40)*time.Second)
		var out map[string]any
		_ = json.Unmarshal(result.Body, &out)
		return out, result.Status
	}
	raw, _ := json.Marshal(map[string]any{"sender_id": source.UserID, "sender_agent": source.Label})
	ask := crewChatCall{Operation: "agent_ask", Tab: tab, Message: m.Text, Caller: sender, Args: raw, SubmissionID: m.ID}
	from := 1 - m.From
	// The Mac must be reachable before the message is handed over; the answer is then read by this message's id.
	_, status := read(0, 0)
	if status == http.StatusServiceUnavailable && !IsLocalMode() {
		if route, reason := teamCopyRoute(dest.ID, tab); route == teamCopyAnswer {
			teamCopy = true
			_, status = read(0, 0)
		} else if holdForMac(dest.ID, reason, ask, c.ID, from) == nil {
			go api.appendMacAgentReply(c.ID, from, shareOwnerLabel(dest.UserID)+"'s Mac is off. Your message waits; the answer comes here when the Mac is back.")
			return nil
		}
	}
	if status == http.StatusOK {
		status = run(ask, 60*time.Second).Status
	}
	log.Printf("[CREW_SHARED_AGENT] project=%s tab=%s action=message sender_agent=%s user=%s mac_status=%d team_copy=%t", dest.ID, tab, source.NamedAgentID, logUsernameForUserID(source.UserID), status, teamCopy)
	if status == http.StatusServiceUnavailable {
		go api.appendMacAgentReply(c.ID, from, shareOwnerLabel(dest.UserID)+"'s Mac is off. Try again later.")
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("the agent could not take this message; try again shortly")
	}
	go api.awaitMacAgentAnswer(c.ID, m.ID, from, read)
	return nil
}

// macAgentAnswered remembers which messages already got their answer, so each is answered once.
var macAgentAnswered sync.Map

// awaitMacAgentAnswer sends only the agent's final answer to this sender's turn back to the inbox: once "Still
// working" if it takes long, then the answer. Never its tool steps.
func (api *StreamingAPI) awaitMacAgentAnswer(inbox, submission string, from int, read func(after, wait int) (map[string]any, int)) {
	started, toldWorking := time.Now(), false
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
				api.appendMacAgentReply(inbox, from, answer)
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
func (api *StreamingAPI) appendMacAgentReply(inbox string, from int, text string) {
	ctx := context.Background()
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return
	}
	index := slices.IndexFunc(s.Conversations, func(c agentConversation) bool { return c.ID == inbox })
	if index < 0 {
		agentMessageMu.Unlock()
		return
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
				"label": agent.Name, "purpose": agent.Purpose, "online": online, "presence": row["presence"], "delivery_mode": "answer"})
		}
	}
	return items
}
