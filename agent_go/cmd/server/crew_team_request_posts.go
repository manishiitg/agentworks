package server

import (
	"context"
	"time"
)

// A request to a person (the Needs-you card) becomes one Activity line once the
// person answers it: "You answered ...". Tracking work is the project's own issue
// tracker (owner, 19 Nov), so nothing else here comes from request threads: no
// "doing" lines and no decisions.
func crewTeamRequestPosts(ctx context.Context, t crewTeam, viewer string) (map[string]crewTeamActivityPost, []crewTeamActivityPost, []crewTeamActivityPost) {
	doing, decisions, work := map[string]crewTeamActivityPost{}, []crewTeamActivityPost{}, []crewTeamActivityPost{}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil || !t.On || !t.member(viewer) {
		return doing, decisions, work
	}
	visible := crewWorkVisible(t, viewer)
	labels := crewTeamPersonLabels(t)
	now := time.Now().UTC()
	for _, c := range store.Conversations {
		if len(c.Messages) == 0 || !c.Messages[0].TeamRequest {
			continue
		}
		request, dest := c.Messages[0], c.Endpoints[1]
		if dest.Kind != agentTeamPersonKind || dest.ID != t.ProjectID {
			continue
		}
		asker, ok := agentTeamAskerRow(t, c.Endpoints[0])
		if !ok || !visible(!asker.InTeam, asker.ID, asker.OwnerID) {
			continue
		}
		state, _ := agentTaskState(c, request, now)
		latest, replied := agentTaskLatestReply(c, request)
		if state == "open" || !replied {
			continue // still waiting: it is the person's pinned Needs-you card, not a feed line
		}
		work = append(work, crewTeamActivityPost{ID: "request:" + request.ID, Kind: "task", ChatKey: crewWorkPersonPrefix + dest.UserID, OwnerID: dest.UserID, Person: labels[dest.UserID], Line: request.Title + ": " + firstLineClip(latest.Text, 120), Event: "answered", At: latest.SentAt})
	}
	return doing, decisions, work
}
