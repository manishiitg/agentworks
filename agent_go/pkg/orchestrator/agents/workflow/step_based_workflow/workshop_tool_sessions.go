package step_based_workflow

import (
	"strings"
	"sync"
)

// A Workshop background agent (a Pulse reviewer, Goal Work, or a plain
// run_in_background task) calls tools over the MCP bridge with its own tool
// session, which carries no user claims. Server-side tools that act for a
// workflow must scope to the workflow and chat session that started the agent
// from trusted state, never from a model-chosen argument; this registry is
// that state. Entries live exactly as long as the agent's tool session.

// WorkshopToolSessionOwner is the workflow and Builder chat session a
// background agent's tool session belongs to.
type WorkshopToolSessionOwner struct {
	WorkspacePath string
	ChatSessionID string
}

var workshopToolSessions sync.Map // tool session ID -> WorkshopToolSessionOwner

// RegisterWorkshopToolSession records a background agent's tool session owner
// until the returned release is called. Workshop background agents register
// themselves (configureWorkshopToolAgentSessionWithID).
func RegisterWorkshopToolSession(toolSessionID, workspacePath, chatSessionID string) func() {
	toolSessionID = strings.TrimSpace(toolSessionID)
	if toolSessionID == "" || strings.TrimSpace(workspacePath) == "" {
		return func() {}
	}
	workshopToolSessions.Store(toolSessionID, WorkshopToolSessionOwner{
		WorkspacePath: strings.Trim(strings.TrimSpace(workspacePath), "/"),
		ChatSessionID: strings.TrimSpace(chatSessionID),
	})
	return func() { workshopToolSessions.Delete(toolSessionID) }
}

// LookupWorkshopToolSession returns the owner of a background agent's tool
// session, when the session is one.
func LookupWorkshopToolSession(toolSessionID string) (WorkshopToolSessionOwner, bool) {
	value, ok := workshopToolSessions.Load(strings.TrimSpace(toolSessionID))
	if !ok {
		return WorkshopToolSessionOwner{}, false
	}
	owner, ok := value.(WorkshopToolSessionOwner)
	return owner, ok
}
