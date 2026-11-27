package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Project tasks (approved mockup 24 Nov): one curated task list per project,
// shared by everyone who can open the project and their agents. Watchers send
// what they find to the project's lead agent; the lead drops noise, adds an
// event to the task it belongs to, or creates a task, and hands tasks to the
// best-fit agent. Each task keeps where it came from, what is linked to it
// (tickets, docs, threads, pull requests), what is happening on those, and who
// moved it. The server only stores the list and checks steps; agents decide.

const (
	projectTasksOpenShown = 200 // the Project tasks panel lists every open task
	projectTasksDoneShown = 20
	projectTaskGroupsMax  = 8
	projectTasksDoneKept  = 100
	projectTaskRefsMax    = 20
	projectTaskEventsMax  = 30
	projectTaskMovesMax   = 40
	projectTaskMovesShown = 8
	projectTasksSortedMax = 100
	projectTaskChecksMax  = 20
)

var projectTaskDefaultSteps = []string{"New", "Doing", "Review", "Done"}

type projectTaskRef struct {
	Kind  string `json:"kind"` // slack, mail, tasks, github, notion, drive, other
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	By    string `json:"by,omitempty"`
	At    int64  `json:"at"`
}

// projectTaskCheck is one checklist item inside a task: a small step an agent
// ticks off without handing work to anyone.
type projectTaskCheck struct {
	Text string `json:"text"`
	Done bool   `json:"done,omitempty"`
	By   string `json:"by,omitempty"`
	At   int64  `json:"at"`
}

type projectTaskEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
	At   int64  `json:"at"`
}

type projectTaskLink struct {
	conditionFeedTask
	Agent string `json:"agent,omitempty"`
}

// projectTaskGroup is one group the lead arranges the list into (#4294):
// by area, release, customer, whatever fits the project.
type projectTaskGroup struct {
	Name   string `json:"name"`
	Note   string `json:"note,omitempty"`
	Folded bool   `json:"folded,omitempty"` // starts folded in the panel (for example "Later")
}

type projectTask struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Group    string   `json:"group,omitempty"`  // one of the list's groups; "" or an unknown name shows under Other
	Pinned   bool     `json:"pinned,omitempty"` // first in its group
	Due      string   `json:"due,omitempty"`    // YYYY-MM-DD
	Step     string   `json:"step,omitempty"`
	Assignee string   `json:"assignee,omitempty"` // the agent or person who has it; "" = not handed out yet
	Proposed string   `json:"proposed,omitempty"` // "Ask me first": the agent the lead suggests, until the person approves
	Waiting  string   `json:"waiting,omitempty"`  // what the lead needs the person to decide
	Choices  []string `json:"choices,omitempty"`  // short answers for Waiting, shown as buttons
	Held     bool     `json:"held,omitempty"`     // the person put it on hold; the lead leaves it alone
	// Chase (owner, 27 Nov): where the lead's chasing stands, "nudged" or "stuck";
	// handing the task to someone clears it. From is who had it before a move.
	Chase    string              `json:"chase,omitempty"`
	ChaseAt  int64               `json:"chase_at,omitempty"`
	From     string              `json:"from,omitempty"`
	Tasks    []projectTaskLink   `json:"tasks,omitempty"`
	Checks   []projectTaskCheck  `json:"checklist,omitempty"`
	Sources  []projectTaskRef    `json:"sources,omitempty"`
	Links    []projectTaskRef    `json:"links,omitempty"`
	Activity []projectTaskEvent  `json:"activity,omitempty"`
	History  []conditionFeedMove `json:"history,omitempty"`
	Created  int64               `json:"created_at"`
	Updated  int64               `json:"updated_at"`
}

