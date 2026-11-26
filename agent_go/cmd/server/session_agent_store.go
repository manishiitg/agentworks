package server

import agentwrapper "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"

// storeSessionAgent transfers conversation ownership to next. Replacing a
// wrapper closes its durable mcpagent Session after the map swap, so a prior
// turn cannot remain pinned while the new conversation owner is live.
func (api *StreamingAPI) storeSessionAgent(sessionID string, next *agentwrapper.LLMAgentWrapper) {
	if api == nil || sessionID == "" || next == nil {
		return
	}
	api.sessionAgentsMux.Lock()
	previous := api.sessionAgents[sessionID]
	api.sessionAgents[sessionID] = next
	api.sessionAgentsMux.Unlock()
	if previous != nil && previous != next {
		_ = previous.Close()
	}
}

func (api *StreamingAPI) removeSessionAgent(sessionID string) bool {
	if api == nil || sessionID == "" {
		return false
	}
	api.sessionAgentsMux.Lock()
	previous, exists := api.sessionAgents[sessionID]
	delete(api.sessionAgents, sessionID)
	api.sessionAgentsMux.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return exists
}
