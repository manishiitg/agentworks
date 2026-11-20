package server

import "time"

// The Claude Code Team pane (#3357) shows what the Team panel already decides for
// this exact attached session: the Needs-you cards for its person, the asks sent
// to this agent that are not finished, and the Team agents it can reach. It is a
// read-only projection; the pane never answers, sends or filters.

const teamPaneLimit = 20

type teamPaneNeed struct {
	Line    string    `json:"line"`
	Ref     string    `json:"ref,omitempty"`
	Agent   string    `json:"agent"`
	Person  string    `json:"person,omitempty"`
	Mine    bool      `json:"mine,omitempty"`
	Buttons []string  `json:"buttons"`
	At      time.Time `json:"at"`
}

type teamPaneAsk struct {
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Ref     string    `json:"ref,omitempty"`
	AskedBy string    `json:"asked_by,omitempty"`
	Last    string    `json:"last,omitempty"`
	At      time.Time `json:"at"`
}

type teamPaneAgent struct {
	Name     string `json:"name"`
	Person   string `json:"person,omitempty"`
	Kind     string `json:"kind"`
	Online   bool   `json:"online"`
	Activity string `json:"activity,omitempty"`
	Offline  string `json:"offline_line,omitempty"`
}

func teamPaneNeeds(needs []*crewWorkNeed) []teamPaneNeed {
	out := []teamPaneNeed{}
	for _, n := range needs {
		if len(out) == teamPaneLimit {
			break
		}
		out = append(out, teamPaneNeed{Line: n.Line, Ref: n.Link, Agent: n.Agent, Person: n.Person, Mine: n.Mine, Buttons: n.Buttons, At: n.At})
	}
	return out
}

// The same rule as work_brief's my_tasks: this agent's asks that are not done.
func teamPaneAsks(tasks []crewTeamTask, agentID string) []teamPaneAsk {
	out := []teamPaneAsk{}
	for _, t := range tasks {
		if agentID == "" || t.AgentKey != agentID || t.State == "done" || t.inbox == "" || len(out) == teamPaneLimit {
			continue
		}
		out = append(out, teamPaneAsk{Title: t.Title, State: t.State, Ref: t.Group, AskedBy: t.AskedBy, Last: t.Line, At: t.At})
	}
	return out
}

func teamPaneAgents(reach []crewTeamReachableAgent) []teamPaneAgent {
	out := []teamPaneAgent{}
	for _, a := range reach {
		if len(out) == 2*teamPaneLimit {
			break
		}
		out = append(out, teamPaneAgent{Name: a.Name, Person: a.Person, Kind: a.Kind, Online: a.Online, Activity: a.Activity, Offline: a.OfflineLine})
	}
	return out
}