// projectTaskSorted is one decision the lead made about something a Watcher found.
type projectTaskSorted struct {
	At     int64  `json:"at"`
	Kind   string `json:"kind"` // dropped, added, created
	Text   string `json:"text"`
	URL    string `json:"url,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	By     string `json:"by,omitempty"`
}

// projectTaskMove is one line of "What the lead did": a hand-out, nudge, move,
// question or close, recorded by the server when a task changes that way.
type projectTaskMove struct {
	At     int64  `json:"at"`
	By     string `json:"by,omitempty"`
	Kind   string `json:"kind"` // gave, moved, nudged, stuck, suggested, asked, closed, step, held
	Text   string `json:"text"`
	TaskID string `json:"task_id,omitempty"`
}

type projectTaskList struct {
	Steps []string `json:"steps,omitempty"`
	Rules string   `json:"rules,omitempty"`
	Lead  string   `json:"lead,omitempty"` // the lead agent's name, for people reading the list
	// LeadChat is the lead's chat id in this project. While Paused, Watchers that
	// send to that chat hold their events and deliver them on resume.
	LeadChat string              `json:"lead_chat,omitempty"`
	Paused   bool                `json:"paused,omitempty"`
	Mode     string              `json:"mode,omitempty"` // auto (default): the lead hands work out; ask: it proposes and the person approves
	Groups   []projectTaskGroup  `json:"groups,omitempty"`
	Focus    string              `json:"focus,omitempty"` // the lead's one line on what matters now
	Next     int                 `json:"next"`
	Tasks    []projectTask       `json:"tasks"`
	Sorted   []projectTaskSorted `json:"sorted,omitempty"`
	Moves    []projectTaskMove   `json:"moves,omitempty"`
}

func (l projectTaskList) steps() []string {
	if len(l.Steps) >= 2 {
		return l.Steps
	}
	return projectTaskDefaultSteps
}

func (l projectTaskList) stepOf(t projectTask) string {
	steps := l.steps()
	for _, s := range steps {
		if strings.EqualFold(s, t.Step) {
			return s
		}
	}
	return steps[0]
}

// groupOf is the task's group as the list names it, or "" (Other) when the
// lead removed or never made that group.
func (l projectTaskList) groupOf(t projectTask) string {
	for _, g := range l.Groups {
		if strings.EqualFold(g.Name, t.Group) {
			return g.Name
		}
	}
	return ""
}

func projectTaskGroups(v interface{}) ([]projectTaskGroup, error) {
	raw, ok := v.([]interface{})
	if !ok {
		return nil, errors.New("groups is a list of {name, note, folded}, first to last")
	}
	if len(raw) > projectTaskGroupsMax {
		return nil, fmt.Errorf("keep it to %d groups or fewer; merge the small ones", projectTaskGroupsMax)
	}
	out := []projectTaskGroup{}
	seen := map[string]bool{}
	for _, item := range raw {
		m, _ := item.(map[string]interface{})
		name := feedText(m["name"], 30)
		if name == "" || strings.EqualFold(name, "Other") || strings.EqualFold(name, "Needs you") {
			return nil, errors.New("each group needs a short name (not Other or Needs you, which the panel adds itself)")
		}
		if seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("group %q is listed twice", name)
		}
		seen[strings.ToLower(name)] = true
		folded, _ := m["folded"].(bool)
		out = append(out, projectTaskGroup{Name: name, Note: feedText(m["note"], 80), Folded: folded})
	}
	return out, nil
}

// projectTaskDue reads due: YYYY-MM-DD, or "" to clear it.
func projectTaskDue(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", errors.New(`due is a date like 2026-12-04, or "" to clear it`)
	}
	return v, nil
}

// projectTaskPlace applies group, pinned and due from task_add or task_update.
func projectTaskPlace(list projectTaskList, t *projectTask, args map[string]interface{}) (bool, error) {
	changed := false
	if v, ok := args["group"].(string); ok {
		name := strings.TrimSpace(v)
		if name != "" {
			name = list.groupOf(projectTask{Group: name})
			if name == "" {
				return false, fmt.Errorf("%q isn't one of this list's groups; add it first with task_settings groups", strings.TrimSpace(v))
			}
		}
		t.Group, changed = name, true
	}
	if v, ok := args["pinned"].(bool); ok {
		t.Pinned, changed = v, true
	}
	if v, ok := args["due"].(string); ok {
		due, err := projectTaskDue(v)
		if err != nil {
			return false, err
		}
		t.Due, changed = due, true
	}
	return changed, nil
}

func (l projectTaskList) done(t projectTask) bool {
	steps := l.steps()
	return l.stepOf(t) == steps[len(steps)-1]
}

var projectTasksLock sync.Mutex

// projectTasksPath is the project's own shared file, beside its database.
func projectTasksPath(base productScheduleJob) string {
	return path.Join(base.WorkspacePath, "db", "project-tasks.json")
}

func (c *conditionService) projectTasksBase(ctx context.Context, actor, project string) (productScheduleJob, error) {
	base, err := c.source(ctx, actor, crewProfileID, project)
	if err == nil && strings.TrimSpace(base.WorkspacePath) == "" {
		err = errors.New("this project has no folder for its task list")
	}
	return base, err
}

func (c *conditionService) readProjectTasks(ctx context.Context, base productScheduleJob) (projectTaskList, error) {
	var list projectTaskList
	raw, found, err := c.s.readFile(ctx, projectTasksPath(base))
	if err == nil && found {
		err = json.Unmarshal([]byte(raw), &list)
	}
	return list, err
}

func (c *conditionService) writeProjectTasks(ctx context.Context, base productScheduleJob, list projectTaskList) error {
	b, err := json.Marshal(projectTasksTrim(list))
	if err != nil {
		return err
	}
	return c.s.writeFile(ctx, projectTasksPath(base), string(b))
}

// projectTasksTrim keeps every open task and only the newest finished ones, so
// work in progress never drops off the list.
func projectTasksTrim(list projectTaskList) projectTaskList {
	kept, done := []projectTask{}, 0
	for i := len(list.Tasks) - 1; i >= 0; i-- {
		if list.done(list.Tasks[i]) {
			if done >= projectTasksDoneKept {
				continue
			}
			done++
		}
		kept = append([]projectTask{list.Tasks[i]}, kept...)
	}
	list.Tasks = kept
	if len(list.Moves) > projectTaskMovesMax {
		list.Moves = list.Moves[len(list.Moves)-projectTaskMovesMax:]
	}
	if len(list.Sorted) > projectTasksSortedMax {
		list.Sorted = list.Sorted[len(list.Sorted)-projectTasksSortedMax:]
	}
	return list
}

func projectTaskKind(v interface{}) string {
	kind := strings.ToLower(feedText(v, 20))
	switch kind {
	case "slack", "mail", "tasks", "github", "notion", "drive":
		return kind
	}
	return "other"
}

func projectTaskRefArg(v interface{}, by string) (*projectTaskRef, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	ref := projectTaskRef{Kind: projectTaskKind(raw["kind"]), Title: feedText(raw["title"], 200), URL: feedURL(raw["url"]), By: by, At: time.Now().Unix()}
	if ref.Title == "" {
		return nil, errors.New("a source or link needs a title (and its kind and url when it has one)")
	}
	return &ref, nil
}

func addProjectTaskRef(refs []projectTaskRef, ref projectTaskRef) ([]projectTaskRef, error) {
	for _, r := range refs {
		if (ref.URL != "" && r.URL == ref.URL) || (ref.URL == "" && r.Title == ref.Title) {
			return refs, nil
		}
	}
	if len(refs) >= projectTaskRefsMax {
		return refs, fmt.Errorf("a task keeps at most %d sources and %d links", projectTaskRefsMax, projectTaskRefsMax)
	}
	return append(refs, ref), nil
}

// crewPanelProjectTask is one task as the side panel and the tool show it.
type crewPanelProjectTask struct {
	crewPanelFeedItem
	Assignee string `json:"assignee,omitempty"`
	// AssigneeCLI is the assignee's coding program, from its latest linked ask.
	AssigneeCLI string             `json:"assignee_cli,omitempty"`
	Sources     []projectTaskRef   `json:"sources,omitempty"`
	Links       []projectTaskRef   `json:"links,omitempty"`
	Activity    []projectTaskEvent `json:"activity,omitempty"`
	Checks      []projectTaskCheck `json:"checklist,omitempty"`
	Proposed    string             `json:"proposed,omitempty"`
	Held        bool               `json:"held,omitempty"`
	Chase       string             `json:"chase,omitempty"`
	ChaseAt     int64              `json:"chase_at,omitempty"`
	From        string             `json:"from,omitempty"`
	Choices     []string           `json:"choices,omitempty"`
	Group       string             `json:"group,omitempty"`
	Pinned      bool               `json:"pinned,omitempty"`
	Due         string             `json:"due,omitempty"`
	// Overdue: past its due date and not done; the server decides it.
	Overdue bool `json:"overdue,omitempty"`
	// NeedsYou says what the person must do; the server decides it so the panel
	// and the lead read the same "Needs you" list.
	NeedsYou string `json:"needs_you,omitempty"`
}

// mode is how the list is run (owner, 27 Nov): off keeps it a plain list
// nobody drives; ask has the lead propose and the person approve; auto (the
// default) has the lead hand work out and chase it.
func (l projectTaskList) mode() string {
	if l.Mode == "ask" || l.Mode == "off" {
		return l.Mode
	}
	return "auto"
}

// move records one line of "What the lead did".
func (l *projectTaskList) move(by, kind, text, taskID string, at int64) {
	l.Moves = append(l.Moves, projectTaskMove{At: at, By: by, Kind: kind, Text: feedText(text, 200), TaskID: taskID})
}

func projectTaskView(list projectTaskList, t projectTask, tasks *conditionFeedTasks) crewPanelProjectTask {
	step := list.stepOf(t)
	url := ""
	if len(t.Sources) > 0 {
		url = t.Sources[0].URL
	}
	view := crewPanelProjectTask{crewPanelFeedItem: crewPanelFeedItem{ID: t.ID, Title: t.Title, URL: url, At: t.Updated, Step: step, Done: list.done(t)}, Assignee: t.Assignee, Sources: t.Sources, Links: t.Links}
	for _, link := range t.Tasks {
		if task, ok := tasks.view(link.conditionFeedTask); ok {
			view.Tasks = append(view.Tasks, task)
		} else if link.Agent != "" {
			// Someone else's ask: its thread is theirs, so only who has it shows.
			view.Tasks = append(view.Tasks, crewPanelFeedTask{Agent: link.Agent, State: "asked"})
		}
	}
	for _, task := range view.Tasks {
		if task.CLI != "" && (task.Agent == t.Assignee || view.AssigneeCLI == "") {
			view.AssigneeCLI = task.CLI
		}
	}
	view.Proposed, view.Held, view.Checks = t.Proposed, t.Held, t.Checks
	view.From = t.From
	if !view.Done && !t.Held && list.mode() != "off" {
		view.Chase, view.ChaseAt = t.Chase, t.ChaseAt
	}
	view.Group, view.Pinned, view.Due = list.groupOf(t), t.Pinned, t.Due
	view.Overdue = t.Due != "" && !view.Done && t.Due < time.Now().Format("2006-01-02")
	if !view.Done && !t.Held {
		for _, task := range view.Tasks {
			if task.State == "needs_you" || task.State == "blocked" {
				view.NeedsYou = task.Agent + " is " + map[string]string{"needs_you": "waiting on you", "blocked": "blocked"}[task.State]
			}
		}
		switch {
		case t.Waiting != "":
			view.NeedsYou, view.Choices = t.Waiting, t.Choices
		case t.Proposed != "":
			view.NeedsYou = "Approve handing it to " + t.Proposed
		case t.Assignee == "" && view.NeedsYou == "" && step == list.steps()[0] && list.mode() != "off":
			// Only work not handed out yet; a task already past the first step needs nobody.
			view.NeedsYou = "No agent fits yet"
		}
	}
	view.History = t.History
	if len(view.History) > 10 {
		view.History = view.History[len(view.History)-10:]
	}
	view.Activity = t.Activity
	if len(view.Activity) > 10 {
		view.Activity = view.Activity[len(view.Activity)-10:]
	}
	return view
}

// teamAgentCLI names, by Team agent id, the coding program of each agent in this
// project's Team (the same label the Team panel shows), read once on first use.
// It only decorates asks the viewer already sees; it never admits an agent.
func (s *ProjectActionDispatcher) teamAgentCLI(ctx context.Context, project string) func(string) string {
	var names map[string]string
	return func(id string) string {
		if names == nil {
			names = map[string]string{}
			team, err := readCrewTeam(project)
			if s.api != nil && err == nil {
				agents := make([]crewTeamReachableAgent, 0, len(team.Agents))
				for agentID := range team.Agents {
					agents = append(agents, crewTeamReachableAgent{Team: project, ID: agentID, Address: agentID})
				}
				for _, row := range s.api.runtimeInfoForAdmittedReach(ctx, agents) {
					if row.Info != nil {
						names[row.Reach.ID] = firstNonEmptyTrimmed(row.Info.CLI, row.Info.Engine)
					}
				}
			}
		}
		return names[id]
	}
}

// projectTasksRow is the side panel's one Project tasks row, or false when the
// project has no task list yet.
func (c *conditionService) projectTasksRow(ctx context.Context, viewer, project string) (crewPanelRow, bool, error) {
	base, err := c.projectTasksBase(ctx, viewer, project)
	if err != nil {
		return crewPanelRow{}, false, err
	}
	list, err := c.readProjectTasks(ctx, base)
	if err != nil || (len(list.Tasks) == 0 && list.Lead == "" && len(list.Steps) == 0) {
		return crewPanelRow{}, false, err
	}
	row := crewPanelRow{
		ID:                   "project-tasks",
		Label:                "Project tasks",
		Group:                "project-tasks",
		Detail:               "No tasks yet",
		crewPanelActivityRow: crewPanelActivityRow{Steps: list.steps(), Rules: list.Rules},
	}
	tasks := conditionFeedTaskIndex(ctx, viewer)
	tasks.cli = c.s.teamAgentCLI(ctx, project)
	open, done := 0, 0
	for i := len(list.Tasks) - 1; i >= 0; i-- {
		t := list.Tasks[i]
		view := projectTaskView(list, t, tasks)
		if view.Done {
			row.DoneCount++
			if done >= projectTasksDoneShown {
				continue
			}
			done++
		} else {
			open++
			if view.NeedsYou != "" {
				row.NeedsYouCount++
			}
			if open > projectTasksOpenShown {
				continue
			}
		}
		row.Tasks = append(row.Tasks, view)
	}
	row.OpenCount = open
	if open > 0 {
		row.Count = strconv.Itoa(open) + " open"
		row.Detail = row.Count
	}
	// What the lead sorted stays in the tasks read for agents; the panel doesn't list it (owner, 24 Nov).
	row.Lead, row.TasksMode, row.Paused = list.Lead, list.mode(), list.Paused
	row.TaskGroups, row.Focus = list.Groups, list.Focus
	for i := len(list.Moves) - 1; i >= 0 && len(row.TaskMoves) < projectTaskMovesShown; i-- {
		row.TaskMoves = append(row.TaskMoves, list.Moves[i])
	}
	// Pinned tasks lead their group; the rest keep newest first.
	sort.SliceStable(row.Tasks, func(i, j int) bool { return row.Tasks[i].Pinned && !row.Tasks[j].Pinned })
	return row, true, nil
}

func projectTasksStep(list projectTaskList, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", nil
	}
	for _, s := range list.steps() {
		if strings.EqualFold(s, arg) {
			return s, nil
		}
	}
	return "", fmt.Errorf("%q isn't one of this project's steps (%s); use one of them, or change the steps with task_settings", arg, strings.Join(list.steps(), " → "))
}

// projectTasksAction runs the Project tasks actions of the Watcher tool.
func (c *conditionService) projectTasksAction(ctx context.Context, actor, profile, project, action string, args map[string]interface{}) (interface{}, error) {
	if profile != crewProfileID {
		return nil, errors.New("Project tasks are only in a project")
	}
	base, err := c.projectTasksBase(ctx, actor, project)
	if err != nil {
		return nil, err
	}
	tasks := conditionFeedTaskIndex(ctx, actor)
	if action == "tasks" {
		list, err := c.readProjectTasks(ctx, base)
		if err != nil {
			return nil, err
		}
		out := []crewPanelProjectTask{}
		for _, t := range list.Tasks {
			if v := projectTaskView(list, t, tasks); !v.Done || args["include_done"] == true {
				out = append(out, v)
			}
		}
		sorted := list.Sorted
		if len(sorted) > 20 {
			sorted = sorted[len(sorted)-20:]
		}
		return map[string]interface{}{"steps": list.steps(), "rules": list.Rules, "lead": list.Lead, "lead_chat": list.LeadChat, "mode": list.mode(), "paused": list.Paused, "groups": list.Groups, "focus": list.Focus, "tasks": out, "recently_sorted": sorted}, nil
	}

	by := feedText(args["by"], 60)
	var link *projectTaskLink
	if raw, ok := args["task"].(map[string]interface{}); ok {
		inbox, _ := raw["inbox_id"].(string)
		message, _ := raw["message_id"].(string)
		if inbox == "" || message == "" {
			return nil, errors.New("task needs inbox_id and message_id of the ask you sent")
		}
		conv, m, ok := tasks.find(conditionFeedTask{Inbox: inbox, Message: message})
		if !ok {
			return nil, errors.New("that task isn't in your inboxes; link the ask you sent (its inbox_id and message_id)")
		}
		link = &projectTaskLink{conditionFeedTask: conditionFeedTask{Inbox: inbox, Message: message}, Agent: conv.Endpoints[1-m.From].Label}
		by = firstNonEmptyTrimmed(by, conv.Endpoints[m.From].Label)
	}
	by = firstNonEmptyTrimmed(by, "An agent")
	source, err := projectTaskRefArg(args["source"], by)
	if err != nil {
		return nil, err
	}
	linkRef, err := projectTaskRefArg(args["link"], by)
	if err != nil {
		return nil, err
	}
	note := feedText(args["note"], 300)
	now := time.Now().Unix()

	projectTasksLock.Lock()
	defer projectTasksLock.Unlock()
	list, err := c.readProjectTasks(ctx, base)
	if err != nil {
		return nil, err
	}
	switch action {
	case "task_settings":
		if raw, ok := args["steps"].([]interface{}); ok {
			steps := []string{}
			for _, v := range raw {
				s, _ := v.(string)
				steps = append(steps, s)
			}
			if err := validProjectTaskSteps(steps); err != nil {
				return nil, err
			}
			list.Steps = steps
		}
		if v, ok := args["rules"].(string); ok {
			if len([]rune(v)) > 2000 {
				return nil, errors.New("rules can be at most 2000 characters")
			}
			list.Rules = strings.TrimSpace(v)
		}
		if v, ok := args["lead"].(string); ok {
			list.Lead = feedText(v, 60)
		}
		if v, ok := args["lead_chat"].(string); ok {
			if v = strings.TrimSpace(v); v != "" && !conditionChatIDPattern.MatchString(v) {
				return nil, errors.New("lead_chat is the lead's chat id from sessions list")
			}
			list.LeadChat = strings.TrimSpace(v)
		}
		if v, ok := args["mode"].(string); ok {
			if v != "auto" && v != "ask" && v != "off" {
				return nil, errors.New("mode is auto (the lead hands work out and chases it), ask (the lead proposes, the person approves) or off (a plain list nobody drives)")
			}
			list.Mode = v
		}
		if v, ok := args["groups"]; ok {
			groups, err := projectTaskGroups(v)
			if err != nil {
				return nil, err
			}
			list.Groups = groups
		}
		if v, ok := args["focus"].(string); ok {
			list.Focus = feedText(v, 120)
		}
		if v, ok := args["paused"].(bool); ok {
			if v && list.LeadChat == "" {
				return nil, errors.New("set lead_chat (the lead's chat id from sessions list) before pausing, so its Watchers know to wait")
			}
			list.Paused = v
		}
		if err := c.writeProjectTasks(ctx, base, list); err != nil {
			return nil, err
		}
		log.Printf("[PROJECT_TASKS] settings mode=%s paused=%t", list.mode(), list.Paused)
		return map[string]interface{}{"steps": list.steps(), "rules": list.Rules, "lead": list.Lead, "lead_chat": list.LeadChat, "mode": list.mode(), "paused": list.Paused, "groups": list.Groups, "focus": list.Focus}, nil
	case "task_sorted":
		kind := feedText(args["kind"], 10)
		if kind != "dropped" && kind != "added" {
			return nil, errors.New("task_sorted records kind=dropped (noise) or kind=added (put on an existing task with task_update); task_add records created tasks itself")
		}
		text := feedText(args["text"], 200)
		if text == "" {
			return nil, errors.New("task_sorted needs text: a short line saying what came in")
		}
		list.Sorted = append(list.Sorted, projectTaskSorted{At: now, Kind: kind, Text: text, URL: feedURL(args["url"]), TaskID: feedText(args["task_id"], 20), By: by})
		if err := c.writeProjectTasks(ctx, base, list); err != nil {
			return nil, err
		}
		return map[string]interface{}{"recorded": kind}, nil
	case "task_add":
		title := feedText(args["title"], 200)
		if title == "" {
			return nil, errors.New("task_add needs a short title saying what needs doing")
		}
		step, err := projectTasksStep(list, firstNonEmptyTrimmed(stringArg(args, "step"), list.steps()[0]))
		if err != nil {
			return nil, err
		}
		list.Next++
		t := projectTask{ID: "t" + strconv.Itoa(list.Next), Title: title, Step: step, Assignee: feedText(args["assignee"], 60), Proposed: feedText(args["proposed"], 60), Waiting: feedText(args["waiting"], 200), Created: now, Updated: now}
		if t.Choices, err = projectTaskChoices(args["choices"]); err != nil {
			return nil, err
		}
		if source != nil {
			t.Sources = []projectTaskRef{*source}
		}
		if linkRef != nil {
			t.Links = []projectTaskRef{*linkRef}
		}
		if link != nil {
			t.Tasks = []projectTaskLink{*link}
			t.Assignee = firstNonEmptyTrimmed(t.Assignee, link.Agent)
		}
		if _, err := projectTaskChecklist(&t, args, by, now); err != nil {
			return nil, err
		}
		if _, err := projectTaskPlace(list, &t, args); err != nil {
			return nil, err
		}
		t.History = []conditionFeedMove{{At: now, By: by, Step: step, Note: firstNonEmptyTrimmed(note, "Created")}}
		list.Tasks = append(list.Tasks, t)
		if t.Assignee != "" {
			list.move(by, "gave", "Gave #"+t.ID+" to "+t.Assignee, t.ID, now)
		}
		sortedText := title
		if source != nil {
			sortedText = source.Title
		}
		list.Sorted = append(list.Sorted, projectTaskSorted{At: now, Kind: "created", Text: sortedText, TaskID: t.ID, By: by})
		if err := c.writeProjectTasks(ctx, base, list); err != nil {
			return nil, err
		}
		log.Printf("[PROJECT_TASKS] add task=%s step=%q", t.ID, step)
		return map[string]interface{}{"steps": list.steps(), "task": projectTaskView(list, t, tasks)}, nil
	}

	// task_update
	id := feedText(args["task_id"], 20)
	at := -1
	for i, t := range list.Tasks {
		if t.ID == id {
			at = i
		}
	}
	if at < 0 {
		return nil, errors.New("task not found; read the list with action=tasks and use its id")
	}
	t := &list.Tasks[at]
	step, err := projectTasksStep(list, stringArg(args, "step"))
	if err != nil {
		return nil, err
	}
	changed := false
	if title := feedText(args["title"], 200); title != "" && title != t.Title {
		t.Title, changed = title, true
	}
	if v, ok := args["assignee"].(string); ok {
		// Handing it out settles a proposal and ends any chasing.
		v = feedText(v, 60)
		if v != t.Assignee {
			if t.Assignee != "" && v != "" {
				t.From = t.Assignee
				list.move(by, "moved", "Moved #"+t.ID+" from "+t.Assignee+" to "+v, t.ID, now)
			} else if v != "" {
				list.move(by, "gave", "Gave #"+t.ID+" to "+v, t.ID, now)
			}
			t.Chase, t.ChaseAt = "", 0
		}
		t.Assignee, t.Proposed, changed = v, "", true
	}
	if v, ok := args["chase"].(string); ok {
		if v != "" && v != "nudged" && v != "stuck" {
			return nil, errors.New("chase is nudged (you reminded the agent), stuck (still no progress after a nudge) or \"\" (moving again)")
		}
		if v != t.Chase {
			t.Chase, t.ChaseAt, changed = v, now, true
			who := firstNonEmptyTrimmed(t.Assignee, "the agent")
			switch v {
			case "nudged":
				list.move(by, "nudged", "Nudged "+who+" on #"+t.ID, t.ID, now)
			case "stuck":
				list.move(by, "stuck", "#"+t.ID+" is stuck with "+who, t.ID, now)
			}
			if v == "" {
				t.ChaseAt = 0
			}
		}
	}
	if v, ok := args["proposed"].(string); ok {
		if v = feedText(v, 60); v != "" && v != t.Proposed {
			list.move(by, "suggested", "Suggested "+v+" for #"+t.ID, t.ID, now)
		}
		t.Proposed, changed = v, true
	}
	if v, ok := args["waiting"].(string); ok && feedText(v, 200) != "" && feedText(v, 200) != t.Waiting {
		list.move(by, "asked", "Asked you about #"+t.ID+": "+feedText(v, 120), t.ID, now)
	}
	if v, ok := args["waiting"].(string); ok {
		// A new question replaces the old answers; clearing it clears them.
		t.Waiting, t.Choices, changed = feedText(v, 200), nil, true
	}
	if _, ok := args["choices"]; ok {
		if t.Choices, err = projectTaskChoices(args["choices"]); err != nil {
			return nil, err
		}
		changed = true
	}
	if v, ok := args["held"].(bool); ok && v != t.Held {
		t.Held, changed = v, true
		note = firstNonEmptyTrimmed(note, map[bool]string{true: "On hold", false: "Off hold"}[v])
	}
	if link != nil {
		known := false
		for _, l := range t.Tasks {
			known = known || l.conditionFeedTask == link.conditionFeedTask
		}
		if !known {
			if len(t.Tasks) >= conditionFeedTasksMax {
				return nil, fmt.Errorf("a task links at most %d asks", conditionFeedTasksMax)
			}
			t.Tasks = append(t.Tasks, *link)
			if _, set := args["assignee"]; !set {
				t.Assignee = link.Agent
			}
			changed = true
		}
	}
	if source != nil {
		if t.Sources, err = addProjectTaskRef(t.Sources, *source); err != nil {
			return nil, err
		}
		changed = true
	}
	if linkRef != nil {
		if t.Links, err = addProjectTaskRef(t.Links, *linkRef); err != nil {
			return nil, err
		}
		changed = true
	}
	if raw, ok := args["activity"].(map[string]interface{}); ok {
		text := feedText(raw["text"], 200)
		if text == "" {
			return nil, errors.New("activity needs text: one line on what happened")
		}
		t.Activity = append(t.Activity, projectTaskEvent{Kind: projectTaskKind(raw["kind"]), Text: text, URL: feedURL(raw["url"]), At: now})
		if len(t.Activity) > projectTaskEventsMax {
			t.Activity = t.Activity[len(t.Activity)-projectTaskEventsMax:]
		}
		changed = true
	}
	if ticked, err := projectTaskChecklist(t, args, by, now); err != nil {
		return nil, err
	} else if ticked {
		changed = true
	}
	if placed, err := projectTaskPlace(list, t, args); err != nil {
		return nil, err
	} else if placed {
		changed = true
	}
	before := list.stepOf(*t)
	if step != "" {
		t.Step = step
	}
	if step != "" && step != before {
		if list.done(*t) {
			list.move(by, "closed", "Closed #"+t.ID, t.ID, now)
			t.Chase, t.ChaseAt = "", 0
		} else {
			list.move(by, "step", "Moved #"+t.ID+" to "+step, t.ID, now)
		}
	}
	if step != "" && step != before || note != "" {
		t.History = append(t.History, conditionFeedMove{At: now, By: by, Step: step, Note: note})
		if len(t.History) > conditionFeedMovesMax {
			t.History = t.History[len(t.History)-conditionFeedMovesMax:]
		}
		changed = true
	}
	if !changed {
		return nil, errors.New("task_update needs something to change: step, title, assignee, chase, proposed, waiting, choices, held, group, pinned, due, task, source, link, activity, checklist_add, checklist_done, checklist_open, checklist_remove or note")
	}
	t.Updated = now
	saved := *t
	if err := c.writeProjectTasks(ctx, base, list); err != nil {
		return nil, err
	}
	log.Printf("[PROJECT_TASKS] update task=%s step=%q", saved.ID, list.stepOf(saved))
	return map[string]interface{}{"steps": list.steps(), "task": projectTaskView(list, saved, tasks)}, nil
}

// projectTaskChecklist applies checklist_add (new items), checklist_done /
// checklist_open (tick or untick, 1-based) and checklist_remove. It reports
// whether anything changed.
// projectTaskChoices reads up to four short answers the person can pick for waiting.
func projectTaskChoices(v interface{}) ([]string, error) {
	raw, ok := v.([]interface{})
	if v == nil || (ok && len(raw) == 0) {
		return nil, nil
	}
	if !ok || len(raw) > 4 {
		return nil, errors.New("choices: up to 4 short answers the person can pick")
	}
	var out []string
	for _, item := range raw {
		text := feedText(item, 40)
		if text == "" {
			return nil, errors.New("choices: each answer needs text")
		}
		out = append(out, text)
	}
	return out, nil
}

func projectTaskChecklist(t *projectTask, args map[string]interface{}, by string, now int64) (bool, error) {
	changed := false
	var adds []string
	switch v := args["checklist_add"].(type) {
	case nil:
	case string:
		adds = []string{v}
	case []interface{}:
		for _, item := range v {
			text, _ := item.(string)
			adds = append(adds, text)
		}
	default:
		return false, errors.New("checklist_add is a list of short items")
	}
	for _, raw := range adds {
		text := feedText(raw, 120)
		if text == "" {
			return false, errors.New("checklist_add items need text")
		}
		if len(t.Checks) >= projectTaskChecksMax {
			return false, fmt.Errorf("a task keeps at most %d checklist items", projectTaskChecksMax)
		}
		t.Checks = append(t.Checks, projectTaskCheck{Text: text, By: by, At: now})
		changed = true
	}
	item := func(key string) (int, bool, error) {
		var v float64
		switch raw := args[key].(type) {
		case nil:
			return 0, false, nil
		case float64:
			v = raw
		case int:
			v = float64(raw)
		case json.Number:
			f, err := raw.Float64()
			if err != nil {
				return 0, false, fmt.Errorf("%s is the item's number (1 is the first)", key)
			}
			v = f
		default:
			return 0, false, fmt.Errorf("%s is the item's number (1 is the first)", key)
		}
		n := int(v)
		if float64(n) != v || n < 1 || n > len(t.Checks) {
			return 0, false, fmt.Errorf("%s: no checklist item %v; this task has %d", key, v, len(t.Checks))
		}
		return n - 1, true, nil
	}
	for _, key := range []string{"checklist_done", "checklist_open"} {
		at, ok, err := item(key)
		if err != nil {
			return false, err
		}
		if ok {
			done := key == "checklist_done"
			if t.Checks[at].Done != done {
				t.Checks[at].Done, t.Checks[at].By, t.Checks[at].At = done, by, now
				changed = true
			}
		}
	}
	at, ok, err := item("checklist_remove")
	if err != nil {
		return false, err
	}
	if ok {
		t.Checks = append(t.Checks[:at], t.Checks[at+1:]...)
		changed = true
	}
	return changed, nil
}

func validProjectTaskSteps(steps []string) error {
	if len(steps) < 2 || len(steps) > 8 {
		return errors.New("steps: 2 to 8 short names, first to last")
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if s == "" || s != strings.TrimSpace(s) || len([]rune(s)) > 24 || seen[strings.ToLower(s)] {
			return errors.New("steps: each a different name of at most 24 characters")
		}
		seen[strings.ToLower(s)] = true
	}
	return nil
}

// feedURL keeps only an https link (source data is untrusted).
func feedURL(v interface{}) string {
	link, _ := v.(string)
	if u, err := url.Parse(link); err == nil && u.Scheme == "https" && u.Host != "" && len(link) <= 2048 {
		return link
	}
	return ""
}

func stringArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

// projectTasksLeadPaused reports whether the project's lead is paused and chatID
// is its chat: its Watchers then hold events until the person resumes it, and
// its schedules skip their timed runs (#3995).
func (s *ProjectActionDispatcher) projectTasksLeadPaused(ctx context.Context, workspace, chatID string) bool {
	if workspace == "" || chatID == "" {
		return false
	}
	var list projectTaskList
	raw, found, err := s.readFile(ctx, path.Join(workspace, "db", "project-tasks.json"))
	if err != nil || !found || json.Unmarshal([]byte(raw), &list) != nil {
		return false
	}
	return list.Paused && list.LeadChat == chatID
}
