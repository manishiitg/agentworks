package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path"
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
	projectTasksOpenShown = 12
	projectTasksDoneShown = 5
	projectTasksDoneKept  = 100
	projectTaskRefsMax    = 20
	projectTaskEventsMax  = 30
	projectTasksSortedMax = 100
)

var projectTaskDefaultSteps = []string{"New", "Doing", "Review", "Done"}

type projectTaskRef struct {
	Kind  string `json:"kind"` // slack, mail, tasks, github, notion, drive, other
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	By    string `json:"by,omitempty"`
	At    int64  `json:"at"`
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

type projectTask struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Step     string              `json:"step,omitempty"`
	Assignee string              `json:"assignee,omitempty"` // the agent or person who has it; "" = not handed out yet
	Proposed string              `json:"proposed,omitempty"` // "Ask me first": the agent the lead suggests, until the person approves
	Waiting  string              `json:"waiting,omitempty"`  // what the lead needs the person to decide
	Held     bool                `json:"held,omitempty"`     // the person put it on hold; the lead leaves it alone
	Tasks    []projectTaskLink   `json:"tasks,omitempty"`
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

type projectTaskList struct {
	Steps []string `json:"steps,omitempty"`
	Rules string   `json:"rules,omitempty"`
	Lead  string   `json:"lead,omitempty"` // the lead agent's name, for people reading the list
	// LeadChat is the lead's chat id in this project. While Paused, Watchers that
	// send to that chat hold their events and deliver them on resume.
	LeadChat string              `json:"lead_chat,omitempty"`
	Paused   bool                `json:"paused,omitempty"`
	Mode     string              `json:"mode,omitempty"` // auto (default): the lead hands work out; ask: it proposes and the person approves
	Next     int                 `json:"next"`
	Tasks    []projectTask       `json:"tasks"`
	Sorted   []projectTaskSorted `json:"sorted,omitempty"`
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
	Assignee string             `json:"assignee,omitempty"`
	Sources  []projectTaskRef   `json:"sources,omitempty"`
	Links    []projectTaskRef   `json:"links,omitempty"`
	Activity []projectTaskEvent `json:"activity,omitempty"`
	Proposed string             `json:"proposed,omitempty"`
	Held     bool               `json:"held,omitempty"`
	// NeedsYou says what the person must do; the server decides it so the panel
	// and the lead read the same "Needs you" list.
	NeedsYou string `json:"needs_you,omitempty"`
}

func (l projectTaskList) mode() string {
	if l.Mode == "ask" {
		return "ask"
	}
	return "auto"
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
	view.Proposed, view.Held = t.Proposed, t.Held
	if !view.Done && !t.Held {
		for _, task := range view.Tasks {
			if task.State == "needs_you" || task.State == "blocked" {
				view.NeedsYou = task.Agent + " is " + map[string]string{"needs_you": "waiting on you", "blocked": "blocked"}[task.State]
			}
		}
		switch {
		case t.Waiting != "":
			view.NeedsYou = t.Waiting
		case t.Proposed != "":
			view.NeedsYou = "Approve handing it to " + t.Proposed
		case t.Assignee == "" && view.NeedsYou == "":
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
	row := crewPanelRow{ID: "project-tasks", Label: "Project tasks", Group: "project-tasks", Steps: list.steps(), Rules: list.Rules, Detail: "No tasks yet"}
	tasks := conditionFeedTaskIndex(ctx, viewer)
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
			if open > projectTasksOpenShown {
				continue
			}
		}
		row.Tasks = append(row.Tasks, view)
	}
	if open > 0 {
		row.Count = strconv.Itoa(open) + " open"
		row.Detail = row.Count
	}
	day := time.Now().Add(-24 * time.Hour).Unix()
	for _, s := range list.Sorted {
		if s.At >= day {
			row.Sorted = append(row.Sorted, s)
		}
	}
	row.Lead, row.TasksMode, row.Paused = list.Lead, list.mode(), list.Paused
	if n := len(list.Sorted); n > 0 {
		row.LastSorted = list.Sorted[n-1].At
	}
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
		return map[string]interface{}{"steps": list.steps(), "rules": list.Rules, "lead": list.Lead, "lead_chat": list.LeadChat, "mode": list.mode(), "paused": list.Paused, "tasks": out, "recently_sorted": sorted}, nil
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
			if v != "auto" && v != "ask" {
				return nil, errors.New("mode is auto (the lead hands work out) or ask (the lead proposes, the person approves)")
			}
			list.Mode = v
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
		return map[string]interface{}{"steps": list.steps(), "rules": list.Rules, "lead": list.Lead, "lead_chat": list.LeadChat, "mode": list.mode(), "paused": list.Paused}, nil
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
		t.History = []conditionFeedMove{{At: now, By: by, Step: step, Note: firstNonEmptyTrimmed(note, "Created")}}
		list.Tasks = append(list.Tasks, t)
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
		// Handing it out settles a proposal.
		t.Assignee, t.Proposed, changed = feedText(v, 60), "", true
	}
	if v, ok := args["proposed"].(string); ok {
		t.Proposed, changed = feedText(v, 60), true
	}
	if v, ok := args["waiting"].(string); ok {
		t.Waiting, changed = feedText(v, 200), true
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
	before := list.stepOf(*t)
	if step != "" {
		t.Step = step
	}
	if step != "" && step != before || note != "" {
		t.History = append(t.History, conditionFeedMove{At: now, By: by, Step: step, Note: note})
		if len(t.History) > conditionFeedMovesMax {
			t.History = t.History[len(t.History)-conditionFeedMovesMax:]
		}
		changed = true
	}
	if !changed {
		return nil, errors.New("task_update needs something to change: step, title, assignee, proposed, waiting, held, task, source, link, activity or note")
	}
	t.Updated = now
	saved := *t
	if err := c.writeProjectTasks(ctx, base, list); err != nil {
		return nil, err
	}
	log.Printf("[PROJECT_TASKS] update task=%s step=%q", saved.ID, list.stepOf(saved))
	return map[string]interface{}{"steps": list.steps(), "task": projectTaskView(list, saved, tasks)}, nil
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
// is its chat: its Watchers then hold events until the person resumes it.
func (c *conditionService) projectTasksLeadPaused(ctx context.Context, workspace, chatID string) bool {
	if workspace == "" || chatID == "" {
		return false
	}
	var list projectTaskList
	raw, found, err := c.s.readFile(ctx, path.Join(workspace, "db", "project-tasks.json"))
	if err != nil || !found || json.Unmarshal([]byte(raw), &list) != nil {
		return false
	}
	return list.Paused && list.LeadChat == chatID
}
