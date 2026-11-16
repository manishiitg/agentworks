package server

import (
	"bufio"
	"bytes"
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
	"unicode/utf8"

	"github.com/google/uuid"
)

// The work log (#2871): one append-only record per Crew, kept for good, next to
// the Team record. Agents add, start, update, decide, finish and release tasks;
// the server works out each task's state from the entries. A task's label is the
// team's own (#123, OPS-77, a file name); an outside tracker stays the source of
// truth for the task's text and state, and the log keeps the work around it.
const (
	crewWorkFile = "work.jsonl"
	// A started task with no entry for this long shows as stalled and may be taken over.
	crewWorkStalledAfter = 24 * time.Hour
)

var crewWorkActions = []string{"work_brief", "work_add", "work_start", "work_update", "work_decide", "work_done", "work_release", "work_find"}

type crewWorkEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Event    string    `json:"event"` // added, started, update, decided, done, released
	Task     string    `json:"task,omitempty"`
	Label    string    `json:"label,omitempty"`
	Title    string    `json:"title,omitempty"`
	Link     string    `json:"link,omitempty"`
	PartOf   string    `json:"part_of,omitempty"`
	For      string    `json:"for,omitempty"`
	Line     string    `json:"line,omitempty"`
	Why      string    `json:"why,omitempty"`
	Replaces string    `json:"replaces,omitempty"`
	Private  bool      `json:"private,omitempty"`
	AgentID  string    `json:"agent_id,omitempty"`
	ChatKey  string    `json:"chat_key,omitempty"`
	OwnerID  string    `json:"owner_id"`
	Agent    string    `json:"agent"`
}

type crewWorkTask struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Title       string    `json:"title"`
	Link        string    `json:"link,omitempty"`
	PartOf      string    `json:"part_of,omitempty"`
	For         string    `json:"for,omitempty"`
	State       string    `json:"state"` // to_do, started, stalled, done
	Agent       string    `json:"agent,omitempty"`
	Person      string    `json:"person,omitempty"`
	Line        string    `json:"line,omitempty"`
	Outcome     string    `json:"outcome,omitempty"`
	Parts       int       `json:"parts,omitempty"`
	PartsDone   int       `json:"parts_done,omitempty"`
	Private     bool      `json:"private,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	holderID    string
	holderKey   string
	holderOwner string
	ownerID     string
	agentID     string
	chatKey     string
}

type crewWorkDecision struct {
	ID         string    `json:"id"`
	Task       string    `json:"task,omitempty"`
	Label      string    `json:"label,omitempty"`
	Line       string    `json:"line"`
	Why        string    `json:"why"`
	Agent      string    `json:"agent"`
	Person     string    `json:"person,omitempty"`
	ReplacedBy string    `json:"replaced_by,omitempty"`
	Private    bool      `json:"private,omitempty"`
	At         time.Time `json:"at"`
	ownerID    string
	agentID    string
}

type crewWorkInput struct {
	Op       string `json:"op"`
	Task     string `json:"task"`
	Label    string `json:"label"`
	Title    string `json:"title"`
	Link     string `json:"link"`
	PartOf   string `json:"part_of"`
	For      string `json:"for"`
	Line     string `json:"line"`
	Why      string `json:"why"`
	Replaces string `json:"replaces"`
	Query    string `json:"query"`
}

func crewWorkPath(project string) (string, string, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, crewWorkFile), nil
}

// readCrewWorkLog returns every entry in order. A torn last line (a crash mid-append) is skipped.
func readCrewWorkLog(project string) ([]crewWorkEntry, error) {
	dir, path, err := crewWorkPath(project)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		mu := crewTeamRecordMutex(dir)
		mu.Lock()
		err = crewWorkMigrateLocked(project, dir, path)
		mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return readCrewWorkFile(path)
}

func readCrewWorkFile(path string) ([]crewWorkEntry, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []crewWorkEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []crewWorkEntry{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e crewWorkEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// One-off move of the old Team decisions (activity.json kept the newest 200)
// into the work log, the first time a Crew's log is read or written. Delete
// once every server has moved.
func crewWorkMigrateLocked(project, dir, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	var old struct {
		Decisions []crewTeamActivityPost `json:"decisions"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "activity.json")); err == nil {
		_ = json.Unmarshal(raw, &old)
	}
	var buf bytes.Buffer
	for _, p := range old.Decisions {
		line, _ := json.Marshal(crewWorkEntry{ID: p.ID, At: p.At, Event: "decided", Line: p.Line, Why: p.Why, Private: p.Private, AgentID: p.AgentID, ChatKey: p.ChatKey, OwnerID: p.OwnerID, Agent: p.Agent})
		buf.Write(append(line, '\n'))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		return err
	}
	log.Printf("[WORK] project=%s event=migrated decisions=%d", project, len(old.Decisions))
	return nil
}

