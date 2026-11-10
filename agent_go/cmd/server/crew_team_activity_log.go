package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// The Team tab's activity log (owner, 10 Nov, approved mockup "Team Activity"): the main log of what the Team's agents
// say to each other and what is going on. Now is each agent's state from the agents list (teamAgentsByPerson), messages
// the last agent-to-agent messages between this Crew's Team agents from the message store, other the open clashes. A
// message's text shows only while both agents' chats are Live for the viewer (agentChatShare); nothing a person types to
// their own agent is ever here, since both ends must be this Crew's Team agents.

const crewTeamActivityMessages = 50

type crewTeamActivityNow struct {
	Person string `json:"person"`
	Agent  string `json:"agent"`
	You    bool   `json:"you,omitempty"`
	State  string `json:"state"` // "running" or ""
	Line   string `json:"line"`
}

type crewTeamActivityEnd struct {
	Person  string `json:"person"`
	Agent   string `json:"agent"`
	You     bool   `json:"you,omitempty"`
	Label   string `json:"label"` // "Your Main", "tester-b's Tester"
	ChatKey string `json:"chat_key"`
	// WatchKey is its tab's key for someone else where that differs (the owner's Mac Main), as on the Team tab.
	WatchKey string `json:"watch_key,omitempty"`
}

type crewTeamActivityMessage struct {
	ID         string              `json:"id"`
	From       crewTeamActivityEnd `json:"from"`
	To         crewTeamActivityEnd `json:"to"`
	SentAt     time.Time           `json:"sent_at"`
	Status     string              `json:"status"` // "answered", "waiting" or "mac_off"
	StatusLine string              `json:"status_line"`
	AnsweredAt *time.Time          `json:"answered_at,omitempty"`
	Question   string              `json:"question,omitempty"`
	Answer     string              `json:"answer,omitempty"`
	HiddenLine string              `json:"hidden_line,omitempty"`
}

type crewTeamActivityOther struct {
	Kind string `json:"kind"` // "clash"
	Line string `json:"line"`
}

type crewTeamActivityView struct {
	Now      []crewTeamActivityNow     `json:"now"`
	Messages []crewTeamActivityMessage `json:"messages"`
	Other    []crewTeamActivityOther   `json:"other"`
	Foot     string                    `json:"foot"`
}

// crewTeamActivityAgent is one Team agent with its person, for placing a message's ends.
type crewTeamActivityAgent struct {
	person crewTeamAgentsPerson
	agent  crewTeamAgent
	end    crewTeamActivityEnd
}

// crewTeamActivityFor builds the log for caller, a member of t.
func crewTeamActivityFor(ctx context.Context, t crewTeam, caller string) crewTeamActivityView {
	out := crewTeamActivityView{Now: []crewTeamActivityNow{}, Messages: []crewTeamActivityMessage{}, Other: []crewTeamActivityOther{},
		Foot: "Shows messages between agents only. What a person types to their own agent is never shown here."}
	onMac := crewRelayTwin(t.ProjectID)
	// Every name a message endpoint may carry for an agent: its row key, and for the owner's Mac agents the keys the Mac
	// sends under (macSharedAgentChatKey; the Crew's own id for Main).
	byKey := map[string]*crewTeamActivityAgent{}
	for _, p := range teamAgentsByPerson(ctx, t, caller) {
		for _, a := range p.Agents {
			state, line := "", firstNonEmptyTrimmed(a.StatusLabel, "Idle")
			if a.Activity == "running" {
				state, line = "running", "Working"
			}
			out.Now = append(out.Now, crewTeamActivityNow{Person: p.Name, Agent: a.Name, You: p.You, State: state, Line: line})
			label := p.Name + "'s " + a.Name
			if p.You {
				label = "Your " + a.Name
			}
			entry := &crewTeamActivityAgent{person: p, agent: a, end: crewTeamActivityEnd{Person: p.Name, Agent: a.Name, You: p.You, Label: label, ChatKey: a.ChatKey}}
			if p.Owner && onMac && a.Tab == macMainTab && !p.You {
				entry.end.WatchKey = crewMacTabRow(t.ProjectID, crewMacTeamTab{Person: p.UserID, Tab: macMainTab}).ConversationKey
			}
			keys := []string{a.ChatKey}
			if p.Owner && a.Place == "mac" {
				keys = append(keys, macSharedAgentChatKey(t.ProjectID, a.Tab))
			}
			for _, key := range keys {
				if key != "" && byKey[key] == nil {
					byKey[key] = entry
				}
			}
		}
	}
	if store, err := readAgentMessageSnapshot(ctx); err == nil {
		out.Messages = crewTeamActivityMessagesFrom(ctx, t, caller, store, byKey)
	}
	if summary, err := readCrewTeamLedgerSummary(t.ProjectID, t.Folder, false); err == nil {
		for _, path := range summary.clashes {
			if t.Folder.holds(path) {
				out.Other = append(out.Other, crewTeamActivityOther{Kind: "clash", Line: "Clash in " + path + " — not sorted out yet"})
			}
		}
	}
	return out
}

