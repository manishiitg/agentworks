package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// A chat/MCP tool posts only as its actual registered session. The model never
// selects a different agent_id, even another agent owned by the same person.
func (api *StreamingAPI) crewTeamActivityAgentForSession(ctx context.Context, caller *UserClaims, connection *desktopConnection, localProject, project, sessionID, sourceKey string) (crewTeamRegisteredAgent, error) {
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(caller.UserID)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := store.loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return crewTeamRegisteredAgent{}, fmt.Errorf("Could not identify this agent's chat")
	}
	key := ""
	for _, record := range doc.Entries {
		if record.SessionID == sessionID && record.ProfileID == crewProfileID && crewResourceProjectID(record.ConversationKey) == localProject {
			if key != "" && key != record.ConversationKey {
				return crewTeamRegisteredAgent{}, fmt.Errorf("This session does not identify one Team agent")
			}
			key = record.ConversationKey
		}
	}
	if key == "" {
		return crewTeamRegisteredAgent{}, fmt.Errorf("Post Team activity from an In team agent in this Crew")
	}
	// A shared ask has its own private thread session. Its source agent comes
	// only from the server-owned ask provenance, never from a model argument or
	// a guessed thread-name alias; resolve that exact owner record.
	if sourceKey != "" {
		if !isSharedThreadKey(key) {
			return crewTeamRegisteredAgent{}, fmt.Errorf("This source binding is not a shared-agent thread")
		}
		record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, sourceKey)]
		if !ok || record.ProfileID != crewProfileID || record.ConversationKey != sourceKey || record.SessionID == "" || crewResourceProjectID(sourceKey) != localProject {
			return crewTeamRegisteredAgent{}, fmt.Errorf("This shared agent's own chat is not available")
		}
		key = sourceKey
	}
	if sourceKey == "" && isSharedThreadKey(key) {
		return crewTeamRegisteredAgent{}, fmt.Errorf("Post activity from your own agent's chat")
	}
	actor := crewTeamRegisteredAgent{OwnerID: caller.UserID, ChatKey: key, Name: doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)].Title}
	if actor.Name == "" {
		actor.Name = "Agent"
	}
	if connection == nil {
		team, err := readCrewTeam(project)
		if err != nil || !crewTeamAgentsVisible(team, caller.UserID) {
			return crewTeamRegisteredAgent{}, fmt.Errorf("This Crew's Team is not available to you")
		}
		for _, agent := range team.Agents {
			if agent.OwnerID == caller.UserID && agent.ChatKey == key {
				actor = agent
				break
			}
		}
	} else {
		tab := strings.TrimPrefix(key, localProject+codeChatSideMarker)
		if key == localProject || key == crewOwnerMainChatKey(localProject) {
			tab = macMainTab
		}
		var result struct {
			People []crewTeamAgentsPerson `json:"people"`
		}
		if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/agents", nil, &result); err != nil {
			return crewTeamRegisteredAgent{}, err
		}
		actor.ChatKey = project + codeChatSideMarker + tab
		for _, person := range result.People {
			if person.You {
				for _, agent := range person.Agents {
					if agent.Tab == tab {
						actor.ID, actor.ChatKey, actor.Name, actor.InTeam = agent.ID, agent.ChatKey, agent.Name, agent.InTeam
					}
				}
			}
		}
	}
	if sourceKey != "" && !actor.InTeam {
		return crewTeamRegisteredAgent{}, fmt.Errorf("A shared ask cannot post Only-you activity. Post from your own chat")
	}
	return actor, nil
}

// Missing registry rows may post privately only after proving the caller owns
// the native chat. A paired Mac vouches only for its own owner's local chats.
func (api *StreamingAPI) crewTeamActivityActorForRequest(r *http.Request, team crewTeam, person, id, key, name string) (crewTeamRegisteredAgent, error) {
	if id != "" {
		actor, ok := team.Agents[id]
		if !ok || actor.OwnerID != person {
			return crewTeamRegisteredAgent{}, fmt.Errorf("Not your agent")
		}
		return actor, nil
	}
	if crewResourceProjectID(key) != team.ProjectID || isSharedThreadKey(key) {
		return crewTeamRegisteredAgent{}, fmt.Errorf("Not this Crew's own chat")
	}
	if person == team.OwnerID && crewMacPairSent(r, team.ProjectID) {
		tab := strings.TrimPrefix(key, team.ProjectID+codeChatSideMarker)
		if !crewMacTabID(tab) {
			return crewTeamRegisteredAgent{}, fmt.Errorf("Invalid chat")
		}
		return crewTeamRegisteredAgent{OwnerID: person, ChatKey: key, Name: crewMacTabName(name)}, nil
	}
	path := productConversationRegistryPath(person)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := defaultProductConversationRegistryStore().loadDocument(r.Context(), path)
	mu.Unlock()
	if err != nil {
		return crewTeamRegisteredAgent{}, err
	}
	record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)]
	if !ok || record.ConversationKey != key || record.SessionID == "" || record.ProfileID != crewProfileID {
		return crewTeamRegisteredAgent{}, fmt.Errorf("Not your agent")
	}
	return crewTeamRegisteredAgent{OwnerID: person, ChatKey: key, Name: record.Title}, nil
}
