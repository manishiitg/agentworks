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
	return assignCrewTeamAgentIDsWithMain(t, people, crewOwnerMainChatKey(t.ProjectID))
}

func assignCrewTeamAgentIDsWithMain(t crewTeam, people []crewTeamAgentsPerson, ownerMainKey string) error {
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
			if crewTeamMainAgentWithKey(current, people[i].UserID, a.ChatKey, ownerMainKey) || tab == macMainTab {
				tab = macMainTab
			}
			source := people[i].UserID + "/" + tab
			chat := people[i].UserID + "/" + a.ChatKey
			id := bySource[source]
			if existing := byChat[chat]; existing != "" {
				if id != "" && id != existing && people[i].UserID == current.OwnerID && tab == macMainTab && a.ChatKey == ownerMainKey {
					// Main moved onto an existing chat. Identity and privacy stay
					// with each chat; only their role/tab changes.
					previous := current.Agents[id]
					physical, ok := strings.CutPrefix(previous.ChatKey, t.ProjectID+codeChatSideMarker)
					if ok && physical != "" && physical != macMainTab && bySource[previous.OwnerID+"/"+physical] == "" {
						previous.Tab = physical
						current.Agents[id] = previous
						delete(bySource, source)
						bySource[previous.OwnerID+"/"+physical] = id
						log.Printf("[CREW_TEAM] project=%s event=agent_main_role_changed agent=%s main=false in_team=%v", t.ProjectID, id, previous.InTeam)
						id = existing
					}
				}
				if id != "" && id != existing {
					log.Printf("[CREW_TEAM] project=%s event=agent_registry_row_refused person=%s tab=%q reason=%q", t.ProjectID, people[i].UserID, tab, "Team chat is already bound to another agent")
					continue
				}
				id = existing
			}
			record, ok := current.Agents[id]
			if !ok {
				id = crewTeamAgentIDPrefix + uuid.NewString()
				inTeam := true
				if _, memberMac := current.memberMac(people[i].UserID); memberMac {
					inTeam = a.initialInTeam != nil && *a.initialInTeam
				}
				record = crewTeamRegisteredAgent{ID: id, OwnerID: people[i].UserID, Tab: tab, InTeam: inTeam}
				bySource[source] = id
			}
			if record.Tab != tab && (record.Tab == macMainTab || tab == macMainTab) {
				log.Printf("[CREW_TEAM] project=%s event=agent_main_role_changed agent=%s main=%v in_team=%v", t.ProjectID, id, tab == macMainTab, record.InTeam)
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
	// Exact chat identity wins before role aliases, including while discovery
	// is catching up with a changed Main pointer.
	for _, a := range t.Agents {
		if a.OwnerID == person && a.ChatKey == key {
			return a, true
		}
	}
	for _, a := range t.Agents {
		if a.OwnerID == person && a.Tab == key {
			return a, true
		}
	}
	// Only an unresolved owner binding can need the Main role alias. Ordinary
	// registry IDs, exact chats and physical tabs above require no manifest read.
	if person == t.OwnerID {
		for _, a := range t.Agents {
			if a.OwnerID == person && a.Tab == macMainTab && crewTeamMainAgent(t, person, key) {
				return a, true
			}
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
	compared := []string{}
	for _, person := range teamAgentsByPerson(ctx, t, caller) {
		for _, agent := range person.Agents {
			if agent.InTeam {
				compared = append(compared, agent.ID)
			}
			if agent.ID == id && agent.InTeam {
				return agent, person.UserID, nil
			}
		}
	}
	log.Printf("[CREW_TEAM] project=%s event=agent_lookup_refused agent_id=%q caller_id=%q compared_agent_ids=%q", project, id, caller, compared)
	return crewTeamAgent{}, "", fmt.Errorf("That agent isn't in this Crew's Team now")
}

func (api *StreamingAPI) resolveTeamAgentTarget(ctx context.Context, target triggerTarget, id string) (triggerTarget, error) {
	caller := GetUserIDFromContext(ctx)
	agent, owner, err := crewTeamMessageAgent(ctx, target.CrewID, caller, id)
	if err != nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_target_failed agent_id=%q caller_id=%q reason=%q", target.CrewID, id, caller, err)
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
	mainKey := crewOwnerMainChatKey(project)
	for _, p := range teamAgentsByPersonWithMain(ctx, t, person, mainKey) {
		if p.UserID != person {
			continue
		}
		for _, a := range p.Agents {
			if a.InTeam && (a.ChatKey == chatKey || crewTeamNativeChatKey(project, person, a) == chatKey || crewTeamMainAgentWithKey(t, person, chatKey, mainKey) && crewTeamMainAgentWithKey(t, person, a.ChatKey, mainKey)) {
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

// Agent deletion removes its public identity, not another agent's sharing.
func removeCrewAgentListing(project, person, key string) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	t, err := readCrewTeam(project)
	if os.IsNotExist(err) {
		mu.Unlock()
		return nil
	}
	if err != nil {
		mu.Unlock()
		return err
	}
	for id, a := range t.Agents {
		if a.OwnerID == person && (a.ChatKey == key || crewTeamNativeChatKey(project, person, crewTeamAgent{Tab: a.Tab, ChatKey: a.ChatKey}) == key) {
			delete(t.Agents, id)
		}
	}
	t.Revision++
	err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), t, 0600)
	mu.Unlock()
	if err == nil {
		pruneCrewStandby(project)
		go pushCrewTeamRecordChanged(t)
	}
	return err
}
