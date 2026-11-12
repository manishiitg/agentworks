package server

import "fmt"

// The sidebar receives only names, identity and the server's live state, never
// a Doing line, a pending question's text or another chat's contents.
type crewTeamLiveAgent struct {
	ID       string `json:"id"`
	OwnerID  string `json:"owner_id"`
	Tab      string `json:"tab"`
	ChatKey  string `json:"chat_key"`
	Name     string `json:"name"`
	Person   string `json:"person"`
	Label    string `json:"label"`
	Activity string `json:"activity"`
}
type crewTeamLiveView struct {
	Agents  []crewTeamLiveAgent `json:"agents"`
	Idle    []crewTeamLiveAgent `json:"idle"`
	Summary string              `json:"summary"`
}

// Input has already passed current Team membership/In-team admission and the
// one live wait cutoff in teamAgentsByPersonWithMain. Both open and unopened
// Crews use this projection; a screen never computes a teammate's state.
func crewTeamLiveFor(people []crewTeamAgentsPerson, viewer string) crewTeamLiveView {
	out := crewTeamLiveView{Agents: []crewTeamLiveAgent{}, Idle: []crewTeamLiveAgent{}}
	for _, person := range people {
		if person.You || person.UserID == viewer {
			continue
		}
		for _, agent := range person.Agents {
			if !agent.InTeam {
				continue
			}
			row := crewTeamLiveAgent{ID: agent.ID, OwnerID: person.UserID, Tab: agent.Tab, ChatKey: agent.ChatKey, Name: agent.Name, Person: crewTeamFirstName(person.Name), Activity: agent.Activity}
			row.Label = row.Person + " · " + row.Name
			if row.Activity == "running" {
				row.Label += " working"
				out.Agents = append(out.Agents, row)
			} else if row.Activity == "waiting" {
				row.Label += " waiting"
				out.Agents = append(out.Agents, row)
			} else {
				out.Idle = append(out.Idle, row)
			}
		}
	}
	if len(out.Agents) == 1 {
		out.Summary = out.Agents[0].Person + " working"
	} else if len(out.Agents) > 1 {
		out.Summary = fmt.Sprintf("%d working", len(out.Agents))
	}
	return out
}
