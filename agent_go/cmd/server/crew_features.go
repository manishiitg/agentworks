package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// Features (#3210) group the agents and the history of one piece of work in a project:
// many agents and people, over weeks. A feature is only a name, an optional link to an
// outside tracker (an issue such as #2965, a ticket key or a URL) and the agents joined
// by hand. Everything else is read from tasks: a task belongs to a feature when it names
// it, or when its issue is the feature's link; its agents are the ones joined plus the
// ones with an unfinished task in it. Done tasks and decisions are copied into the
// feature's story as they are seen, so the story outlives the task threads.
//
// Features live with the project, not its Team: a person without a Team has them too.
// With a Team on, everyone who sees the Team's tasks sees its features.

const (
	crewFeatureNameMax  = 80
	crewFeatureLinkMax  = 300
	crewFeatureStoryMax = 400
	crewFeaturesMax     = 200
)

type crewFeature struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Link      string             `json:"link,omitempty"`
	Done      bool               `json:"done,omitempty"`
	Agents    []string           `json:"agents,omitempty"` // chat keys joined by hand
	Names     map[string]string  `json:"names,omitempty"`  // their names when they joined
	Story     []crewFeatureEvent `json:"story,omitempty"`  // newest first
	CreatedBy string             `json:"created_by"`
	CreatedAt time.Time          `json:"created_at"`
	ChangedAt time.Time          `json:"changed_at"`
}

// crewFeatureEvent is one line of a feature's story: a finished task, a decision with
// why, or a change to the feature itself. ID is the message it came from, so a line is
// copied once.
type crewFeatureEvent struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"` // done, decision, started, joined, left, linked, finished, reopened
	By    string    `json:"by,omitempty"`
	Text  string    `json:"text"`
	Why   string    `json:"why,omitempty"`
	Proof string    `json:"proof,omitempty"`
	Task  string    `json:"task,omitempty"`
	// Owner: the event came from a task only this person sees (an Only-me agent's, or
	// from before the Team was on), so only they see it in the story.
	Owner string `json:"owner,omitempty"`
}

var crewFeaturesMu sync.Mutex

func crewFeaturesPath(project string) (string, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "features.json"), nil
}

