package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	storeEvents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The Claude Code Team pane (#3357) shows what the Team panel already decides for
// this exact attached session: the Needs-you cards for its person, the asks sent
// to this agent that are not finished, and the Team agents it can reach. It is a
// read-only projection; the pane never answers, sends or filters.

const teamPaneLimit = 20

type teamPaneNeed struct {
	Line    string    `json:"line"`
	Ref     string    `json:"ref,omitempty"`
	Agent   string    `json:"agent"`
	Person  string    `json:"person,omitempty"`
	Mine    bool      `json:"mine,omitempty"`
	Buttons []string  `json:"buttons"`
	At      time.Time `json:"at"`
}

type teamPaneAsk struct {
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Ref     string    `json:"ref,omitempty"`
	AskedBy string    `json:"asked_by,omitempty"`
	Last    string    `json:"last,omitempty"`
	At      time.Time `json:"at"`
}

type teamPaneAgent struct {
	ID       string `json:"id"`
	Mine     bool   `json:"mine,omitempty"`
	Name     string `json:"name"`
	Person   string `json:"person,omitempty"`
	Kind     string `json:"kind"`
	Online   bool   `json:"online"`
	Activity string `json:"activity,omitempty"`
	Offline  string `json:"offline_line,omitempty"`
}

func teamPaneNeeds(needs []*crewWorkNeed) []teamPaneNeed {
	out := []teamPaneNeed{}
	for _, n := range needs {
		if len(out) == teamPaneLimit {
			break
		}
		out = append(out, teamPaneNeed{Line: n.Line, Ref: n.Link, Agent: n.Agent, Person: n.Person, Mine: n.Mine, Buttons: n.Buttons, At: n.At})
	}
	return out
}

// The same rule as work_brief's my_tasks: this agent's asks that are not done.
func teamPaneAsks(tasks []crewTeamTask, agentID string) []teamPaneAsk {
	out := []teamPaneAsk{}
	for _, t := range tasks {
		if agentID == "" || t.AgentKey != agentID || t.State == "done" || t.inbox == "" || len(out) == teamPaneLimit {
			continue
		}
		out = append(out, teamPaneAsk{Title: t.Title, State: t.State, Ref: t.Group, AskedBy: t.AskedBy, Last: t.Line, At: t.At})
	}
	return out
}

// The person's own agents first (the same rows the Team panel shows them), then the
// teammates' agents the Team admits; the pane groups them by person.
func teamPaneAgents(own, reach []crewTeamReachableAgent) []teamPaneAgent {
	out := []teamPaneAgent{}
	add := func(a crewTeamReachableAgent, mine bool) {
		if len(out) < 2*teamPaneLimit {
			// The pane keeps only how long it has been offline; the asker's choices are the Team panel's.
			offline, _, _ := strings.Cut(a.OfflineLine, " · ")
			out = append(out, teamPaneAgent{ID: a.ID, Mine: mine, Name: a.Name, Person: a.Person, Kind: a.Kind, Online: a.Online, Activity: a.Activity, Offline: offline})
		}
	}
	for _, a := range own {
		add(a, true)
	}
	for _, a := range reach {
		add(a, false)
	}
	return out
}

type teamPaneMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// teamPaneDetail is one agent opened in the pane: its row and, when its chat is a
// terminal chat shared Live (or the caller's own), its last few messages. Note says
// why there are none: not_live, open_in_app or unavailable.
type teamPaneDetail struct {
	teamPaneAgent
	Messages []teamPaneMessage `json:"messages"`
	Note     string            `json:"note,omitempty"`
	// An AgentWorks app agent shared Live: what it is doing and its last message, the
	// same lines agent_status shows a teammate (#3357).
	Doing string `json:"doing,omitempty"`
	Last  string `json:"last,omitempty"`
}

const teamPaneDetailMessages = 6

// The agent must be a row the pane already lists for this caller; the chat read uses
// the Team panel's own Live rule (crewTeamTerminalLiveRow) and is re-admitted after it.
func (api *StreamingAPI) teamPaneAgentDetail(ctx context.Context, t crewTeam, viewer, id string) (teamPaneDetail, bool) {
	var row *teamPaneAgent
	for _, a := range teamPaneAgents(crewTeamOwnSourceRows(ctx, t, viewer), crewTeamReachableIn(ctx, t, viewer)) {
		if a.ID == id {
			a := a
			row = &a
			break
		}
	}
	if row == nil {
		return teamPaneDetail{}, false
	}
	out := teamPaneDetail{teamPaneAgent: *row, Messages: []teamPaneMessage{}}
	if registered, ok := t.Agents[id]; !ok || registered.Kind != "terminal" {
		out.Note = "open_in_app"
		if row.Mine {
			api.teamPaneOwnAppAgentChat(ctx, t, viewer, id, &out)
		} else {
			api.teamPaneAppAgentChat(ctx, t, viewer, id, &out)
		}
		return out, true
	}
	a, err := crewTeamTerminalLiveRow(ctx, t, viewer, id)
	if err != nil {
		out.Note = "not_live"
		return out, true
	}
	payload, _ := json.Marshal(map[string]string{"provider": a.Terminal.Provider, "session": a.ChatKey, "attachment": a.Terminal.AttachmentID, "agent": a.Terminal.NamedAgentID, "folder": t.Folder.ID})
	result := api.readCrewTerminalMac(ctx, t.ProjectID, a, crewChatCall{Operation: "native_terminal_read", Args: payload})
	current, readErr := readCrewTeam(t.ProjectID)
	again, checkErr := crewTeamTerminalLiveRow(ctx, current, viewer, id)
	if readErr != nil || checkErr != nil || again.ChatKey != a.ChatKey {
		out.Note = "not_live"
		return out, true
	}
	var body struct {
		Messages []builderConversationMessage `json:"messages"`
	}
	if result.Status != http.StatusOK || json.Unmarshal(result.Body, &body) != nil {
		out.Note = "unavailable"
		return out, true
	}
	for _, m := range body.Messages {
		if m.ToolID != "" || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		parts := []string{}
		for _, p := range m.Parts {
			if text := strings.TrimSpace(p.Text); text != "" {
				parts = append(parts, text)
			}
		}
		if text := strings.Join(parts, "\n"); text != "" {
			if r := []rune(text); len(r) > 600 {
				text = string(r[:599]) + "…"
			}
			out.Messages = append(out.Messages, teamPaneMessage{Role: m.Role, Text: text})
		}
	}
	if n := len(out.Messages); n > teamPaneDetailMessages {
		out.Messages = out.Messages[n-teamPaneDetailMessages:]
	}
	return out, true
}

// teamPaneAppAgentChat fills an AgentWorks app agent's current task and, when its chat is
// shared Live with this viewer, its last few messages: the same chat the Team panel opens.
// Admission is the viewer's canonical reach set plus the agent's Live rule, checked again
// after the read. A server agent's chat comes from its saved history; a Mac agent's from its
// Mac over the Crew's relay, or the server's relay copy while that Mac is off.
func (api *StreamingAPI) teamPaneAppAgentChat(ctx context.Context, t crewTeam, viewer, id string, out *teamPaneDetail) {
	admitted := func() (reachableAgentRuntime, crewTeamRegisteredAgent, bool) {
		current, err := readCrewTeam(t.ProjectID)
		if err != nil {
			return reachableAgentRuntime{}, crewTeamRegisteredAgent{}, false
		}
		registered, ok := current.Agents[id]
		if !ok || registered.Kind == "terminal" {
			return reachableAgentRuntime{}, crewTeamRegisteredAgent{}, false
		}
		for _, r := range api.reachableAgentRuntimeInfo(ctx, viewer) {
			if r.Reach.ID == id && r.Reach.Team == t.ProjectID {
				live := r.Reach.ChatShare == crewChatShareLive && registered.Rule.ChatShare == crewChatShareLive &&
					current.agentChatShareFor(ctx, viewer, registered.OwnerID, registered.Tab) == crewChatShareLive
				if post, ok := crewTeamActivityPostsFor(current, viewer).Doing[id]; ok {
					out.Doing = crewTeamMessageLine(post.Line)
				}
				return r, registered, live
			}
		}
		return reachableAgentRuntime{}, crewTeamRegisteredAgent{}, false
	}
	r, registered, live := admitted()
	if !live {
		if registered.ID != "" {
			out.Note = "not_live" // its owner keeps the chat Hidden; the Team sees only its status
		}
		return
	}
	if r.Info != nil {
		if r.Info.OwnerStatus == nil && r.Record != nil {
			r.Info.OwnerStatus = api.ownerStatusForRecord(ctx, r.Owner, *r.Record)
		}
		if s := r.Info.OwnerStatus; s != nil {
			if out.Doing == "" {
				out.Doing = s.CurrentTask
			}
			if len(s.LastMessages) > 0 {
				out.Last = s.LastMessages[len(s.LastMessages)-1]
			}
		}
	}
	messages, source := api.teamPaneAppAgentMessages(ctx, t.ProjectID, viewer, registered, r.Record)
	// A rule change or an un-share during the read releases nothing.
	if _, again, ok := admitted(); !ok || again.ChatKey != registered.ChatKey || again.OwnerID != registered.OwnerID {
		out.Doing, out.Last = "", ""
		return
	}
	if len(messages) > 0 {
		out.Messages, out.Note, out.Last = messages, "", ""
	}
	logTeamPaneAppChat(t.ProjectID, viewer, id, source, len(messages))
}

// teamPaneOwnAppAgentChat is the person's own app agent: they read their own chat, whatever
// its Team rule, exactly as their own Crew shows it to them.
func (api *StreamingAPI) teamPaneOwnAppAgentChat(ctx context.Context, t crewTeam, viewer, id string, out *teamPaneDetail) {
	registered, ok := t.Agents[id]
	if !ok || registered.Kind == "terminal" || registered.OwnerID != viewer || !t.member(viewer) {
		return
	}
	var record *ProductConversationRecord
	if registered.Place != "mac" {
		source, err := api.agentDirectorySource(ctx, viewer)
		if err != nil {
			return
		}
		if found, ok := source.Document.Entries[productConversationRegistryEntryKey(crewProfileID, registered.ChatKey)]; ok {
			record = &found
		}
	}
	messages, from := api.teamPaneAppAgentMessages(ctx, t.ProjectID, viewer, registered, record)
	if len(messages) > 0 {
		out.Messages, out.Note = messages, ""
	}
	logTeamPaneAppChat(t.ProjectID, viewer, id, "own_"+from, len(messages))
}

// teamPaneAppAgentMessages reads an admitted app agent's recent chat: a Mac agent's from its
// Mac, a server agent's from its saved history.
func (api *StreamingAPI) teamPaneAppAgentMessages(ctx context.Context, project, viewer string, registered crewTeamRegisteredAgent, record *ProductConversationRecord) ([]teamPaneMessage, string) {
	if registered.Place == "mac" {
		return api.teamPaneMacAgentMessages(ctx, project, viewer, registered), "mac"
	}
	if record == nil || record.SessionID == "" {
		return nil, "none"
	}
	saved, ok, err := readRestoredChatHistoryPersistTargetForSession(registered.OwnerID, record.SessionID, record.WorkspacePath)
	if err != nil || !ok {
		return nil, "server"
	}
	return teamPaneHistoryMessages(saved.History), "server"
}

func logTeamPaneAppChat(project, viewer, id, source string, n int) {
	log.Printf("[TERMINAL_STATUS] project=%s viewer=%s pane_app_agent=%s source=%s messages=%d", project, logUsernameForUserID(viewer), id, source, n)
}

// teamPaneMacAgentMessages reads a Mac agent's recent chat events over its person's relay pair.
func (api *StreamingAPI) teamPaneMacAgentMessages(ctx context.Context, project, viewer string, a crewTeamRegisteredAgent) []teamPaneMessage {
	var conversation string
	for _, tab := range crewMacTabsFor(ctx, a.OwnerID, project, viewer) {
		if tab.Tab == a.Tab && !tab.OnlyMe {
			conversation = tab.ConversationID
		}
	}
	pair, ok := crewMacPairFor(a.OwnerID, project)
	if conversation == "" || !ok {
		return nil
	}
	var result crewChatResult
	if crewMacSocketFor(a.OwnerID, project, pair) == nil {
		result = api.crewMacOffRead(a.OwnerID, project, conversation, "events")
	} else {
		result = callCrewMacPair(ctx, a.OwnerID, project, pair, crewChatCall{Operation: "events", ConversationID: conversation, Query: url.Values{"limit": {"200"}, "durable_chat": {"1"}}}, 15*time.Second)
	}
	var body struct {
		Events []storeEvents.Event `json:"events"`
	}
	if result.Status != http.StatusOK || json.Unmarshal(result.Body, &body) != nil {
		return nil
	}
	return teamPaneEventMessages(body.Events)
}

// teamPaneEventMessages keeps the top-level asks and finished replies of a chat's events.
func teamPaneEventMessages(events []storeEvents.Event) []teamPaneMessage {
	out := []teamPaneMessage{}
	for _, e := range events {
		if e.Data == nil || e.Data.HierarchyLevel != 0 || (e.Type != "user_message" && e.Type != "llm_generation_end") {
			continue
		}
		var fields struct {
			Content string `json:"content"`
		}
		raw, err := json.Marshal(e.Data.Data)
		if err != nil || json.Unmarshal(raw, &fields) != nil {
			continue
		}
		role := "assistant"
		if e.Type == "user_message" {
			role = "user"
		}
		out = teamPaneAppend(out, role, fields.Content)
	}
	return teamPaneTail(out)
}

// teamPaneHistoryMessages keeps the visible asks and replies of a saved chat, as its preview does.
func teamPaneHistoryMessages(history []llmtypes.MessageContent) []teamPaneMessage {
	out := []teamPaneMessage{}
	for _, m := range history {
		role := strings.ToLower(string(m.Role))
		switch role {
		case "human", "user":
			role = "user"
		case "ai", "assistant":
			role = "assistant"
		default:
			continue
		}
		parts := []string{}
		for _, part := range m.Parts {
			if text := chatHistoryPartText(part); text != "" {
				parts = append(parts, text)
			}
		}
		text := cleanChatHistoryQuery(strings.Join(parts, "\n"))
		if shouldSkipChatHistoryPreviewText(text) || isClaudeLocalCommandMessage(role, text) {
			continue
		}
		out = teamPaneAppend(out, role, text)
	}
	return teamPaneTail(out)
}

func teamPaneAppend(out []teamPaneMessage, role, text string) []teamPaneMessage {
	text = strings.TrimSpace(redaction.Text(text))
	if text == "" {
		return out
	}
	if r := []rune(text); len(r) > 600 {
		text = string(r[:599]) + "…"
	}
	return append(out, teamPaneMessage{Role: role, Text: text})
}

func teamPaneTail(out []teamPaneMessage) []teamPaneMessage {
	if n := len(out); n > teamPaneDetailMessages {
		return out[n-teamPaneDetailMessages:]
	}
	return out
}

// One Team setup for this person (#3393): the same setups, steps and key states the Team page's
// Kit shows (setup_list / setup_get), as names and short lines only, never key values.
type teamPaneKit struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Purpose string   `json:"purpose,omitempty"`
	State   string   `json:"state"` // none, progress, done or key
	Steps   []string `json:"steps"`
	Done    int      `json:"done"`
	Keys    []string `json:"keys,omitempty"` // names of keys this person still has to add
}

func teamPaneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// First plain sentence of the shared instruction file (skipping headings and list marks).
func teamPanePurpose(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "---") {
			continue
		}
		return teamPaneLine(strings.TrimLeft(line, "-*> "), 200)
	}
	return ""
}

func (api *StreamingAPI) teamPaneKit(ctx context.Context, claims *UserClaims, project string) []teamPaneKit {
	out := []teamPaneKit{}
	team, err := crewChecklistTeam(claims, project, false)
	if err != nil {
		return out
	}
	data, err := readCrewChecklists(project)
	if err != nil {
		return out
	}
	state, err := readCrewTeamCurrentState(project)
	if err != nil {
		return out
	}
	for _, setup := range sortedCrewChecklists(data) {
		if len(out) == teamPaneLimit {
			break
		}
		if !crewChecklistFor(setup, claims.UserID) {
			continue
		}
		if token := claims.AccessToken; token != nil && token.FileGuard != nil && !token.FileGuard.Allows(setup.Instruction.Path, false) {
			continue
		}
		current, versionErr := crewChecklistInstructionVersion(claims, team, setup.Instruction.Path, state)
		if versionErr != nil {
			continue
		}
		done := crewChecklistDone(setup, setup.Progress[claims.UserID], current)
		item := teamPaneKit{ID: setup.ID, Title: teamPaneLine(setup.Title, 160), Steps: []string{}, Done: done, State: "none"}
		if text, e := crewChecklistReadInstructions(project, setup.Instruction.Path, state); e == nil {
			item.Purpose = teamPanePurpose(text)
		}
		for _, step := range setup.Items {
			if len(item.Steps) < 12 {
				item.Steps = append(item.Steps, teamPaneLine(step.Label, 200))
			}
		}
		switch {
		case done >= len(setup.Items):
			item.State = "done"
		case done > 0:
			item.State = "progress"
		}
		if item.State != "done" {
			for _, k := range api.crewChecklistNeedStates(ctx, project, setup, []string{claims.UserID}, current)[claims.UserID] {
				if !crewChecklistInstallable(k.crewChecklistKey) && k.State == "missing" && len(item.Keys) < 12 {
					item.State = "key"
					item.Keys = append(item.Keys, teamPaneLine(firstNonEmptyTrimmed(k.Name, k.Provider), 80))
				}
			}
		}
		out = append(out, item)
	}
	return out
}
