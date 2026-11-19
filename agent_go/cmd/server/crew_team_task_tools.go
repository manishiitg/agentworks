package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// #2965: tasks as tools, so agents run the tracker themselves. tasks lists what the
// person sees in Team › Tasks, with filters; task reads one with its history and
// decisions; task_update changes its status, category or tags, records a decision
// with why, or attaches a note (a Markdown file) in the task's own thread.
//
// Who may update: the agent that asked for the task, the agent doing it, and the
// Team owner's own agents (the owner runs the tracker). Everyone else is refused.
var crewTaskActions = []string{"tasks", "task", "task_update"}

const (
	crewTaskListDefault = 30
	crewTaskListMax     = 100
	crewTaskHistoryMax  = 30
	crewTaskNoteMax     = 32 << 10
)

func crewTaskOp(op string) bool { return slices.Contains(crewTaskActions, op) }

func crewFeatureOp(op string) bool {
	return slices.Contains([]string{"features", "feature", "feature_join", "feature_update"}, op)
}

// crewTaskDo runs tasks, task and task_update on the tasks this person sees: the Team's,
// or with the Team off their own (t is then only the project and the person).
func crewTaskDo(ctx context.Context, t crewTeam, person string, agent crewTeamRegisteredAgent, tasks []crewTeamTask, store agentMessageStore, in crewWorkInput) (any, error) {
	if in.Op == "tasks" {
		return crewTaskList(tasks, person, agent, in)
	}
	id := strings.TrimSpace(in.Task)
	if id == "" {
		return nil, fmt.Errorf("Say which task: its id from tasks")
	}
	at := slices.IndexFunc(tasks, func(task crewTeamTask) bool { return task.ID == id })
	if at < 0 {
		return nil, fmt.Errorf("No task %s that you can see. Use tasks to find it", id)
	}
	task := tasks[at]
	if in.Op == "task" {
		return crewTaskDetail(store, task, person), nil
	}
	if runningServerAPI == nil {
		return nil, fmt.Errorf("The server is starting; try again in a moment")
	}
	return runningServerAPI.updateTeamTask(ctx, t, person, agent, task, in)
}

// crewTaskRow is one task in a list: short, with the ids an agent needs to act on it.
func crewTaskRow(task crewTeamTask) map[string]any {
	row := map[string]any{"id": task.ID, "title": task.Title, "state": task.State, "at": task.At}
	for key, value := range map[string]string{"issue": task.Group, "feature": task.FeatureName, "parent": task.Parent, "category": task.Category, "agent": task.Agent, "person": task.Person, "asked_by": task.AskedBy, "last": task.Line, "proof": task.Proof, "inbox_id": task.inbox} {
		if value != "" {
			row[key] = value
		}
	}
	if len(task.Tags) > 0 {
		row["tags"] = task.Tags
	}
	return row
}

