package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The share reply (owner, 9 Nov, approved mocks "Share a Crew" and "Share in Slack"; 11 Nov: Share split into Team and
// Online; 12 Nov: Share agents moved into Team › Agents): the Team option with its status, the Agents tab (people ask
// the viewer's agents, or start actions the owner set up), plus the Connections Slack bot rules.
// Dashboards have their own Online tab (crew_dashboards_tab.go). The server writes every line, chip and
// status and says which controls this viewer may change; the page prints them and sends the person's change to the
// existing routes (Team save, people, agent-visibility, agent-rule, the Slack bot routes), plus POST action for one
// action's on/off.
//
// It is its own reply, not part of /tab: /tab is re-read by every screen on each panel notice, and this reads the
// Crew's actions, which are needed only while the page is open.

type crewShareOption struct {
	// Key is team.
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	Line   string   `json:"line"`
	Chips  []string `json:"chips"`
	Status string   `json:"status"`
	On     bool     `json:"on"`
	// Open: the page opens this option's setup when it first shows.
	Open bool `json:"open"`
}

// crewShareAsk is an agent's share part: one switch (the agent is shared with the Team, POST agent-visibility) and, while
// on, whether they see its chat (chat_share); the owner's own rule is the only limit on what it does for them. Every line is
// the server's; the page prints them.
type crewShareAsk struct {
	On        bool `json:"on"`
	CanChange bool `json:"can_change"`
	// Title is the switch's line ("Teammates can ask it"); Line is its sub line, for on and off.
	Title    string `json:"title"`
	Line     string `json:"line"`
	Summary  string `json:"summary"`
	UsesLine string `json:"uses_line,omitempty"`
	// Fixed says why the switch cannot be changed now; Note is one plain line under it.
	Fixed string `json:"fixed,omitempty"`
	Note  string `json:"note,omitempty"`
	// CanEdit: the viewer sets the permissions (their own agent).
	CanEdit          bool                  `json:"can_edit"`
	ChatShare        string                `json:"chat_share"`
	ChatShareLabel   string                `json:"chat_share_label"`
	ChatShareOptions []crewShareChatOption `json:"chat_share_options"`
	// RulesLabel is the link to the agent's permissions ("Permissions…").
	RulesLabel string `json:"rules_label"`
	// HiddenLine is the card's one hint while the viewer's own chat is Hidden.
	HiddenLine string `json:"hidden_line"`
	// SetupAsk is the draft "✦ Ask AI to configure" opens in the viewer's own agent for this card.
	SetupAsk string `json:"setup_ask,omitempty"`
}

type crewShareChatOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Line    string `json:"line"`
	Example string `json:"example"`
}

// crewShareAction is one action (a Crew function the owner declared): its name (sent
// back with POST action), the words people see, its input names as tags and its own on/off.
type crewShareAction struct {
	Name      string   `json:"name"`
	Title     string   `json:"title"`
	Line      string   `json:"line,omitempty"`
	Inputs    []string `json:"inputs"`
	On        bool     `json:"on"`
	CanChange bool     `json:"can_change"`
	Fixed     string   `json:"fixed,omitempty"`
	// Starts is what it starts: "Asks the agent <name>".
	Starts string `json:"starts"`
	// Who is "Who can ask: ..." (people allowed, and "N other Crews" that may call it; no ids).
	Who string `json:"who"`
}

// crewShareAgent is one card of Team › Agents: one of the viewer's own agents, with its Ask part. Actions are not per
// agent: they are one group below the cards.
type crewShareAgent struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Line string `json:"line,omitempty"`
	// Hint is optional hover text for the agent's placement.
	Hint string        `json:"hint,omitempty"`
	Ask  *crewShareAsk `json:"ask,omitempty"`
}

// crewShareActionsGroup is the one "Actions" group: every action the Crew declared, once.
type crewShareActionsGroup struct {
	Hidden bool   `json:"hidden,omitempty"`
	Title  string `json:"title"`
	// Line is the intro under the title, or the muted "None yet." when there are no rows.
	Line string            `json:"line"`
	Rows []crewShareAction `json:"rows"`
	// SetupAsk is the draft "✦ Ask AI to set up an action" opens in the viewer's Main; WizardFor names what the
	// first-time wizard sets up for (no actions yet). Both empty when the viewer cannot set up actions here.
	SetupAsk  string `json:"setup_ask,omitempty"`
	WizardFor string `json:"wizard_for,omitempty"`
}

