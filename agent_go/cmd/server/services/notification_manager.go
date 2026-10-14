package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// ButtonOptions represents button configuration for interactive notifications
type ButtonOptions struct {
	YesNoOnly bool     // If true, show only Yes/No buttons
	YesLabel  string   // Custom label for Yes button (default: "Approve")
	NoLabel   string   // Custom label for No button (default: "Reject")
	Options   []string // Array of option labels for multiple choice (renders as buttons)
}

// NotificationConnector is the interface that all notification connectors must implement
// This allows for multiple connectors (Slack, WhatsApp) to be registered
type NotificationConnector interface {
	// Name returns the name of the connector (e.g., "slack", "whatsapp")
	Name() string

	// IsEnabled checks if the connector is enabled and ready to send notifications
	IsEnabled() bool

	// SendNotification sends a notification to the connector
	// uniqueID: unique identifier for the feedback request
	// message: the message/question to send
	// contextMsg: additional context information
	// buttonOptions: optional button configuration (nil if no buttons)
	// dest: optional destination hint (per-request channel/thread/recipient
	//   override). Nil means "use this connector's configured default".
	// Returns: message identifier (e.g., Slack timestamp) and error.
	// A connector that receives a dest it can't satisfy and has no default
	// should return ("", nil) to skip silently.
	SendNotification(ctx context.Context, uniqueID string, message string, contextMsg string, buttonOptions *ButtonOptions, dest *NotificationDestination) (string, error)
}

// UserNotificationConnector is an optional extension for connectors that can
// send a non-blocking message to a human without creating a feedback request.
type UserNotificationConnector interface {
	SendUserNotification(ctx context.Context, message string, contextMsg string, dest *NotificationDestination) (string, error)
}

// ConnectorResult is the per-connector outcome of a synchronous user notification.
// Delivered = OK && MsgID != ""; a connector that had no destination to send to
// returns OK with an empty MsgID (a "skip", not a delivery).
type ConnectorResult struct {
	Channel string
	OK      bool
	MsgID   string
	Err     string
}

// FeedbackResponseFunc is a function type for submitting feedback responses (to avoid import cycle)
type FeedbackResponseFunc func(uniqueID string, response string) error

// NotificationManager manages all notification connectors
// It sends notifications to all enabled connectors when a feedback request is created
// It also receives notifications from connectors and updates the feedback store
type NotificationManager struct {
	connectors             map[string]NotificationConnector
	submitFeedbackResponse FeedbackResponseFunc
	mu                     sync.RWMutex
}

var (
	globalNotificationManager *NotificationManager
	notificationManagerOnce   sync.Once
)

// GetNotificationManager returns the global notification manager instance
func GetNotificationManager() *NotificationManager {
	notificationManagerOnce.Do(func() {
		globalNotificationManager = &NotificationManager{
			connectors: make(map[string]NotificationConnector),
		}
	})
	return globalNotificationManager
}

// RegisterConnector registers a notification connector
func (nm *NotificationManager) RegisterConnector(connector NotificationConnector) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	name := connector.Name()
	nm.connectors[name] = connector
	log.Printf("[NOTIFICATION_MANAGER] Registered connector: %s", name)
}

// UnregisterConnector removes a notification connector
func (nm *NotificationManager) UnregisterConnector(name string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	delete(nm.connectors, name)
	log.Printf("[NOTIFICATION_MANAGER] Unregistered connector: %s", name)
}

// SendNotification sends a notification to all enabled connectors
// This is called by the feedback store when a new request is created.
// dest is an optional destination hint (channel/thread/recipient) that
// per-connector resolvers consult before falling back to user prefs and
// then to workspace defaults.
func (nm *NotificationManager) SendNotification(ctx context.Context, uniqueID string, message string, contextMsg string, buttonOptions *ButtonOptions, dest *NotificationDestination) error {
	nm.mu.RLock()
	connectors := make([]NotificationConnector, 0, len(nm.connectors))
	for _, connector := range nm.connectors {
		if connector.IsEnabled() {
			connectors = append(connectors, connector)
		}
	}
	nm.mu.RUnlock()

	if len(connectors) == 0 {
		log.Printf("[NOTIFICATION_MANAGER] No enabled connectors available for notification")
		return nil // Not an error, just no connectors enabled
	}

	// Send to all enabled connectors (async, non-blocking)
	for _, connector := range connectors {
		go func(conn NotificationConnector) {
			msgID, err := conn.SendNotification(ctx, uniqueID, message, contextMsg, buttonOptions, dest)
			if err != nil {
				log.Printf("[NOTIFICATION_MANAGER] Failed to send notification via %s: %v", conn.Name(), err)
			} else if msgID != "" {
				log.Printf("[NOTIFICATION_MANAGER] ✅ Notification sent via %s (msgID: %s)", conn.Name(), msgID)
			}
		}(connector)
	}

	return nil
}