// crewTaskList filters the person's Team tasks. Every filter is optional and they combine.
func crewTaskList(tasks []crewTeamTask, person string, agent crewTeamRegisteredAgent, in crewWorkInput) (map[string]any, error) {
	state := strings.ToLower(strings.TrimSpace(in.State))
	switch state {
	case "", "active", "all", "open", "in_progress", "blocked", "needs_you", "stalled", "done":
	default:
		return nil, fmt.Errorf("state is one of active (not done, the default), all, open, in_progress, blocked, needs_you, stalled or done")
	}
	if state == "" {
		state = "active"
	}
	category := agentTaskLabel(in.Category)
	tag := strings.ReplaceAll(agentTaskLabel(in.Tag), " ", "-")
	issue := strings.TrimSpace(in.Issue)
	if issue != "" && !strings.HasPrefix(issue, "#") && !strings.Contains(issue, "/") && strings.Trim(issue, "0123456789") == "" {
		issue = "#" + issue
	}
	who := strings.ToLower(strings.TrimSpace(in.Who))
	feature := strings.TrimSpace(in.Feature)
	words := strings.Fields(strings.ToLower(in.Query))
	limit := in.Limit
	if limit <= 0 {
		limit = crewTaskListDefault
	}
	limit = min(limit, crewTaskListMax)
	counts := map[string]map[string]int{"state": {}, "category": {}, "tag": {}}
	out := []map[string]any{}
	total := 0
	for _, task := range tasks {
		switch {
		case state == "active" && task.State == "done", state != "active" && state != "all" && task.State != state:
			continue
		case category == "none" && task.Category != "", category != "" && category != "none" && task.Category != category:
			continue
		case tag != "" && !slices.Contains(task.Tags, tag):
			continue
		case issue != "" && !strings.EqualFold(task.Group, issue):
			continue
		case feature != "" && task.Feature != feature && !strings.EqualFold(task.FeatureName, feature):
			continue
		case in.Mine && !(agent.ID != "" && task.AgentKey == agent.ID || agent.ID == "" && agent.ChatKey != "" && !strings.HasPrefix(agent.ChatKey, crewWorkPersonPrefix) && task.agentChat == agent.ChatKey || agent.ID == "" && strings.HasPrefix(agent.ChatKey, crewWorkPersonPrefix) && task.Mine):
			continue
		case who != "" && !strings.Contains(strings.ToLower(task.Agent+" "+task.Person+" "+task.AskedBy), who):
			continue
		}
		text := strings.ToLower(task.Title + " " + task.Line + " " + task.GroupTitle)
		if slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text, w) }) {
			continue
		}
		total++
		counts["state"][task.State]++
		counts["category"][firstNonEmptyTrimmed(task.Category, "none")]++
		for _, t := range task.Tags {
			counts["tag"][t]++
		}
		if len(out) < limit {
			out = append(out, crewTaskRow(task))
		}
	}
	result := map[string]any{"tasks": out, "total": total, "counts": counts}
	if total > len(out) {
		result["more"] = fmt.Sprintf("%d more; narrow the filters or raise limit (up to %d)", total-len(out), crewTaskListMax)
	}
	return result, nil
}