// crewSharePage feeds the Team page: its team option's title and line, and its Agents tab.
type crewSharePage struct {
	Options []crewShareOption `json:"options"`
	// Agents: the cards, the line under them ("Who can use these: ..."), and its empty line.
	Agents      []crewShareAgent `json:"agents"`
	AgentsIntro string           `json:"agents_intro"`
	AgentsEmpty string           `json:"agents_empty,omitempty"`
	// AgentsSetupAsk is the draft the list's "✦ Ask AI to configure" opens, for all of the viewer's agents.
	AgentsSetupAsk string `json:"agents_setup_ask,omitempty"`
	WhoCanUse      string `json:"who_can_use"`
	// ActionsGroup: all actions in one group under the agents.
	ActionsGroup crewShareActionsGroup `json:"actions_group"`
	// SharedWithYou: what other people in this Crew share with the viewer (people -> agents -> actions), read only.
	// Empty (the page prints no heading) when nobody shares anything.
	SharedWithYou []crewShareSharedPerson `json:"shared_with_you"`
	// Slack: the Slack option's "Who can talk to it" lines; nil when the Slack option is not shown.
	Slack *crewShareSlack `json:"slack,omitempty"`
}

// crewShareSlack is Share › Slack's "Who can talk to it": today's Slack message rules in plain words (no new rule).
type crewShareSlack struct {
	Rules []crewShareSlackRule `json:"rules"`
	Note  string               `json:"note"`
}

// crewShareSlackRule is one line: who may talk to the Crew there (Allowed) or who is refused.
type crewShareSlackRule struct {
	Allowed bool   `json:"allowed"`
	Where   string `json:"where,omitempty"`
	Line    string `json:"line"`
}

// crewShareSlackRules words today's rules: a channel @mention answers as the Crew's owner in a thread (Run mode, no
// changes to the Crew); a direct message runs as its sender with their own access, for people the Crew is shared with;
// guests, other workspaces and Slack emails that are not exactly one AgentWorks account are refused.
func crewShareSlackRules(owner bool) *crewShareSlack {
	as := "the project's owner"
	if owner {
		as = "you"
	}
	return &crewShareSlack{
		Rules: []crewShareSlackRule{
			{Allowed: true, Where: "In a channel:", Line: "anyone in that channel. It answers as " + as + ", in a thread; it can't change the project."},
			{Allowed: true, Where: "In a direct message:", Line: "people this project is shared with, as themselves, with their own access."},
			{Allowed: false, Line: "Guests, people from other Slack workspaces, and anyone whose Slack email isn't an AgentWorks account."},
		},
		Note: "Each Slack thread becomes its own chat in this project. Who the project is shared with is set under Team.",
	}
}

// crewSlackBotChoice is the Slack bot a Crew answers with, from the same /api/integrations/slack/bots reply its Slack
// settings print: the Crew menu's Slack bot row and the Share page's Slack option say the same thing.
type crewSlackBotChoice struct {
	// Allowed is false when this Crew may not use Slack bots (an administrator's limit).
	Allowed bool
	// Selected: a saved bot is chosen for this Crew.
	Selected                      bool
	ID, Name                      string
	Configured, Enabled, Verified bool
}

// Label is the chosen bot's state: Needs setup, Paused, Identity verified or Saved.
func (c crewSlackBotChoice) Label() string {
	switch {
	case !c.Configured:
		return "Needs setup"
	case !c.Enabled:
		return "Paused"
	case c.Verified:
		return "Identity verified"
	}
	return "Saved"
}

// Status is the Share page's Slack status: On · <bot> when a set-up, enabled bot is chosen, else Needs setup, Paused
// or Off.
func (c crewSlackBotChoice) Status() (string, bool) {
	switch {
	case !c.Selected:
		return "Off", false
	case !c.Configured:
		return "Needs setup", false
	case !c.Enabled:
		return "Paused", false
	}
	return "On · " + c.Name, true
}