// crewTeamActivityMessagesFrom lists the store's messages between two of this Crew's Team agents, newest first. A
// message from the other side answers every earlier one still open in that conversation.
func crewTeamActivityMessagesFrom(ctx context.Context, t crewTeam, caller string, store agentMessageStore, byKey map[string]*crewTeamActivityAgent) []crewTeamActivityMessage {
	agentOf := func(e agentMessageEndpoint) *crewTeamActivityAgent {
		if e.Kind != triggerCallerCrew || e.ID != t.ProjectID || e.External || e.NamedAgentID != "" {
			return nil
		}
		return byKey[e.ChatKey]
	}
	// hiddenBy is the person whose chat the viewer may not read ("" when both are Live for them).
	hiddenBy := func(ends ...*crewTeamActivityAgent) string {
		for _, end := range ends {
			if end.person.UserID != caller && t.agentChatShare(ctx, end.person.UserID, end.agent.ChatKey) != crewChatShareLive {
				return end.person.Name
			}
		}
		return ""
	}
	out := []crewTeamActivityMessage{}
	for _, c := range store.Conversations {
		ends := [2]*crewTeamActivityAgent{agentOf(c.Endpoints[0]), agentOf(c.Endpoints[1])}
		if ends[0] == nil || ends[1] == nil || ends[0] == ends[1] {
			continue
		}
		hidden := hiddenBy(ends[0], ends[1])
		open, openFrom := []int{}, -1
		for _, m := range c.Messages {
			if m.Kind == "ping" || m.From < 0 || m.From > 1 {
				continue
			}
			if len(open) > 0 && m.From != openFrom {
				at := m.SentAt
				for _, i := range open {
					row := &out[i]
					row.Status, row.StatusLine, row.AnsweredAt = "answered", "answered in "+crewTeamAnsweredIn(at.Sub(row.SentAt)), &at
					if hidden == "" {
						row.Answer = m.Text
					}
				}
				open = open[:0]
				continue
			}
			from, to := ends[m.From], ends[1-m.From]
			row := crewTeamActivityMessage{ID: m.ID, From: from.end, To: to.end, SentAt: m.SentAt, Status: "waiting", StatusLine: "waiting for answer"}
			// Waiting on an agent of a Mac that is off and that the server does not answer for (the agents list's
			// "Mac off" status, teamAgentsByPerson).
			if to.agent.Place == "mac" && !to.person.MacOn && !to.agent.PlaceServer {
				whose := to.person.Name + "'s"
				if to.person.You {
					whose = "your"
				}
				row.Status, row.StatusLine = "mac_off", "not answered — "+whose+" Mac is off"
			}
			if hidden == "" {
				row.Question = m.Text
			} else {
				row.HiddenLine = "Chat hidden by " + hidden + " — only the line is shown"
			}
			open, openFrom = append(open, len(out)), m.From
			out = append(out, row)
		}
	}
	slices.SortStableFunc(out, func(a, b crewTeamActivityMessage) int { return b.SentAt.Compare(a.SentAt) })
	if len(out) > crewTeamActivityMessages {
		out = out[:crewTeamActivityMessages]
	}
	return out
}

// crewTeamAnsweredIn is how long an answer took, in plain words: "30 s", "2 min", "3 h".
func crewTeamAnsweredIn(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", max(1, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// handleCrewTeamActivity is the Team tab's activity log for one of the Team's members, with the agents list's check.
func (api *StreamingAPI) handleCrewTeamActivity(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, false) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_forbidden", "This team is not shared with you. Ask its owner to add you.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, crewTeamActivityFor(r.Context(), t, strings.TrimSpace(claims.UserID)))
}
