package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// An ask between the Team's agents, or a request to a person, read from its
// AgentBridge thread. It feeds an agent's session-start brief (the asks it has not
// finished) and lets a Needs-you card open its files. Tracking work is the
// project's own issue tracker (owner, 19 Nov), so nothing is listed as tasks.
type crewTeamTask struct {
	ID         string               `json:"id"`
	Parent     string               `json:"parent,omitempty"`
	Group      string               `json:"group,omitempty"`
	GroupTitle string               `json:"group_title,omitempty"`
	Title      string               `json:"title"`
	State      string               `json:"state"` // open, in_progress, blocked, needs_you, stalled, done
	Agent      string               `json:"agent,omitempty"`
	AgentKey   string               `json:"agent_key,omitempty"`
	Person     string               `json:"person,omitempty"`
	Mine       bool                 `json:"mine,omitempty"`
	ForMe      bool                 `json:"for_me,omitempty"`
	CLI        string               `json:"cli,omitempty"`
	AskedBy    string               `json:"asked_by,omitempty"`
	Line       string               `json:"line,omitempty"`
	Proof      string               `json:"proof,omitempty"`
	Links      []string             `json:"links,omitempty"`
	Files      []crewTeamTaskFile   `json:"files,omitempty"`
	At         time.Time            `json:"at"`
	Bridge     *crewTeamBridgeEvent `json:"bridge,omitempty"`
	inbox      string
}

// crewTeamTaskFile is a file sent in the task's thread (one per message, AgentBridge file_path).
// Whoever sees the task can open it through the Activity route by task ID.
type crewTeamTaskFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int    `json:"size"`
}

const (
	crewTeamTasksKept     = 300
	crewTeamTasksDoneDays = 14
)

func crewTeamTasks(ctx context.Context, t crewTeam, viewer string, store agentMessageStore) []crewTeamTask {
	out := []crewTeamTask{}
	if !t.On || !t.member(viewer) {
		return crewTeamTaskTree(out)
	}
	labels := crewTeamPersonLabels(t)
	visible := crewWorkVisible(t, viewer)
	now := time.Now().UTC()
	transport := map[string]bool{}
	who := func(task *crewTeamTask, row crewTeamRegisteredAgent) {
		task.Agent, task.AgentKey, task.Person, task.Mine = firstNonEmptyTrimmed(row.Name, row.Tab), row.ID, labels[row.OwnerID], row.OwnerID == viewer
		if row.Terminal != nil {
			task.CLI = providerAccountProductName(row.Terminal.Provider)
		}
	}
	asked := func(row crewTeamRegisteredAgent) string {
		if row.OwnerID == viewer {
			return "your " + firstNonEmptyTrimmed(row.Name, row.Tab)
		}
		return labels[row.OwnerID] + "'s " + firstNonEmptyTrimmed(row.Name, row.Tab)
	}
	for _, c := range store.Conversations {
		if len(c.Messages) == 0 {
			continue
		}
		if c.Messages[0].TeamRequest {
			request := c.Messages[0]
			asker, ok := agentTeamAskerRow(t, c.Endpoints[0])
			dest := c.Endpoints[1]
			if !ok || !visible(!asker.InTeam, asker.ID, asker.OwnerID) || (agentTeamEndpoint(dest) && dest.ID != t.ProjectID) {
				continue
			}
			task := crewTeamRequestTask(c, request, now)
			task.AskedBy, task.inbox = asked(asker), c.ID
			switch {
			case dest.Kind == agentTeamPersonKind:
				task.Person, task.ForMe = labels[dest.UserID], dest.UserID == viewer
				if task.State == "open" {
					task.State = "needs_you"
				}
			default:
				row, sameTeam := agentTeamAskerRow(t, dest)
				if !sameTeam {
					continue
				}
				who(&task, row)
			}
			out = append(out, task)
			continue
		}
		for _, m := range c.Messages {
			if m.MessageType != "request" {
				continue
			}
			rows, ok := crewTeamActivityBridgeRows(ctx, t, viewer, c, m, transport)
			if !ok {
				continue
			}
			task := crewTeamRequestTask(c, m, now)
			task.AskedBy, task.inbox = asked(rows[m.From]), c.ID
			who(&task, rows[1-m.From])
			task.Bridge = &crewTeamBridgeEvent{ThreadID: c.ID, CanOpen: viewer == rows[0].OwnerID || viewer == rows[1].OwnerID}
			out = append(out, task)
		}
	}
	return crewTeamTaskTree(out)
}