func (api *StreamingAPI) crewSlackBotChoiceFor(r *http.Request, workspace string, vars map[string]string) (crewSlackBotChoice, error) {
	var data struct {
		Selected string `json:"selected_id"`
		Allow    *bool  `json:"allow_bots"`
		Bots     []struct {
			ID, Name            string
			Configured, Enabled bool
			Identity            struct {
				Name     string
				Verified bool
			}
		}
	}
	err := crewPanelRead(r, api.handleSlackVaultBots, http.MethodGet, "/api/integrations/slack/bots", vars, url.Values{"workspace_path": {workspace}, "profile_id": {"work"}}, nil, &data)
	choice := crewSlackBotChoice{Allowed: data.Allow == nil || *data.Allow}
	for _, b := range data.Bots {
		if b.ID != data.Selected {
			continue
		}
		name := b.Identity.Name
		if name == "" {
			name = b.Name
		}
		if name == "Default" {
			name = "Saved Slack bot"
		}
		choice.Selected, choice.ID, choice.Name = true, b.ID, name
		choice.Configured, choice.Enabled, choice.Verified = b.Configured, b.Enabled, b.Identity.Verified
		break
	}
	return choice, err
}

// crewShareSharedPerson is one person whose agents (and, for the Crew's owner, actions) are shared with the viewer.
type crewShareSharedPerson struct {
	userAvatarFields
	Name string `json:"name"`
	// Role is "Owner of this Crew" or "Team member". Where says where their agents run now: "Mac on", "Mac off" or
	// "agents on the server" (from their rows' places, the same words as the Team header).
	Role  string                 `json:"role"`
	Where string                 `json:"where,omitempty"`
	Items []crewShareSharedEntry `json:"items"`
}

// crewShareSharedEntry is one shared agent or action: its name, where it runs or what it takes, and what the viewer can do.
type crewShareSharedEntry struct {
	TerminalAgentID string `json:"terminal_agent_id,omitempty"`
	Name            string `json:"name"`
	Line            string `json:"line"`
	// Working: it is working now. Watch: its chat is shared live, so the viewer may watch it. Address is the agent's
	// AgentBridge address for "Ask" from the viewer's own agent; empty for an action.
	Working bool   `json:"working,omitempty"`
	Watch   bool   `json:"watch,omitempty"`
	Address string `json:"address,omitempty"`
	// Key is a teammate agent's chat key (the same watch key the Team panel opens), so its row opens that
	// agent (owner, 12 Nov); empty for an action. OwnerID picks that person's view of the chat.
	Key     string `json:"key,omitempty"`
	OwnerID string `json:"owner_id,omitempty"`
	// AgentID is the Team agent's ID for Fork; CopyBytes is the size of its conversation copy on this server.
	AgentID   string `json:"agent_id,omitempty"`
	CopyBytes int64  `json:"copy_bytes,omitempty"`
}

