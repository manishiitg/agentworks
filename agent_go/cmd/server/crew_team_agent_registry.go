package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	ID      string `json:"id"`
	OwnerID string `json:"owner_id"`
	Tab     string `json:"tab"`
}

var crewTeamAgentRegistryLocks sync.Map

// assignCrewTeamAgentIDs is server-owned. Only rows already admitted by the
// Team list receive an identity. A stale saved identity grants no access: every
// delivery must find it in the current Team list again.
func assignCrewTeamAgentIDs(t crewTeam, people []crewTeamAgentsPerson) error {
	dir, err := crewTeamDir(t.ProjectID)
	if err != nil {
		return err
	}
	v, _ := crewTeamAgentRegistryLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	filename := filepath.Join(dir, "agent-identities.json")
	identities := map[string]crewTeamRegisteredAgent{}
	raw, err := os.ReadFile(filename)
	if err == nil {
		if err = json.Unmarshal(raw, &identities); err != nil || identities == nil {
			return fmt.Errorf("Team agent registry is unavailable")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	before, err := json.Marshal(identities)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for key, identity := range identities {
		if identity.OwnerID == "" || identity.Tab == "" || key != identity.OwnerID+"/"+identity.Tab || seen[identity.ID] {
			return fmt.Errorf("Team agent registry is inconsistent")
		}
		if _, err := uuid.Parse(strings.TrimPrefix(identity.ID, crewTeamAgentIDPrefix)); err != nil || !strings.HasPrefix(identity.ID, crewTeamAgentIDPrefix) {
			return fmt.Errorf("Team agent registry identity is invalid")
		}
		seen[identity.ID] = true
	}
	for i := range people {
		for j := range people[i].Agents {
			a := &people[i].Agents[j]
			tab := a.Tab
			if crewTeamMainAgent(t, people[i].UserID, a.ChatKey) || tab == macMainTab {
				tab = macMainTab
			}
			key := people[i].UserID + "/" + tab
			identity, ok := identities[key]
			if !ok {
				identity = crewTeamRegisteredAgent{ID: crewTeamAgentIDPrefix + uuid.NewString(), OwnerID: people[i].UserID, Tab: tab}
				identities[key] = identity
			}
			a.ID = identity.ID
			a.Address = crewTeamAgentAddress(t.ProjectID, identity.ID)
		}
	}
	after, err := json.Marshal(identities)
	if err != nil || bytes.Equal(before, after) {
		return err
	}
	if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filename, identities, 0600)
}

// crewTeamAgentByID admits a Team message by actual agent ownership, never by
// Crew editing rights. Only current shared agents of active members are targets.
func crewTeamAgentByID(ctx context.Context, project, caller, id string) (crewTeamAgent, string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !t.On || !t.member(caller) || !crewTeamAccountActive(caller) || !crewTeamAccountActive(t.OwnerID) || ownerOfCrewProjectID(project) != t.OwnerID {
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
	key := agent.ChatKey
	if crewRelayTwin(target.CrewID) && owner == ownerOfCrewProjectID(target.CrewID) {
		key = macSharedAgentChatKey(target.CrewID, agent.Tab)
	}
	target.CrewOwner = owner
	target.Chat = &codeChat{OwnerID: owner, ID: key, Key: key, Name: agent.Name, Purpose: agent.Purpose, SessionID: agent.sessionID}
	target.Label += " · " + agent.Name
	return target, nil
}