func crewWorkAppendLocked(path string, e crewWorkEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// crewWorkState folds the entries into tasks and decisions. Callers filter the
// entries for a viewer first; the start/finish rules use the unfiltered log.
func crewWorkState(entries []crewWorkEntry, now time.Time) (map[string]*crewWorkTask, []*crewWorkDecision) {
	tasks := map[string]*crewWorkTask{}
	decisions := []*crewWorkDecision{}
	byID := map[string]*crewWorkDecision{}
	for _, e := range entries {
		if e.Event == "decided" {
			d := &crewWorkDecision{ID: e.ID, Task: e.Task, Line: e.Line, Why: e.Why, Agent: e.Agent, Private: e.Private, At: e.At, ownerID: e.OwnerID, agentID: e.AgentID}
			if t := tasks[e.Task]; t != nil {
				d.Label = t.Label
			}
			if old := byID[e.Replaces]; old != nil {
				old.ReplacedBy = e.ID
			}
			decisions = append(decisions, d)
			byID[e.ID] = d
			continue
		}
		if e.Event == "added" {
			tasks[e.Task] = &crewWorkTask{ID: e.Task, Label: e.Label, Title: e.Title, Link: e.Link, PartOf: e.PartOf, For: e.For, State: "to_do", Private: e.Private, UpdatedAt: e.At, ownerID: e.OwnerID, agentID: e.AgentID, chatKey: e.ChatKey}
			continue
		}
		t := tasks[e.Task]
		if t == nil {
			continue
		}
		t.UpdatedAt = e.At
		if e.Link != "" {
			t.Link = e.Link
		}
		switch e.Event {
		case "started":
			t.State, t.Agent, t.holderID, t.holderKey, t.holderOwner = "started", e.Agent, e.AgentID, e.ChatKey, e.OwnerID
			if e.Line != "" {
				t.Line = e.Line
			}
		case "update":
			t.Line = e.Line
		case "done":
			t.State, t.Outcome = "done", e.Line
		case "released":
			t.State, t.Agent, t.holderID, t.holderKey, t.holderOwner = "to_do", "", "", "", ""
			if e.Line != "" {
				t.Line = e.Line
			}
		}
	}
	for _, t := range tasks {
		if t.State == "started" && now.Sub(t.UpdatedAt) > crewWorkStalledAfter {
			t.State = "stalled"
		}
		if parent := tasks[t.PartOf]; parent != nil {
			parent.Parts++
			if t.State == "done" {
				parent.PartsDone++
			}
		}
	}
	return tasks, decisions
}

func (t *crewWorkTask) heldBy(actor crewTeamRegisteredAgent) bool {
	if t.holderID != "" || actor.ID != "" {
		return t.holderID == actor.ID && t.holderOwner == actor.OwnerID
	}
	return t.holderKey == actor.ChatKey && t.holderOwner == actor.OwnerID
}

// crewWorkFindTask resolves the model's task reference: an ID, or the label of
// a task that is not done (open labels are unique).
func crewWorkFindTask(tasks map[string]*crewWorkTask, ref string) (*crewWorkTask, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("Say which task: its id or label (see work_brief)")
	}
	if t := tasks[ref]; t != nil {
		return t, nil
	}
	done := false
	for _, t := range tasks {
		if strings.EqualFold(t.Label, ref) {
			if t.State != "done" {
				return t, nil
			}
			done = true
		}
	}
	if done {
		return nil, fmt.Errorf("Task %s is done. Add a new task if there is more work", ref)
	}
	return nil, fmt.Errorf("No task %s. Use work_brief or work_find to see tasks, or work_add to add it", ref)
}

