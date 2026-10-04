package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// OAuth callbacks may inject a model turn, so the target must be owned by the
// caller, not merely visible to an administrator or a workflow collaborator.
func (api *StreamingAPI) oauthNotificationSession(r *http.Request, requested string) (string, error) {
	session := strings.TrimSpace(requested)
	if session == "" {
		return "", nil
	}
	person := GetUserIDFromContext(r.Context())
	if person == "" || len(session) > 256 {
		return "", errors.New("invalid chat session")
	}
	if active, exists := api.getActiveSession(session); exists {
		if active.UserID == person {
			return session, nil
		}
		return "", errors.New("chat belongs to another user")
	}
	if api.eventStore != nil {
		if owner := api.eventStore.GetSessionOwner(session); owner != "" {
			if owner == person {
				return session, nil
			}
			return "", errors.New("chat belongs to another user")
		}
	}
	// A product chat can exist before its first model turn or after a restart.
	// Check the server-owned registry under this user's workspace in that case.
	registry := defaultProductConversationRegistryStore()
	doc, err := registry.loadDocument(r.Context(), productConversationRegistryPath(person))
	if err != nil {
		return "", err
	}
	for _, record := range doc.Entries {
		if record.SessionID == session {
			return session, nil
		}
	}
	return "", errors.New("unknown chat session")
}

// Use the public private-connection name, never its internal credential key.
func (api *StreamingAPI) notifyPlaceOAuthFlowOutcome(sessionID, name, notificationID string, success bool, detail string) {
	if sessionID == "" {
		return
	}
	status := "completed"
	message := fmt.Sprintf("MCP connection %q finished sign-in and the token was saved. Check its current status and tools through the API bridge before saying it is ready. This is the user's private login, not a Vault group grant. Changes apply from the next agent turn.", name)
	if !success {
		status = "failed"
		message = fmt.Sprintf("MCP sign-in for %q did not complete: %s. Tell the user and offer to retry.", name, detail)
	}
	api.emitSyntheticTurnReady(sessionID, notificationID, name, status, message)
	// Continue through the shared browser queue, which works without a resident agent.
}