// SendUserNotification sends a non-blocking user notification to all enabled
// connectors that support the optional UserNotificationConnector extension.
func (nm *NotificationManager) SendUserNotification(ctx context.Context, message string, contextMsg string, dest *NotificationDestination) error {
	nm.mu.RLock()
	connectors := make([]NotificationConnector, 0, len(nm.connectors))
	for _, connector := range nm.connectors {
		if connector.IsEnabled() {
			connectors = append(connectors, connector)
		}
	}
	nm.mu.RUnlock()

	if len(connectors) == 0 {
		log.Printf("[NOTIFICATION_MANAGER] No enabled connectors available for user notification")
		return nil
	}

	for _, connector := range connectors {
		userNotifier, ok := connector.(UserNotificationConnector)
		if !ok {
			continue
		}
		go func(conn NotificationConnector, notifier UserNotificationConnector) {
			msgID, err := notifier.SendUserNotification(ctx, message, contextMsg, dest)
			if err != nil {
				log.Printf("[NOTIFICATION_MANAGER] Failed to send user notification via %s: %v", conn.Name(), err)
			} else if msgID != "" {
				log.Printf("[NOTIFICATION_MANAGER] ✅ User notification sent via %s (msgID: %s)", conn.Name(), msgID)
			}
		}(connector, userNotifier)
	}

	return nil
}

// SendUserNotificationSync sends to every enabled user-notification connector
// SYNCHRONOUSLY and returns a per-connector result. Unlike SendUserNotification
// (fire-and-forget, swallows errors), this waits for delivery so the caller — e.g.
// the workflow summary delivery tool — can report real status back to the agent.
//
// Each send runs on a context that is DETACHED from the caller's cancellation
// (context.WithoutCancel) but time-bounded, so it is not killed when the agent's
// request context is canceled at turn end (the cause of the "gws … signal: killed"
// failures) and a stuck connector can't hang the turn forever.
//
// excludeChannels lets a single notification opt OUT of specific account-level
// channels by connector Name() (e.g. "gmail"). This is how a per-workflow
// notification preference — declared in workflow.json capabilities.notifications
// and applied by the backend — suppresses an inherited global channel for that
// one send without touching the account-wide config. Names are matched
// case-insensitively; unknown names are ignored.
func (nm *NotificationManager) SendUserNotificationSync(ctx context.Context, message string, contextMsg string, dest *NotificationDestination, excludeChannels ...string) []ConnectorResult {
	excluded := make(map[string]bool, len(excludeChannels))
	for _, name := range excludeChannels {
		if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
			excluded[trimmed] = true
		}
	}

	nm.mu.RLock()
	connectors := make([]NotificationConnector, 0, len(nm.connectors))
	for _, connector := range nm.connectors {
		if connector.IsEnabled() && !excluded[strings.ToLower(connector.Name())] {
			connectors = append(connectors, connector)
		}
	}
	nm.mu.RUnlock()

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()

	results := make([]ConnectorResult, 0, len(connectors))
	for _, connector := range connectors {
		notifier, ok := connector.(UserNotificationConnector)
		if !ok {
			continue
		}
		msgID, err := notifier.SendUserNotification(sendCtx, message, contextMsg, dest)
		res := ConnectorResult{Channel: connector.Name(), OK: err == nil, MsgID: msgID}
		if err != nil {
			res.Err = err.Error()
			log.Printf("[NOTIFICATION_MANAGER] Failed to send user notification via %s: %v", connector.Name(), err)
		} else if msgID != "" {
			log.Printf("[NOTIFICATION_MANAGER] ✅ User notification sent via %s (msgID: %s)", connector.Name(), msgID)
		}
		results = append(results, res)
	}
	return results
}

// GetConnector returns a specific connector by name
func (nm *NotificationManager) GetConnector(name string) (NotificationConnector, error) {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	connector, exists := nm.connectors[name]
	if !exists {
		return nil, fmt.Errorf("connector %s not found", name)
	}

	return connector, nil
}

// ListEnabledConnectors returns the sorted names of connectors that are
// currently enabled (IsEnabled() == true). Used to surface the live set of
// delivery channels — e.g. to bake into a tool description at session start.
func (nm *NotificationManager) ListEnabledConnectors() []string {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	names := make([]string, 0, len(nm.connectors))
	for name, connector := range nm.connectors {
		if connector.IsEnabled() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ListConnectors returns a list of all registered connector names
func (nm *NotificationManager) ListConnectors() []string {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	names := make([]string, 0, len(nm.connectors))
	for name := range nm.connectors {
		names = append(names, name)
	}

	return names
}

// SetFeedbackResponseFunc sets the feedback store function for submitting responses
// This is called from server initialization to connect to the feedback store
func (nm *NotificationManager) SetFeedbackResponseFunc(fn FeedbackResponseFunc) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.submitFeedbackResponse = fn
	log.Printf("[NOTIFICATION_MANAGER] Feedback response function set")
}

// ReceiveNotification receives a notification/reply from a connector
// This is called by connectors when they receive a reply (e.g., Slack thread reply)
// The notification manager then updates the human feedback store
func (nm *NotificationManager) ReceiveNotification(uniqueID string, response string, connectorName string) error {
	nm.mu.RLock()
	submitFn := nm.submitFeedbackResponse
	nm.mu.RUnlock()

	if submitFn == nil {
		log.Printf("[NOTIFICATION_MANAGER] ⚠️  Feedback store not available, cannot submit response from %s", connectorName)
		return fmt.Errorf("feedback store not available")
	}

	if err := submitFn(uniqueID, response); err != nil {
		log.Printf("[NOTIFICATION_MANAGER] ❌ Failed to submit feedback from %s for unique_id %s: %v", connectorName, uniqueID, err)
		return fmt.Errorf("failed to submit feedback: %w", err)
	}

	log.Printf("[NOTIFICATION_MANAGER] ✅ Successfully received notification from %s for unique_id: %s", connectorName, uniqueID)
	return nil
}
