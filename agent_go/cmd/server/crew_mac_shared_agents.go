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
	Audience string   `json:"audience"` // everyone | chosen
	People   []string `json:"people,omitempty"`
	Since    string   `json:"since,omitempty"`
	// Address is how other people's agents message it: agent:<owner>/<address>. The server gives it once and keeps it
	// across renames; Off keeps a turned-off entry only to keep its address for this owner.
	Address string `json:"address,omitempty"`
	Off     bool   `json:"off,omitempty"`
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
		out["workspace_id"], out["chat_id"], out["name"], out["purpose"], out["can_write"], out["shared"] = manifest.Product+":"+manifest.ID, macSharedAgentChatKey(manifest.ID, agent.Tab), agent.Name, agent.Purpose, false, true
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
				"chat_id": macSharedAgentChatKey(project, agent.Tab), "name": agent.Name, "crew_name": ownerShort + "'s Mac", "purpose": agent.Purpose, "presence": presence, "your_access": "run",
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
	// Main first: the owner decides which agents to share, Main included (owner, 3 Nov).
	rows := []tabRow{{Tab: macMainTab, Name: "Main"}}
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
				externalError(w, 400, "invalid_agents", "Choose one of this Crew's agents.")
				return
			}
		}
		clean, err := cleanMacSharedAgents(agents, readMacSharedAgents(project))
		if err != nil {
			externalError(w, 400, "invalid_agents", err.Error())
			return
		}
		reply, err := desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "share_agents", PairID: binding.PairID, Agents: clean})
		if err != nil {
			externalError(w, 409, crewSyncErrorCode(err), err.Error())
			return
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

// macSharedAgentByAddress finds the shared agent at <owner>/<address> that this person may message, as a Crew target.
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
			return triggerTarget{Kind: triggerCallerCrew, CrewID: project, CrewProfile: crewProfileID, CrewOwner: owner, Label: agent.Name,
				Chat: &codeChat{OwnerID: owner, Key: key, ID: key, Name: agent.Name, Purpose: agent.Purpose}}, true
		}
	}
	return triggerTarget{}, false
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
	read := func(after, wait int) (map[string]any, int) {
		raw, _ := json.Marshal(map[string]any{"sender_id": source.UserID, "after": after, "wait_seconds": wait})
		result := callCrewMacFor(context.Background(), dest.UserID, dest.ID, crewChatCall{Operation: "agent_read", Tab: tab, Caller: sender, Args: raw}, time.Duration(wait+40)*time.Second)
		var out map[string]any
		_ = json.Unmarshal(result.Body, &out)
		return out, result.Status
	}
	baseline, status := read(0, 0)
	if status == http.StatusOK {
		raw, _ := json.Marshal(map[string]any{"sender_id": source.UserID})
		result := callCrewMacFor(context.WithoutCancel(ctx), dest.UserID, dest.ID, crewChatCall{Operation: "agent_ask", Tab: tab, Message: m.Text, Caller: sender, Args: raw, SubmissionID: m.ID}, 60*time.Second)
		status = result.Status
	}
	log.Printf("[CREW_SHARED_AGENT] project=%s tab=%s action=message sender_agent=%s user=%s mac_status=%d", dest.ID, tab, source.NamedAgentID, logUsernameForUserID(source.UserID), status)
	from := 1 - m.From
	if status == http.StatusServiceUnavailable {
		go api.appendMacAgentReply(c.ID, from, shareOwnerLabel(dest.UserID)+"'s Mac is off. Try again later.")
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("the agent could not take this message; try again shortly")
	}
	after := 0
	if next, ok := baseline["next_after"].(float64); ok {
		after = int(next)
	}
	go api.awaitMacAgentAnswer(c.ID, from, after, read)
	return nil
}

// awaitMacAgentAnswer sends only the agent's final answer to this sender's turn back to the inbox: once "Still
// working" if it takes long, then the answer. Never its tool steps.
func (api *StreamingAPI) awaitMacAgentAnswer(inbox string, from, after int, read func(after, wait int) (map[string]any, int)) {
	started, toldWorking := time.Now(), false
	answer := ""
	for time.Since(started) < macAgentReplyWindow {
		out, status := read(after, 25)
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
			api.appendMacAgentReply(inbox, from, answer)
			return
		}
		if !toldWorking && time.Since(started) > 2*time.Minute {
			api.appendMacAgentReply(inbox, from, "Still working; I'll reply when done.")
			toldWorking = true
		}
	}
	log.Printf("[CREW_SHARED_AGENT] inbox=%s answer not ready within %s", inbox, macAgentReplyWindow)
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