// crewShareSharedWithYou lists the agents other people share with the viewer, from the Team tab's own rows, and the
// Crew owner's enabled actions when the viewer is not the owner. The viewer's rights come from the same rules the
// rows already carry (audience, chat_share); this adds no access. Each agent carries one status line, worded
// here: working shows its Doing post, waiting shows who it waits on, idle shows its last activity time.
func crewShareSharedWithYou(t crewTeam, viewer string, rows []crewTeamTabAgent, live crewTeamLiveView, groups map[string][]crewShareAction) []crewShareSharedPerson {
	out := []crewShareSharedPerson{}
	at := map[string]int{}
	person := func(id, name string) *crewShareSharedPerson {
		i, ok := at[id]
		if !ok {
			role := "Team member"
			if id == t.OwnerID {
				role = "Owner of this project"
			}
			out = append(out, crewShareSharedPerson{userAvatarFields: avatarFieldsFor(id, t.ProjectID), Name: name, Role: role, Items: []crewShareSharedEntry{}})
			i = len(out) - 1
			at[id] = i
		}
		return &out[i]
	}
	status := map[string]crewTeamLiveAgent{}
	for _, a := range live.Agents {
		status[a.ID] = a
		if a.ChatKey != "" {
			status[a.ChatKey] = a
		}
	}
	for _, a := range live.Idle {
		status[a.ID] = a
		if a.ChatKey != "" {
			status[a.ChatKey] = a
		}
	}
	for _, a := range rows {
		if a.Mine || !a.InTeam {
			continue
		}
		found, ok := status[a.Key]
		if !ok {
			found, ok = status[a.WatchKey]
		}
		line := crewShareTeammateStatusLine(a, found, ok)
		p := person(a.OwnerID, a.OwnerName)
		switch {
		case a.Place == "mac" && a.StatusLabel == "Mac off" && p.Where == "":
			p.Where = "Mac off"
		case a.Place == "mac" && a.StatusLabel != "Mac off":
			p.Where = "Mac on"
		case a.Place != "mac" && p.Where == "":
			p.Where = "agents on the server"
		}
		entry := crewShareSharedEntry{Name: a.Name, Line: line, Working: a.Activity == "running", Watch: a.ChatShare == crewChatShareLive, Address: a.Address, Key: firstNonEmptyTrimmed(a.WatchKey, a.Key), OwnerID: a.OwnerID}
		if registered, ok := t.Agents[a.Key]; ok {
			entry.AgentID = registered.ID
			entry.CopyBytes = crewOfflineCopyBytes(t.ProjectID, registered.ID)
		}
		if registered, ok := t.Agents[a.Key]; ok && registered.Kind == "terminal" {
			entry.TerminalAgentID = registered.ID
			entry.Key = "" // never open a native UUID through app-chat routing
		}
		p.Items = append(p.Items, entry)
	}
	if viewer != t.OwnerID {
		var actions []crewShareAction
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			actions = append(actions, groups[key]...)
		}
		for _, a := range actions {
			if !a.On {
				continue
			}
			line := "Action"
			if len(a.Inputs) > 0 {
				line += " · " + strings.Join(a.Inputs, ", ")
			}
			p := person(t.OwnerID, crewPersonShortName(t.OwnerID))
			p.Items = append(p.Items, crewShareSharedEntry{Name: a.Title, Line: line})
		}
	}
	return out
}

// crewShareTeammateStatusLine is the one status line under a teammate's agent: while it works its Doing post, while
// it waits who it waits on, otherwise its last activity time. Only me agents never reach here; a missing live row
// falls back to the row's own words, never to an invented state.
func crewShareTeammateStatusLine(row crewTeamTabAgent, live crewTeamLiveAgent, ok bool) string {
	switch row.Activity {
	case "running":
		if ok && live.Doing != "" {
			return live.Doing
		}
		return "Working"
	case "waiting":
		if ok && live.Waiting != "" {
			return live.Waiting
		}
		if row.StatusLabel != "" {
			return row.StatusLabel
		}
		return "Waiting"
	default:
		if ok && live.LastMessageAt != "" {
			if at, err := time.Parse(time.RFC3339, live.LastMessageAt); err == nil {
				return "Last active " + roughAgo(time.Since(at))
			}
		}
		if row.StatusLabel != "" {
			return row.StatusLabel
		}
		return "Idle"
	}
}

const (
	crewShareActionsIntro     = "Ready-made jobs others can start, like “Review a pull request”. They fill in a few details and get the result back. The job uses your access, but they never get it."
	crewShareOwnerOnlyActions = "Only the project's owner turns actions on or off. Ask the owner to change this action."
	crewSharePublishedAction  = "This action comes from published source. Change its source and publish again."
)

var crewShareChatOptions = []crewShareChatOption{
	{Value: crewChatShareLive, Label: "Yes, live", Example: "They see your conversation with this agent as it happens.", Line: "Example: a teammate can follow your questions and the agent's replies live, including earlier shared conversation."},
	{Value: crewChatShareAgent, Label: "No", Example: "They get their own thread with the agent; your conversation stays private.", Line: "Example: a teammate asks for a review in their own thread. They get that answer, not your private conversation with this agent."},
}

// crewShareSource is where this Crew's actions are read: its function target and its workspace path (which its
// workflows' records name as their Crew).
type crewShareSource struct {
	target    triggerTarget
	workspace string
}

