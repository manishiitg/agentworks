package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The Share page (owner, 9 Nov, approved mock "Share a Crew"): one page "Share this Crew" with three options, each a
// card with a status: Work on it with your team (the Team and the People list), Share agents (people ask the viewer's
// agents, or start actions the owner set up) and Read only (dashboards). The server writes every line, chip and status
// and says which controls this viewer may change; the page prints them and sends the person's change to the existing
// routes (Team save, people, agent-visibility, agent-rule, dashboard share), plus POST action for one action's on/off.
//
// It is its own reply, not part of /tab: /tab is re-read by every screen on each panel notice, and this reads the
// Crew's actions, its workflows and its dashboards, which are needed only while the page is open.

type crewShareOption struct {
	// Key is team, agents or read_only.
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	Line   string   `json:"line"`
	Chips  []string `json:"chips"`
	Status string   `json:"status"`
	On     bool     `json:"on"`
	// Open: the page opens this option's setup when it first shows.
	Open bool `json:"open"`
}

// crewShareAsk is an agent's Ask part: one switch (the agent is shared with the Team, POST agent-visibility) and, while
// on, its two permissions (POST agent-rule can_make_changes and chat_share).
type crewShareAsk struct {
	On        bool `json:"on"`
	CanChange bool `json:"can_change"`
	// Fixed says why the switch cannot be changed now; Note is one plain line under it.
	Fixed string `json:"fixed,omitempty"`
	Note  string `json:"note,omitempty"`
	// CanEdit: the viewer sets the two permissions (their own agent).
	CanEdit          bool                  `json:"can_edit"`
	CanMakeChanges   bool                  `json:"can_make_changes"`
	ChangesLabel     string                `json:"changes_label"`
	ChangesLine      string                `json:"changes_line"`
	ChatShare        string                `json:"chat_share"`
	ChatShareLabel   string                `json:"chat_share_label"`
	ChatShareOptions []crewChatShareOption `json:"chat_share_options"`
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
}

