package server

import (
	"slices"
	"strings"
	"time"
)

// A feed item is projected only from already-admitted posts and permission-filtered
// bridge messages. Personal transcript text is never a source.
type crewTeamFeedItem struct {
	// Source identities are server-only; never infer authorship from a display name.
	ActorID  string `json:"-"`
	NeedsFor string `json:"-"`
	userAvatarFields
	Private   bool                 `json:"private,omitempty"`
	ID        string               `json:"id"`
	Kind      string               `json:"kind"`
	At        time.Time            `json:"at"`
	Person    string               `json:"person"`
	Title     string               `json:"title"`
	Verb      string               `json:"verb"`
	Line      string               `json:"line,omitempty"`
	Why       string               `json:"why,omitempty"`
	State     string               `json:"state,omitempty"`
	StateLine string               `json:"state_line,omitempty"`
	Bridge    *crewTeamBridgeEvent `json:"bridge,omitempty"`
	// Task groups items about one work-log task: its label, title and state ("started", "3 of 5 done").
	Task      string `json:"task,omitempty"`
	TaskTitle string `json:"task_title,omitempty"`
	TaskState string `json:"task_state,omitempty"`
	Today     bool   `json:"today,omitempty"`
	Unseen    bool   `json:"unseen,omitempty"`
	CLI       string `json:"cli,omitempty"`
}

func crewTeamActivityFeed(view crewTeamActivityView, caller string) []crewTeamFeedItem {
	out := []crewTeamFeedItem{}
	for _, group := range []struct {
		kind  string
		posts []crewTeamActivityPost
	}{{"decision", view.Decisions}, {"finding", view.Findings}, {"task", view.Work}} {
		for _, post := range group.posts {
			if post.Private {
				continue
			}
			title, verb := post.Person+"'s "+post.Agent, "decided"
			if post.OwnerID == caller {
				title = "Your " + post.Agent
			}
			if post.AgentID == "" && strings.HasPrefix(post.ChatKey, crewWorkPersonPrefix) {
				title = post.Person // a person's own entry, such as answering a Needs-you card
				if post.OwnerID == caller {
					title = "You"
				}
			}
			if group.kind == "finding" {
				title, verb = post.Schedule, "finding"
			}
			line := post.Line
			if post.Task != "" {
				line = strings.TrimPrefix(line, post.Task+": ")
			}
			if group.kind == "task" {
				verb = map[string]string{"forked": "forked", "added": "added", "started": "started", "done": "finished", "needs": "needs", "answered": "answered", "withdrawn": "withdrew a request"}[post.Event]
			}
			out = append(out, crewTeamFeedItem{userAvatarFields: avatarFieldsFor(post.OwnerID, ""), Private: post.Private, ActorID: post.OwnerID, NeedsFor: post.NeedsFor, ID: group.kind + ":" + post.ID, Kind: group.kind, At: post.At, Person: post.Person, Title: title, Verb: verb, Line: line, Why: post.Why, Task: post.Task, TaskTitle: post.TaskTitle, TaskState: post.TaskState, CLI: post.CLI})
		}
	}
	out = append(out, view.BridgeMessages...)
	for _, item := range view.OnlineItems {
		at, err := time.Parse(time.RFC3339, item.UpdatedAt)
		if err != nil {
			continue
		}
		verb := "added to Team"
		if item.UpdatedAt != item.AddedAt {
			verb = "updated on Team"
		}
		out = append(out, crewTeamFeedItem{userAvatarFields: avatarFieldsFor(item.UpdatedBy, ""), ActorID: item.UpdatedBy, ID: "online:" + item.DestinationItemID + ":" + item.UpdatedAt, Kind: "decision", At: at, Person: teamOnlinePersonName(item.UpdatedBy), Title: teamOnlinePersonName(item.UpdatedBy), Verb: verb, Line: item.Name, Why: "Runs in the Team's online home."})
	}
	slices.SortFunc(out, func(a, b crewTeamFeedItem) int {
		if order := b.At.Compare(a.At); order != 0 {
			return order
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out
}

// The cursor is the last item identity, not an offset: new posts cannot shift an
// older page. All three kinds share the same page size and ordering.
func crewTeamFeedPage(rows []crewTeamFeedItem, before string) ([]crewTeamFeedItem, string) {
	if before != "" {
		i := slices.IndexFunc(rows, func(row crewTeamFeedItem) bool { return row.ID == before })
		if i < 0 {
			return []crewTeamFeedItem{}, ""
		}
		rows = rows[i+1:]
	}
	if len(rows) > crewTeamActivityMessages {
		return rows[:crewTeamActivityMessages], rows[crewTeamActivityMessages-1].ID
	}
	return rows, ""
}
