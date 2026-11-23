package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
)

// Project activity (#3476): a Crew Watcher whose target is "feed" saves what it
// found to its person's own feed instead of starting work, with no model turn.
// Each such Watcher is one row in that person's menu. The feed is personal even
// in a shared or Team Crew (owner, 21 Nov): it lives in the person's private
// watcher folder and only they see its rows.

const conditionFeedKeep = 20

// Items an agent is working on (moved past the first step, not at the last)
// stay past conditionFeedKeep, up to this many, so work never drops off a row.
const conditionFeedKeepWorking = 50

type conditionFeedItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Sub     string `json:"sub,omitempty"`
	URL     string `json:"url,omitempty"`
	At      int64  `json:"at"` // Unix seconds: when it happened (the event's own time, else when it was saved).
	SavedAt int64  `json:"saved_at"`
	Via     string `json:"via,omitempty"` // "browser": the check read the page in the person's Chrome because the app's connection failed.
	// #3625: the step the person's agent moved the item to ("" = the first
	// step), the AgentBridge tasks it became, and who moved it when.
	Step    string              `json:"step,omitempty"`
	Tasks   []conditionFeedTask `json:"tasks,omitempty"`
	History []conditionFeedMove `json:"history,omitempty"`
}

// conditionFeedTask names one AgentBridge task by its thread and request message.
type conditionFeedTask struct {
	Inbox   string `json:"inbox_id"`
	Message string `json:"message_id"`
}

type conditionFeedMove struct {
	At   int64  `json:"at"`
	By   string `json:"by,omitempty"`
	Step string `json:"step,omitempty"`
	Note string `json:"note,omitempty"`
}

type conditionFeed struct {
	Items []conditionFeedItem `json:"items"`
}

var conditionFeedLock sync.Mutex

func conditionFeedPath(job productScheduleJob) string {
	return path.Join(conditionCallerWorkspace(job.UserID, job.WatcherSource.ProfileID, job.WatcherSource.ProjectID), "feeds", job.Schedule.ID+".json")
}

func (c *conditionService) readFeed(ctx context.Context, job productScheduleJob) (conditionFeed, error) {
	var feed conditionFeed
	raw, found, err := c.s.readFile(ctx, conditionFeedPath(job))
	if err == nil && found {
		err = json.Unmarshal([]byte(raw), &feed)
	}
	return feed, err
}