func (api *StreamingAPI) crewShareSourceFor(ctx context.Context, viewer, project string) (crewShareSource, bool) {
	if api.projectActions == nil || api.projectActions.registry == nil {
		return crewShareSource{}, false
	}
	profile, err := api.projectActions.registry.Resolve(crewProfileID, 0, viewer)
	if err != nil {
		return crewShareSource{}, false
	}
	binding, err := resolveCrewProjectBinding(ctx, viewer, profile, project, "")
	if err != nil {
		return crewShareSource{}, false
	}
	ws := binding.Binding.WorkspacePath
	return crewShareSource{workspace: ws, target: triggerTarget{Kind: triggerCallerCrew, Path: ws, CrewID: project, CrewProfile: crewProfileID, CrewOwner: binding.OwnerID}}, true
}

var crewShareBindingName = regexp.MustCompile(`^binding_[0-9a-fA-F]{8}(_[0-9a-fA-F]{4}){3}_[0-9a-fA-F]{12}$`)

func crewShareIsBindingName(name string) bool {
	return crewShareBindingName.MatchString(strings.TrimSpace(name))
}

var crewShareAcronyms = map[string]string{"pr": "PR", "id": "ID", "url": "URL", "api": "API", "sql": "SQL", "csv": "CSV"}

func crewShareWords(name string) string {
	words := strings.Fields(strings.ReplaceAll(strings.ToLower(name), "_", " "))
	for i, w := range words {
		if up, ok := crewShareAcronyms[w]; ok {
			words[i] = up
		}
	}
	return strings.Join(words, " ")
}

// crewShareActionWords is an action's title and the line under it. A short first sentence of the description is the
// title ("Review a pull request"); otherwise the name in plain words ("review_pr" -> "Review PR"). The rest of the
// description is the line.
func crewShareActionWords(name, description string) (string, string) {
	description = strings.TrimSpace(description)
	if description != "" {
		first, rest, found := strings.Cut(description, ". ")
		if !found {
			first, rest = strings.TrimSuffix(description, "."), ""
		}
		if n := len([]rune(first)); n > 0 && n <= 60 && !strings.Contains(first, "\n") {
			return first, strings.TrimSpace(rest)
		}
	}
	words := crewShareWords(name)
	if words == "" {
		return "Action", description
	}
	return strings.ToUpper(words[:1]) + words[1:], description
}

// crewShareInputWords prints input names as plain words: GITHUB_OWNER -> "owner", PR_NUMBER -> "PR number".
func crewShareInputWords(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		words := crewShareWords(strings.TrimPrefix(strings.ToUpper(name), "GITHUB_"))
		if words == "" {
			words = name
		}
		out = append(out, words)
	}
	return out
}