// crewTeamRequestTask is a request's title, link, state, last step and proof.
func crewTeamRequestTask(c agentConversation, request agentMessage, now time.Time) crewTeamTask {
	state, _ := agentTaskState(c, request, now)
	task := crewTeamTask{ID: request.ID, Title: firstNonEmptyTrimmed(request.Title, firstLineClip(agentTaskBody(request.Text), agentTaskShortTitle)), State: state, At: request.SentAt}
	if strings.HasPrefix(request.Ref, "msg-") {
		task.Parent = request.Ref
	} else {
		task.Group = request.Ref
	}
	task.Title = agentTaskTitleWithoutRef(task.Title, task.Group)
	task.Links, task.Files = agentTaskLinksAndFiles(c, request)
	if latest, ok := agentTaskLatestReply(c, request); ok {
		body := agentTaskBody(latest.Text)
		task.At, task.Line = latest.SentAt, firstLineClip(body, 160)
		if state == "done" {
			task.Proof = agentTaskProof(body)
		}
	}
	return task
}

// agentTaskLinksAndFiles are the web links named in the task (its files list and ref)
// and the files sent in its thread, from the request until the next request.
func agentTaskLinksAndFiles(c agentConversation, request agentMessage) ([]string, []crewTeamTaskFile) {
	links := []string{}
	for _, l := range append(slices.Clone(request.Files), request.Ref) {
		if (strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")) && !slices.Contains(links, l) {
			links = append(links, l)
		}
	}
	files := []crewTeamTaskFile{}
	for _, m := range c.Messages {
		if m.Sequence < request.Sequence {
			continue
		}
		if m.Sequence > request.Sequence && m.MessageType == "request" && m.From == request.From {
			break
		}
		if m.File != nil {
			files = append(files, crewTeamTaskFile{ID: m.File.ID, Name: m.File.Name, Size: m.File.Size})
		}
	}
	if len(links) == 0 {
		links = nil
	}
	if len(files) == 0 {
		files = nil
	}
	return links, files
}

// crewTeamTaskFileFor returns a file of a task this viewer sees: the file must be sent in that
// task's thread by the account that stored it, and still kept.
func crewTeamTaskFileFor(ctx context.Context, tasks []crewTeamTask, taskID, fileID string) (agentMessageFile, int, string) {
	if !agentMessageFileIDPattern.MatchString(fileID) {
		return agentMessageFile{}, 400, "Choose a file from the task."
	}
	var inbox string
	for _, task := range tasks {
		if task.ID == taskID && slices.ContainsFunc(task.Files, func(f crewTeamTaskFile) bool { return f.ID == fileID }) {
			inbox = task.inbox
		}
	}
	if inbox == "" {
		return agentMessageFile{}, 404, "This file is not in a task you can see."
	}
	raw, exists, err := readFileFromWorkspace(ctx, agentMessageFilesDir+fileID+".json")
	var rec agentMessageFile
	if err != nil || !exists || json.Unmarshal([]byte(raw), &rec) != nil {
		return rec, 404, "This file is no longer available."
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return rec, 500, "Could not read the task."
	}
	sent := false
	for _, c := range store.Conversations {
		if c.ID == inbox {
			sent = agentMessageFileSentBy(c, fileID, rec.UserID)
			break
		}
	}
	if !sent {
		return rec, 404, "This file is not in a task you can see."
	}
	if time.Since(rec.StoredAt) > agentMessageFileKeep {
		return rec, 410, "This file was removed after 7 days. Ask the agent to send it again."
	}
	return rec, 200, ""
}