func feedText(v interface{}, max int) string {
	s, _ := v.(string)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

func feedTime(v interface{}) int64 {
	switch t := v.(type) {
	case float64:
		if t > 1e12 { // milliseconds
			return int64(t / 1000)
		}
		return int64(t)
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

// feedItem reads the detector's payload {title, sub, url, at}. The text is
// untrusted source data; it is only shown to the Watcher's own person.
func feedItem(ev conditiontrigger.Event, now time.Time) (conditionFeedItem, error) {
	item := conditionFeedItem{ID: ev.ID, Title: feedText(ev.Payload["title"], 200), Sub: feedText(ev.Payload["sub"], 300), At: feedTime(ev.Payload["at"]), SavedAt: now.Unix()}
	if item.Title == "" {
		return item, conditiontrigger.PermanentError{Err: errors.New("a feed event needs a title in its payload")}
	}
	if link, _ := ev.Payload["url"].(string); link != "" {
		if u, err := url.Parse(link); err == nil && u.Scheme == "https" && u.Host != "" && len(link) <= 2048 {
			item.URL = link
		}
	}
	if via, _ := ev.Payload["via"].(string); via == "browser" {
		item.Via = via
	}
	if item.At <= 0 || item.At > now.Unix()+60 {
		item.At = item.SavedAt
	}
	return item, nil
}

func (c *conditionService) saveFeedEvent(ctx context.Context, job productScheduleJob, ev conditiontrigger.Event) (string, error) {
	if job.WatcherSource == nil {
		return "", conditiontrigger.PermanentError{Err: errors.New("feed Watchers belong to a Crew")}
	}
	item, err := feedItem(ev, time.Now())
	if err != nil {
		return "", err
	}
	conditionFeedLock.Lock()
	defer conditionFeedLock.Unlock()
	feed, err := c.readFeed(ctx, job)
	if err != nil {
		return "", err
	}
	kept := []conditionFeedItem{item}
	for _, old := range feed.Items {
		if old.ID != item.ID {
			kept = append(kept, old)
		} else { // A retried dispatch or a changed source item keeps its step and tasks.
			kept[0].Step, kept[0].Tasks, kept[0].History = old.Step, old.Tasks, old.History
		}
	}
	feed.Items = conditionFeedTrim(kept, job.Schedule.Condition.Target.FeedSteps())
	if err := c.writeFeed(ctx, job, feed); err != nil {
		return "", err
	}
	return "saved to Project activity", nil
}

func (c *conditionService) writeFeed(ctx context.Context, job productScheduleJob, feed conditionFeed) error {
	b, err := json.Marshal(conditionFeed{Items: feed.Items})
	if err != nil {
		return err
	}
	return c.s.writeFile(ctx, conditionFeedPath(job), string(b))
}

// conditionFeedStep is the item's step, matched to the row's own spelling;
// an empty or removed step reads as the first one.
func conditionFeedStep(item conditionFeedItem, steps []string) string {
	for _, step := range steps {
		if strings.EqualFold(step, item.Step) {
			return step
		}
	}
	return steps[0]
}

func conditionFeedWorking(item conditionFeedItem, steps []string) bool {
	step := conditionFeedStep(item, steps)
	return step != steps[len(steps)-1] && (step != steps[0] || len(item.Tasks) > 0)
}

// conditionFeedTrim keeps the newest items, and every item being worked on.
func conditionFeedTrim(items []conditionFeedItem, steps []string) []conditionFeedItem {
	sort.SliceStable(items, func(i, j int) bool { return items[i].At > items[j].At })
	out := []conditionFeedItem{}
	working, other := 0, 0
	for _, item := range items {
		if conditionFeedWorking(item, steps) {
			if working < conditionFeedKeepWorking {
				out = append(out, item)
				working++
			}
		} else if other < conditionFeedKeep {
			out = append(out, item)
			other++
		}
	}
	return out
}

// activityRows are the caller's own feed Watchers for this Crew, oldest first,
// read from local files only (no outside calls on a menu read).
func (c *conditionService) activityRows(ctx context.Context, owner, project string) ([]crewPanelRow, error) {
	base, err := c.source(ctx, owner, crewProfileID, project)
	if err != nil {
		return nil, err
	}
	jobs, err := c.summaryJobs(ctx, base)
	if err != nil {
		return nil, err
	}
	var feeds []productScheduleJob
	for _, job := range jobs {
		cfg := job.Schedule.Condition
		if job.WatcherSource != nil && job.Profile.ID == base.Profile.ID && job.ProjectID == base.ProjectID && cfg != nil && cfg.Target.Kind == "feed" {
			feeds = append(feeds, job)
		}
	}
	sort.SliceStable(feeds, func(i, j int) bool { return feeds[i].Schedule.CreatedAt < feeds[j].Schedule.CreatedAt })
	day := time.Now().Add(-24 * time.Hour).Unix()
	tasks := conditionFeedTaskIndex(ctx, owner)
	rows := []crewPanelRow{}
	for _, job := range feeds {
		cfg := job.Schedule.Condition
		steps := cfg.Target.FeedSteps()
		row := crewPanelRow{ID: "feed:" + job.Schedule.ID, Label: job.Schedule.Name, Group: cfg.Target.App, Path: cfg.Target.Link, Destination: "schedules:conditions", Steps: steps, Rules: cfg.Target.Rules}
		feed, err := c.readFeed(ctx, job)
		if err != nil {
			return nil, err
		}
		recent, open, done := 0, 0, 0
		for _, item := range feed.Items {
			if item.At >= day {
				recent++
			}
			view := conditionFeedPanelItem(item, steps, tasks)
			if view.Done {
				row.DoneCount++
				if done >= crewPanelFeedDoneShown {
					continue
				}
				done++
			} else {
				if open >= crewPanelFeedOpenShown {
					continue
				}
				open++
			}
			row.Items = append(row.Items, view)
		}
		if recent > 0 {
			row.Count = strconv.Itoa(recent) + " new today"
		}
		switch {
		case !job.Effective().Enabled:
			row.Detail, row.Attention = "Paused", true
		case job.State.LastStatus == "error":
			row.Detail, row.Attention = "Last check failed", true
		case job.State.LastRunAt == "" && len(feed.Items) == 0:
			row.Detail = "Checking for the first time"
		case len(feed.Items) == 0:
			row.Detail = "Nothing new yet"
		case feed.Items[0].Via == "browser":
			row.Detail, row.Attention = "Read from your browser: reconnect the app", true
		default:
			row.Detail = feed.Items[0].Title
		}
		rows = append(rows, row)
	}
	return rows, nil
}
