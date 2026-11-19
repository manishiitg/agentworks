package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
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
// With a Team on, everyone who sees the Team's tasks sees its features. The store is
// crew_features_store.go.

const (
	crewFeatureNameMax = 80
	crewFeatureLinkMax = 300
	crewFeaturesMax    = 200
)

type crewFeature struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Link      string             `json:"link,omitempty"`
	Done      bool               `json:"done,omitempty"`
	Agents    []string           `json:"agents,omitempty"` // chat keys joined by hand
	Names     map[string]string  `json:"names,omitempty"`  // their names when they joined
	Story     []crewFeatureEvent `json:"story,omitempty"`  // only in a hand-off; read with crewFeatureStory
	CreatedBy string             `json:"created_by"`
	CreatedAt time.Time          `json:"created_at"`
	ChangedAt time.Time          `json:"changed_at"`
}

// crewFeatureEvent is one line of a feature's story: a finished task, a decision with
// why, or a change to the feature itself. ID is the message it came from, so a line is
// copied once.
type crewFeatureEvent struct {
	ID   string    `json:"id"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // done, decision, started, joined, left, linked, finished, reopened
	By   string    `json:"by,omitempty"`
	// Person is whose agent By is (their first name), so a story line says who worked on it.
	Person string `json:"person,omitempty"`
	Text   string `json:"text"`
	Why    string `json:"why,omitempty"`
	Proof  string `json:"proof,omitempty"`
	Task   string `json:"task,omitempty"`
	// Owner: the event came from a task only this person sees (an Only-me agent's, or
	// from before the Team was on), so only they see it in the story.
	Owner string `json:"owner,omitempty"`
}

func crewFeatureNewID() string { return "feat-" + uuid.NewString()[:8] }

func crewFeatureNewEvent(kind, by, text string) crewFeatureEvent {
	return crewFeatureEvent{ID: "evt-" + uuid.NewString(), At: time.Now().UTC(), Kind: kind, By: by, Text: text}
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

// crewFeatureTakeLink reports whether link is free for feature at (-1 for a new one). A done feature gives its link
// up to an active one, so finishing or replacing a feature never strands its issue.
func crewFeatureTakeLink(tx *sql.Tx, features []crewFeature, at int, link string) error {
	other := crewFeatureByLink(features, link)
	if other < 0 || other == at {
		return nil
	}
	if !features[other].Done {
		return fmt.Errorf("Another feature already has the link %s", link)
	}
	features[other].Link = ""
	_, err := tx.Exec(`UPDATE features SET link = '', link_key = '' WHERE id = ?`, features[other].ID)
	return err
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
// story, once each.
func crewFeatureCapture(project string, f crewFeature, tasks []crewTeamTask, store agentMessageStore, viewer string) {
	db, err := openCrewFeatures(project, false)
	if err != nil || db == nil {
		return
	}
	have := crewFeatureStoryHas(db, f.ID)
	events := []crewFeatureEvent{}
	for _, task := range tasks {
		if task.Feature != f.ID {
			continue
		}
		if task.State == "done" && !have[task.ID] {
			events = append(events, crewFeatureEvent{ID: task.ID, At: task.At, Kind: "done", By: task.Agent, Person: task.Person, Text: task.Title, Proof: task.Proof, Task: task.ID, Owner: task.privateTo})
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
			person := ""
			if by == task.Agent {
				person = task.Person
			}
			events = append(events, crewFeatureEvent{ID: m.ID, At: m.SentAt, Kind: "decision", By: by, Person: person, Text: firstLineClip(crewFeatureDecisionText(agentTaskBody(m.Text)), 160), Why: m.Why, Task: task.ID, Owner: task.privateTo})
		}
	}
	if len(events) == 0 {
		return
	}
	err = crewFeatureWrite(project, func(tx *sql.Tx) error {
		for _, e := range events {
			if err := crewFeatureAddEvent(tx, f.ID, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[CREW_FEATURES] project=%s feature=%s event=capture_failed reason=%q", project, f.ID, err)
	}
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
	crewFeatureCapture(project, f, tasks, store, viewer)
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
	if done := crewFeatureStory(project, f.ID, viewer, "done", 1); len(done) > 0 {
		out["last_done"] = done[0]
	}
	out["decisions"], out["story"] = crewFeatureStory(project, f.ID, viewer, "decision", 60), crewFeatureStory(project, f.ID, viewer, "", 60)
	return out
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
		for _, e := range crewFeatureStory(project, f.ID, viewer, "decision", 5) {
			decisions = append(decisions, strings.TrimSpace(e.Text+" Why: "+e.Why))
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
		err := crewFeatureWrite(project, func(tx *sql.Tx) error {
			list, err := crewFeaturesLoad(context.Background(), tx)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			add := func(id string, e crewFeatureEvent) error { return crewFeatureAddEvent(tx, id, e) }
			at := crewFeatureFind(list, key)
			if at < 0 {
				if len(list) >= crewFeaturesMax {
					return fmt.Errorf("This project has %d features; mark finished ones done first", crewFeaturesMax)
				}
				if link != "" {
					if err := crewFeatureTakeLink(tx, list, -1, link); err != nil {
						return err
					}
				}
				f := crewFeature{ID: crewFeatureNewID(), Name: key, Link: link, CreatedBy: person, CreatedAt: now, ChangedAt: now}
				if err := crewFeatureInsert(tx, f); err != nil {
					return err
				}
				if err := add(f.ID, crewFeatureNewEvent("started", who, "Started this feature")); err != nil {
					return err
				}
				list, at, created = append(list, f), len(list), true
			}
			f := list[at]
			if f.Done {
				f.Done = false
				if err := add(f.ID, crewFeatureNewEvent("reopened", who, "Reopened this feature")); err != nil {
					return err
				}
			}
			if link != "" && f.Link == "" && crewFeatureTakeLink(tx, list, at, link) == nil {
				f.Link = link
			}
			if chat != "" {
				joined, err := crewFeatureAddAgent(tx, f.ID, chat, who, now)
				if err != nil {
					return err
				}
				if joined {
					f.Agents = append(f.Agents, chat)
					if f.Names == nil {
						f.Names = map[string]string{}
					}
					f.Names[chat] = who
					if !created {
						if err := add(f.ID, crewFeatureNewEvent("joined", who, who+" joined")); err != nil {
							return err
						}
					}
				}
			}
			f.ChangedAt = now
			result = f
			return crewFeatureSave(tx, f)
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
		rename := agentTaskOneLine(in.Rename)
		if state == "" && link == "" && !in.Leave && rename == "" {
			return nil, fmt.Errorf("Give something to change: rename, link, state (done or active) or leave=true")
		}
		if len([]rune(rename)) > crewFeatureNameMax {
			return nil, fmt.Errorf("Keep the feature's name under %d characters", crewFeatureNameMax)
		}
		var result crewFeature
		err := crewFeatureWrite(project, func(tx *sql.Tx) error {
			list, err := crewFeaturesLoad(context.Background(), tx)
			if err != nil {
				return err
			}
			at := crewFeatureFind(list, key)
			if at < 0 {
				return fmt.Errorf("No feature %q. List them with features", key)
			}
			f := list[at]
			add := func(kind, text string) error {
				return crewFeatureAddEvent(tx, f.ID, crewFeatureNewEvent(kind, who, text))
			}
			if rename != "" && rename != f.Name {
				if other := crewFeatureFind(list, rename); other >= 0 && other != at {
					return fmt.Errorf("Another feature is already called %s; pick another name", list[other].Name)
				}
				old := f.Name
				f.Name = rename
				if err := add("renamed", "Renamed from "+old); err != nil {
					return err
				}
			}
			if link != "" && link != f.Link {
				if err := crewFeatureTakeLink(tx, list, at, link); err != nil {
					return err
				}
				f.Link = link
				if err := add("linked", "Linked "+link); err != nil {
					return err
				}
			}
			if state == "done" && !f.Done {
				f.Done = true
				if err := add("finished", "Marked this feature done"); err != nil {
					return err
				}
			} else if state == "active" && f.Done {
				f.Done = false
				if err := add("reopened", "Reopened this feature"); err != nil {
					return err
				}
			}
			if in.Leave && slices.Contains(f.Agents, agent.ChatKey) {
				if _, err := tx.Exec(`DELETE FROM feature_agents WHERE feature_id = ? AND chat_key = ?`, f.ID, agent.ChatKey); err != nil {
					return err
				}
				f.Agents = slices.DeleteFunc(f.Agents, func(k string) bool { return k == agent.ChatKey })
				if err := add("left", who+" left"); err != nil {
					return err
				}
			}
			f.ChangedAt = time.Now().UTC()
			result = f
			return crewFeatureSave(tx, f)
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