func crewShareSchemaInputs(schema map[string]interface{}) []string {
	props, _ := schema["properties"].(map[string]interface{})
	out := []string{}
	for name := range props {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// crewShareActionsFor is the Crew's own actions, on and off, grouped by the agent they name ("main" for none). The
// implicit "ask" is never an action. A published agent's actions are listed but changed at their source.
func crewShareActionsFor(ctx context.Context, src crewShareSource, canChange bool) (map[string][]crewShareAction, map[string]string) {
	groups, names := map[string][]crewShareAction{}, map[string]string{}
	add := func(fn crewFunction, on, published bool) {
		// A generated Crew-to-Crew connection is not an action (crewShareIsBindingName).
		if fn.Name == "" || fn.Name == crewFunctionAskName || crewShareIsBindingName(fn.Name) {
			return
		}
		agent := strings.TrimSpace(fn.Agent)
		key := crewAgentChatID(agent)
		if agent == "" || agent == fn.Name {
			key, agent = "main", "Main" // a function in its own tab, or a new chat per call: the Crew's Main card
		}
		if _, ok := names[key]; !ok {
			names[key] = agent
		}
		title, line := crewShareActionWords(fn.Name, fn.Description)
		a := crewShareAction{Name: fn.Name, Title: title, Line: line, Inputs: crewShareInputWords(crewShareSchemaInputs(fn.InputSchema)), On: on, CanChange: canChange && !published, Starts: "Asks the agent " + agent}
		switch {
		case published:
			a.Fixed = crewSharePublishedAction
		case !canChange:
			a.Fixed = crewShareOwnerOnlyActions
		}
		groups[key] = append(groups[key], a)
	}
	if enabled, err := readCrewFunctions(ctx, src.target); err == nil {
		for _, fn := range enabled {
			add(fn, true, fn.publication != nil)
		}
	}
	root := crewFunctionRoot(ctx, src.target)
	if publication, err := readPublishedProject(ctx, root); err == nil {
		if publication != nil {
			root = publication.sourceRoot()
		}
		if records, err := readProjectFunctionDefinitions(ctx, root); err == nil {
			for _, record := range records {
				if record.Enabled || record.InputMapping != nil || record.Action.Type != "message" {
					continue
				}
				if fn, err := crewFunctionExecutionView(record); err == nil {
					add(fn, false, publication != nil)
				}
			}
		}
	}
	for key := range groups {
		sort.Slice(groups[key], func(i, j int) bool { return groups[key][i].Title < groups[key][j].Title })
	}
	return groups, names
}

func crewShareSetupAsk(name string) string {
	return fmt.Sprintf("Help me set up another action for %s. Ask me what it should do, what people give it and what they get back, then show me the action before you save it.", name)
}

// crewShareWhoLine is "Who can use these: ...": the people who can ask the shared agents and start the actions.
func crewShareWhoLine(t crewTeam, everyone bool, named []string) string {
	return "Who can use these: " + crewSharePeopleWords(t, everyone, named)
}

// crewSharePeopleWords lists who may ask: the Team, people the Crew is shared with, and "N other Crews" (0: none).
func crewSharePeopleWords(t crewTeam, everyone bool, named []string) string {
	parts := []string{}
	if t.On {
		joined := 0
		for _, p := range t.People {
			if !p.Invited && crewTeamAccountActive(p.UserID) {
				joined++
			}
		}
		if joined > 0 {
			parts = append(parts, fmt.Sprintf("your team (%d %s)", joined, plural(joined, "person", "people")))
		}
	}
	extra := 0
	for _, id := range named {
		if crewTeamAccountActive(id) && !t.member(id) {
			extra++
		}
	}
	switch {
	case everyone:
		parts = append(parts, "everyone on this server")
	case extra > 0:
		parts = append(parts, fmt.Sprintf("%d more %s you shared this project with", extra, plural(extra, "person", "people")))
	}
	if len(parts) == 0 {
		return "only you so far"
	}
	return strings.Join(parts, ", ") + ", in the app, Slack, their own AI apps and other Crews"
}

func (api *StreamingAPI) crewSharePageFor(r *http.Request, t crewTeam, viewer string) crewSharePage {
	ctx := r.Context()
	owner := viewer == t.OwnerID
	onMac := crewOwnerOnlineOfMacCrew(r, t.ProjectID, viewer)
	tab := crewTeamTabFor(ctx, t, viewer)
	root := crewProjectRootByID(t.ProjectID)
	everyone, named := crewPeopleShareRecord(root)
	page := crewSharePage{Agents: []crewShareAgent{}, AgentsIntro: crewShareActionsIntro,
		WhoCanUse: crewShareWhoLine(t, everyone, named)}

	// Agents: the viewer's own agents with Ask, then the Crew's actions.
	canActions := owner && !onMac
	var groups map[string][]crewShareAction
	dashWorkspace := ""
	if src, ok := api.crewShareSourceFor(ctx, viewer, t.ProjectID); ok {
		groups, _ = crewShareActionsFor(ctx, src, canActions)
		dashWorkspace = src.workspace
	}
	for _, a := range tab.Agents {
		if !a.Mine {
			continue
		}
		// The last shared agent's switch is locked (each person keeps one shared); only that agent says why.
		locked := a.InTeam && !a.CanUnshare
		ask := &crewShareAsk{On: a.InTeam, CanChange: a.CanChange && !locked && !onMac, CanEdit: a.CanEditRule && !onMac,
			Title: "Teammates can ask this agent", Line: "Not shared: only you and your agents can reach it.",
			ChatShare: a.ChatShare, ChatShareLabel: "Teammates see your chat", ChatShareOptions: crewShareChatOptions,
			HiddenLine: "Teammates see its answers, not your chat with it.", RulesLabel: "Permissions…"}
		if ask.CanEdit {
			ask.SetupAsk = "Explain your current Team sharing settings in plain words: who can ask you, whether you may act as me, and what they see of our chat. Offer changes that would help, but do not change anything unless I ask. "
			page.AgentsSetupAsk = "Help me set up which of my agents are shared with the team and who can ask them: "
		}
		if a.InTeam {
			ask.Line = "Teammates can ask " + a.Name + " for anything, as you would."
		}
		switch {
		case onMac:
			ask.Fixed = crewChangeOnMacLine
		case !t.On:
			ask.Line, ask.CanChange = "Turn on Team to share", false
		case locked:
			ask.Fixed = errCrewTeamLastShared.Error()
		}
		ask.Summary = crewShareAgentSummary(t.On, a)
		ask.UsesLine = crewShareAgentUses(a)
		if ask.SetupAsk != "" {
			ask.SetupAsk += "Current settings: " + ask.Summary
		}
		card := crewShareAgent{Key: a.Key, Name: a.Name, Line: firstNonEmptyTrimmed(a.PlaceLabel, a.Detail), Ask: ask}
		page.Agents = append(page.Agents, card)
	}
	// Actions: the Crew's own. A workflow is never offered to others: it stays private to the owner's Mac, or on the
	// server for an online Crew (owner, 23 Nov).
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := []crewShareAction{}
	for _, key := range keys {
		rows = append(rows, groups[key]...)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Title < rows[j].Title })
	for i := range rows {
		rows[i].Who = "Who can ask: " + crewSharePeopleWords(t, everyone, named)
	}
	page.ActionsGroup = crewShareActionsGroup{Hidden: IsLocalMode(), Title: "Actions", Line: crewShareActionsIntro, Rows: rows}
	if len(rows) == 0 {
		page.ActionsGroup.Line = "None yet."
	}
	if canActions {
		if len(rows) == 0 {
			page.ActionsGroup.WizardFor = "this project"
			for _, a := range page.Agents {
				page.ActionsGroup.WizardFor = a.Name
				break
			}
		} else {
			page.ActionsGroup.SetupAsk = crewShareSetupAsk("this project")
		}
	}
	page.SharedWithYou = crewShareSharedWithYou(t, viewer, tab.Agents, tab.Live, groups)
	if len(page.Agents) == 0 {
		page.AgentsEmpty = "No agents to share yet."
	}

	// Slack lives under Connections on server projects (owner, 14 Nov; local Mac projects keep Slack as me only, 25 Nov);
	// this reply supplies the unchanged Who can talk to it rules used by that settings panel.
	if dashWorkspace != "" && !IsLocalMode() {
		if bot, err := api.crewSlackBotChoiceFor(r, dashWorkspace, nil); err == nil && bot.Allowed {
			page.Slack = crewShareSlackRules(owner)
		}
	}

	// The options, each with its status.
	teamStatus := "Off"
	if t.On {
		teamStatus = "On"
	}
	page.Options = []crewShareOption{
		{Key: "team", Title: "Team", Line: "People who work on this project with you: the same files and agents, each on their own copy.", Chips: []string{"for: teammates", "they can change things"}, Status: teamStatus, On: t.On},
	}
	anyOpen := false
	for i := range page.Options {
		page.Options[i].Open = page.Options[i].On
		anyOpen = anyOpen || page.Options[i].Open
	}
	if !anyOpen {
		page.Options[0].Open = true
	}
	return page
}

// handleCrewSharePage is GET /crew-teams/{project}/share: the Share page for the owner and everyone in the Team.
func (api *StreamingAPI) handleCrewSharePage(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	started := time.Now()
	w.Header().Set("Cache-Control", "no-store")
	page := api.crewSharePageFor(r, t, claims.UserID)
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		log.Printf("[CREW_SHARE] project=%s viewer=%s agents=%d elapsed_ms=%d", t.ProjectID, claims.UserID, len(page.Agents), elapsed.Milliseconds())
	}
	externalJSON(w, page)
}

