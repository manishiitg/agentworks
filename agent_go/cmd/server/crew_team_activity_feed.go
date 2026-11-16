package server

import (
	"slices"
	"strings"
	"time"
)

// A feed item is projected only from already-admitted posts and permission-filtered
// bridge messages. Personal transcript text is never a source.
type crewTeamFeedItem struct {
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
	Today     bool                 `json:"today,omitempty"`
	Unseen    bool                 `json:"unseen,omitempty"`
}

func crewTeamActivityFeed(view crewTeamActivityView, caller string) []crewTeamFeedItem {
	out := []crewTeamFeedItem{}
	for _, group := range []struct {
		kind  string
		posts []crewTeamActivityPost
	}{{"decision", view.Decisions}, {"finding", view.Findings}} {
		for _, post := range group.posts {
			if post.Private {
				continue
			}
			title, verb := post.Person+"'s "+post.Agent, "decided"
			if post.OwnerID == caller {
				title = "Your " + post.Agent
			}
			if group.kind == "finding" {
				title, verb = post.Schedule, "finding"
			}
			out = append(out, crewTeamFeedItem{userAvatarFields: avatarFieldsFor(post.OwnerID, ""), Private: post.Private, ID: group.kind + ":" + post.ID, Kind: group.kind, At: post.At, Person: post.Person, Title: title, Verb: verb, Line: post.Line, Why: post.Why})
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
		out = append(out, crewTeamFeedItem{userAvatarFields: avatarFieldsFor(item.UpdatedBy, ""), ID: "online:" + item.DestinationItemID + ":" + item.UpdatedAt, Kind: "decision", At: at, Person: teamOnlinePersonName(item.UpdatedBy), Title: teamOnlinePersonName(item.UpdatedBy), Verb: verb, Line: item.Name, Why: "Runs in the Team's online home."})
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