func crewWorkText(name, value string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("Give the %s", name)
	}
	if utf8.RuneCountInString(value) > max || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("Keep the %s to one line of up to %d characters", name, max)
	}
	return value, nil
}

// crewWorkDo is the one write/read path for the screen route, chat/MCP and
// native agents. actor comes from the session boundary, never from the model.
func crewWorkDo(ctx context.Context, team crewTeam, person string, actor crewTeamRegisteredAgent, in crewWorkInput) (any, error) {
	if !slices.Contains(crewWorkActions, in.Op) {
		return nil, fmt.Errorf("Choose one of %s", strings.Join(crewWorkActions, ", "))
	}
	if !crewTeamAgentsVisible(team, person) {
		return nil, fmt.Errorf("This Crew's Team is not available to you")
	}
	agent, registered := team.Agents[actor.ID]
	if !registered {
		agent = actor
		agent.InTeam = false
	}
	if agent.OwnerID != person || agent.ChatKey == "" || isSharedThreadKey(agent.ChatKey) {
		return nil, fmt.Errorf("Use the work log from your own agent in this Crew")
	}
	private := !registered || !agent.InTeam
	if actor.InTeam && private {
		return nil, fmt.Errorf("The agent is no longer shared with the team. Try again from its own chat")
	}
	if in.Op == "work_brief" || in.Op == "work_find" {
		entries, err := readCrewWorkLog(team.ProjectID)
		if err != nil {
			return nil, err
		}
		if in.Op == "work_brief" {
			return crewWorkBrief(team, person, agent, entries, time.Now().UTC()), nil
		}
		return crewWorkFind(team, person, entries, in.Query, time.Now().UTC())
	}

	dir, path, err := crewWorkPath(team.ProjectID)
	if err != nil {
		return nil, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	if err := crewWorkMigrateLocked(team.ProjectID, dir, path); err != nil {
		return nil, err
	}
	entries, err := readCrewWorkFile(path)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tasks, decisions := crewWorkState(entries, now)
	e := crewWorkEntry{ID: uuid.NewString(), At: now, Private: private, AgentID: agent.ID, ChatKey: agent.ChatKey, OwnerID: person, Agent: agent.Name}
	refuse := func(task, reason, text string) (any, error) {
		log.Printf("[WORK] project=%s event=refused op=%s task=%s reason=%s agent_id=%s caller_id=%s", team.ProjectID, in.Op, task, reason, agent.ID, person)
		return nil, fmt.Errorf("%s", text)
	}
	line, err := crewWorkText("line", in.Line, 320, false)
	if err != nil {
		return nil, err
	}
	link, err := crewWorkText("link", in.Link, 500, false)
	if err != nil {
		return nil, err
	}
	e.Line, e.Link = line, link
	var task *crewWorkTask
	if in.Op != "work_add" && in.Op != "work_decide" || in.Op == "work_decide" && strings.TrimSpace(in.Task) != "" {
		if task, err = crewWorkFindTask(tasks, in.Task); err != nil {
			return nil, err
		}
		e.Task = task.ID
	}
	switch in.Op {
	case "work_add":
		e.Event = "added"
		if e.Title, err = crewWorkText("title", in.Title, 200, true); err != nil {
			return nil, err
		}
		if e.Label, err = crewWorkText("label", in.Label, 60, false); err != nil {
			return nil, err
		}
		if e.For, err = crewWorkText("for", in.For, 80, false); err != nil {
			return nil, err
		}
		e.Task = uuid.NewString()[:8]
		if e.Label == "" {
			e.Label = e.Task
		}
		for _, t := range tasks {
			if t.State != "done" && strings.EqualFold(t.Label, e.Label) {
				return refuse(t.ID, "label_open", fmt.Sprintf("Task %s is already open (%s). Use it instead of adding another", t.Label, t.Title))
			}
		}
		if strings.TrimSpace(in.PartOf) != "" {
			parent, err := crewWorkFindTask(tasks, in.PartOf)
			if err != nil {
				return nil, err
			}
			e.PartOf = parent.ID
		}
	case "work_start":
		e.Event = "started"
		if task.State == "done" {
			return refuse(task.ID, "done", fmt.Sprintf("Task %s is done", task.Label))
		}
		if task.State == "started" && !task.heldBy(agent) {
			return refuse(task.ID, "taken", fmt.Sprintf("Task %s is already taken by %s. Pick another task, or ask them", task.Label, task.Agent))
		}
		if task.State == "stalled" && !task.heldBy(agent) {
			log.Printf("[WORK] project=%s event=taken_over task=%s from_agent_id=%s agent_id=%s", team.ProjectID, task.ID, task.holderID, agent.ID)
		}
	case "work_update":
		e.Event = "update"
		if e.Line == "" {
			return nil, fmt.Errorf("Give one short line about where the task is")
		}
	case "work_done":
		e.Event = "done"
		if task.State == "done" {
			return refuse(task.ID, "done", fmt.Sprintf("Task %s is already done", task.Label))
		}
		if task.State == "started" && !task.heldBy(agent) {
			return refuse(task.ID, "taken", fmt.Sprintf("Task %s is %s's. Ask them to finish or release it", task.Label, task.Agent))
		}
		if e.Line == "" {
			return nil, fmt.Errorf("Give the outcome in one short line")
		}
	case "work_release":
		e.Event = "released"
		if task.State != "started" && task.State != "stalled" || task.State == "started" && !task.heldBy(agent) {
			return refuse(task.ID, "not_held", fmt.Sprintf("Task %s is not started by this agent", task.Label))
		}
	case "work_decide":
		e.Event = "decided"
		if e.Line == "" {
			return nil, fmt.Errorf("Give the decision in one short line")
		}
		if e.Why, err = crewWorkText("why", in.Why, 800, true); err != nil {
			return nil, err
		}
		if ref := strings.TrimSpace(in.Replaces); ref != "" {
			found := false
			for _, d := range decisions {
				if d.ID == ref {
					found = d.ReplacedBy == ""
				}
			}
			if !found {
				return nil, fmt.Errorf("No current decision %s to replace. Use work_find to see decisions and their ids", ref)
			}
			e.Replaces = ref
		}
	}
	if err := crewWorkAppendLocked(path, e); err != nil {
		return nil, err
	}
	log.Printf("[WORK] project=%s event=%s task=%s entry_id=%s private=%t agent_id=%s caller_id=%s", team.ProjectID, e.Event, e.Task, e.ID, e.Private, agent.ID, person)
	publishCrewPanelChanged(team.ProjectID)
	go pushCrewTeamActivityChanged(team)
	out := map[string]any{"saved": e.Event, "id": e.ID}
	if e.Task != "" {
		tasks, _ = crewWorkState(append(entries, e), now)
		out["task"] = tasks[e.Task]
	}
	if e.Private {
		out["note"] = "Only you see this, because this agent isn't shared with the team."
	}
	return out, nil
}

// crewWorkVisible applies the Team post rule: an Only-me entry is its owner's;
// a shared entry needs its agent still In team and its owner still a member.
func crewWorkVisible(team crewTeam, viewer string) func(private bool, agentID, ownerID string) bool {
	active := map[string]bool{}
	return func(private bool, agentID, ownerID string) bool {
		if private {
			return team.On && viewer != "" && viewer == ownerID && team.member(viewer) && crewTeamAccountActive(viewer)
		}
		agent, ok := team.Agents[agentID]
		enabled, known := active[agent.OwnerID]
		if !known {
			enabled = crewTeamAccountActive(agent.OwnerID)
			active[agent.OwnerID] = enabled
		}
		return team.On && ok && agent.InTeam && agent.OwnerID == ownerID && team.member(agent.OwnerID) && enabled
	}
}

func crewWorkFor(team crewTeam, viewer string, entries []crewWorkEntry, now time.Time) ([]*crewWorkTask, []*crewWorkDecision) {
	visible := crewWorkVisible(team, viewer)
	shown := slices.DeleteFunc(slices.Clone(entries), func(e crewWorkEntry) bool { return !visible(e.Private, e.AgentID, e.OwnerID) })
	byID, decisions := crewWorkState(shown, now)
	labels := crewTeamPersonLabels(team)
	tasks := make([]*crewWorkTask, 0, len(byID))
	for _, t := range byID {
		t.Person = labels[t.holderOwner]
		tasks = append(tasks, t)
	}
	for _, d := range decisions {
		d.Person = labels[d.ownerID]
	}
	slices.SortFunc(tasks, func(a, b *crewWorkTask) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	slices.SortFunc(decisions, func(a, b *crewWorkDecision) int { return b.At.Compare(a.At) })
	return tasks, decisions
}

// crewWorkBrief is what an agent reads at the start of a session: its own
// tasks, tasks meant for it, open tasks nobody has, what others hold, and the
// current decisions. Short on purpose; work_find reaches the rest.
func crewWorkBrief(team crewTeam, person string, agent crewTeamRegisteredAgent, entries []crewWorkEntry, now time.Time) map[string]any {
	tasks, decisions := crewWorkFor(team, person, entries, now)
	me := strings.ToLower(strings.TrimSpace(agent.Name))
	myName := strings.ToLower(crewTeamPersonLabels(team)[person])
	mine, forMe, open, others, mains := []*crewWorkTask{}, []*crewWorkTask{}, []*crewWorkTask{}, []*crewWorkTask{}, []*crewWorkTask{}
	for _, t := range tasks {
		if t.State == "done" {
			continue
		}
		forWho := strings.ToLower(t.For)
		switch {
		case (t.State == "started" || t.State == "stalled") && t.heldBy(agent):
			mine = append(mine, t)
		case t.State == "to_do" && forWho != "" && (forWho == me || forWho == myName):
			forMe = append(forMe, t)
		case t.State == "to_do" && forWho == "":
			open = append(open, t)
		case t.State != "to_do":
			others = append(others, t)
		}
		if t.Parts > 0 {
			mains = append(mains, t)
		}
	}
	current := []*crewWorkDecision{}
	for _, d := range decisions {
		if d.ReplacedBy == "" && len(current) < 10 {
			current = append(current, d)
		}
	}
	return map[string]any{
		"yours":           mine,
		"for_you":         forMe,
		"open_nobody_yet": open[:min(20, len(open))],
		"others_working":  others[:min(20, len(others))],
		"main_tasks":      mains[:min(10, len(mains))],
		"decisions":       current,
		"hint":            "Start a task before working on it (work_start); post work_update as you go, work_decide for choices others must follow, and work_done with the outcome. A stalled task (no entry for a day) may be taken over.",
	}
}

// crewWorkFind searches every task and decision, done and replaced ones too.
func crewWorkFind(team crewTeam, person string, entries []crewWorkEntry, query string, now time.Time) (map[string]any, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, fmt.Errorf("Give a few words or a label to search for")
	}
	match := func(parts ...string) bool {
		text := strings.ToLower(strings.Join(parts, " "))
		for _, w := range words {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return true
	}
	tasks, decisions := crewWorkFor(team, person, entries, now)
	foundTasks, foundDecisions := []*crewWorkTask{}, []*crewWorkDecision{}
	for _, t := range tasks {
		if len(foundTasks) < 20 && match(t.Label, t.Title, t.Line, t.Outcome, t.For, t.Agent) {
			foundTasks = append(foundTasks, t)
		}
	}
	for _, d := range decisions {
		if len(foundDecisions) < 20 && match(d.Label, d.Line, d.Why, d.Agent) {
			foundDecisions = append(foundDecisions, d)
		}
	}
	return map[string]any{"tasks": foundTasks, "decisions": foundDecisions}, nil
}

// crewWorkActivityViews gives the Team panel its live "doing" line per agent
// (the newest start, update or finish) and the current decisions, both from the log.
func crewWorkActivityViews(team crewTeam, viewer string) (map[string]crewTeamActivityPost, []crewTeamActivityPost) {
	doing, decisions := map[string]crewTeamActivityPost{}, []crewTeamActivityPost{}
	entries, err := readCrewWorkLog(team.ProjectID)
	if err != nil {
		log.Printf("[WORK] project=%s event=read_failed", team.ProjectID)
		return doing, decisions
	}
	visible := crewWorkVisible(team, viewer)
	entries = slices.DeleteFunc(entries, func(e crewWorkEntry) bool { return !visible(e.Private, e.AgentID, e.OwnerID) })
	tasks, _ := crewWorkState(entries, time.Now().UTC())
	labels := crewTeamPersonLabels(team)
	replaced := map[string]bool{}
	for _, e := range entries {
		if e.Replaces != "" {
			replaced[e.Replaces] = true
		}
	}
	for _, e := range entries {
		prefix := ""
		if t := tasks[e.Task]; t != nil {
			prefix = t.Label + ": "
		}
		post := crewTeamActivityPost{ID: e.ID, Private: e.Private, ChatKey: e.ChatKey, AgentID: e.AgentID, OwnerID: e.OwnerID, Agent: e.Agent, Person: labels[e.OwnerID], At: e.At}
		switch e.Event {
		case "started", "update", "done":
			post.Kind, post.Line = "doing", prefix+e.Line
			if e.Event == "started" && e.Line == "" && tasks[e.Task] != nil {
				post.Line = prefix + tasks[e.Task].Title
			}
			if e.Event == "done" {
				post.Line = prefix + "done: " + e.Line
			}
			key := e.AgentID
			if key == "" {
				key = e.ChatKey
			}
			doing[key] = post
		case "decided":
			if !replaced[e.ID] {
				post.Kind, post.Line, post.Why = "decision", prefix+e.Line, e.Why
				decisions = append(decisions, post)
			}
		}
	}
	return doing, decisions
}

// handleCrewWorkLog is the work log's one route: GET op=work_brief|work_find, POST the other ops.
func (api *StreamingAPI) handleCrewWorkLog(w http.ResponseWriter, r *http.Request) {
	team, claims, ok := crewTeamForTab(w, r, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if !team.On {
		externalError(w, 403, "team_off", "Turn on this Crew's Team first")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		crewWorkInput
		AgentID   string `json:"agent_id"`
		ChatKey   string `json:"chat_key"`
		Agent     string `json:"agent"`
		SharedAsk bool   `json:"shared_ask"`
	}
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		body.Op, body.Query, body.AgentID, body.ChatKey, body.Agent = q.Get("op"), q.Get("query"), q.Get("agent_id"), q.Get("chat_key"), q.Get("agent")
		body.SharedAsk = q.Get("shared_ask") == "1"
		if body.Op != "work_brief" && body.Op != "work_find" {
			externalError(w, 400, "invalid_work", "Read with work_brief or work_find")
			return
		}
	} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body) != nil || body.Op == "work_brief" || body.Op == "work_find" {
		externalError(w, 400, "invalid_work", "Choose a work action and give its fields")
		return
	}
	actor, err := api.crewTeamActivityActorForRequest(r, team, claims.UserID, body.AgentID, body.ChatKey, body.Agent)
	if err != nil || body.SharedAsk && !actor.InTeam {
		externalError(w, 403, "work_forbidden", "Use the work log from your own agent in this Crew")
		return
	}
	out, err := crewWorkDo(r.Context(), team, claims.UserID, actor, body.crewWorkInput)
	if err != nil {
		externalError(w, 400, "work_refused", err.Error())
		return
	}
	externalJSON(w, out)
}
