package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
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
			out = append(out, teamPaneAgent{ID: a.ID, Mine: mine, Name: a.Name, Person: a.Person, Kind: a.Kind, Online: a.Online, Activity: a.Activity, Offline: a.OfflineLine})
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