func readCrewFeatures(project string) []crewFeature {
	path, err := crewFeaturesPath(project)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file struct {
		Features []crewFeature `json:"features"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return nil
	}
	return file.Features
}

// changeCrewFeatures reads, changes and saves the project's features under one lock.
func changeCrewFeatures(project string, change func(*[]crewFeature) error) error {
	path, err := crewFeaturesPath(project)
	if err != nil {
		return err
	}
	crewFeaturesMu.Lock()
	defer crewFeaturesMu.Unlock()
	features := readCrewFeatures(project)
	if err := change(&features); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, struct {
		Features []crewFeature `json:"features"`
	}{features}, 0600)
}

// crewFeatureLink is a link as tasks store their issue: a bare number becomes #number.
func crewFeatureLink(s string) string {
	s = agentTaskOneLine(s)
	if s != "" && strings.Trim(s, "0123456789") == "" {
		s = "#" + s
	}
	return s
}

// crewFeatureFind is the feature named by id or by name (any case), done ones included.
func crewFeatureFind(features []crewFeature, key string) int {
	key = agentTaskOneLine(key)
	if key == "" {
		return -1
	}
	if i := slices.IndexFunc(features, func(f crewFeature) bool { return f.ID == key }); i >= 0 {
		return i
	}
	return slices.IndexFunc(features, func(f crewFeature) bool { return strings.EqualFold(f.Name, key) })
}

func crewFeatureByLink(features []crewFeature, ref string) int {
	if ref == "" {
		return -1
	}
	return slices.IndexFunc(features, func(f crewFeature) bool { return f.Link != "" && strings.EqualFold(f.Link, ref) })
}

// crewFeatureDefault: a request with no feature, sent by an agent in exactly one open
// feature of its project, belongs to that feature, unless its issue is another feature's link.
func crewFeatureDefault(sender agentMessageEndpoint, ref string) string {
	if sender.Kind != triggerCallerCrew {
		return ""
	}
	return crewFeatureDefaultIn(sender.ID, sender.ChatKey, ref)
}

func crewFeatureDefaultIn(project, chatKey, ref string) string {
	if project == "" || chatKey == "" {
		return ""
	}
	features := readCrewFeatures(project)
	if crewFeatureByLink(features, ref) >= 0 {
		return ""
	}
	found := ""
	for _, f := range features {
		if !f.Done && slices.Contains(f.Agents, chatKey) {
			if found != "" {
				return ""
			}
			found = f.ID
		}
	}
	return found
}

// agentTaskFeature is the feature a request names, changed by the latest message in its
// thread (until the next request) that names one, as category and tags are.
func agentTaskFeature(c agentConversation, request agentMessage) string {
	feature := request.Feature
	for _, m := range c.Messages {
		if m.Sequence <= request.Sequence {
			continue
		}
		if m.MessageType == "request" && m.From == request.From {
			break
		}
		if m.Feature != "" {
			feature = m.Feature
		}
	}
	return feature
}

// crewFeatureAssign gives each task its feature: the one it names, else the one whose
// link is its issue, else its parent's.
func crewFeatureAssign(project string, tasks []crewTeamTask) []crewTeamTask {
	features := readCrewFeatures(project)
	if len(features) == 0 {
		return tasks
	}
	byID := map[string]int{}
	for i, task := range tasks {
		byID[task.ID] = i
		at := crewFeatureFind(features, task.featureKey)
		if at < 0 {
			at = crewFeatureByLink(features, task.Group)
		}
		if at >= 0 {
			tasks[i].Feature, tasks[i].FeatureName = features[at].ID, features[at].Name
		}
	}
	for i := range tasks {
		for p, depth := tasks[i].Parent, 0; tasks[i].Feature == "" && p != "" && depth < 20; depth++ {
			parent := tasks[byID[p]]
			tasks[i].Feature, tasks[i].FeatureName = parent.Feature, parent.FeatureName
			p = parent.Parent
		}
	}
	return tasks
}

// crewFeatureMembers are the feature's agents: joined by hand, plus any agent doing an
// unfinished task in it. Each is {name, chat_key, joined}.
func crewFeatureMembers(f crewFeature, tasks []crewTeamTask, names map[string]string) []map[string]any {
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, key := range f.Agents {
		seen[key] = true
		out = append(out, map[string]any{"name": firstNonEmptyTrimmed(names[key], f.Names[key], "an agent"), "chat_key": key, "joined": true})
	}
	for _, task := range tasks {
		if task.Feature != f.ID || task.State == "done" || task.agentChat == "" || seen[task.agentChat] {
			continue
		}
		seen[task.agentChat] = true
		out = append(out, map[string]any{"name": task.Agent, "chat_key": task.agentChat, "person": task.Person})
	}
	return out
}

func crewFeatureAgentNames(tasks []crewTeamTask, actor crewTeamRegisteredAgent) map[string]string {
	names := map[string]string{}
	for _, task := range tasks {
		if task.agentChat != "" && task.Agent != "" {
			names[task.agentChat] = task.Agent
		}
		if task.askerChat != "" && task.askerName != "" && names[task.askerChat] == "" {
			names[task.askerChat] = task.askerName
		}
	}
	if actor.ChatKey != "" {
		names[actor.ChatKey] = firstNonEmptyTrimmed(actor.Name, actor.Tab, names[actor.ChatKey])
	}
	return names
}

// crewFeatureCapture copies done tasks and decisions of the feature's tasks into its
// story, once each, and returns the feature as saved.
func crewFeatureCapture(project string, f crewFeature, tasks []crewTeamTask, store agentMessageStore, viewer string) crewFeature {
	events := []crewFeatureEvent{}
	have := map[string]bool{}
	for _, e := range f.Story {
		have[e.ID] = true
	}
	for _, task := range tasks {
		if task.Feature != f.ID {
			continue
		}
		if task.State == "done" && !have[task.ID] {
			events = append(events, crewFeatureEvent{ID: task.ID, At: task.At, Kind: "done", By: task.Agent, Text: task.Title, Proof: task.Proof, Task: task.ID, Owner: task.privateTo})
		}
		at := slices.IndexFunc(store.Conversations, func(c agentConversation) bool { return c.ID == task.inbox })
		if at < 0 {
			continue
		}
		c := store.Conversations[at]
		start := slices.IndexFunc(c.Messages, func(m agentMessage) bool { return m.ID == task.ID })
		if start < 0 {
			continue
		}
		request := c.Messages[start]
		for _, m := range c.Messages[start:] {
			if m.Sequence > request.Sequence && m.MessageType == "request" && m.From == request.From {
				break
			}
			if !m.Decision || have[m.ID] {
				continue
			}
			by := firstNonEmptyTrimmed(m.UpdatedBy, store.endpointName(c.Endpoints[m.From], viewer))
			events = append(events, crewFeatureEvent{ID: m.ID, At: m.SentAt, Kind: "decision", By: by, Text: firstLineClip(crewFeatureDecisionText(agentTaskBody(m.Text)), 160), Why: m.Why, Task: task.ID, Owner: task.privateTo})
		}
	}
	if len(events) == 0 {
		return f
	}
	saved := f
	err := changeCrewFeatures(project, func(features *[]crewFeature) error {
		at := slices.IndexFunc(*features, func(x crewFeature) bool { return x.ID == f.ID })
		if at < 0 {
			return errors.New("gone")
		}
		x := &(*features)[at]
		for _, e := range events {
			if !slices.ContainsFunc(x.Story, func(old crewFeatureEvent) bool { return old.ID == e.ID }) {
				x.Story = append(x.Story, e)
			}
		}
		crewFeatureSortStory(x)
		saved = *x
		return nil
	})
	if err != nil {
		return f
	}
	return saved
}

// crewFeatureDecisionText drops the "Decision by X:" header task_update writes and the
// "Why:" part, which the event keeps on its own.
func crewFeatureDecisionText(text string) string {
	if head, rest, ok := strings.Cut(text, ": "); ok && strings.HasPrefix(head, "Decision by ") {
		text = rest
	}
	text, _, _ = strings.Cut(text, "\n\nWhy: ")
	return strings.TrimSpace(text)
}

func crewFeatureSortStory(f *crewFeature) {
	slices.SortStableFunc(f.Story, func(a, b crewFeatureEvent) int { return b.At.Compare(a.At) })
	if len(f.Story) > crewFeatureStoryMax {
		f.Story = f.Story[:crewFeatureStoryMax]
	}
}

func crewFeatureRow(f crewFeature, tasks []crewTeamTask, names map[string]string) map[string]any {
	open, done := 0, 0
	for _, task := range tasks {
		if task.Feature == f.ID {
			if task.State == "done" {
				done++
			} else {
				open++
			}
		}
	}
	row := map[string]any{"id": f.ID, "name": f.Name, "state": map[bool]string{true: "done", false: "active"}[f.Done], "agents": crewFeatureMembers(f, tasks, names), "open_tasks": open, "done_tasks": done, "changed_at": f.ChangedAt}
	if f.Link != "" {
		row["link"] = f.Link
	}
	return row
}

// crewFeatureView is one feature as the Work tab shows it: Now (open tasks with who is
// on each, blocked ones and the ones waiting on a person), the last finished task,
// decisions with why, the story and the agents.
func crewFeatureView(project string, f crewFeature, tasks []crewTeamTask, store agentMessageStore, viewer string, names map[string]string) map[string]any {
	f = crewFeatureCapture(project, f, tasks, store, viewer)
	f.Story = crewFeatureStoryFor(f.Story, viewer)
	out := crewFeatureRow(f, tasks, names)
	now, blocked, waiting := []map[string]any{}, []map[string]any{}, []map[string]any{}
	for _, task := range tasks {
		if task.Feature != f.ID || task.State == "done" {
			continue
		}
		row := crewTaskRow(task)
		switch task.State {
		case "blocked":
			blocked = append(blocked, row)
		case "needs_you":
			waiting = append(waiting, row)
		default:
			now = append(now, row)
		}
	}
	out["now"], out["blocked"], out["waiting_on_a_person"] = now, blocked, waiting
	decisions := []crewFeatureEvent{}
	for _, e := range f.Story {
		if e.Kind == "decision" {
			decisions = append(decisions, e)
		}
		if e.Kind == "done" && out["last_done"] == nil {
			out["last_done"] = e
		}
	}
	out["decisions"], out["story"] = decisions, f.Story[:min(len(f.Story), 60)]
	return out
}

// crewFeatureStoryFor drops the events only another person may see.
func crewFeatureStoryFor(story []crewFeatureEvent, viewer string) []crewFeatureEvent {
	return slices.DeleteFunc(slices.Clone(story), func(e crewFeatureEvent) bool { return e.Owner != "" && e.Owner != viewer })
}

// crewFeatureBrief is the features an agent is in, for its session-start brief: each
// with its open tasks and latest decisions, so a new agent picks up the why.
func crewFeatureBrief(project, viewer string, tasks []crewTeamTask, chatKey string) []map[string]any {
	out := []map[string]any{}
	for _, f := range readCrewFeatures(project) {
		in := slices.Contains(f.Agents, chatKey) || slices.ContainsFunc(tasks, func(t crewTeamTask) bool {
			return t.Feature == f.ID && t.State != "done" && t.agentChat == chatKey
		})
		if f.Done || !in || chatKey == "" {
			continue
		}
		item := map[string]any{"id": f.ID, "name": f.Name}
		if f.Link != "" {
			item["link"] = f.Link
		}
		open := []string{}
		for _, t := range tasks {
			if t.Feature == f.ID && t.State != "done" && len(open) < 8 {
				open = append(open, fmt.Sprintf("%s (%s, %s)", t.Title, firstNonEmptyTrimmed(t.Agent, t.Person, "nobody yet"), strings.ReplaceAll(t.State, "_", " ")))
			}
		}
		decisions := []string{}
		for _, e := range crewFeatureStoryFor(f.Story, viewer) {
			if e.Kind == "decision" && len(decisions) < 5 {
				decisions = append(decisions, strings.TrimSpace(e.Text+" Why: "+e.Why))
			}
		}
		item["open_tasks"], item["latest_decisions"] = open, decisions
		out = append(out, item)
	}
	return out
}

// crewFeatureDo runs features, feature, feature_join and feature_update for an agent
// (or a person over MCP) in this project. tasks are the ones this person sees.
func crewFeatureDo(project, person string, agent crewTeamRegisteredAgent, tasks []crewTeamTask, store agentMessageStore, in crewWorkInput) (map[string]any, error) {
	names := crewFeatureAgentNames(tasks, agent)
	who := firstNonEmptyTrimmed(agent.Name, agent.Tab, "you")
	key := strings.TrimSpace(in.Feature)
	features := readCrewFeatures(project)
	switch in.Op {
	case "features":
		all := strings.EqualFold(strings.TrimSpace(in.State), "all")
		rows := []map[string]any{}
		for _, f := range features {
			if all || !f.Done {
				rows = append(rows, crewFeatureRow(f, tasks, names))
			}
		}
		return map[string]any{"features": rows, "hint": "Read one with feature (feature = its id or name). Join one with feature_join; a new name starts a feature."}, nil
	case "feature":
		at := crewFeatureFind(features, key)
		if at < 0 {
			return nil, fmt.Errorf("No feature %q. List them with features", key)
		}
		return crewFeatureView(project, features[at], tasks, store, person, names), nil
	case "feature_join":
		if key == "" {
			return nil, fmt.Errorf("Say which feature: its name, or a new name to start one")
		}
		if len([]rune(key)) > crewFeatureNameMax {
			return nil, fmt.Errorf("Keep the feature's name under %d characters", crewFeatureNameMax)
		}
		link := crewFeatureLink(in.Link)
		if len([]rune(link)) > crewFeatureLinkMax {
			return nil, fmt.Errorf("The link is too long; give the issue (#2965), a ticket key or a URL")
		}
		chat := agent.ChatKey
		if chat == "" || strings.HasPrefix(chat, crewWorkPersonPrefix) {
			chat = ""
		}
		var result crewFeature
		created := false
		err := changeCrewFeatures(project, func(list *[]crewFeature) error {
			now := time.Now().UTC()
			at := crewFeatureFind(*list, key)
			if at < 0 {
				if len(*list) >= crewFeaturesMax {
					return fmt.Errorf("This project has %d features; mark finished ones done first", crewFeaturesMax)
				}
				if link != "" && crewFeatureByLink(*list, link) >= 0 {
					return fmt.Errorf("Another feature already has the link %s", link)
				}
				*list = append(*list, crewFeature{ID: "feat-" + uuid.NewString()[:8], Name: key, Link: link, CreatedBy: person, CreatedAt: now, ChangedAt: now,
					Story: []crewFeatureEvent{{ID: "evt-" + uuid.NewString(), At: now, Kind: "started", By: who, Text: "Started this feature"}}})
				at, created = len(*list)-1, true
			}
			f := &(*list)[at]
			if f.Done {
				f.Done = false
				f.Story = append(f.Story, crewFeatureEvent{ID: "evt-" + uuid.NewString(), At: now, Kind: "reopened", By: who, Text: "Reopened this feature"})
			}
			if link != "" && f.Link == "" && crewFeatureByLink(*list, link) < 0 {
				f.Link = link
			}
			if chat != "" && !slices.Contains(f.Agents, chat) {
				f.Agents = append(f.Agents, chat)
				if f.Names == nil {
					f.Names = map[string]string{}
				}
				f.Names[chat] = who
				if !created {
					f.Story = append(f.Story, crewFeatureEvent{ID: "evt-" + uuid.NewString(), At: now, Kind: "joined", By: who, Text: who + " joined"})
				}
			}
			f.ChangedAt = now
			crewFeatureSortStory(f)
			result = *f
			return nil
		})
		if err != nil {
			return nil, err
		}
		names[chat] = who
		out := crewFeatureRow(result, tasks, names)
		out["created"] = created
		return out, nil
	case "feature_update":
		state := strings.ToLower(strings.TrimSpace(in.State))
		link := crewFeatureLink(in.Link)
		if state != "" && state != "done" && state != "active" {
			return nil, fmt.Errorf("state is done or active")
		}
		if state == "" && link == "" && !in.Leave {
			return nil, fmt.Errorf("Give something to change: link, state (done or active) or leave=true")
		}
		var result crewFeature
		err := changeCrewFeatures(project, func(list *[]crewFeature) error {
			at := crewFeatureFind(*list, key)
			if at < 0 {
				return fmt.Errorf("No feature %q. List them with features", key)
			}
			f, now := &(*list)[at], time.Now().UTC()
			add := func(kind, text string) {
				f.Story = append(f.Story, crewFeatureEvent{ID: "evt-" + uuid.NewString(), At: now, Kind: kind, By: who, Text: text})
			}
			if link != "" && link != f.Link {
				if other := crewFeatureByLink(*list, link); other >= 0 && other != at {
					return fmt.Errorf("Another feature already has the link %s", link)
				}
				f.Link = link
				add("linked", "Linked "+link)
			}
			if state == "done" && !f.Done {
				f.Done = true
				add("finished", "Marked this feature done")
			} else if state == "active" && f.Done {
				f.Done = false
				add("reopened", "Reopened this feature")
			}
			if in.Leave {
				if i := slices.Index(f.Agents, agent.ChatKey); i >= 0 {
					f.Agents = slices.Delete(f.Agents, i, i+1)
					delete(f.Names, agent.ChatKey)
					add("left", who+" left")
				}
			}
			f.ChangedAt = now
			crewFeatureSortStory(f)
			result = *f
			return nil
		})
		if err != nil {
			return nil, err
		}
		return crewFeatureRow(result, tasks, names), nil
	}
	return nil, fmt.Errorf("Choose features, feature, feature_join or feature_update")
}

// crewFeatureResolve is the feature named by id or name, for task_update and sends.
func crewFeatureResolve(project, key string) (crewFeature, error) {
	features := readCrewFeatures(project)
	at := crewFeatureFind(features, key)
	if at < 0 {
		return crewFeature{}, fmt.Errorf("No feature %q. List them with features, or start it with feature_join", key)
	}
	return features[at], nil
}