// crewTaskDetail is one task with its thread: each step's status, decisions with why,
// files and notes. The thread text of two agents' own conversation shows in full only
// to their people; others see each step's first line, as the Tasks view does.
func crewTaskDetail(store agentMessageStore, task crewTeamTask, viewer string) map[string]any {
	out := crewTaskRow(task)
	if task.GroupTitle != "" {
		out["issue_title"] = task.GroupTitle
	}
	if len(task.Links) > 0 {
		out["links"] = task.Links
	}
	if len(task.Files) > 0 {
		out["files"] = task.Files
	}
	full := task.Bridge == nil || task.Bridge.CanOpen
	at := slices.IndexFunc(store.Conversations, func(c agentConversation) bool { return c.ID == task.inbox })
	if at < 0 {
		return out
	}
	c := store.Conversations[at]
	start := slices.IndexFunc(c.Messages, func(m agentMessage) bool { return m.ID == task.ID })
	if start < 0 {
		return out
	}
	request := c.Messages[start]
	history, decisions := []map[string]any{}, []map[string]any{}
	for _, m := range c.Messages[start:] {
		if m.Sequence > request.Sequence && m.MessageType == "request" && m.From == request.From {
			break
		}
		if m.Kind != "" {
			continue
		}
		text := agentTaskBody(m.Text)
		if full {
			text = clipRunes(strings.TrimSpace(text), 1200)
		} else {
			text = firstLineClip(text, 160)
		}
		step := map[string]any{"at": m.SentAt, "by": firstNonEmptyTrimmed(m.UpdatedBy, store.endpointName(c.Endpoints[m.From], viewer)), "text": text}
		if m.ReplyStatus != "" {
			step["status"] = agentReplyStatus(m.ReplyStatus)
		}
		if m.Category != "" {
			step["category"] = m.Category
		}
		if len(m.Tags) > 0 {
			step["tags"] = m.Tags
		}
		if m.File != nil {
			step["file"] = map[string]any{"id": m.File.ID, "name": m.File.Name}
		}
		if m.Decision {
			step["decision"] = true
			decisions = append(decisions, map[string]any{"at": m.SentAt, "by": step["by"], "decision": firstLineClip(text, 320), "why": m.Why})
		}
		history = append(history, step)
	}
	if len(history) > crewTaskHistoryMax {
		out["earlier_steps"] = len(history) - crewTaskHistoryMax
		history = history[len(history)-crewTaskHistoryMax:]
	}
	out["history"], out["decisions"] = history, decisions
	if len(task.Files) > 0 {
		out["files_hint"] = "Read a file with messages action=get_file, inbox_id and its file id, when you are in this thread."
	}
	return out
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// updateTeamTask writes one update into the task's thread. It goes on the caller's own
// side when the caller is in the thread, else on the side doing the task, and names
// who made it. A labels-only update is quiet; any other is delivered like a reply.
func (api *StreamingAPI) updateTeamTask(ctx context.Context, t crewTeam, person string, agent crewTeamRegisteredAgent, task crewTeamTask, in crewWorkInput) (map[string]any, error) {
	status := agentReplyStatus(strings.ToLower(strings.TrimSpace(in.Status)))
	if status != "" && (status == "open" || !validAgentReplyStatus(status)) {
		return nil, fmt.Errorf("status is in_progress, blocked, needs_you or done")
	}
	fields, err := agentTaskFieldsFromArgs(map[string]interface{}{"category": in.Category, "tags": anySlice(in.Tags), "decision": in.Decision, "why": in.Why})
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(in.Line)
	if utf8.RuneCountInString(line) > 2000 {
		return nil, fmt.Errorf("Keep the line under 2000 characters; attach longer notes as a file")
	}
	var file *agentMessageFile
	if name := strings.TrimSpace(in.FileName); name != "" || in.FileText != "" {
		if name == "" || strings.TrimSpace(in.FileText) == "" {
			return nil, fmt.Errorf("A note needs both file_name (for example plan.md) and file_text")
		}
		if len(in.FileText) > crewTaskNoteMax {
			return nil, fmt.Errorf("Keep the note under %d KB; put bigger documents in the Team folder and link them", crewTaskNoteMax>>10)
		}
		if file, err = prepareMessageFile(map[string]any{"file_name": name, "file_base64": base64.StdEncoding.EncodeToString([]byte(in.FileText))}); err != nil {
			return nil, err
		}
	}
	switch {
	case fields.Decision && line == "":
		return nil, fmt.Errorf("Give the decision in line, and the reason in why")
	case fields.Decision && fields.Why == "":
		return nil, fmt.Errorf("A decision needs why: one or two sentences on the reason")
	case fields.Decision && status != "":
		return nil, fmt.Errorf("Send the decision and the status change as two updates")
	case status == "" && fields.Category == "" && len(fields.Tags) == 0 && line == "" && file == nil && strings.TrimSpace(in.Feature) == "":
		return nil, fmt.Errorf("Give something to change: status, category, tags, feature, a line, a decision (line + why) or a note (file_name + file_text)")
	}
	var feature crewFeature
	if key := strings.TrimSpace(in.Feature); key != "" {
		if feature, err = crewFeatureResolve(t.ProjectID, key); err != nil {
			return nil, err
		}
	}
	labels := crewTeamPersonLabels(t)
	who := firstNonEmptyTrimmed(agent.Name, agent.Tab)
	if agent.ID == "" {
		who = labels[person]
	} else if labels[person] != "" {
		who += " (" + labels[person] + ")"
	}

	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	index := slices.IndexFunc(s.Conversations, func(c agentConversation) bool { return c.ID == task.inbox })
	if index < 0 {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("Task %s is no longer kept", task.ID)
	}
	c := &s.Conversations[index]
	at := slices.IndexFunc(c.Messages, func(m agentMessage) bool { return m.ID == task.ID && m.MessageType == "request" })
	if at < 0 {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("Task %s is no longer kept", task.ID)
	}
	request := c.Messages[at]
	side, role := -1, "team_owner"
	for i, e := range c.Endpoints {
		if row, ok := agentTeamAskerRow(t, e); ok && agent.ID != "" && row.ID == agent.ID {
			side, role = i, map[bool]string{true: "asker", false: "doer"}[i == request.From]
		}
	}
	if side < 0 && person != t.OwnerID {
		agentMessageMu.Unlock()
		log.Printf("[AGENT_TASK] inbox=%s request=%s event=task_update outcome=refused reason=not_in_task agent_id=%s caller_id=%s", c.ID, request.ID, agent.ID, person)
		return nil, fmt.Errorf("Only the agent that asked for this task, the agent doing it, or the Team owner's agents can change it. Message the agent doing it instead")
	}
	if side < 0 {
		side = 1 - request.From
		if request.SelfTaken {
			side = request.From
		}
	}
	now := time.Now().UTC()
	before, _ := agentTaskState(*c, request, now)
	// A line without a status keeps the task's state, so the note shows as its last step.
	if status == "" && line != "" && !fields.Decision && before != "open" && before != "stalled" {
		status = before
	}
	var parts []string
	if status != "" && status != before {
		parts = append(parts, "marked it "+strings.ReplaceAll(status, "_", " "))
	}
	if fields.Category != "" {
		parts = append(parts, "set the category to "+fields.Category)
	}
	if len(fields.Tags) > 0 {
		parts = append(parts, "set the tags to "+strings.Join(fields.Tags, ", "))
	}
	if feature.ID != "" {
		parts = append(parts, "put it in the feature "+feature.Name)
	}
	text := who + " updated this task"
	if len(parts) > 0 {
		text += ": " + strings.Join(parts, "; ")
	}
	text += "."
	if fields.Decision {
		text = "Decision by " + who + ": " + line + "\n\nWhy: " + fields.Why
	} else if line != "" {
		text += "\n\n" + line
	}
	c.Next++
	m := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: c.Next, From: side, SentAt: now, Submission: "task-update-" + uuid.NewString(), MessageType: "notification", ReplyStatus: status, TaskReplyTo: request.ID, Text: text, Category: fields.Category, Tags: fields.Tags, Decision: fields.Decision, Why: fields.Why, Feature: feature.ID, UpdatedBy: who, UpdatedByUser: person}
	if file != nil {
		ref, err := storeMessageFile(ctx, person, file)
		if err != nil {
			agentMessageMu.Unlock()
			return nil, err
		}
		m.File, m.Text = ref, ref.message(m.Text)
	}
	quiet := (status == "" || status == before) && line == "" && file == nil && !fields.Decision
	if quiet {
		m.ReplyStatus = ""
		m.Delivery, m.DeliveredAt = "delivered", now
	} else {
		agentTeamDeliveryFor(&s, c, &m)
	}
	c.Messages = append(c.Messages, m)
	conversation := *c
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	after, _ := agentTaskState(conversation, request, now)
	log.Printf("[AGENT_TASK] inbox=%s request=%s reply=%s event=task_update role=%s state=%s->%s category=%q tags=%q decision=%t file=%t quiet=%t agent_id=%s caller_id=%s", conversation.ID, request.ID, m.ID, role, before, after, fields.Category, strings.Join(fields.Tags, ","), fields.Decision, file != nil, quiet, agent.ID, person)
	agentTeamRequestChanged(t)
	if !quiet {
		api.kickAgentMessages(conversation.ID)
	}
	out := map[string]any{"updated": task.ID, "state": after, "message_id": m.ID}
	if m.File != nil {
		out["file_id"] = m.File.ID
	}
	return out, nil
}

