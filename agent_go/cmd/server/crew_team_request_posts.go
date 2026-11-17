package server

import (
	"context"
	"strings"
	"time"
)

// #2965 step 3: the Team views read request threads. Each request to the Team or
// to a person becomes one Activity line for its current state (asked, taken,
// done), decisions in any Team request thread are decisions, and a task in
// progress is its agent's "doing" line. These sit beside the work log's entries
// until the work log is removed.
func crewTeamRequestPosts(ctx context.Context, t crewTeam, viewer string) (map[string]crewTeamActivityPost, []crewTeamActivityPost, []crewTeamActivityPost) {
	doing, decisions, work := map[string]crewTeamActivityPost{}, []crewTeamActivityPost{}, []crewTeamActivityPost{}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil || !t.On || !t.member(viewer) {
		return doing, decisions, work
	}
	visible := crewWorkVisible(t, viewer)
	labels := crewTeamPersonLabels(t)
	now := time.Now().UTC()
	post := func(row crewTeamRegisteredAgent, at time.Time, id, kind, event, line string, m agentMessage, state string) crewTeamActivityPost {
		p := crewTeamActivityPost{ID: id, Kind: kind, AgentID: row.ID, OwnerID: row.OwnerID, ChatKey: row.ChatKey, Agent: firstNonEmptyTrimmed(row.Name, row.Tab), Person: labels[row.OwnerID], Line: line, Task: m.Ref, TaskTitle: m.Title, TaskState: state, Event: event, At: at, Private: !row.InTeam}
		if row.Terminal != nil {
			p.CLI = providerAccountProductName(row.Terminal.Provider)
		}
		return p
	}
	for _, c := range store.Conversations {
		if len(c.Messages) == 0 || !c.Messages[0].TeamRequest {
			continue
		}
		request := c.Messages[0]
		asker, ok := agentTeamAskerRow(t, c.Endpoints[0])
		if !ok || !visible(!asker.InTeam, asker.ID, asker.OwnerID) {
			continue
		}
		dest := c.Endpoints[1]
		if agentTeamEndpoint(dest) {
			if dest.ID != t.ProjectID {
				continue
			}
		} else if _, sameTeam := agentTeamAskerRow(t, dest); !sameTeam {
			continue
		}
		state, _ := agentTaskState(c, request, now)
		// Who moved it last: the taker (or the sender itself), the person who answered, or the asker.
		actor, at := asker, request.SentAt
		latest, replied := agentTaskLatestReply(c, request)
		if replied {
			at = latest.SentAt
			if latest.From == 1 && dest.Kind != agentTeamPersonKind && dest.Kind != agentTeamOpenKind {
				if row, ok := agentTeamAskerRow(t, dest); ok {
					actor = row
				}
			}
		}
		line := request.Title
		switch {
		case dest.Kind == agentTeamPersonKind && state == "open":
			// A request waiting on a person is their pinned Needs-you card, not a feed line.
		case dest.Kind == agentTeamPersonKind:
			answer := crewTeamActivityPost{ID: "request:" + request.ID, Kind: "task", ChatKey: crewWorkPersonPrefix + dest.UserID, OwnerID: dest.UserID, Person: labels[dest.UserID], Line: request.Title + ": " + firstLineClip(latest.Text, 120), Task: request.Ref, TaskTitle: request.Title, TaskState: state, Event: "answered", At: at}
			work = append(work, answer)
		case state == "open":
			work = append(work, post(asker, at, "request:"+request.ID, "task", "added", line, request, state))
		case state == "done":
			work = append(work, post(actor, at, "request:"+request.ID, "task", "done", firstNonEmptyTrimmed(firstLineClip(latest.Text, 160), line), request, state))
		default:
			p := post(actor, at, "request:"+request.ID, "task", "started", line, request, state)
			work = append(work, p)
			if state == "in_progress" && (doing[actor.ID].At.Before(at) || doing[actor.ID].ID == "") {
				p.Line = strings.TrimSpace(strings.TrimPrefix(request.Ref+": "+line, ": "))
				doing[actor.ID] = p
			}
		}
		for _, m := range c.Messages[1:] {
			if !m.Decision {
				continue
			}
			by := asker
			if m.From == 1 {
				if row, ok := agentTeamAskerRow(t, dest); ok {
					by = row
				}
			}
			// The stored text is the task header, a blank line, then what the agent wrote.
			text := m.Text
			if _, body, found := strings.Cut(text, "\n\n"); found && strings.HasPrefix(text, "Decision.") {
				text = body
			}
			d := post(by, m.SentAt, "decision:"+m.ID, "decision", "", firstLineClip(text, 200), request, "")
			d.Why = m.Why
			decisions = append(decisions, d)
		}
	}
	return doing, decisions, work
}
