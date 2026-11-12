package server

import (
	"slices"
	"strings"
	"time"
)

// A feed item is projected only from already-admitted posts and permission-filtered
// messages. Hidden question/answer text never enters the title, verb or line.
type crewTeamFeedItem struct {
	ID        string                   `json:"id"`
	Kind      string                   `json:"kind"`
	At        time.Time                `json:"at"`
	Person    string                   `json:"person"`
	Title     string                   `json:"title"`
	Verb      string                   `json:"verb"`
	Line      string                   `json:"line,omitempty"`
	Why       string                   `json:"why,omitempty"`
	State     string                   `json:"state,omitempty"`
	StateLine string                   `json:"state_line,omitempty"`
	Message   *crewTeamActivityMessage `json:"message,omitempty"`
}

func crewTeamActivityFeed(view crewTeamActivityView, caller string) []crewTeamFeedItem {
	out := []crewTeamFeedItem{}
	for _, group := range []struct {
		kind  string
		posts []crewTeamActivityPost
	}{{"decision", view.Decisions}, {"finding", view.Findings}} {
		for _, post := range group.posts {
			title, verb := post.Person+"'s "+post.Agent, "decided"
			if post.OwnerID == caller {
				title = "Your " + post.Agent
			}
			if group.kind == "finding" {
				title, verb = post.Schedule, "finding"
			}
			out = append(out, crewTeamFeedItem{ID: group.kind + ":" + post.ID, Kind: group.kind, At: post.At, Person: post.Person, Title: title, Verb: verb, Line: post.Line, Why: post.Why})
		}
	}
	for _, message := range view.Messages {
		target := message.To.Label
		if message.To.You {
			target = "your " + message.To.Agent
		}
		stateLine := message.StatusLine
		if message.Status == "waiting" || message.Status == "no_answer" {
			stateLine = "No answer yet"
		}
		out = append(out, crewTeamFeedItem{ID: "question:" + message.ID, Kind: "question", At: message.SentAt, Person: message.From.Person, Title: message.From.Label, Verb: "asked " + target, Line: message.Topic, State: message.Status, StateLine: stateLine, Message: &message})
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
