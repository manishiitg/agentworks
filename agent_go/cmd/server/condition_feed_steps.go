package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Project activity steps (#3625, approved mockup 23 Nov): each feed row has the
// person's own steps (New → Fixing → Tested → Done). Their agent links an item to
// the AgentBridge tasks it became and moves it from step to step, following the
// person's rules; the server only checks the step is on the row's list, keeps
// who moved it when, and reads each task's state from its own thread.

const (
	crewPanelFeedOpenShown = 8
	crewPanelFeedDoneShown = 5
	conditionFeedTasksMax  = 10
	conditionFeedMovesMax  = 30
)

// crewPanelFeedItem is one line under a Project activity row.
type crewPanelFeedItem struct {
	ID      string              `json:"id,omitempty"`
	Title   string              `json:"title"`
	Sub     string              `json:"sub,omitempty"`
	URL     string              `json:"url,omitempty"`
	At      int64               `json:"at"`
	Step    string              `json:"step,omitempty"`
	Done    bool                `json:"done,omitempty"`
	Tasks   []crewPanelFeedTask `json:"tasks,omitempty"`
	History []conditionFeedMove `json:"history,omitempty"`
}

// crewPanelFeedTask is a linked task as its thread has it now.
type crewPanelFeedTask struct {
	Agent string `json:"agent"`
	Title string `json:"title"`
	State string `json:"state"` // open, in_progress, blocked, needs_you, stalled, done
	// CLI is the coding program the agent runs on (Claude Code, Codex...), from
	// its Team record, so the panel can show its logo (#4027).
	CLI string `json:"cli,omitempty"`
}

// conditionFeedTasks reads the person's own AgentBridge threads at most once,
// and only when an item links a task.
type conditionFeedTasks struct {
	ctx    context.Context
	owner  string
	loaded bool
	convs  map[string]agentConversation
	// cli names a Team agent's coding program by its Team agent id; nil skips it.
	cli func(teamAgentID string) string
}

func conditionFeedTaskIndex(ctx context.Context, owner string) *conditionFeedTasks {
	return &conditionFeedTasks{ctx: ctx, owner: owner}
}

func (t *conditionFeedTasks) find(link conditionFeedTask) (agentConversation, agentMessage, bool) {
	if !t.loaded {
		t.loaded, t.convs = true, map[string]agentConversation{}
		if store, err := readAgentMessageSnapshot(t.ctx); err == nil {
			for _, c := range store.Conversations {
				if c.Endpoints[0].UserID == t.owner || c.Endpoints[1].UserID == t.owner {
					t.convs[c.ID] = c
				}
			}
		}
	}
	c, ok := t.convs[link.Inbox]
	if !ok {
		return c, agentMessage{}, false
	}
	for _, m := range c.Messages {
		if m.ID == link.Message && m.MessageType == "request" && m.Kind == "" {
			return c, m, true
		}
	}
	return c, agentMessage{}, false
}

func (t *conditionFeedTasks) view(link conditionFeedTask) (crewPanelFeedTask, bool) {
	c, m, ok := t.find(link)
	if !ok {
		return crewPanelFeedTask{}, false
	}
	state, _ := agentTaskState(c, m, time.Now())
	if state == "" {
		state = "open"
	}
	to := c.Endpoints[1-m.From]
	view := crewPanelFeedTask{Agent: firstNonEmptyTrimmed(to.Label, "an agent"), Title: agentTaskStallTitle(m), State: state}
	if t.cli != nil && to.TeamAgentID != "" {
		view.CLI = t.cli(to.TeamAgentID)
	}
	return view, true
}

func conditionFeedPanelItem(item conditionFeedItem, steps []string, tasks *conditionFeedTasks) crewPanelFeedItem {
	step := conditionFeedStep(item, steps)
	view := crewPanelFeedItem{ID: item.ID, Title: item.Title, Sub: item.Sub, URL: item.URL, At: item.At, Step: step, Done: step == steps[len(steps)-1]}
	for _, link := range item.Tasks {
		if task, ok := tasks.view(link); ok {
			view.Tasks = append(view.Tasks, task)
		}
	}
	view.History = item.History
	if len(view.History) > 10 {
		view.History = view.History[len(view.History)-10:]
	}
	return view
}

// feedJob is one of the caller's own feed Watchers in this Crew: Project
// activity is personal even in a shared Crew, so the Crew owner's rows are not
// reachable by a teammate and the other way round.
func (c *conditionService) feedJob(ctx context.Context, actor, project, id string) (productScheduleJob, error) {
	base, err := c.source(ctx, actor, crewProfileID, project)
	if err != nil {
		return productScheduleJob{}, err
	}
	jobs, err := c.summaryJobs(ctx, base)
	if err != nil {
		return productScheduleJob{}, err
	}
	for _, job := range jobs {
		cfg := job.Schedule.Condition
		if job.Schedule.ID == id && job.WatcherSource != nil && job.Profile.ID == base.Profile.ID && job.ProjectID == base.ProjectID && cfg != nil && cfg.Target.Kind == "feed" {
			return job, nil
		}
	}
	return productScheduleJob{}, errors.New("Project activity row not found among your own rows; list watchers and use the id of a feed Watcher")
}

func (c *conditionService) feedItemsAction(ctx context.Context, actor, profile, project, action string, args map[string]interface{}) (interface{}, error) {
	if profile != crewProfileID {
		return nil, errors.New("Project activity is only in a project")
	}
	id, _ := args["id"].(string)
	job, err := c.feedJob(ctx, actor, project, strings.TrimPrefix(id, "feed:"))
	if err != nil {
		return nil, err
	}
	steps := job.Schedule.Condition.Target.FeedSteps()
	tasks := conditionFeedTaskIndex(ctx, actor)
	if action == "items" {
		feed, err := c.readFeed(ctx, job)
		if err != nil {
			return nil, err
		}
		items := []crewPanelFeedItem{}
		for _, item := range feed.Items {
			items = append(items, conditionFeedPanelItem(item, steps, tasks))
		}
		return map[string]interface{}{"steps": steps, "rules": job.Schedule.Condition.Target.Rules, "items": items}, nil
	}

	itemID, _ := args["item_id"].(string)
	stepArg, _ := args["step"].(string)
	note := feedText(args["note"], 300)
	var link *conditionFeedTask
	if raw, ok := args["task"].(map[string]interface{}); ok {
		inbox, _ := raw["inbox_id"].(string)
		message, _ := raw["message_id"].(string)
		if inbox == "" || message == "" {
			return nil, errors.New("task needs inbox_id and message_id of the ask you sent")
		}
		link = &conditionFeedTask{Inbox: inbox, Message: message}
	}
	if itemID == "" || (strings.TrimSpace(stepArg) == "" && link == nil) {
		return nil, errors.New("item_update needs item_id and a step, a task, or both")
	}
	step := ""
	if strings.TrimSpace(stepArg) != "" {
		for _, s := range steps {
			if strings.EqualFold(s, strings.TrimSpace(stepArg)) {
				step = s
			}
		}
		if step == "" {
			return nil, fmt.Errorf("%q isn't one of this row's steps (%s), so the item stays where it is. Use one of them, or ask your person to add the step", stepArg, strings.Join(steps, " → "))
		}
	}
	by := ""
	if link != nil {
		c, m, ok := tasks.find(*link)
		if !ok {
			return nil, errors.New("that task isn't in your inboxes; link the ask you sent (its inbox_id and message_id)")
		}
		by = c.Endpoints[m.From].Label
	}

	conditionFeedLock.Lock()
	defer conditionFeedLock.Unlock()
	feed, err := c.readFeed(ctx, job)
	if err != nil {
		return nil, err
	}
	at := -1
	for i, item := range feed.Items {
		if item.ID == itemID {
			at = i
		}
	}
	if at < 0 {
		return nil, errors.New("item not found in this row; read it with action=items")
	}
	item := &feed.Items[at]
	if link != nil {
		known := false
		for _, t := range item.Tasks {
			known = known || t == *link
		}
		if !known {
			if len(item.Tasks) >= conditionFeedTasksMax {
				return nil, fmt.Errorf("an item links at most %d tasks", conditionFeedTasksMax)
			}
			item.Tasks = append(item.Tasks, *link)
		}
	}
	if by == "" && len(item.Tasks) > 0 {
		if c, m, ok := tasks.find(item.Tasks[len(item.Tasks)-1]); ok {
			by = c.Endpoints[m.From].Label
		}
	}
	by = firstNonEmptyTrimmed(by, "Your agent")
	before := conditionFeedStep(*item, steps)
	if step != "" {
		item.Step = step
	}
	if step != "" && step != before || note != "" {
		item.History = append(item.History, conditionFeedMove{At: time.Now().Unix(), By: by, Step: step, Note: note})
		if len(item.History) > conditionFeedMovesMax {
			item.History = item.History[len(item.History)-conditionFeedMovesMax:]
		}
	}
	saved := *item
	feed.Items = conditionFeedTrim(feed.Items, steps)
	if err := c.writeFeed(ctx, job, feed); err != nil {
		return nil, err
	}
	log.Printf("[PROJECT_ACTIVITY] row=%s item_step=%q tasks=%d", job.Schedule.ID, conditionFeedStep(saved, steps), len(saved.Tasks))
	return map[string]interface{}{"steps": steps, "item": conditionFeedPanelItem(saved, steps, tasks)}, nil
}