// agentTaskBody drops the task header (Task:, Ref:, Files:) the send put in front of the text.
func agentTaskBody(text string) string {
	head, body, ok := strings.Cut(text, "\n\n")
	if !ok {
		return text
	}
	for _, line := range strings.Split(head, "\n") {
		if !strings.HasPrefix(line, "Task: ") && !strings.HasPrefix(line, "Ref: ") && !strings.HasPrefix(line, "Files: ") {
			return text
		}
	}
	return body
}

// crewTeamTaskTree keeps open tasks and recently finished ones, drops links to
// parents this viewer can't see, gives sub-tasks their parent's group, and names
// each group after its oldest task.
func crewTeamTaskTree(tasks []crewTeamTask) []crewTeamTask {
	cutoff := time.Now().UTC().AddDate(0, 0, -crewTeamTasksDoneDays)
	tasks = slices.DeleteFunc(tasks, func(task crewTeamTask) bool { return task.State == "done" && task.At.Before(cutoff) })
	slices.SortFunc(tasks, func(a, b crewTeamTask) int { return b.At.Compare(a.At) })
	tasks = tasks[:min(len(tasks), crewTeamTasksKept)]
	byID := map[string]int{}
	for i, task := range tasks {
		byID[task.ID] = i
	}
	for i := range tasks {
		if _, ok := byID[tasks[i].Parent]; !ok {
			tasks[i].Parent = ""
		}
	}
	for i := range tasks {
		at := i
		for depth := 0; depth < 20 && tasks[at].Group == "" && tasks[at].Parent != ""; depth++ {
			at = byID[tasks[at].Parent]
		}
		if tasks[i].Group == "" {
			tasks[i].Group = tasks[at].Group
		}
	}
	titles := map[string]crewTeamTask{}
	for _, task := range tasks {
		if first, ok := titles[task.Group]; task.Group != "" && task.Parent == "" && (!ok || task.At.Before(first.At)) {
			titles[task.Group] = task
		}
	}
	for i := range tasks {
		tasks[i].GroupTitle = titles[tasks[i].Group].Title
	}
	return tasks
}

// crewPersonalTasks are the asks one person's agents send each other in a Crew whose Team is off.
func crewPersonalTasks(ctx context.Context, project, viewer string) []crewTeamTask {
	out := []crewTeamTask{}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil || project == "" || viewer == "" {
		return crewTeamTaskTree(out)
	}
	now := time.Now().UTC()
	own := func(e agentMessageEndpoint) bool {
		return e.UserID == viewer && !e.External && e.Guest == "" && e.ShareID == ""
	}
	inCrew := func(e agentMessageEndpoint) bool { return e.Kind == triggerCallerCrew && e.ID == project }
	key := func(e agentMessageEndpoint) string { return firstNonEmptyTrimmed(e.ChatKey, e.NamedAgentID, e.Label) }
	for _, c := range store.Conversations {
		if len(c.Messages) == 0 || !own(c.Endpoints[0]) {
			continue
		}
		if !own(c.Endpoints[1]) || agentTeamEndpoint(c.Endpoints[1]) || !inCrew(c.Endpoints[0]) && !inCrew(c.Endpoints[1]) {
			continue
		}
		for _, m := range c.Messages {
			if m.MessageType != "request" || m.Kind != "" || m.From < 0 || m.From > 1 {
				continue
			}
			doer := c.Endpoints[1-m.From]
			task := crewTeamRequestTask(c, m, now)
			task.Agent, task.AgentKey, task.Mine, task.AskedBy, task.inbox = doer.Label, key(doer), true, "your "+c.Endpoints[m.From].Label, c.ID
			out = append(out, task)
		}
	}
	return crewTeamTaskTree(out)
}

// crewTasksBrief is an agent's own unfinished request tasks for its session-start brief.
func crewTasksBrief(tasks []crewTeamTask, mine func(crewTeamTask) bool) []map[string]any {
	out := []map[string]any{}
	for _, task := range tasks {
		if task.State == "done" || task.inbox == "" || !mine(task) || len(out) >= 20 {
			continue
		}
		item := map[string]any{"title": task.Title, "state": task.State, "inbox_id": task.inbox, "message_id": task.ID}
		if task.Group != "" {
			item["ref"] = task.Group
		}
		if task.Line != "" {
			item["last"] = task.Line
		}
		out = append(out, item)
	}
	return out
}