func anySlice(items []string) []any {
	out := make([]any, 0, len(items))
	for _, s := range items {
		out = append(out, s)
	}
	return out
}

// crewTaskToolHelp and crewTaskProperties describe tasks, task and task_update on every
// team_sync surface (Crew chat, MCP, Claude Code and Codex chats).
const crewTaskToolHelp = " Manage tasks with tools: tasks lists the Team's tasks as Team › Tasks shows them, as compact rows with counts by state, category and tag; filters combine: state (active = not done, the default; all, open, in_progress, blocked, needs_you, stalled, done), category (none = not categorised yet), tag, issue (#2965), who (an agent or person name), mine=true, query (words in the title or last step), limit (default 30, up to 100). task (task = its id) reads one task: its steps, decisions with why, links, files and notes. task_update (task = its id) changes it: status (in_progress, blocked, needs_you, done), category and tags (tags replace the old ones), line (a short note; it shows as the task's last step), decision=true with line and why (a choice and the reason others should know), or a note file (file_name such as plan.md or why.md, file_text). The agent that asked for a task, the agent doing it and the Team owner's agents may update it; the update is kept in the task's thread under your name. Features group the agents and history of one piece of work, with or without a Team: features lists them; feature (feature = id or name) reads one: its open tasks, blocked ones and ones waiting on a person, the last finished task, decisions with why, its story and agents. feature_join (feature = a name; a new name starts one; optional link = its issue such as #2965, a ticket key or URL) adds you. feature_update (feature) sets rename (a new name), link, state=done or active, or leave=true; a done feature gives its link to the feature that takes it. A task belongs to a feature when it names it (feature on the send, or task_update feature) or when its issue is the feature's link; tasks takes feature as a filter."

