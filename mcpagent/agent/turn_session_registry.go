package mcpagent

import (
	"strings"
	"sync"
)

// turnSessions keeps the host-facing Session alive for exactly as long as the
// provider conversation is reusable. MCP connection sessions and provider
// conversations have the same external session ID but different internal
// registries; this registry deliberately owns only the normalized turn API.
var turnSessions sync.Map // map[string]*Session

func registerTurnSession(sessionID string, session *Session) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || session == nil {
		return
	}
	turnSessions.Store(sessionID, session)
	notifyNativeRuntime(session.agent)
}

// LookupSession resolves the durable, transport-neutral turn session for a
// provider conversation. Hosts use Session.Send instead of inspecting the
// provider, transport, tmux pane, or continuation handle themselves.
func LookupSession(sessionID string) (*Session, bool) {
	value, ok := turnSessions.Load(strings.TrimSpace(sessionID))
	if !ok {
		return nil, false
	}
	session, ok := value.(*Session)
	return session, ok && session != nil
}

func unregisterTurnSession(session *Session) {
	if session == nil || session.agent == nil {
		return
	}
	sessionID := strings.TrimSpace(session.agent.sessionID)
	if sessionID == "" {
		return
	}
	// A newer immutable Agent may already have replaced this session under the
	// same conversation ID. Never let the older Session.Close delete the newer
	// provider conversation.
	if current, ok := turnSessions.Load(sessionID); ok && current == session {
		turnSessions.Delete(sessionID)
	}
}

func closeTurnSession(sessionID string) {
	if session, ok := LookupSession(sessionID); ok {
		_ = session.Close()
	}
}

func closeTurnSessionForAgent(agent *Agent) {
	if agent == nil {
		return
	}
	session, ok := LookupSession(agent.sessionID)
	if !ok || session.agent != agent {
		return
	}
	_ = session.Close()
}
