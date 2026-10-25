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
	if incoming.SlackWebhook != nil {
		current.SlackWebhook = incoming.SlackWebhook
	}
	if incoming.WhatsApp != nil {
		current.WhatsApp = incoming.WhatsApp
	}
	if incoming.Gmail != nil {
		current.Gmail = incoming.Gmail
	}
	if len(incoming.ExcludeChannels) > 0 {
		current.ExcludeChannels = incoming.ExcludeChannels
	}
	if len(incoming.RunSummaryChannels) > 0 {
		current.RunSummaryChannels = incoming.RunSummaryChannels
	}
	if len(incoming.RunSummaryGmailConnectionIDs) > 0 {
		current.RunSummaryGmailConnectionIDs = incoming.RunSummaryGmailConnectionIDs
	}
	if len(incoming.RunSummaryRecipients) > 0 {
		current.RunSummaryRecipients = incoming.RunSummaryRecipients
	}
	if len(incoming.RunSummaryWebhooks) > 0 {
		current.RunSummaryWebhooks = incoming.RunSummaryWebhooks
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

// UpdateSessionSlackWebhook applies a workflow webhook change immediately to
// subsequent workflow summary delivery calls in the same coding-agent session. The URL stays
// in backend memory and is never returned to the model.
func UpdateSessionSlackWebhook(sessionID string, webhook *services.SlackWebhookDest) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	sessionNotificationDestinations.Lock()
	defer sessionNotificationDestinations.Unlock()
	dest := cloneNotificationDestination(sessionNotificationDestinations.values[sessionID])
	if dest == nil {
		dest = &services.NotificationDestination{}
	}
	if webhook == nil {
		dest.SlackWebhook = nil
	} else {
		dest.SlackWebhook = &services.SlackWebhookDest{
			SecretName: webhook.SecretName,
			URL:        webhook.URL,
		}
	}
	sessionNotificationDestinations.values[sessionID] = dest
}

// UpdateSessionGmailSender applies a validated workflow sender edit to the
// current session only. Children created afterward inherit this new selection.
// Explicit clear must replace, rather than merge, the old sender and overrides.
func UpdateSessionGmailSender(sessionID, workspacePath, senderID string) bool {
	sessionID, workspacePath = strings.TrimSpace(sessionID), strings.TrimSpace(workspacePath)
	if sessionID == "" || workspacePath == "" {
		return false
	}
	sessionNotificationDestinations.Lock()
	defer sessionNotificationDestinations.Unlock()
	dest := cloneNotificationDestination(sessionNotificationDestinations.values[sessionID])
	if dest == nil || dest.WorkspacePath != workspacePath {
		return false
	}
	if dest.Gmail == nil {
		dest.Gmail = &services.GmailDest{}
	}
	dest.Gmail.Workspace = workspacePath
	dest.Gmail.ConnectionIDs = nil
	if senderID = strings.TrimSpace(senderID); senderID != "" {
		dest.Gmail.ConnectionIDs = []string{senderID}
	}
	dest.RunSummaryGmailConnectionIDs = nil
	sessionNotificationDestinations.values[sessionID] = dest
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