func crewTaskProperties(props map[string]any) map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	props["task"] = str("task, task_update: the task's id from tasks.")
	props["line"] = str("task_update: a short note, or the decision with decision=true. finding: one short line.")
	props["why"] = str("task_update with decision=true: the reason, in one or two sentences.")
	props["query"] = str("tasks: words in the title or last step.")
	props["status"] = map[string]any{"type": "string", "enum": []any{"in_progress", "blocked", "needs_you", "done"}, "description": "task_update: the task's new state."}
	props["category"] = str("tasks: only this category (none = not categorised yet). task_update: the task's category, from your project's instructions.")
	props["tags"] = map[string]any{"type": "array", "maxItems": agentTaskTagsMax, "items": map[string]any{"type": "string", "maxLength": agentTaskTagMax}, "description": "task_update: up to 5 tags from your project's instructions; they replace the task's tags."}
	props["decision"] = map[string]any{"type": "boolean", "description": "task_update: line is a decision, with why; kept with the task and never changes its state."}
	props["file_name"] = str("task_update: a note to attach, such as plan.md or why.md (with file_text).")
	props["file_text"] = map[string]any{"type": "string", "maxLength": crewTaskNoteMax, "description": "task_update: the note's text, usually Markdown."}
	props["state"] = map[string]any{"type": "string", "enum": []any{"active", "all", "open", "in_progress", "blocked", "needs_you", "stalled", "done"}, "description": "tasks: which tasks (default active = not done). features: all lists done features too. feature_update: done or active."}
	props["tag"] = str("tasks: only tasks with this tag.")
	props["issue"] = str("tasks: only tasks under this issue, such as #2965.")
	props["who"] = str("tasks: only tasks an agent or person (by name) is doing or asked for.")
	props["mine"] = map[string]any{"type": "boolean", "description": "tasks: only the tasks you are doing."}
	props["feature"] = str("features: unused. feature, feature_join, feature_update: the feature's id or name (feature_join: a new name starts one). tasks: only tasks in this feature. task_update: put the task in this feature.")
	props["link"] = str("feature_join, feature_update: the feature's issue (#2965), ticket key or URL; tasks with that issue join it.")
	props["leave"] = map[string]any{"type": "boolean", "description": "feature_update: take yourself out of the feature."}
	props["rename"] = str("feature_update: the feature's new name.")
	props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": crewTaskListMax, "description": "tasks: how many rows (default 30)."}
	return props
}