// crewShareAgent is one card of Share agents: one of the viewer's own agents (with Ask), or an agent-like card that
// has only actions (a workflow, or an agent named only by actions).
type crewShareAgent struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Line string `json:"line,omitempty"`
	// Target is "" for the Crew's own actions, or the workflow's id for a workflow's (sent back with POST action).
	Target  string            `json:"target"`
	Ask     *crewShareAsk     `json:"ask,omitempty"`
	Actions []crewShareAction `json:"actions"`
	// ActionsLine is the muted line of the Actions part ("None yet." when there are none).
	ActionsLine string `json:"actions_line"`
	// ConnectedLine is the muted line under a workflow card for Crew-to-Crew connections (no ids): "Called by 3 other Crews".
	ConnectedLine string `json:"connected_line,omitempty"`
	// SetupAsk is the draft "✦ Ask AI to set up an action" opens in the viewer's Main; WizardFor names the agent in the
	// first-time wizard (no actions yet). Both empty when the viewer cannot set up actions here.
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
	// Read only: how many dashboards, and the draft "Ask the Crew" opens in Main when there is none.
	Dashboards   int    `json:"dashboards"`
	DashboardAsk string `json:"dashboard_ask,omitempty"`
	// SharedWithYou: what other people in this Crew share with the viewer (people -> agents -> actions), read only.
	// Empty (the page prints no heading) when nobody shares anything.
	SharedWithYou []crewShareSharedPerson `json:"shared_with_you"`
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
		case crewChatShareHistory:
			can += " · see its saved chat"
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
				if sched.Enabled && !crewShareIsBinding(sched.Function) {
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

var crewShareChatOptions = []crewChatShareOption{{crewChatShareLive, "Live"}, {crewChatShareHistory, "Saved only"}, {crewChatShareAgent, "Hidden"}}

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
	records, err := readCrewExecutables()
	if err != nil || crewWorkspace == "" {
		return nil
	}
	out := []crewShareWorkflow{}
	for _, binding := range records {
		if binding.CrewRoot != crewWorkspace {
			continue
		}
		manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
		if err != nil || !found || manifest == nil {
			continue
		}
		wf := crewShareWorkflow{id: binding.ID, root: binding.Root, label: firstNonEmptyTrimmed(manifest.Label, "Workflow")}
		for _, sched := range manifest.Schedules {
			if sched.IsFunctionTrigger() {
				wf.functions = append(wf.functions, sched)
			}
		}
		out = append(out, wf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	return out
}

// crewShareBindingName matches the name generated for a Crew-to-Crew connection ("binding_<uuid>"): a connection,
// not an action a person set up.
var crewShareBindingName = regexp.MustCompile(`^binding_[0-9a-f_-]{16,}$`)

// crewShareIsBinding: a generated connection, or an unnamed function that only names callers.
func crewShareIsBinding(fn *WorkflowFunctionSpec) bool {
	return crewShareBindingName.MatchString(fn.Name) || (strings.TrimSpace(fn.Description) == "" && len(fn.AllowedCallers) > 0)
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
		if fn.Name == "" || fn.Name == crewFunctionAskName || crewShareBindingName.MatchString(fn.Name) {
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
		a := crewShareAction{Name: fn.Name, Title: title, Line: line, Inputs: crewShareInputWords(crewShareSchemaInputs(fn.InputSchema)), On: on, CanChange: canChange && !published}
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
	return fmt.Sprintf("Help me set up another action for %s in this Crew. Ask me what it should do, what people give it and what they get back, then show me the action before you save it.", name)
}

// crewShareWhoLine is "Who can use these: ...": the people who can ask the shared agents and start the actions.
func crewShareWhoLine(t crewTeam, everyone bool, named []string) string {
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
	if len(parts) == 0 {
		return "Who can use these: only you so far"
	}
	return "Who can use these: " + strings.Join(parts, ", ") + ", in the app, Slack, their own AI apps and other Crews"
}

func (api *StreamingAPI) crewSharePageFor(r *http.Request, t crewTeam, viewer string) crewSharePage {
	ctx := r.Context()
	owner := viewer == t.OwnerID
	onMac := crewOwnerOnlineOfMacCrew(r, t.ProjectID, viewer)
	tab := crewTeamTabFor(ctx, t, viewer)
	root := crewProjectRootByID(t.ProjectID)
	everyone, named := crewPeopleShareRecord(root)
	page := crewSharePage{Title: "Share this Crew", Lead: "Three ways. Use any of them. Click one to set it up.", Agents: []crewShareAgent{}, AgentsIntro: crewShareActionsIntro,
		WhoCanUse: crewShareWhoLine(t, everyone, named)}

	// Share agents: the viewer's own agents with Ask, the owner's actions on them, then cards with actions only.
	canActions := owner && !onMac
	var groups map[string][]crewShareAction
	var groupNames map[string]string
	var workflows []crewShareWorkflow
	dashWorkspace := ""
	if src, ok := api.crewShareSourceFor(ctx, viewer, t.ProjectID); ok {
		groups, groupNames = crewShareActionsFor(ctx, src, canActions)
		workflows = crewShareWorkflows(ctx, src.workspace)
		dashWorkspace = src.workspace
	}
	sharedAgents, actionsOn := 0, 0
	for _, a := range tab.Agents {
		if !a.Mine {
			continue
		}
		ask := &crewShareAsk{On: a.InTeam, CanChange: a.CanChange && !onMac, CanEdit: a.CanEditRule && !onMac,
			ChangesLabel: "Let them make changes", ChangesLine: "Off: they only get answers.", ChatShare: a.ChatShare, ChatShareLabel: "Your chat with it", ChatShareOptions: crewShareChatOptions}
		if a.CanMakeChanges != nil {
			ask.CanMakeChanges = *a.CanMakeChanges
		}
		switch {
		case onMac:
			ask.Fixed = crewChangeOnMacLine
		case !t.On:
			ask.Note = crewShareTeamOffNote
		}
		if a.InTeam && t.On {
			sharedAgents++
		}
		card := crewShareAgent{Key: a.Key, Name: a.Name, Line: firstNonEmptyTrimmed(a.PlaceLabel, a.Detail), Ask: ask, Actions: []crewShareAction{}}
		if owner {
			key := crewAgentChatID(a.Name)
			card.Actions = append(card.Actions, groups[key]...)
			delete(groups, key)
		}
		page.Agents = append(page.Agents, card)
	}
	// Agents named only by actions (or the owner's agents, for a teammate): cards with their actions only.
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		line := "Runs in its own tab"
		if !owner {
			line = crewPersonShortName(t.OwnerID) + "'s actions"
		}
		page.Agents = append(page.Agents, crewShareAgent{Key: "actions:" + key, Name: groupNames[key], Line: line, Actions: groups[key]})
	}
	for _, wf := range workflows {
		card := crewShareAgent{Key: "workflow:" + wf.id, Name: wf.label, Line: "Workflow", Target: wf.id, Actions: []crewShareAction{}}
		callers := map[string]bool{}
		for _, sched := range wf.functions {
			if crewShareIsBinding(sched.Function) {
				for _, c := range sched.Function.AllowedCallers {
					callers[c.Type+":"+c.ID] = true
				}
				continue
			}
			title, line := crewShareActionWords(sched.Function.Name, firstNonEmptyTrimmed(sched.Function.Description, sched.Name))
			a := crewShareAction{Name: sched.Function.Name, Title: title, Line: line, Inputs: crewShareInputWords(sched.Function.Inputs), On: sched.Enabled, CanChange: canActions}
			if !canActions {
				a.Fixed = crewShareOwnerOnlyActions
			}
			card.Actions = append(card.Actions, a)
		}
		sort.Slice(card.Actions, func(i, j int) bool { return card.Actions[i].Title < card.Actions[j].Title })
		if n := len(callers); n > 0 {
			card.ConnectedLine = fmt.Sprintf("Called by %d other %s", n, plural(n, "Crew", "Crews"))
		}
		page.Agents = append(page.Agents, card)
	}
	page.SharedWithYou = crewShareSharedWithYou(t, viewer, tab.Agents, groups, workflows)
	for i := range page.Agents {
		card := &page.Agents[i]
		for _, a := range card.Actions {
			if a.On {
				actionsOn++
			}
		}
		if len(card.Actions) == 0 {
			card.ActionsLine = "None yet."
		}
		if canActions {
			name := card.Name
			if card.Target != "" {
				name = "the " + card.Name + " workflow"
			}
			if len(card.Actions) == 0 {
				card.WizardFor = name
			} else {
				card.SetupAsk = crewShareSetupAsk(name)
			}
		}
	}
	if len(page.Agents) == 0 {
		page.AgentsEmpty = "No agents to share yet."
	}

	// Read only: the Crew's published dashboards (the same rows as its Dashboards list).
	rows, _ := api.crewTeamDashboardRows(httptest.NewRecorder(), r, t, root, false)
	page.Dashboards = len(rows)
	// Shared means shared with someone: the same read the Crew menu prints, so both say the same thing.
	dashShared := false
	if page.Dashboards > 0 && dashWorkspace != "" {
		if _, shared, err := api.crewDashboardSharing(r, dashWorkspace); err == nil {
			dashShared = shared != "Not shared"
		}
	}
	if page.Dashboards == 0 && owner {
		page.DashboardAsk = "Make a dashboard for this Crew from its results, so people can look at them without changing anything. Show me before you publish it."
	}

	// The three options, each with its status.
	members := 1
	for _, p := range t.People {
		if crewTeamAccountActive(p.UserID) {
			members++
		}
	}
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
	readStatus := "Off"
	if page.Dashboards > 0 {
		noun := plural(page.Dashboards, "dashboard", "dashboards")
		if dashShared {
			readStatus = fmt.Sprintf("On · %d %s shared", page.Dashboards, noun)
		} else {
			readStatus = fmt.Sprintf("Off · %d %s not shared yet", page.Dashboards, noun)
		}
	}
	page.Options = []crewShareOption{
		{Key: "team", Title: "Work on it with your team", Line: "They build and run this Crew with you: same files, same agents, their own copy.", Chips: []string{"for: teammates", "they can change things"}, Status: teamStatus, On: t.On},
		{Key: "agents", Title: "Share agents", Line: "Let others ask your agents, or use actions you set up.", Chips: []string{"in the app", "Slack", "Claude, ChatGPT", "other Crews"}, Status: agentStatus, On: len(agentParts) > 0},
		{Key: "read_only", Title: "Read only", Line: "Others look at results on dashboards. They cannot change anything.", Chips: []string{"for: anyone you choose", "view only"}, Status: readStatus, On: dashShared},
	}
	anyOpen := false
	for i := range page.Options {
		page.Options[i].Open = page.Options[i].On && page.Options[i].Key != "read_only"
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
		log.Printf("[CREW_SHARE] project=%s viewer=%s agents=%d dashboards=%d elapsed_ms=%d", t.ProjectID, claims.UserID, len(page.Agents), page.Dashboards, elapsed.Milliseconds())
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
		for _, wf := range crewShareWorkflows(r.Context(), src.workspace) {
			if wf.id == in.Target {
				root = wf.root
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
