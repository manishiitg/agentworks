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
	Kind     string            `json:"kind,omitempty"`
	Terminal *crewTeamTerminal `json:"terminal,omitempty"`
	ID       string            `json:"id"`
	OwnerID  string            `json:"owner_id"`
	Tab      string            `json:"tab"`
	ChatKey  string            `json:"chat_key"`
	Name     string            `json:"name"`
	Purpose  string            `json:"purpose,omitempty"`
	Examples []string          `json:"examples,omitempty"`
	Place    string            `json:"place"`
	InTeam   bool              `json:"in_team"`
	Rule     crewTeamAgentRule `json:"rule"`
}

var crewTeamAgentRegistryLocks sync.Map

func crewTeamRecordMutex(dir string) *sync.Mutex {
	v, _ := crewTeamAgentRegistryLocks.LoadOrStore(dir, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Team people, agent identities and agent settings live in the same record.
// Native agent discovery fills metadata; only an agent's person changes its rule.
// crewTeamNoChatLogged holds the agents already logged as having no chat yet.
var crewTeamNoChatLogged sync.Map

// A saved or explicit choice wins. Only a never-chosen agent gets the shared default.
func crewTeamDefaultInTeam(choice *bool) bool {
	if choice != nil {
		return *choice
	}
	return false // a new agent is Only me until its person shares it (owner, 26 Nov)
}

// A new agent's chat is Live: once its person shares it, teammates see the chat (owner, 26 Nov). Agents saved
// earlier keep their own choice.
func crewTeamDefaultRule() crewTeamAgentRule { return crewTeamAgentRule{ChatShare: crewChatShareLive} }

func assignCrewTeamAgentIDs(t crewTeam, people []crewTeamAgentsPerson) error {
	return assignCrewTeamAgentIDsWithDefault(t, people, crewOwnerDefaultAgentKey(t.ProjectID))
}

func assignCrewTeamAgentIDsWithDefault(t crewTeam, people []crewTeamAgentsPerson, ownerDefaultKey string) error {
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
	// An online project's agents belong to their maker (project:<id>/<maker>, owner 26 Nov); rows saved under the bare
	// project identity before that are dropped, so those agents start again as Only me.
	for id, a := range current.Agents {
		if _, isProject := crewTeamMemberProjectID(a.OwnerID); isProject && crewTeamProjectMaker(a.OwnerID) == "" {
			delete(current.Agents, id)
		}
	}
	bySource := map[string]string{}
	byChat := map[string]string{}
	created := []crewTeamRegisteredAgent{}
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
		if !current.memberOrProject(people[i].UserID) {
			people[i].Agents = nil
			continue
		}
		accepted := make([]crewTeamAgent, 0, len(people[i].Agents))
		for _, discovered := range people[i].Agents {
			a := &discovered
			// An agent without a chat yet (a Mac default agent that is not ready) has no stable
			// identity to bind. Saving it would leave a row the check above refuses,
			// which made the whole Team registry unavailable until fixed by hand.
			if a.ChatKey == "" {
				// Every Team read sees it again: log it once per server run, not on each read.
				if _, seen := crewTeamNoChatLogged.LoadOrStore(t.ProjectID+"/"+people[i].UserID+"/"+a.Tab, true); !seen {
					log.Printf("[CREW_TEAM] project=%s event=agent_registry_row_refused person=%s tab=%q reason=%q", t.ProjectID, people[i].UserID, a.Tab, "agent has no chat yet")
				}
				continue
			}
			tab := a.Tab
			if crewTeamDefaultAgentWithKey(current, people[i].UserID, a.ChatKey, ownerDefaultKey) || tab == macDefaultTab {
				tab = macDefaultTab
			}
			source := people[i].UserID + "/" + tab
			chat := people[i].UserID + "/" + a.ChatKey
			id := bySource[source]
			if existing := byChat[chat]; existing != "" {
				if id != "" && id != existing && people[i].UserID == current.OwnerID && tab == macDefaultTab && a.ChatKey == ownerDefaultKey {
					// The default agent moved onto an existing chat. Identity and privacy stay
					// with each chat; only their role/tab changes.
					previous := current.Agents[id]
					physical, ok := strings.CutPrefix(previous.ChatKey, t.ProjectID+codeChatSideMarker)
					if ok && physical != "" && physical != macDefaultTab && bySource[previous.OwnerID+"/"+physical] == "" {
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
				inTeam := crewTeamDefaultInTeam(a.initialInTeam) || current.AgentDefaults[people[i].UserID] == crewTeamAudienceWholeTeam
				record = crewTeamRegisteredAgent{ID: id, OwnerID: people[i].UserID, Tab: tab, InTeam: inTeam, Rule: crewTeamDefaultRule()}
				created = append(created, record)
				bySource[source] = id
			}
			if record.Tab != tab && (record.Tab == macDefaultTab || tab == macDefaultTab) {
				log.Printf("[CREW_TEAM] project=%s event=agent_default_role_changed agent=%s default=%v in_team=%v", t.ProjectID, id, tab == macDefaultTab, record.InTeam)
			}
			delete(bySource, record.OwnerID+"/"+record.Tab)
			record.Tab = tab
			delete(byChat, record.OwnerID+"/"+record.ChatKey)
			byChat[chat] = id
			bySource[source] = id
			record.ChatKey, record.Name, record.Purpose, record.Place = a.ChatKey, a.Name, a.Purpose, a.Place
			record.Examples = a.Examples
			current.Agents[id] = record
			a.ID, a.InTeam, a.Address = id, record.InTeam, crewTeamAgentBridgeAddress(record.OwnerID, id)
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
	if err = fsutil.WriteJSONAtomic(filename, current, 0600); err != nil {
		return err
	}
	for _, a := range created {
		reason := "new_agent"
		if !a.InTeam {
			reason = "explicit_only_me"
		}
		log.Printf("[TEAM_AGENT_DEFAULT] project=%s actor=%s agent=%s outcome=saved in_team=%t rule=%s reason=%s", current.ProjectID, a.OwnerID, a.ID, a.InTeam, a.Rule.ChatShare, reason)
	}

	return nil
}

// Native keys are accepted only inside the transport/discovery adapter. Team
// controls and addresses use IDs; ownership is checked even for an exact ID.
func (t crewTeam) registeredAgent(person, key string) (crewTeamRegisteredAgent, bool) {
	if a, ok := t.Agents[key]; ok {
		return a, a.OwnerID == person
	}
	// Exact chat identity wins before role aliases, including while discovery
	// is catching up with a changed default agent pointer.
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
	// Only an unresolved owner binding can need the default agent role alias. Ordinary
	// registry IDs, exact chats and physical tabs above require no manifest read.
	if person == t.OwnerID {
		for _, a := range t.Agents {
			if a.OwnerID == person && a.Tab == macDefaultTab && crewTeamDefaultAgent(t, person, key) {
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
	if !ok || a.OwnerID != person || !crewTeamSourceRowAdmits(t, person, a) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(crewTeamRowRunsAs(person)) {
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
	return t.On && crewTeamMemberCurrent(t, person) && crewTeamAccountActive(t.OwnerID) && ownerOfCrewProjectID(t.ProjectID) == t.OwnerID
}

// crewTeamAgentByID admits a Team message by actual agent ownership, never by
// Crew editing rights. Only current shared agents of active members are targets.
func crewTeamAgentByID(ctx context.Context, project, caller, id string) (crewTeamAgent, string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(t, caller) {
		return crewTeamAgent{}, "", fmt.Errorf("This project's Team is not available to you")
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
	return crewTeamAgent{}, "", fmt.Errorf("That agent isn't in this project's Team now")
}

func (api *StreamingAPI) resolveTeamAgentTarget(ctx context.Context, target triggerTarget, id string) (triggerTarget, error) {
	caller := GetUserIDFromContext(ctx)
	t, err := readCrewTeam(target.CrewID)
	if err != nil {
		return target, err
	}
	a, ok := t.Agents[id]
	if !ok {
		return target, fmt.Errorf("Choose the current agent address from list_agents")
	}
	if a.OwnerID != caller {
		return crewTeamBridgeTarget(ctx, caller, crewTeamAgentAddress(t.ProjectID, id))
	}
	// Own-agent access is not a Team sharing grant.
	if !crewProjectOwnedByCaller(caller, target.Path) {
		return target, fmt.Errorf("own project unavailable")
	}
	key := crewTeamNativeChatKey(t.ProjectID, a.OwnerID, crewTeamAgent{Tab: a.Tab, ChatKey: a.ChatKey})
	target.CrewOwner = a.OwnerID
	target.Chat = &codeChat{OwnerID: a.OwnerID, ID: key, Key: key, Name: a.Name, Purpose: a.Purpose}
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
		return crewTeamAgent{}, fmt.Errorf("This project's Team is not available to you")
	}
	defaultKey := crewOwnerDefaultAgentKey(project)
	for _, p := range teamAgentsByPersonWithDefault(ctx, t, person, defaultKey) {
		if p.UserID != person {
			continue
		}
		for _, a := range p.Agents {
			if a.InTeam && (a.ChatKey == chatKey || crewTeamNativeChatKey(project, person, a) == chatKey || crewTeamDefaultAgentWithKey(t, person, chatKey, defaultKey) && crewTeamDefaultAgentWithKey(t, person, a.ChatKey, defaultKey)) {
				return a, nil
			}
		}
	}
	return crewTeamAgent{}, nil // A private sender keeps its ordinary chat endpoint.
}

// Re-resolve queued messages and replies by ID. The saved chat key cannot route
// to another person after a move, and an old ID cannot bypass removal/Only me.
func resolveTeamMessageEndpoint(ctx context.Context, endpoint agentMessageEndpoint, viewer string) (agentMessageEndpoint, string, error) {
	if err := crewTeamAdmitEndpoint(ctx, viewer, endpoint); err != nil {
		return endpoint, "", err
	}
	t, found := crewTeamOfAgent(endpoint.ID, endpoint.TeamAgentID)
	if !found {
		return endpoint, "", fmt.Errorf("That agent isn't in this project's Team now")
	}
	a, owner, err := crewTeamAgentByID(ctx, t.ProjectID, viewer, endpoint.TeamAgentID)
	if err != nil {
		return endpoint, "", err
	}
	if crewTeamRowRunsAs(owner) != endpoint.UserID || crewTeamRowProject(t, owner) != endpoint.ID {
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
		go pushCrewTeamRecordChanged(t)
	}
	return err
}
