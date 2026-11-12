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
func (api *StreamingAPI) crewTeamActivityAgentForSession(ctx context.Context, caller *UserClaims, connection *desktopConnection, localProject, project, sessionID, sourceKey string) (string, error) {
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(caller.UserID)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := store.loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("Could not identify this agent's chat")
	}
	key := ""
	for _, record := range doc.Entries {
		if record.SessionID == sessionID && record.ProfileID == crewProfileID && crewResourceProjectID(record.ConversationKey) == localProject {
			if key != "" && key != record.ConversationKey {
				return "", fmt.Errorf("This session does not identify one Team agent")
			}
			key = record.ConversationKey
		}
	}
	if key == "" {
		return "", fmt.Errorf("Post Team activity from an In team agent in this Crew")
	}
	// A shared ask has its own private thread session. Its source agent comes
	// only from the server-owned ask provenance, never from a model argument or
	// a guessed thread-name alias; resolve that exact owner record.
	if sourceKey != "" {
		if !isSharedThreadKey(key) {
			return "", fmt.Errorf("This source binding is not a shared-agent thread")
		}
		record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, sourceKey)]
		if !ok || record.ProfileID != crewProfileID || record.ConversationKey != sourceKey || record.SessionID == "" || crewResourceProjectID(sourceKey) != localProject {
			return "", fmt.Errorf("This shared agent's own chat is not available")
		}
		key = sourceKey
	}
	if connection == nil {
		agent, err := crewTeamSendingAgent(ctx, project, caller.UserID, key)
		if err != nil || agent.ID == "" {
			return "", fmt.Errorf("Only an In team agent can post Team activity")
		}
		return agent.ID, nil
	}
	tab := strings.TrimPrefix(key, localProject+codeChatSideMarker)
	if key == localProject || key == crewOwnerMainChatKey(localProject) {
		tab = macMainTab
	}
	var result struct {
		People []crewTeamAgentsPerson `json:"people"`
	}
	if err := crewTeamRemoteJSON(ctx, caller.UserID, *connection, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project)+"/agents", nil, &result); err != nil {
		return "", err
	}
	for _, person := range result.People {
		if !person.You {
			continue
		}
		for _, agent := range person.Agents {
			if agent.Tab == tab && agent.InTeam {
				return agent.ID, nil
			}
		}
	}
	return "", fmt.Errorf("Only an In team agent can post Team activity")
}
