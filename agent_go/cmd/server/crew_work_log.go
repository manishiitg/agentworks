package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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

// crewWorkPersonPrefix marks an entry written by a person over MCP rather than
// by one of their agents (Claude Cowork, ChatGPT): ChatKey is this prefix plus
// their user ID, never a model-chosen value.
const crewWorkPersonPrefix = "person:"

// crewWorkScope is where entries live and who sees them: a Crew's Team, or,
// when the Team is off or was never set up, the Crew itself, where every entry
// is visible only to the person who wrote it.
type crewWorkScope struct {
	Project string
	Team    *crewTeam
}

var crewWorkActions = []string{"work_brief", "work_add", "work_start", "work_update", "work_decide", "work_done", "work_release", "work_find", "work_need", "work_withdraw", "work_answer"}

type crewWorkEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Event    string    `json:"event"` // added, started, update, decided, done, released, needs, answered, withdrawn
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
	// A needs entry asks one person (ForID) with up to three Buttons; answered and
	// withdrawn entries name it in Need. Helper is the person's own agent that an
	// "Ask my agent" answer went to, remembered for their next one.
	Buttons []string `json:"buttons,omitempty"`
	ForID   string   `json:"for_id,omitempty"`
	Need    string   `json:"need,omitempty"`
	Helper  string   `json:"helper,omitempty"`
	AgentID string   `json:"agent_id,omitempty"`
	ChatKey string   `json:"chat_key,omitempty"`
	OwnerID string   `json:"owner_id"`
	Agent   string   `json:"agent"`
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
	Op       string   `json:"op"`
	Task     string   `json:"task"`
	Label    string   `json:"label"`
	Title    string   `json:"title"`
	Link     string   `json:"link"`
	PartOf   string   `json:"part_of"`
	For      string   `json:"for"`
	Line     string   `json:"line"`
	Why      string   `json:"why"`
	Replaces string   `json:"replaces"`
	Query    string   `json:"query"`
	Buttons  []string `json:"buttons"`
	Need     string   `json:"need"`
	Button   string   `json:"button"`
	Helper   string   `json:"helper"`
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
	// A Mac-paired or personal Crew may not have server Team state yet.
	// The caller holds the existing Crew mutex; the first append owns creation.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
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
func crewWorkDo(ctx context.Context, scope crewWorkScope, person string, actor crewTeamRegisteredAgent, in crewWorkInput) (any, error) {
	if !slices.Contains(crewWorkActions, in.Op) {
		return nil, fmt.Errorf("Choose one of %s", strings.Join(crewWorkActions, ", "))
	}
	agent, private := actor, true
	if actor.OwnerID != person || actor.ChatKey == "" || isSharedThreadKey(actor.ChatKey) {
		return nil, fmt.Errorf("Use the work log from your own agent in this Crew")
	}
	if team := scope.Team; team != nil {
		if !crewTeamAgentsVisible(*team, person) {
			return nil, fmt.Errorf("This Crew's Team is not available to you")
		}
		registered := false
		if actor.ID == "" && actor.ChatKey == crewWorkPersonPrefix+person {
			// A person over MCP writes as themselves: shared with the Team they are in.
			private = false
		} else {
			agent, registered = team.Agents[actor.ID]
			if !registered {
				agent = actor
				agent.InTeam = false
			}
			if agent.OwnerID != person || agent.ChatKey == "" || isSharedThreadKey(agent.ChatKey) {
				return nil, fmt.Errorf("Use the work log from your own agent in this Crew")
			}
			private = !registered || !agent.InTeam
			if actor.InTeam && private {
				return nil, fmt.Errorf("The agent is no longer shared with the team. Try again from its own chat")
			}
		}
		switch {
		case in.Op == "work_answer":
			agent, private = crewWorkPersonActor(person), false // the answer is the person's, whichever of their chats clicked
		case in.Op == "work_need" && agent.ID == "":
			return nil, fmt.Errorf("Ask a person from one of your agents, so the answer has an agent to go back to")
		}
	}
	if crewWorkNeedOp(in.Op) && scope.Team == nil {
		return nil, fmt.Errorf("Asking a person shows a card in the Team panel, so it needs this Crew's Team. Turn the Team on first, or just ask your person in this chat")
	}
	var helper *crewTeamReachableAgent
	if in.Op == "work_answer" {
		// Resolved before the lock: reading the person's own agents may touch the Team record.
		var choose map[string]any
		var err error
		if helper, choose, err = crewWorkAnswerHelper(ctx, *scope.Team, person, in); err != nil {
			return nil, err
		}
		if choose != nil {
			return choose, nil
		}
	}
	if in.Op == "work_brief" || in.Op == "work_find" {
		entries, err := readCrewWorkLog(scope.Project)
		if err != nil {
			return nil, err
		}
		if in.Op == "work_brief" {
			return crewWorkBrief(scope, person, agent, entries, time.Now().UTC()), nil
		}
		return crewWorkFind(scope, person, entries, in.Query, time.Now().UTC())
	}

	dir, path, err := crewWorkPath(scope.Project)
	if err != nil {
		return nil, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	unlock := sync.OnceFunc(mu.Unlock)
	defer unlock()
	if err := crewWorkMigrateLocked(scope.Project, dir, path); err != nil {
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
		log.Printf("[WORK] project=%s event=refused op=%s task=%s reason=%s agent_id=%s caller_id=%s", scope.Project, in.Op, task, reason, agent.ID, person)
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
	optionalTask := in.Op == "work_decide" || in.Op == "work_need"
	if in.Op != "work_add" && !optionalTask && !crewWorkNeedOp(in.Op) || optionalTask && strings.TrimSpace(in.Task) != "" {
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
			log.Printf("[WORK] project=%s event=taken_over task=%s from_agent_id=%s agent_id=%s", scope.Project, task.ID, task.holderID, agent.ID)
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
	case "work_need", "work_withdraw", "work_answer":
		if err := crewWorkNeedEntry(scope, person, agent, in, entries, helper, &e); err != nil {
			if reason, ok := err.(crewWorkRefusal); ok {
				return refuse(e.Need, reason.code, reason.text)
			}
			return nil, err
		}
	}
	if err := crewWorkAppendLocked(path, e); err != nil {
		return nil, err
	}
	unlock()
	log.Printf("[WORK] project=%s event=%s task=%s entry_id=%s private=%t team=%t agent_id=%s caller_id=%s", scope.Project, e.Event, e.Task, e.ID, e.Private, scope.Team != nil, agent.ID, person)
	publishCrewPanelChanged(scope.Project)
	if scope.Team != nil {
		go pushCrewTeamActivityChanged(*scope.Team)
	}
	out := map[string]any{"saved": e.Event, "id": e.ID}
	if crewWorkNeedOp(in.Op) {
		crewWorkNeedDone(ctx, *scope.Team, person, append(entries, e), e, helper, out)
	}
	if e.Task != "" {
		tasks, _ = crewWorkState(append(entries, e), now)
		out["task"] = tasks[e.Task]
	}
	switch {
	case scope.Team == nil:
		out["note"] = "Only you see this, because this Crew's Team is off."
	case e.Private:
		out["note"] = "Only you see this, because this agent isn't shared with the team."
	}
	return out, nil
}

// crewWorkVisible applies the Team post rule: an Only-me entry is its owner's;
// a shared entry needs its agent still In team and its owner still a member. A
// person's own MCP entry needs that person still a member.
func crewWorkVisible(team crewTeam, viewer string) func(private bool, agentID, ownerID string) bool {
	active := map[string]bool{}
	return func(private bool, agentID, ownerID string) bool {
		if private {
			return team.On && viewer != "" && viewer == ownerID && team.member(viewer) && crewTeamAccountActive(viewer)
		}
		if agentID == "" {
			return team.On && team.member(ownerID) && crewTeamAccountActive(ownerID)
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

// crewWorkScopeView is the scope's filter and name labels for one viewer.
func crewWorkScopeView(scope crewWorkScope, viewer string) (func(e crewWorkEntry) bool, map[string]string) {
	if scope.Team == nil {
		return func(e crewWorkEntry) bool { return viewer != "" && e.OwnerID == viewer }, map[string]string{viewer: crewTeamFirstName(crewPersonShortName(viewer))}
	}
	visible := crewWorkVisible(*scope.Team, viewer)
	return func(e crewWorkEntry) bool {
		if e.AgentID == "" && e.ChatKey != crewWorkPersonPrefix+e.OwnerID && !e.Private {
			return false // only a person's own MCP entry may have no agent
		}
		return visible(e.Private, e.AgentID, e.OwnerID)
	}, crewTeamPersonLabels(*scope.Team)
}

func crewWorkFor(scope crewWorkScope, viewer string, entries []crewWorkEntry, now time.Time) ([]*crewWorkTask, []*crewWorkDecision) {
	visible, labels := crewWorkScopeView(scope, viewer)
	shown := slices.DeleteFunc(slices.Clone(entries), func(e crewWorkEntry) bool { return !visible(e) })
	byID, decisions := crewWorkState(shown, now)
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
func crewWorkBrief(scope crewWorkScope, person string, agent crewTeamRegisteredAgent, entries []crewWorkEntry, now time.Time) map[string]any {
	tasks, decisions := crewWorkFor(scope, person, entries, now)
	_, labels := crewWorkScopeView(scope, person)
	me := strings.ToLower(strings.TrimSpace(agent.Name))
	myName := strings.ToLower(labels[person])
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
	needYou, asks := []*crewWorkNeed{}, []*crewWorkNeed{}
	needs := crewWorkNeedsFor(scope, person, entries, now)
	for i := len(needs) - 1; i >= 0; i-- {
		n := needs[i]
		if n.State == "waiting" && n.ForMe {
			needYou = append(needYou, n)
		}
		if len(asks) < 10 && n.agentID == agent.ID && n.ownerID == agent.OwnerID && (agent.ID != "" || n.chatKey == agent.ChatKey) {
			asks = append(asks, n)
		}
	}
	return map[string]any{
		"waiting_on_your_person": needYou,
		"your_requests":          asks,
		"yours":                  mine,
		"for_you":                forMe,
		"open_nobody_yet":        open[:min(20, len(open))],
		"others_working":         others[:min(20, len(others))],
		"main_tasks":             mains[:min(10, len(mains))],
		"decisions":              current,
		"hint":                   "Start a task before working on it (work_start); post work_update as you go, work_decide for choices others must follow, and work_done with the outcome. A stalled task (no entry for a day) may be taken over. Blocked on a person (a review, an approval)? Use work_need with up to three buttons; their click comes back to you as a message.",
	}
}

// crewWorkFind searches every task and decision, done and replaced ones too.
func crewWorkFind(scope crewWorkScope, person string, entries []crewWorkEntry, query string, now time.Time) (map[string]any, error) {
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
	tasks, decisions := crewWorkFor(scope, person, entries, now)
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
// (the newest start, update or finish), the current decisions, and the task
// events (added, started, done) for Just happened, all from the log. Each item
// about a task carries its label, title and state so the panel can group them.
func crewWorkActivityViews(scope crewWorkScope, viewer string) (map[string]crewTeamActivityPost, []crewTeamActivityPost, []crewTeamActivityPost) {
	doing, decisions, work := map[string]crewTeamActivityPost{}, []crewTeamActivityPost{}, []crewTeamActivityPost{}
	entries, err := readCrewWorkLog(scope.Project)
	if err != nil {
		log.Printf("[WORK] project=%s event=read_failed", scope.Project)
		return doing, decisions, work
	}
	visible, labels := crewWorkScopeView(scope, viewer)
	entries = slices.DeleteFunc(entries, func(e crewWorkEntry) bool { return !visible(e) })
	tasks, _ := crewWorkState(entries, time.Now().UTC())
	_, needs := crewWorkNeedsState(entries)
	replaced := map[string]bool{}
	for _, e := range entries {
		if e.Replaces != "" {
			replaced[e.Replaces] = true
		}
	}
	for _, e := range entries {
		prefix := ""
		post := crewTeamActivityPost{ID: e.ID, Private: e.Private && scope.Team != nil, ChatKey: e.ChatKey, AgentID: e.AgentID, OwnerID: e.OwnerID, Agent: e.Agent, Person: labels[e.OwnerID], Event: e.Event, At: e.At}
		if t := tasks[e.Task]; t != nil {
			prefix = t.Label + ": "
			post.Task, post.TaskTitle, post.TaskState = t.Label, t.Title, crewWorkStateLine(t)
		}
		if e.Event == "added" || e.Event == "started" || e.Event == "done" {
			event := post
			event.Kind, event.Line = "task", e.Line
			if e.Event != "done" || e.Line == "" {
				event.Line = post.TaskTitle
			}
			work = append(work, event)
		}
		if n := needs[firstNonEmptyTrimmed(e.Need, e.ID)]; n != nil && (e.Event == "needs" || e.Event == "answered" || e.Event == "withdrawn") {
			event := post
			event.NeedsFor = n.forID
			event.Kind, event.Line = "task", labels[n.forID]+": "+n.Line
			if t := tasks[n.taskID]; t != nil {
				event.Task, event.TaskTitle, event.TaskState = t.Label, t.Title, crewWorkStateLine(t)
			}
			if e.Event == "answered" {
				event.Line = e.Line + " · " + n.Line
			}
			work = append(work, event)
		}
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
	return doing, decisions, work
}

// crewWorkStateLine is a task's state in plain words for the panel.
func crewWorkStateLine(t *crewWorkTask) string {
	if t.Parts > 0 && t.State != "done" {
		return fmt.Sprintf("%d of %d done", t.PartsDone, t.Parts)
	}
	return map[string]string{"to_do": "to do", "started": "started", "stalled": "stalled", "done": "done"}[t.State]
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
	var actor crewTeamRegisteredAgent
	var err error
	if body.AgentID == "" && (body.ChatKey == crewWorkPersonPrefix+claims.UserID || body.ChatKey == "" && body.Op == "work_answer") {
		actor = crewWorkPersonActor(claims.UserID) // a person over MCP forwarded from their Mac, or a Needs-you button click
	} else if actor, err = api.crewTeamActivityActorForRequest(r, team, claims.UserID, body.AgentID, body.ChatKey, body.Agent); err != nil || body.SharedAsk && !actor.InTeam {
		externalError(w, 403, "work_forbidden", "Use the work log from your own agent in this Crew")
		return
	}
	out, err := crewWorkDo(r.Context(), crewWorkScope{Project: team.ProjectID, Team: &team}, claims.UserID, actor, body.crewWorkInput)
	if err != nil {
		externalError(w, 400, "work_refused", err.Error())
		return
	}
	externalJSON(w, out)
}

func crewWorkPersonActor(person string) crewTeamRegisteredAgent {
	return crewTeamRegisteredAgent{OwnerID: person, ChatKey: crewWorkPersonPrefix + person, Name: crewTeamFirstName(crewPersonShortName(person))}
}

// crewWorkTeamOff reports whether this Crew's work log is personal: its Team was
// never set up or is turned off. On a Mac, a Crew paired with the server reads
// its Team there; an unreachable server is not treated as "off".
func crewWorkTeamOff(ctx context.Context, person, project string) (bool, error) {
	if !IsLocalMode() {
		team, err := readCrewTeam(project)
		return err != nil || !team.On, nil
	}
	b, err := crewroot.ReadSync(project)
	if err != nil {
		return false, err
	}
	if b == nil || b.Side != "local" || b.PeerProjectID == "" {
		return true, nil
	}
	c, err := desktopConnectionFor(person, b.ConnectionID)
	if err != nil {
		return false, err
	}
	var team crewTeam
	if err := crewTeamRemoteJSON(ctx, person, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID), nil, &team); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return true, nil
		}
		return false, err
	}
	return !team.On, nil
}

// crewWorkPersonal runs a work action on a Crew whose Team is off, for the
// person's own chat (chat) or for the person over MCP (person actor).
func crewWorkPersonal(ctx context.Context, project, person string, actor crewTeamRegisteredAgent, args map[string]any) (string, error) {
	out, err := crewWorkDo(ctx, crewWorkScope{Project: project}, person, actor, crewWorkInputFromArgs(args))
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

func crewWorkInputFromArgs(args map[string]any) crewWorkInput {
	in := crewWorkInput{Op: externalArg(args, "action")}
	if op := externalArg(args, "team_action"); op != "" {
		in.Op = op
	}
	for key, field := range map[string]*string{"task": &in.Task, "label": &in.Label, "title": &in.Title, "link": &in.Link, "part_of": &in.PartOf, "for": &in.For, "line": &in.Line, "why": &in.Why, "replaces": &in.Replaces, "query": &in.Query, "need": &in.Need, "button": &in.Button, "helper": &in.Helper} {
		*field = externalArg(args, key)
	}
	switch buttons := args["buttons"].(type) {
	case []any:
		for _, b := range buttons {
			if text, ok := b.(string); ok {
				in.Buttons = append(in.Buttons, text)
			}
		}
	case []string:
		in.Buttons = buttons
	case string:
		in.Buttons = strings.Split(buttons, ",")
	}
	return in
}
