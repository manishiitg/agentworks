package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

const crewTeamAgentIDPrefix = "team-agent-"

// Team identity is independent of the chat binding and of where the agent runs.
// Native keys remain transport details, never the public Team message address.
type crewTeamRegisteredAgent struct {
	ID      string            `json:"id"`
	OwnerID string            `json:"owner_id"`
	Tab     string            `json:"tab"`
	ChatKey string            `json:"chat_key"`
	Name    string            `json:"name"`
	Purpose string            `json:"purpose,omitempty"`
	Place   string            `json:"place"`
	InTeam  bool              `json:"in_team"`
	Rule    crewTeamAgentRule `json:"rule"`
}

var crewTeamAgentRegistryLocks sync.Map

func crewTeamRecordMutex(dir string) *sync.Mutex {
	v, _ := crewTeamAgentRegistryLocks.LoadOrStore(dir, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Team people, agent identities and agent settings live in the same record.
// Native agent discovery fills metadata; only an agent's person changes its rule.
func assignCrewTeamAgentIDs(t crewTeam, people []crewTeamAgentsPerson) error {
	dir, err := crewTeamDir(t.ProjectID)
	if err != nil {
		return err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	filename := filepath.Join(dir, "team.json")
	current := t
	current.RegistryVersion = 1
	raw, err := os.ReadFile(filename)
	if err == nil {
		current = crewTeam{}
		if err = json.Unmarshal(raw, &current); err != nil || current.RegistryVersion != 1 || current.ProjectID != t.ProjectID || current.OwnerID != t.OwnerID || t.On && !current.On {
			return fmt.Errorf("Team registry is unavailable")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if current.Agents == nil {
		current.Agents = map[string]crewTeamRegisteredAgent{}
	}
	before, err := json.Marshal(current.Agents)
	if err != nil {
		return err
	}
	bySource := map[string]string{}
	byChat := map[string]string{}
	for id, a := range current.Agents {
		source := a.OwnerID + "/" + a.Tab
		chat := a.OwnerID + "/" + a.ChatKey
		if a.OwnerID == "" || a.Tab == "" || id != a.ID || bySource[source] != "" || a.ChatKey == "" || byChat[chat] != "" {
			return fmt.Errorf("Team registry is inconsistent")
		}
		if _, err := uuid.Parse(strings.TrimPrefix(id, crewTeamAgentIDPrefix)); err != nil || !strings.HasPrefix(id, crewTeamAgentIDPrefix) {
			return fmt.Errorf("Team agent identity is invalid")
		}
		bySource[source] = id
		byChat[chat] = id
	}
	for i := range people {
		if !current.member(people[i].UserID) {
			people[i].Agents = nil
			continue
		}
		accepted := make([]crewTeamAgent, 0, len(people[i].Agents))
		for _, discovered := range people[i].Agents {
			a := &discovered
			tab := a.Tab
			if crewTeamMainAgent(current, people[i].UserID, a.ChatKey) || tab == macMainTab {
				tab = macMainTab
			}
			source := people[i].UserID + "/" + tab
			chat := people[i].UserID + "/" + a.ChatKey
			id := bySource[source]
			if existing := byChat[chat]; existing != "" {
				if id != "" && id != existing {
					log.Printf("[CREW_TEAM] project=%s event=agent_registry_row_refused person=%s tab=%q reason=%q", t.ProjectID, people[i].UserID, tab, "Team chat is already bound to another agent")
					continue
				}
				id = existing
			}
			record, ok := current.Agents[id]
			if !ok {
				id = crewTeamAgentIDPrefix + uuid.NewString()
				record = crewTeamRegisteredAgent{ID: id, OwnerID: people[i].UserID, Tab: tab, InTeam: true}
				bySource[source] = id
			}
			delete(bySource, record.OwnerID+"/"+record.Tab)
			record.Tab = tab
			delete(byChat, record.OwnerID+"/"+record.ChatKey)
			byChat[chat] = id
			bySource[source] = id
			record.ChatKey, record.Name, record.Purpose, record.Place = a.ChatKey, a.Name, a.Purpose, a.Place
			current.Agents[id] = record
			a.ID, a.InTeam, a.Address = id, record.InTeam, crewTeamAgentAddress(t.ProjectID, id)
			accepted = append(accepted, *a)
		}
		people[i].Agents = accepted
	}
	after, err := json.Marshal(current.Agents)
	if err != nil || bytes.Equal(before, after) {
		return err
	}
	if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filename, current, 0600)
}

// Native keys are accepted only inside the transport/discovery adapter. Team
// controls and addresses use IDs; ownership is checked even for an exact ID.
func (t crewTeam) registeredAgent(person, key string) (crewTeamRegisteredAgent, bool) {
	if a, ok := t.Agents[key]; ok {
		return a, a.OwnerID == person
	}
	for _, a := range t.Agents {
		if a.OwnerID == person && (a.Tab == key || a.ChatKey == key || crewTeamMainAgent(t, person, key) && a.Tab == macMainTab) {
			return a, true
		}
	}
	return crewTeamRegisteredAgent{}, false
}

func updateCrewTeamRegisteredAgent(project, person, id string, change func(*crewTeamRegisteredAgent)) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	t, err := readCrewTeam(project)
	if err != nil {
		return err
	}
	a, ok := t.Agents[id]
	if !ok || a.OwnerID != person || !t.member(person) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(person) {
		return errCrewTeamAgentNotYours
	}
	change(&a)
	t.Agents[id] = a
	t.Revision++
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), t, 0600); err != nil {
		return err
	}
	publishCrewPanelChanged(project)
	go pushCrewTeamRecordChanged(t)
	return nil
}

// Shared by directory discovery and message admission. No calling-Crew restriction.
func crewTeamAgentsVisible(t crewTeam, person string) bool {
	return t.On && t.member(person) && crewTeamAccountActive(person) && crewTeamAccountActive(t.OwnerID) && ownerOfCrewProjectID(t.ProjectID) == t.OwnerID
}

// crewTeamAgentByID admits a Team message by actual agent ownership, never by
// Crew editing rights. Only current shared agents of active members are targets.
func crewTeamAgentByID(ctx context.Context, project, caller, id string) (crewTeamAgent, string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(t, caller) {
		return crewTeamAgent{}, "", fmt.Errorf("This Crew's Team is not available to you")
	}
	for _, person := range teamAgentsByPerson(ctx, t, caller) {
		for _, agent := range person.Agents {
			if agent.ID == id && agent.InTeam {
				return agent, person.UserID, nil
			}
		}
	}
	return crewTeamAgent{}, "", fmt.Errorf("That agent isn't in this Crew's Team now")
}

func (api *StreamingAPI) resolveTeamAgentTarget(ctx context.Context, target triggerTarget, id string) (triggerTarget, error) {
	agent, owner, err := crewTeamAgentByID(ctx, target.CrewID, GetUserIDFromContext(ctx), id)
	if err != nil {
		return target, err
	}
	key := crewTeamNativeChatKey(target.CrewID, owner, agent)
	target.TeamAgentID = agent.ID
	target.CrewOwner = owner
	target.Chat = &codeChat{OwnerID: owner, ID: key, Key: key, Name: agent.Name, Purpose: agent.Purpose, SessionID: agent.sessionID}
	target.Label += " · " + agent.Name
	return target, nil
}

func crewTeamNativeChatKey(project, owner string, agent crewTeamAgent) string {
	if crewRelayTwin(project) && owner == ownerOfCrewProjectID(project) {
		return macSharedAgentChatKey(project, agent.Tab)
	}
	return agent.ChatKey
}

func crewTeamSendingAgent(ctx context.Context, project, person, chatKey string) (crewTeamAgent, error) {
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(t, person) {
		return crewTeamAgent{}, fmt.Errorf("This Crew's Team is not available to you")
	}
	for _, p := range teamAgentsByPerson(ctx, t, person) {
		if p.UserID != person {
			continue
		}
		for _, a := range p.Agents {
			if a.InTeam && (a.ChatKey == chatKey || crewTeamNativeChatKey(project, person, a) == chatKey || crewTeamMainAgent(t, person, chatKey) && crewTeamMainAgent(t, person, a.ChatKey)) {
				return a, nil
			}
		}
	}
	return crewTeamAgent{}, nil // A private sender keeps its ordinary chat endpoint.
}

// Re-resolve queued messages and replies by ID. The saved chat key cannot route
// to another person after a move, and an old ID cannot bypass removal/Only me.
func resolveTeamMessageEndpoint(ctx context.Context, endpoint agentMessageEndpoint, viewer string) (agentMessageEndpoint, string, error) {
	a, owner, err := crewTeamAgentByID(ctx, endpoint.ID, viewer, endpoint.TeamAgentID)
	if err != nil {
		return endpoint, "", err
	}
	if owner != endpoint.UserID {
		return endpoint, "", fmt.Errorf("Team agent owner changed")
	}
	endpoint.ChatKey = crewTeamNativeChatKey(endpoint.ID, owner, a)
	route := "server"
	if crewRelayTwin(endpoint.ID) && owner == ownerOfCrewProjectID(endpoint.ID) {
		route = "owner_mac"
	} else if a.Place == "mac" {
		route = "member_mac"
	} else {
		endpoint.Session = a.sessionID
	}
	return endpoint, route, nil
}
