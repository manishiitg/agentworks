package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Needs-you cards (#2853, owner 16 Nov): an agent asks one person in its Team for
// something with up to three buttons it picks. The card stays pinned in the Team
// panel until that person clicks or the agent withdraws it. A click is the
// person's answer: it goes back to the asking agent as a message from them, and
// "Ask my agent" hands the request to one of the person's own agents instead.
// Only the person asked can answer. Since #2965 each card is a request thread
// addressed to the person, and the click is the reply in that thread.
const crewWorkAskAgentButton = "Ask my agent"

type crewWorkRefusal struct{ code, text string }

func (r crewWorkRefusal) Error() string { return r.text }

type crewWorkNeed struct {
	ID   string `json:"id"`
	Line string `json:"line"`
	// The request's own words, shown under its title so the person knows what they are answering.
	Message string `json:"message,omitempty"`
	Link    string `json:"link,omitempty"`
	// A request's web links and attached files (a PR, a plan .md), opened like a task's (#2965).
	Links     []string           `json:"links,omitempty"`
	Files     []crewTeamTaskFile `json:"files,omitempty"`
	Task      string             `json:"task,omitempty"`
	TaskTitle string             `json:"task_title,omitempty"`
	For       string             `json:"for"`
	ForMe     bool               `json:"for_me"`
	Buttons   []string           `json:"buttons"`
	Agent     string             `json:"agent"`
	Person    string             `json:"person"`
	Mine      bool               `json:"mine,omitempty"` // the asking agent is the viewer's own
	State     string             `json:"state"`          // waiting
	At        time.Time          `json:"at"`
}

// crewWorkNeedsView is the Team panel's pinned cards: the requests still waiting on the viewer, newest first.
func crewWorkNeedsView(team crewTeam, viewer string) []*crewWorkNeed {
	return crewWorkNeedsFromRequests(agentTeamRequests(context.Background(), team, viewer), viewer)
}

// crewWorkNeedsFromRequests keeps only the requests to the viewer: a card is for the person asked, not
// for every Team member or the viewer's other accounts (owner, 19 Nov).
func crewWorkNeedsFromRequests(requests []agentTeamRequest, viewer string) []*crewWorkNeed {
	waiting := []*crewWorkNeed{}
	for _, r := range requests {
		if !r.ForMe {
			continue
		}
		waiting = append(waiting, &crewWorkNeed{ID: r.MessageID, Line: r.Title, Message: needMessage(r.Title, r.Text), Link: r.Ref, For: r.For, ForMe: r.ForMe, Buttons: r.Buttons, Agent: r.Agent, Person: r.Person, Mine: r.personID != "" && r.personID == viewer, State: "waiting", At: r.At, Links: r.links, Files: r.files})
	}
	slices.SortFunc(waiting, func(a, b *crewWorkNeed) int { return b.At.Compare(a.At) })
	return waiting
}

// crewWorkNeedFor resolves who is asked: a current member of the Team, by first name, name or user ID.
func crewWorkNeedFor(team crewTeam, who string) (string, error) {
	who = strings.ToLower(strings.TrimSpace(who))
	if who == "" {
		return "", fmt.Errorf("Say which person you need (their name, as the Team panel shows it)")
	}
	found := []string{}
	for id, label := range crewTeamPersonLabels(team) {
		if !team.member(id) || !crewTeamAccountActive(id) {
			continue
		}
		if who == strings.ToLower(id) || who == strings.ToLower(label) || who == strings.ToLower(crewPersonShortName(id)) || who == strings.ToLower(shareOwnerLabel(id)) {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("Nobody called %s is in this Team. Use a name from the Team panel", who)
	}
	return "", fmt.Errorf("More than one person matches %s. Use their full name", who)
}

func crewWorkNeedButtons(raw []string) ([]string, error) {
	out := []string{}
	for _, b := range raw {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if strings.EqualFold(b, crewWorkAskAgentButton) {
			b = crewWorkAskAgentButton
		}
		if utf8.RuneCountInString(b) > 24 || strings.ContainsAny(b, "\r\n") {
			return nil, fmt.Errorf("Keep each button to a few words (up to 24 characters)")
		}
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, b) }) {
			out = append(out, b)
		}
	}
	if len(out) == 0 || len(out) > 3 {
		return nil, fmt.Errorf("Give one to three short buttons, for example Approve, Not now, %s", crewWorkAskAgentButton)
	}
	return out, nil
}

// crewWorkAnswerHelper picks the person's own agent for an "Ask my agent" answer:
// the one named, else their only one. With several it returns the list to choose from.
func crewWorkAnswerHelper(ctx context.Context, team crewTeam, person string, in crewWorkInput) (*crewTeamReachableAgent, map[string]any, error) {
	if !strings.EqualFold(strings.TrimSpace(in.Button), crewWorkAskAgentButton) {
		return nil, nil, nil
	}
	rows := crewTeamOwnSourceRows(ctx, team, person)
	pick := func(ref string) *crewTeamReachableAgent {
		for i := range rows {
			if ref != "" && (rows[i].ID == ref || strings.EqualFold(rows[i].Name, ref)) {
				return &rows[i]
			}
		}
		return nil
	}
	if ref := strings.TrimSpace(in.Helper); ref != "" {
		if row := pick(ref); row != nil {
			return row, nil, nil
		}
		return nil, nil, fmt.Errorf("Choose one of your own agents in this Team")
	}
	switch len(rows) {
	case 0:
		return nil, nil, fmt.Errorf("None of your agents is in this Team yet, so there is nobody to hand this to. Answer with another button, or add one of your agents to the Team")
	case 1:
		return &rows[0], nil, nil
	}
	choose := []map[string]any{}
	for _, row := range rows {
		choose = append(choose, map[string]any{"id": row.ID, "name": row.Name, "online": row.Online})
	}
	return nil, map[string]any{"choose_agent": choose, "note": "Choose which of your agents handles this request."}, nil
}

// needMessage is a request's text for its card, without the title it starts with (a title defaults to the first line).
func needMessage(title, text string) string {
	text, title = strings.TrimSpace(text), strings.TrimSpace(title)
	if title != "" {
		text = strings.TrimSpace(strings.TrimPrefix(text, title))
	}
	return text
}
