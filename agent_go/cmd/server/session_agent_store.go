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
	previous, exists := api.detachSessionAgent(sessionID)
	if previous != nil {
		_ = previous.Close()
	}
	return exists
}

// detachSessionAgent removes and returns the stored session agent without
// closing it yet, so callers holding a per-session turn lane can release the
// lane before running a potentially blocking Close.
func (api *StreamingAPI) detachSessionAgent(sessionID string) (*agentwrapper.LLMAgentWrapper, bool) {
	if api == nil || sessionID == "" {
		return nil, false
	}
	api.sessionAgentsMux.Lock()
	previous, exists := api.sessionAgents[sessionID]
	delete(api.sessionAgents, sessionID)
	api.sessionAgentsMux.Unlock()
	return previous, exists
}