// setProjectFunctionEnabled turns one saved action on or off in a project's functions file. An action that is off
// stays saved but is not offered or callable (readCrewFunctionSource and workflow function triggers skip it).
func setProjectFunctionEnabled(ctx context.Context, root, name string, on bool) error {
	if on {
		if err := requireServerInteractive("function_enable"); err != nil {
			return err
		}
	}
	if refusal := publishedWriteRefusal(ctx, root+"/functions.json"); refusal != "" {
		return fmt.Errorf("%s", refusal)
	}
	records, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].Name != name || records[i].InputMapping != nil {
			continue
		}
		if records[i].Enabled == on {
			return nil
		}
		records[i].Enabled = on
		records[i].UpdatedAt = time.Now().UTC()
		return writeProjectFunctionDefinitions(ctx, root, records)
	}
	return fmt.Errorf("This action is not saved in this project. It may come from a published agent, or it was removed. Refresh the page.")
}

// handleCrewShareAction is POST /crew-teams/{project}/action {target, name, on}: one action's own switch on the Share
// page. Only the Crew's owner turns an action on or off (the same rule as changing its functions); an action that is
// off refuses every caller until it is on again.
func (api *StreamingAPI) handleCrewShareAction(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, true)
	if !ok {
		return
	}
	if claims.UserID != t.OwnerID {
		externalError(w, http.StatusForbidden, "owner_only", crewShareOwnerOnlyActions)
		return
	}
	if refuseCrewOwnerOnline(w, r, t.ProjectID, claims.UserID) {
		return
	}
	var in struct {
		Name string `json:"name"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		externalError(w, http.StatusBadRequest, "invalid_action", "Choose one action to turn on or off.")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	src, ok := api.crewShareSourceFor(r.Context(), claims.UserID, t.ProjectID)
	if !ok {
		externalError(w, http.StatusNotFound, "crew_unavailable", "This project can't be read right now. Try again.")
		return
	}
	root := crewFunctionRoot(r.Context(), src.target)
	if err := setProjectFunctionEnabled(r.Context(), root, in.Name, in.On); err != nil {
		externalError(w, http.StatusConflict, "action_unavailable", err.Error())
		return
	}
	log.Printf("[CREW_SHARE] project=%s actor=%s event=action_switch action=%s on=%v", t.ProjectID, logUsernameForUserID(claims.UserID), in.Name, in.On)
	externalJSON(w, map[string]any{"name": in.Name, "on": in.On})
}

// One plain description of the existing server choices; never an admission rule.
func crewShareAgentSummary(teamOn bool, a crewTeamTabAgent) string {
	if !teamOn || !a.InTeam {
		return "Only you and your agents can reach " + a.Name + "."
	}
	who := "Anyone on your team"
	if a.Audience == crewTeamAudienceChosen {
		names := []string{}
		for _, person := range a.People {
			names = append(names, person.Name)
		}
		if len(names) == 0 {
			return "Nobody else can ask " + a.Name + ". Pick the teammates who may ask."
		}
		who = strings.Join(names, ", ")
	}
	line := who + " can ask " + a.Name + ". "
	// No default limits (owner, 22 Nov): it helps them as it helps you; the owner's written rule is the only layer.
	if strings.TrimSpace(a.WhenOthersAsk) != "" {
		line += "It helps them as it helps you, following your rule when others ask. "
	} else {
		line += "It helps them as it helps you, using your files, tools and signed-in apps, personal ones included. "
	}
	if a.ChatShare == crewChatShareLive {
		line += "They can watch your chat live."
	} else {
		line += "They can't see your chat."
	}
	return line
}

func crewShareAgentUses(a crewTeamTabAgent) string {
	names := []string{}
	connections := 0
	for _, use := range a.Uses {
		names = append(names, use.Name)
		if use.Kind == "app" {
			connections++
		}
	}
	if len(names) == 0 {
		return firstNonEmptyTrimmed(a.UsesNote, "No connections selected")
	}
	line := strings.Join(names, " · ")
	if connections == 1 {
		line += " (1 connection)"
	} else if connections > 1 {
		line += fmt.Sprintf(" (%d connections)", connections)
	}
	return line
}

// crewOfflineCopyBytes is the size of an agent's conversation copy on this server; 0 when it has none.
func crewOfflineCopyBytes(project, agent string) int64 {
	dir, err := crewOfflineDirectory(project, agent)
	if err != nil {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
