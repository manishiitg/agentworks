package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The Share page (owner, 9 Nov, approved mocks "Share a Crew" and "Share in Slack"): one page "Share this Crew" with
// up to three options, each a card with a status: Work on it with your team (the Team and the People list), Share
// agents (people ask the viewer's agents, or start actions the owner set up) and Slack (the Crew's Slack bot, today's
// bot settings moved here from the Crew menu). Dashboards have their own Access tab next to Share (owner, 9 Nov:
// "we should have a separate tab for dashboards"; crew_dashboards_tab.go). The server writes every line, chip and
// status and says which controls this viewer may change; the page prints them and sends the person's change to the
// existing routes (Team save, people, agent-visibility, agent-rule, the Slack bot routes), plus POST action for one
// action's on/off.
//
// It is its own reply, not part of /tab: /tab is re-read by every screen on each panel notice, and this reads the
// Crew's actions and its workflows, which are needed only while the page is open.

type crewShareOption struct {
	// Key is team, agents or slack.
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
// on, what teammates can do (POST agent-rule can_make_changes) and whether they see its chat (chat_share). Every line is
// the server's; the page prints them.
type crewShareAsk struct {
	On        bool `json:"on"`
	CanChange bool `json:"can_change"`
	// Title is the switch's line ("Share coach with your team"); Line is its sub line, for on and off.
	Title string `json:"title"`
	Line  string `json:"line"`
	// Fixed says why the switch cannot be changed now; Note is one plain line under it.
	Fixed string `json:"fixed,omitempty"`
	Note  string `json:"note,omitempty"`
	// CanEdit: the viewer sets the permissions (their own agent).
	CanEdit        bool `json:"can_edit"`
	CanMakeChanges bool `json:"can_make_changes"`
	// ChangesLabel heads the two choices in ChangesOptions (Get answers; Get answers and make changes).
	ChangesLabel     string                `json:"changes_label"`
	ChangesOptions   []crewShareChangeOpt  `json:"changes_options"`
	ChatShare        string                `json:"chat_share"`
	ChatShareLabel   string                `json:"chat_share_label"`
	ChatShareOptions []crewChatShareOption `json:"chat_share_options"`
	// RulesLabel is the link to the agent's permissions ("Permissions for coach…").
	RulesLabel string `json:"rules_label"`
}

// crewShareChangeOpt is one choice of what teammates can do: Value is the can_make_changes it sends.
type crewShareChangeOpt struct {
	Value bool   `json:"value"`
	Label string `json:"label"`
	Line  string `json:"line"`
}

// crewShareAction is one action (a Crew function the owner declared, or a workflow's function trigger): its name (sent
// back with POST action), the words people see, its input names as tags and its own on/off.
type crewShareAction struct {
	Name      string   `json:"name"`
	Title     string   `json:"title"`
	Line      string   `json:"line,omitempty"`
	Inputs    []string `json:"inputs"`
	On        bool     `json:"on"`
	CanChange bool     `json:"can_change"`
	Fixed     string   `json:"fixed,omitempty"`
	// Target is "" for a Crew action, or the workflow's id for a workflow action (sent back with POST action).
	Target string `json:"target"`
	// Starts is what it starts: "Starts the workflow" or "Asks the agent <name>".
	Starts string `json:"starts"`
	// Who is "Who can ask: ..." (people allowed, and "N other Crews" that may call it; no ids).
	Who string `json:"who"`
}

// crewShareAgent is one card of Share agents: one of the viewer's own agents, with its Ask part. Actions are not per
// agent: they are one group below the cards.
type crewShareAgent struct {
	Key  string        `json:"key"`
	Name string        `json:"name"`
	Line string        `json:"line,omitempty"`
	Ask  *crewShareAsk `json:"ask,omitempty"`
}

// crewShareActionsGroup is the one "Actions" group: every action of the Crew (its own and the workflow's), once.
type crewShareActionsGroup struct {
	Title string `json:"title"`
	// Line is the intro under the title, or the muted "None yet." when there are no rows.
	Line string            `json:"line"`
	Rows []crewShareAction `json:"rows"`
	// SetupAsk is the draft "✦ Ask AI to set up an action" opens in the viewer's Main; WizardFor names what the
	// first-time wizard sets up for (no actions yet). Both empty when the viewer cannot set up actions here.
	SetupAsk  string `json:"setup_ask,omitempty"`
	WizardFor string `json:"wizard_for,omitempty"`
}

type crewSharePage struct {
	Title   string            `json:"title"`
	Lead    string            `json:"lead"`
	Options []crewShareOption `json:"options"`
	// Share agents: the cards, the line under them ("Who can use these: ..."), and its empty line.
	Agents      []crewShareAgent `json:"agents"`
	AgentsIntro string           `json:"agents_intro"`
	AgentsEmpty string           `json:"agents_empty,omitempty"`
	WhoCanUse   string           `json:"who_can_use"`
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
	as := "the Crew's owner"
	if owner {
		as = "you"
	}
	return &crewShareSlack{
		Rules: []crewShareSlackRule{
			{Allowed: true, Where: "In a channel:", Line: "anyone in that channel. It answers as " + as + ", in a thread; it can't change the Crew."},
			{Allowed: true, Where: "In a direct message:", Line: "people this Crew is shared with, as themselves, with their own access."},
			{Allowed: false, Line: "Guests, people from other Slack workspaces, and anyone whose Slack email isn't an AgentWorks account."},
		},
		Note: "Each Slack thread becomes its own chat in this Crew. Who the Crew is shared with is set under “Work on it with your team”.",
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

// crewSlackTabOffered: the Access page lists a Slack tab when this viewer can read the Crew's Slack bot choice and bots
// are allowed here (the same read as the Crew menu's Slack bot row); who may change it stays the Slack bot routes' own
// rule (can_select, can_manage).
func (api *StreamingAPI) crewSlackTabOffered(r *http.Request, viewer, project string) bool {
	src, ok := api.crewShareSourceFor(r.Context(), viewer, project)
	if !ok || src.workspace == "" {
		return false
	}
	bot, err := api.crewSlackBotChoiceFor(r, src.workspace, nil)
	return err == nil && bot.Allowed
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
	Name string `json:"name"`
	// Role is "Owner of this Crew" or "Team member".
	Role  string                 `json:"role"`
	Items []crewShareSharedEntry `json:"items"`
}

// crewShareSharedEntry is one shared agent or action: its name, where it runs or what it takes, and what the viewer can do.
type crewShareSharedEntry struct {
	Name string `json:"name"`
	Line string `json:"line"`
	Can  string `json:"can"`
}

// crewShareSharedWithYou lists the agents other people share with the viewer, from the Team tab's own rows, and the
// Crew owner's enabled actions when the viewer is not the owner. The viewer's rights come from the same rules the
// rows already carry (can_make_changes, chat_share); this adds no access.
func crewShareSharedWithYou(t crewTeam, viewer string, rows []crewTeamTabAgent, groups map[string][]crewShareAction, workflows []crewShareWorkflow) []crewShareSharedPerson {
	out := []crewShareSharedPerson{}
	at := map[string]int{}
	person := func(id, name string) *crewShareSharedPerson {
		i, ok := at[id]
		if !ok {
			role := "Team member"
			if id == t.OwnerID {
				role = "Owner of this Crew"
			}
			out = append(out, crewShareSharedPerson{Name: name, Role: role, Items: []crewShareSharedEntry{}})
			i = len(out) - 1
			at[id] = i
		}
		return &out[i]
	}
	for _, a := range rows {
		if a.Mine || !a.InTeam {
			continue
		}
		line := a.PlaceLabel
		if line == "" {
			line = "On the server · always on"
			if a.Place == "mac" {
				line = "On their Mac"
			}
		}
		can := "ask it"
		if a.CanMakeChanges != nil && *a.CanMakeChanges {
			can = "make changes"
		}
		switch a.ChatShare {
		case crewChatShareLive:
			can += " · see its live chat"
		default:
			can += " · chat hidden"
		}
		p := person(a.OwnerID, a.OwnerName)
		p.Items = append(p.Items, crewShareSharedEntry{Name: a.Name, Line: line, Can: "You can: " + can})
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
		for _, wf := range workflows {
			for _, sched := range wf.functions {
				if sched.Enabled && !crewShareIsBinding(sched) {
					title, _ := crewShareActionWords(sched.Function.Name, firstNonEmptyTrimmed(sched.Function.Description, sched.Name))
					actions = append(actions, crewShareAction{Title: title, Inputs: crewShareInputWords(sched.Function.Inputs), On: true})
				}
			}
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
			p.Items = append(p.Items, crewShareSharedEntry{Name: a.Title, Line: line, Can: "You can: use it"})
		}
	}
	return out
}

const (
	crewShareActionsIntro     = "Ready-made jobs others can start, like “Review a pull request”. They fill in a few details and get the result back. The job uses your access, but they never get it."
	crewShareOwnerOnlyActions = "Only the Crew's owner turns actions on or off. Ask the owner to change this action."
	crewSharePublishedAction  = "This action comes from published source. Change its source and publish again."
	crewShareTeamOffNote      = "Turn on “Work on it with your team” first. Then people in it can ask this agent."
)

var crewShareChatOptions = []crewChatShareOption{{crewChatShareLive, "Live"}, {crewChatShareAgent, "Hidden"}}

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

// crewShareWorkflow is one of the Crew's workflows with its function triggers, on or off.
type crewShareWorkflow struct {
	id, root, label string
	functions       []WorkflowSchedule
}

func crewShareWorkflows(ctx context.Context, crewWorkspace string) []crewShareWorkflow {
	// One workflow per Crew: an older Crew that still holds several shows its newest Python one only.
	binding := crewShownWorkflow(crewWorkspace)
	if binding == nil {
		return nil
	}
	manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found || manifest == nil {
		return nil
	}
	wf := crewShareWorkflow{id: binding.ID, root: binding.Root, label: firstNonEmptyTrimmed(manifest.Label, "Workflow")}
	for _, sched := range manifest.Schedules {
		if sched.IsFunctionTrigger() || crewShareIsBinding(sched) {
			wf.functions = append(wf.functions, sched)
		}
	}
	return []crewShareWorkflow{wf}
}

// Native loading projects InputMapping records as internal triggers with a
// caller. A named function's optional description and allowed callers are not
// evidence that it is a generated connection. An older record has no such
// marker but keeps its generated name (internalFunctionName: "binding_" and the
// id's uuid with underscores), so that name marks a connection too.
func crewShareIsBinding(schedule WorkflowSchedule) bool {
	if isInternalTriggerKind(schedule.Kind) && schedule.Caller != nil {
		return true
	}
	return schedule.Function != nil && crewShareIsBindingName(schedule.Function.Name)
}

var crewShareBindingName = regexp.MustCompile(`^binding_[0-9a-fA-F]{8}(_[0-9a-fA-F]{4}){3}_[0-9a-fA-F]{12}$`)

func crewShareIsBindingName(name string) bool {
	return crewShareBindingName.MatchString(strings.TrimSpace(name))
}

// crewShareBindingCallers are the keys of the Crews (or workflows) a connection is for: its caller, else the allowed
// callers an older record lists, else the connection itself (one other Crew).
func crewShareBindingCallers(schedule WorkflowSchedule) []string {
	callers := []triggerCaller{}
	if schedule.Caller != nil {
		callers = append(callers, *schedule.Caller)
	} else if schedule.Function != nil {
		callers = append(callers, schedule.Function.AllowedCallers...)
	}
	keys := []string{}
	for _, c := range callers {
		keys = append(keys, c.Type+":"+c.ID)
	}
	if len(keys) == 0 {
		keys = append(keys, "connection:"+schedule.ID)
	}
	return keys
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
	return "Who can use these: " + crewSharePeopleWords(t, everyone, named, 0)
}

// crewSharePeopleWords lists who may ask: the Team, people the Crew is shared with, and "N other Crews" (0: none).
func crewSharePeopleWords(t crewTeam, everyone bool, named []string, crews int) string {
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
		parts = append(parts, fmt.Sprintf("%d more %s you shared this Crew with", extra, plural(extra, "person", "people")))
	}
	if crews > 0 {
		parts = append(parts, fmt.Sprintf("%d other %s", crews, plural(crews, "Crew", "Crews")))
	}
	if len(parts) == 0 {
		return "only you so far"
	}
	if crews > 0 {
		return strings.Join(parts, ", ")
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
	page := crewSharePage{Title: "Share this Crew", Lead: "Two ways. Use any of them. Click one to set it up.", Agents: []crewShareAgent{}, AgentsIntro: crewShareActionsIntro,
		WhoCanUse: crewShareWhoLine(t, everyone, named)}

	// Share agents: the viewer's own agents with Ask, the owner's actions on them, then cards with actions only.
	canActions := owner && !onMac
	var groups map[string][]crewShareAction
	var workflows []crewShareWorkflow
	dashWorkspace := ""
	if src, ok := api.crewShareSourceFor(ctx, viewer, t.ProjectID); ok {
		groups, _ = crewShareActionsFor(ctx, src, canActions)
		workflows = crewShareWorkflows(ctx, src.workspace)
		dashWorkspace = src.workspace
	}
	sharedAgents, actionsOn := 0, 0
	for _, a := range tab.Agents {
		if !a.Mine {
			continue
		}
		// The last shared agent's switch is locked (each person keeps one shared); only that agent says why.
		locked := a.InTeam && !a.CanUnshare
		ask := &crewShareAsk{On: a.InTeam, CanChange: a.CanChange && !locked && !onMac, CanEdit: a.CanEditRule && !onMac,
			Title: "Share " + a.Name + " with your team", Line: "Off. Only you can chat with " + a.Name + ".",
			ChangesLabel: "What teammates can do with " + a.Name,
			ChangesOptions: []crewShareChangeOpt{
				{Value: false, Label: "Get answers", Line: "They ask, " + a.Name + " answers. " + a.Name + " changes nothing."},
				{Value: true, Label: "Get answers and make changes", Line: a.Name + " can also change files and settings when they ask."}},
			ChatShare: a.ChatShare, ChatShareLabel: "Can teammates see your own chat with " + a.Name + "?", ChatShareOptions: crewShareChatOptions,
			RulesLabel: "Permissions for " + a.Name + "…"}
		if a.InTeam {
			ask.Line = "Teammates can chat with " + a.Name + " and get answers."
		}
		if a.CanMakeChanges != nil {
			ask.CanMakeChanges = *a.CanMakeChanges
		}
		switch {
		case onMac:
			ask.Fixed = crewChangeOnMacLine
		case !t.On:
			ask.Note = crewShareTeamOffNote
		case locked:
			ask.Fixed = errCrewTeamLastShared.Error()
		}
		if a.InTeam && t.On {
			sharedAgents++
		}
		card := crewShareAgent{Key: a.Key, Name: a.Name, Line: firstNonEmptyTrimmed(a.PlaceLabel, a.Detail), Ask: ask}
		page.Agents = append(page.Agents, card)
	}
	// Actions: the Crew's own and the workflow's, once, in one group. Crew-to-Crew connections are not rows: they
	// are counted into the "Who can ask" line of the workflow's actions.
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := []crewShareAction{}
	for _, key := range keys {
		rows = append(rows, groups[key]...)
	}
	callers := map[string]bool{}
	for _, wf := range workflows {
		for _, sched := range wf.functions {
			if crewShareIsBinding(sched) {
				for _, key := range crewShareBindingCallers(sched) {
					callers[key] = true
				}
				continue
			}
			title, line := crewShareActionWords(sched.Function.Name, firstNonEmptyTrimmed(sched.Function.Description, sched.Name))
			a := crewShareAction{Name: sched.Function.Name, Title: title, Line: line, Inputs: crewShareInputWords(sched.Function.Inputs), On: sched.Enabled, CanChange: canActions, Target: wf.id, Starts: "Starts the workflow"}
			if !canActions {
				a.Fixed = crewShareOwnerOnlyActions
			}
			rows = append(rows, a)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Title < rows[j].Title })
	for i := range rows {
		crews := 0
		if rows[i].Target != "" {
			crews = len(callers)
		}
		rows[i].Who = "Who can ask: " + crewSharePeopleWords(t, everyone, named, crews)
		if rows[i].On {
			actionsOn++
		}
	}
	page.ActionsGroup = crewShareActionsGroup{Title: "Actions", Line: crewShareActionsIntro, Rows: rows}
	if len(rows) == 0 {
		page.ActionsGroup.Line = "None yet."
	}
	if canActions {
		if len(rows) == 0 {
			page.ActionsGroup.WizardFor = "this Crew"
			for _, a := range page.Agents {
				page.ActionsGroup.WizardFor = a.Name
				break
			}
		} else {
			page.ActionsGroup.SetupAsk = crewShareSetupAsk("this Crew")
		}
	}
	page.SharedWithYou = crewShareSharedWithYou(t, viewer, tab.Agents, groups, workflows)
	if len(page.Agents) == 0 {
		page.AgentsEmpty = "No agents to share yet."
	}

	// Slack is its own Access tab (owner, 9 Nov), offered by crewSlackTabOffered; this page only carries the "Who can
	// talk to it" lines the Slack tab prints.
	if dashWorkspace != "" {
		if bot, err := api.crewSlackBotChoiceFor(r, dashWorkspace, nil); err == nil && bot.Allowed {
			page.Slack = crewShareSlackRules(owner)
		}
	}

	// The options, each with its status.
	teamStatus := "Off"
	if t.On {
		teamStatus = "On"
	}
	agentParts := []string{}
	if sharedAgents > 0 {
		agentParts = append(agentParts, fmt.Sprintf("%d %s", sharedAgents, plural(sharedAgents, "agent", "agents")))
	}
	if actionsOn > 0 {
		agentParts = append(agentParts, fmt.Sprintf("%d %s", actionsOn, plural(actionsOn, "action", "actions")))
	}
	agentStatus := "Off"
	if len(agentParts) > 0 {
		agentStatus = "On · " + strings.Join(agentParts, " · ")
	}
	page.Options = []crewShareOption{
		{Key: "team", Title: "Work on it with your team", Line: "They build and run this Crew with you: same files, same agents, their own copy.", Chips: []string{"for: teammates", "they can change things"}, Status: teamStatus, On: t.On},
		{Key: "agents", Title: "Share agents", Line: "Let others ask your agents, or use actions you set up.", Chips: []string{"in the app", "Slack", "Claude, ChatGPT", "other Crews"}, Status: agentStatus, On: len(agentParts) > 0},
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
	return fmt.Errorf("This action is not saved in this Crew. It may come from a published agent, or it was removed. Refresh the page.")
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
		Target string `json:"target"`
		Name   string `json:"name"`
		On     bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		externalError(w, http.StatusBadRequest, "invalid_action", "Choose one action to turn on or off.")
		return
	}
	in.Name, in.Target = strings.TrimSpace(in.Name), strings.TrimSpace(in.Target)
	src, ok := api.crewShareSourceFor(r.Context(), claims.UserID, t.ProjectID)
	if !ok {
		externalError(w, http.StatusNotFound, "crew_unavailable", "This Crew can't be read right now. Try again.")
		return
	}
	root := crewFunctionRoot(r.Context(), src.target)
	if in.Target != "" {
		root = ""
		// A submitted target is authorized by its current Crew binding, not
		// by which saved workflow the screen happens to show.
		records, err := readCrewExecutables()
		if err == nil {
			for _, binding := range records {
				if binding.ID == in.Target && canonicalCrewWorkspaceRoot(binding.CrewRoot) == canonicalCrewWorkspaceRoot(src.workspace) {
					root = binding.Root
					break
				}
			}
		}
		if root == "" {
			externalError(w, http.StatusNotFound, "workflow_unavailable", "This workflow is not in this Crew any more. Refresh the page.")
			return
		}
	}
	if err := setProjectFunctionEnabled(r.Context(), root, in.Name, in.On); err != nil {
		externalError(w, http.StatusConflict, "action_unavailable", err.Error())
		return
	}
	if in.Target != "" && api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	log.Printf("[CREW_SHARE] project=%s actor=%s event=action_switch target=%s action=%s on=%v", t.ProjectID, logUsernameForUserID(claims.UserID), firstNonEmptyTrimmed(in.Target, "crew"), in.Name, in.On)
	externalJSON(w, map[string]any{"name": in.Name, "on": in.On})
}
