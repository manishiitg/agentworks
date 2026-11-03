package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Sharing a Mac Crew's agents (PLAT-856-509, owner 3 Nov): the owner ticks side tabs that other people's agents may
// message while the Mac is on, each with its own audience. The Mac keeps its choice and pushes the same list to its
// online copy; the server decides who is in the audience, the Mac decides which of its tabs are shared. A message
// runs on the Mac in that tab, in Run mode, framed as someone else's words, and its sender reads back only their own
// exchange. Main and the owner's other chats are never shared.

type macSharedAgent struct {
	Tab      string   `json:"tab"`
	Name     string   `json:"name"`
	Purpose  string   `json:"purpose"`
	Audience string   `json:"audience"` // everyone | chosen
	People   []string `json:"people,omitempty"`
	Since    string   `json:"since,omitempty"`
}

const macSharedAgentsMax = externalCrewMaxSideChats

func macSharedAgentsPath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid Crew identity")
	}
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", project+"-agents.json"), err
}

func readMacSharedAgents(project string) []macSharedAgent {
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
			return nil, fmt.Errorf("choose one of this Crew's tabs")
		case agent.Name == "":
			return nil, fmt.Errorf("an agent needs a name")
		case agent.Purpose == "" || len([]rune(agent.Purpose)) > productAgentPurposeMaxRunes:
			return nil, fmt.Errorf("say in one line what %s is for (at most %d characters)", agent.Name, productAgentPurposeMaxRunes)
		case agent.Audience != "everyone" && agent.Audience != "chosen":
			return nil, fmt.Errorf("choose Everyone or Chosen people for %s", agent.Name)
		case agent.Audience == "chosen" && (len(agent.People) == 0 || len(agent.People) > 100):
			return nil, fmt.Errorf("choose who can message %s", agent.Name)
		}
		if agent.Audience == "everyone" {
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
		if err = os.Remove(path); os.IsNotExist(err) {
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
	agents, err := cleanMacSharedAgents(input.Agents, readMacSharedAgents(roots.ID))
	if err != nil {
		externalError(w, 400, "invalid_agents", err.Error())
		return
	}
	if err = writeMacSharedAgents(roots.ID, agents); err != nil {
		externalError(w, 500, "save_failed", "The shared agents could not be saved. Try again.")
		return
	}
	log.Printf("[CREW_SHARED_AGENT] project=%s owner=%s shared=%d action=set", roots.ID, logUsernameForUserID(claims.UserID), len(agents))
	externalJSON(w, map[string]any{"project_id": roots.ID, "agents": agents})
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
	agent, reason := macSharedAudience(readMacSharedAgents(project), tab, caller, owner)
	if reason != "" {
		return agent, reason
	}
	if claims := (&UserClaims{UserID: caller}); userAccessForClaims(claims).Disabled || !userAllowedProduct(claims, crewProfileID) {
		return macSharedAgent{}, "no_access"
	}
	return agent, ""
}

// macSharedAudience: whether the shared list lets this caller message the tab.
func macSharedAudience(agents []macSharedAgent, tab, caller, owner string) (macSharedAgent, string) {
	if caller == "" || caller == owner {
		return macSharedAgent{}, "owner"
	}
	for _, agent := range agents {
		if agent.Tab != tab {
			continue
		}
		if agent.Audience == "everyone" || slices.Contains(agent.People, caller) {
			return agent, ""
		}
		return macSharedAgent{}, "not_in_audience"
	}
	return macSharedAgent{}, "not_shared"
}

// macSharedAgentTab reads the tab id from a chat_id: "<project>:chat:<id>" or the bare id.
func macSharedAgentTab(project, chatID string) string {
	chatID = strings.TrimSpace(chatID)
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
	if IsLocalMode() || (action != "ask" && action != "read_chat") || externalArg(args, "chat_id") == "" {
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
	if action != "ask" && action != "read_chat" {
		externalError(w, 403, "tab_owner", "Send this agent a message; its owner controls its settings and confirmations.")
		return true
	}
	if token := claims.AccessToken; token != nil && !token.Allows(map[bool]string{true: "crews:run", false: "crews:read"}[action == "ask"]) {
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
	raw, _ := json.Marshal(map[string]any{"sender_id": claims.UserID, "after": externalInt(args, "after", 0), "wait_seconds": externalInt(args, "wait_seconds", 0)})
	call := crewChatCall{Operation: operation, Tab: agent.Tab, Message: externalArg(args, "message"), Caller: crewOwnerDisplayName(sanitizeUserIDForPath(claims.UserID)), Args: raw,
		SubmissionID: externalArg(args, "submission_id")}
	result := callCrewMacFor(r.Context(), project.OwnerID, manifest.ID, call, externalCrewWait(args)+40*time.Second)
	log.Printf("[CREW_SHARED_AGENT] project=%s user=%s tab=%s action=%s mac_status=%d", manifest.ID, logUsernameForUserID(claims.UserID), agent.Tab, action, result.Status)
	if result.Status == http.StatusServiceUnavailable {
		externalError(w, 503, "owner_mac_off", "Its owner's Mac is off. Try again later.")
		return true
	}
	var out map[string]any
	if result.Status == http.StatusOK && json.Unmarshal(result.Body, &out) == nil {
		out["workspace_id"], out["chat_id"], out["name"], out["purpose"], out["can_write"], out["shared"] = manifest.Product+":"+manifest.ID, manifest.ID+codeChatSideMarker+agent.Tab, agent.Name, agent.Purpose, false, true
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
		owner := ownerOfCrewProjectID(project)
		if owner == "" {
			continue
		}
		online := crewMacSocketFor(owner, project, b.PairID) != nil
		ownerShort := strings.SplitN(logUsernameForUserID(owner), "@", 2)[0]
		for _, agent := range readMacSharedAgents(project) {
			if _, reason := macSharedAgentFor(project, agent.Tab, caller, owner); reason != "" {
				continue
			}
			presence := "Its owner's Mac is off"
			if online {
				presence = "Answers now, on its owner's Mac"
			}
			target := "workspace:" + crewProfileID + ":" + project
			rows = append(rows, map[string]any{"kind": "crew_agent", "type": "Crew agent", "id": target + "/agent/" + agent.Tab, "target": target, "crew_id": project,
				"chat_id": project + codeChatSideMarker + agent.Tab, "name": agent.Name, "crew_name": ownerShort + "'s Mac", "purpose": agent.Purpose, "presence": presence, "your_access": "run",
				"reach":       "workspace action=ask with workspace_id=" + crewProfileID + ":" + project + " and this chat_id, then read_chat for its answer",
				"owner_short": ownerShort, "available": online})
		}
	}
	return rows
}

// ownerOfCrewProjectID is the main owner of a Crew by its project id, from the owner registry.
func ownerOfCrewProjectID(project string) string {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return ""
	}
	for _, record := range records {
		if record.Product == crewProfileID && record.ProjectID == project {
			return record.OwnerID
		}
	}
	return ""
}

// desktopShareMacCrewAgents is the Mac app's Access page for shared agents: the Crew's side tabs with their sharing.
func (api *StreamingAPI) desktopShareMacCrewAgents(w http.ResponseWriter, r *http.Request, claims *UserClaims, project string, binding *crewroot.SyncBinding, connection desktopConnection, action string, agents []macSharedAgent) {
	tabs, err := api.macCrewTabs(r.Context(), claims, project)
	if err != nil {
		externalError(w, 409, "tabs_unavailable", err.Error())
		return
	}
	type tabRow struct {
		Tab  string `json:"tab"`
		Name string `json:"name"`
	}
	rows := []tabRow{}
	for _, tab := range tabs {
		if _, id, ok := strings.Cut(tab.ConversationKey, codeChatSideMarker); ok {
			rows = append(rows, tabRow{Tab: id, Name: tab.Name})
		}
	}
	if action == "share_agents" {
		if binding == nil || binding.Mode != "relay" || binding.Editing != "local" {
			externalError(w, 409, "reach_off", "Turn on Reach it from your phone and the web first.")
			return
		}
		for _, agent := range agents {
			if !slices.ContainsFunc(rows, func(row tabRow) bool { return row.Tab == agent.Tab }) {
				externalError(w, 400, "invalid_agents", "Choose one of this Crew's tabs; Main stays private.")
				return
			}
		}
		clean, err := cleanMacSharedAgents(agents, readMacSharedAgents(project))
		if err != nil {
			externalError(w, 400, "invalid_agents", err.Error())
			return
		}
		if _, err = desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "share_agents", PairID: binding.PairID, Agents: clean}); err != nil {
			externalError(w, 409, crewSyncErrorCode(err), err.Error())
			return
		}
		if err = writeMacSharedAgents(project, clean); err != nil {
			externalError(w, 500, "save_failed", "The shared agents could not be saved. Try again.")
			return
		}
		log.Printf("[CREW_SHARED_AGENT] local project=%s server_project=%s shared=%d action=set", project, binding.PeerProjectID, len(clean))
	}
	externalJSON(w, map[string]any{"tabs": rows, "agents": readMacSharedAgents(project)})
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
				"owner_name": row["owner_short"], "purpose": row["purpose"], "available": row["available"]})
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"agents": agents})
}
