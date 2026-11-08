package virtualtools

import (
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

var sessionNotificationDestinations = struct {
	sync.RWMutex
	values map[string]*services.NotificationDestination
}{values: make(map[string]*services.NotificationDestination)}

// RegisterSessionNotificationDestination records backend-only notification
// fields for a chat/workflow session. It merges non-empty fields so a stale
// continuation request cannot erase an explicit same-session config update.
func RegisterSessionNotificationDestination(sessionID string, dest *services.NotificationDestination) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || notificationDestinationEmpty(dest) {
		return
	}
	sessionNotificationDestinations.Lock()
	defer sessionNotificationDestinations.Unlock()
	current := cloneNotificationDestination(sessionNotificationDestinations.values[sessionID])
	if current == nil {
		current = &services.NotificationDestination{}
	}
	incoming := cloneNotificationDestination(dest)
	if incoming.UserID != "" {
		current.UserID = incoming.UserID
	}
	if incoming.WorkflowName != "" {
		current.WorkflowName = incoming.WorkflowName
	}
	if incoming.WorkspacePath != "" {
		current.WorkspacePath = incoming.WorkspacePath
	}
	if len(incoming.RouteSelections) > 0 {
		current.RouteSelections = incoming.RouteSelections
	}
	if incoming.Slack != nil {
		current.Slack = incoming.Slack
	}
	if incoming.WhatsApp != nil {
		current.WhatsApp = incoming.WhatsApp
	}
	if incoming.Gmail != nil {
		current.Gmail = incoming.Gmail
	}

	sessionNotificationDestinations.values[sessionID] = current
}

// InheritSessionNotificationDestination copies the backend-owned notification
// routing from a trusted parent session to a child tool session. Workflow groups
// and execution steps use distinct MCP session IDs, but notifications emitted by
// those children must retain the originating workflow path and delivery routes.
// The destination is cloned so later child-specific updates cannot mutate the
// parent's registration.
func InheritSessionNotificationDestination(parentSessionID, childSessionID string) bool {
	parentSessionID = strings.TrimSpace(parentSessionID)
	childSessionID = strings.TrimSpace(childSessionID)
	if parentSessionID == "" || childSessionID == "" || parentSessionID == childSessionID {
		return false
	}

	sessionNotificationDestinations.Lock()
	defer sessionNotificationDestinations.Unlock()
	parent := cloneNotificationDestination(sessionNotificationDestinations.values[parentSessionID])
	if notificationDestinationEmpty(parent) {
		return false
	}
	sessionNotificationDestinations.values[childSessionID] = parent
	return true
}

// DeleteSessionNotificationDestination releases state when the owning runtime
// session is evicted.
func DeleteSessionNotificationDestination(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	sessionNotificationDestinations.Lock()
	delete(sessionNotificationDestinations.values, sessionID)
	sessionNotificationDestinations.Unlock()
}

func sessionNotificationDestination(sessionID string) *services.NotificationDestination {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	sessionNotificationDestinations.RLock()
	dest := cloneNotificationDestination(sessionNotificationDestinations.values[sessionID])
	sessionNotificationDestinations.RUnlock()
	return dest
}
