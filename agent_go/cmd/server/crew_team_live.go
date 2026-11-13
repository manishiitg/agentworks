package server

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// The Monitor's Team view (PLAT-746-926): names, identity, the server's live
// state, each teammate's presence and an agent's own Doing post (the same post
// Team › Activity shows them), never a pending question's text or a chat.
type crewTeamLiveAgent struct {
	Private  bool   `json:"private,omitempty"`
	ID       string `json:"id"`
	OwnerID  string `json:"owner_id"`
	Tab      string `json:"tab"`
	ChatKey  string `json:"chat_key"`
	Name     string `json:"name"`
	Person   string `json:"person"`
	Label    string `json:"label"`
	Activity string `json:"activity"`
	// Doing is the agent's latest Team "Doing" post, only while it works or waits.
	Doing string `json:"doing,omitempty"`
}

// crewTeamLivePerson is one teammate (never the viewer): whether they are active now ("Mac on", "Active now on the
// web") or when they were last seen, from the same reading as the Team tab's people dots.
type crewTeamLivePerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Line   string `json:"line"`
}
type crewTeamLiveView struct {
	Agents  []crewTeamLiveAgent  `json:"agents"`
	Idle    []crewTeamLiveAgent  `json:"idle"`
	People  []crewTeamLivePerson `json:"people"`
	Summary string               `json:"summary"`
}

// Input has already passed current Team membership/In-team admission and the
// one live wait cutoff in teamAgentsByPersonWithMain. Both open and unopened
// Crews use this projection; a screen never computes a teammate's state.
func crewTeamLiveFor(t crewTeam, people []crewTeamAgentsPerson, viewer string) crewTeamLiveView {
	project := t.ProjectID
	out := crewTeamLiveView{Agents: []crewTeamLiveAgent{}, Idle: []crewTeamLiveAgent{}, People: []crewTeamLivePerson{}}
	doing := crewTeamActivityPostsFor(t, viewer).Doing
	for _, person := range people {
		own := person.You || person.UserID == viewer
		if !own {
			out.People = append(out.People, crewTeamLivePerson{UserID: person.UserID, Name: person.Name, Online: person.Active, Line: person.ActiveLabel})
		}
		for _, agent := range person.Agents {
			post := doing[agent.ID]
			if agent.ID == "" {
				post = doing[agent.ChatKey]
			}
			if own {
				if !post.Private || (agent.Activity != "running" && agent.Activity != "waiting") {
					continue
				}
			} else if !agent.InTeam {
				continue
			}
			row := crewTeamLiveAgent{Private: post.Private, ID: agent.ID, OwnerID: person.UserID, Tab: agent.Tab, ChatKey: agent.ChatKey, Name: agent.Name, Person: person.Name, Activity: agent.Activity}
			row.Label = row.Person + " · " + row.Name
			if row.Activity == "running" || row.Activity == "waiting" {
				row.Doing = post.Line
			}
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
	crewTeamLiveLog(project, viewer, out)
	return out
}

// Telemetry only, never reused as activity data: one count summary per minute
// instead of one log per navigation request. No names or message/task text.
var crewTeamLiveLogMinutes sync.Map

func crewTeamLiveLog(project, viewer string, live crewTeamLiveView) {
	value, _ := crewTeamLiveLogMinutes.LoadOrStore(project+"/"+viewer, &atomic.Int64{})
	minute := time.Now().Unix() / 60
	previous := value.(*atomic.Int64).Load()
	if previous == minute || !value.(*atomic.Int64).CompareAndSwap(previous, minute) {
		return
	}
	waiting := 0
	for _, row := range live.Agents {
		if row.Activity == "waiting" {
			waiting++
		}
	}
	log.Printf("[CREW_TEAM_LIVE] project=%s viewer=%s working=%d waiting=%d idle=%d", project, viewer, len(live.Agents)-waiting, waiting, len(live.Idle))
}

// Only metadata from admitted Team-to-Team conversations affects the live line.
// A reply closes the preceding outstanding question; it is not a new wait.
func crewTeamMessageWaitStarts(ctx context.Context, project string, people []crewTeamAgentsPerson) map[string]time.Time {
	out := map[string]time.Time{}
	allowed := map[string]bool{}
	for _, person := range people {
		for _, agent := range person.Agents {
			if agent.InTeam {
				allowed[agent.ChatKey] = true
			}
		}
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return out
	}
	for _, conversation := range store.Conversations {
		valid := true
		for _, endpoint := range conversation.Endpoints {
			if endpoint.Kind != triggerCallerCrew || endpoint.ID != project || endpoint.External || endpoint.NamedAgentID != "" || !allowed[endpoint.ChatKey] {
				valid = false
			}
		}
		if !valid {
			continue
		}
		from := -1
		var since time.Time
		for _, message := range conversation.Messages {
			if message.Kind == "ping" || message.From < 0 || message.From > 1 {
				continue
			}
			if from >= 0 && from != message.From {
				from = -1
				continue
			}
			if from < 0 {
				from, since = message.From, message.SentAt
			}
		}
		if from >= 0 {
			key := conversation.Endpoints[from].ChatKey
			if previous, ok := out[key]; !ok || since.Before(previous) {
				out[key] = since
			}
		}
	}
	return out
}

// Local navigation fetches the same server projection once per saved connection,
// not once per Crew. This memo lives only for this request, not a busy cache.
func crewTeamLocalNavigationLive(ctx context.Context, owner, project string, reads map[string]map[string]crewTeamLiveView) *crewTeamLiveView {
	binding, err := crewroot.ReadSync(project)
	if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" || ownerOfCrewProjectID(project) != owner {
		return nil
	}
	connection, err := desktopConnectionFor(owner, binding.ConnectionID)
	if err != nil {
		return nil
	}
	rows, read := reads[connection.ID]
	if !read {
		rows = map[string]crewTeamLiveView{}
		reads[connection.ID] = rows
		var reply struct {
			Workspaces []struct {
				ID   string            `json:"id"`
				Live *crewTeamLiveView `json:"team_live"`
			} `json:"workspaces"`
		}
		round, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		if err := crewTeamRemoteJSON(round, owner, connection, http.MethodPost, "/api/workspaces", map[string]string{"action": "list"}, &reply); err != nil {
			log.Printf("[CREW_TEAM_LIVE] project=%s event=navigation_read_failed", project)
			return nil
		}
		for _, row := range reply.Workspaces {
			if row.Live != nil {
				rows[row.ID] = *row.Live
			}
		}
	}
	if live, ok := rows[binding.PeerProjectID]; ok {
		return &live
	}
	return nil
}
