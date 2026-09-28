package virtualtools

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// HumanFeedbackRequest represents a pending feedback request
type HumanFeedbackRequest struct {
	OperationID    string    `json:"operation_id,omitempty"` // Trusted turn provenance, never model arguments.
	UniqueID       string    `json:"unique_id"`
	MessageForUser string    `json:"message_for_user"`
	Context        string    `json:"context,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	Options        []string  `json:"options,omitempty"`
	AllowFeedback  bool      `json:"allow_feedback"`
	UserResponse   string    `json:"-"`
	IsCompleted    bool      `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// BotSessionCheckerFunc checks if a given session ID belongs to a bot session.
// Set by the server layer to enable skip-delay behavior.
type BotSessionCheckerFunc func(sessionID string) bool

// HumanFeedbackStore manages interactive feedback requests
type HumanFeedbackStore struct {
	requests          map[string]*HumanFeedbackRequest
	waiters           map[string]chan string
	mu                sync.RWMutex
	botSessionChecker BotSessionCheckerFunc
}

// SetBotSessionChecker sets the function to check if a session is a bot session
func (s *HumanFeedbackStore) SetBotSessionChecker(fn BotSessionCheckerFunc) {
	s.botSessionChecker = fn
}

// Global singleton instance
var (
	globalHumanFeedbackStore *HumanFeedbackStore
	humanFeedbackStoreOnce   sync.Once
)

// GetHumanFeedbackStore returns the global singleton instance
func GetHumanFeedbackStore() *HumanFeedbackStore {
	humanFeedbackStoreOnce.Do(func() {
		globalHumanFeedbackStore = &HumanFeedbackStore{
			requests: make(map[string]*HumanFeedbackRequest),
			waiters:  make(map[string]chan string),
		}
	})
	return globalHumanFeedbackStore
}

// CreateRequest creates a new feedback request
func (s *HumanFeedbackStore) CreateRequest(uniqueID, message string) error {
	return s.CreateRequestWithNotification(context.Background(), uniqueID, message, "", nil, nil)
}

// CreateRequestWithoutNotification creates a new feedback request without sending any notifications
// This is used for blocking_human_feedback events that should only appear in the frontend UI
func (s *HumanFeedbackStore) CreateRequestWithoutNotification(uniqueID, message string) error {
	return s.CreatePendingRequest(uniqueID, message, "", "", nil, true, defaultHumanFeedbackTimeout)
}

// CreatePendingRequest registers the authoritative UI-visible state for a
// blocking human request. Frontends list this store directly instead of
// relying on whether the originating session's event stream is mounted.
func (s *HumanFeedbackStore) CreatePendingRequest(uniqueID, message, contextMsg, sessionID string, options []string, allowFeedback bool, timeout time.Duration, operationID ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if request already exists
	if existingRequest, exists := s.requests[uniqueID]; exists {
		// If the request is completed, clean it up and allow creating a new one
		if existingRequest.IsCompleted {
			// Clean up completed request
			delete(s.requests, uniqueID)
			if waiter, exists := s.waiters[uniqueID]; exists {
				close(waiter)
				delete(s.waiters, uniqueID)
			}
		} else {
			// Request exists and is still pending - cannot create duplicate
			return fmt.Errorf("feedback request %s already exists and is pending", uniqueID)
		}
	}

	s.requests[uniqueID] = &HumanFeedbackRequest{
		UniqueID:       uniqueID,
		MessageForUser: message,
		Context:        contextMsg,
		SessionID:      sessionID,
		Options:        append([]string(nil), options...),
		AllowFeedback:  allowFeedback,
		IsCompleted:    false,
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(timeout),
	}

	if len(operationID) > 0 {
		s.requests[uniqueID].OperationID = operationID[0]
	}
	s.waiters[uniqueID] = make(chan string, 1)

	return nil
}

// ListPending returns safe copies of currently answerable requests. Responses
// are deliberately never included because they can contain OTPs or secrets.
func (s *HumanFeedbackStore) ListPending(now time.Time) []HumanFeedbackRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pending := make([]HumanFeedbackRequest, 0, len(s.requests))
	for _, request := range s.requests {
		if request == nil || request.IsCompleted || (!request.ExpiresAt.IsZero() && now.After(request.ExpiresAt)) {
			continue
		}
		copy := *request
		copy.Options = append([]string(nil), request.Options...)
		copy.UserResponse = ""
		pending = append(pending, copy)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].CreatedAt.Before(pending[j].CreatedAt) })
	return pending
}

// CreateRequestWithNotification creates a new feedback request and sends a notification
// after 2 minutes if no response arrives via the in-app UI first.
//
// dest is an optional destination hint passed through to the notification
// fanout — connectors use it to override their workspace-wide default
// (per-user prefs and per-request hints both flow through this). Pass nil
// for the legacy "use whatever the connectors are configured for" behavior.
func (s *HumanFeedbackStore) CreateRequestWithNotification(ctx context.Context, uniqueID, message, contextMsg string, buttonOptions *services.ButtonOptions, dest *services.NotificationDestination) error {
	// First register the request (without notifications)
	if err := s.CreateRequestWithoutNotification(uniqueID, message); err != nil {
		return err
	}

	s.ScheduleNotification(ctx, uniqueID, message, contextMsg, buttonOptions, dest)
	return nil
}

// ScheduleNotification sends the historical two-minute connector reminder for
// an existing pending request.
func (s *HumanFeedbackStore) ScheduleNotification(ctx context.Context, uniqueID, message, contextMsg string, buttonOptions *services.ButtonOptions, dest *services.NotificationDestination) {
	s.ScheduleNotificationAfter(ctx, uniqueID, message, contextMsg, buttonOptions, dest, 2*time.Minute)
}

// ScheduleNotificationAfter is the delay-aware notification path. Normal
// feedback requests keep the historical two-minute reminder through
// ScheduleNotification; short-lived human-only tool calls pass zero so the
// user is notified immediately.
func (s *HumanFeedbackStore) ScheduleNotificationAfter(ctx context.Context, uniqueID, message, contextMsg string, buttonOptions *services.ButtonOptions, dest *services.NotificationDestination, delay time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}
	if delay < 0 {
		delay = 0
	}

	// Start delayed notification, then check if the user responded.
	// If no response, send notification via connectors.
	// For bot sessions, send immediately (the thread IS the primary interface).
	go func() {
		// Check if this is a bot session (extract sessionID from uniqueID pattern)
		isBotSession := false
		if s.botSessionChecker != nil {
			// uniqueID often contains the sessionID — check if any active bot session matches
			isBotSession = s.botSessionChecker(uniqueID)
		}

		if !isBotSession && delay > 0 {
			time.Sleep(delay)
		} else {
			log.Printf("[HUMAN_FEEDBACK_STORE] Sending notification immediately for %s", uniqueID)
		}

		// Check if user has already responded
		s.mu.RLock()
		request, exists := s.requests[uniqueID]
		hasResponded := exists && request != nil && request.IsCompleted
		s.mu.RUnlock()

		if !exists {
			log.Printf("[HUMAN_FEEDBACK_STORE] Request %s no longer exists, skipping delayed notification", uniqueID)
			return
		}

		if hasResponded {
			log.Printf("[HUMAN_FEEDBACK_STORE] User already responded to %s, skipping connector notification", uniqueID)
			return
		}

		// The user has not responded during the configured delay.
		log.Printf("[HUMAN_FEEDBACK_STORE] No response after %s for %s, sending connector notification", delay, uniqueID)
		notificationManager := services.GetNotificationManager()
		if notificationManager == nil {
			log.Printf("[HUMAN_FEEDBACK_STORE] Notification manager not available")
			return
		}

		if buttonOptions != nil {
			log.Printf("[HUMAN_FEEDBACK_STORE] Sending feedback notification - buttonOptions: YesNoOnly=%v, YesLabel=%s, NoLabel=%s, Options=%v",
				buttonOptions.YesNoOnly, buttonOptions.YesLabel, buttonOptions.NoLabel, buttonOptions.Options)
		}

		// Send notification via notification manager (async, non-blocking)
		// This will send to all enabled connectors (Slack, WhatsApp)
		if err := notificationManager.SendNotification(ctx, uniqueID, message, contextMsg, buttonOptions, dest); err != nil {
			// Log error but don't fail - this is a reminder notification
			log.Printf("[HUMAN_FEEDBACK_STORE] Failed to send connector notification: %v", err)
		} else {
			log.Printf("[HUMAN_FEEDBACK_STORE] ✅ Connector notification sent for %s", uniqueID)
		}
	}()
}

// GetResponse gets a user response for a feedback request (if available)
func (s *HumanFeedbackStore) GetResponse(uniqueID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	request, exists := s.requests[uniqueID]
	if !exists {
		return "", false
	}

	if !request.IsCompleted || request.UserResponse == "" {
		return "", false
	}

	return request.UserResponse, true
}

// SubmitResponse submits a user response to a feedback request
func (s *HumanFeedbackStore) SubmitResponse(uniqueID, response string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, exists := s.requests[uniqueID]
	if !exists {
		return fmt.Errorf("feedback request %s not found", uniqueID)
	}

	if request.IsCompleted {
		return fmt.Errorf("feedback request %s already completed", uniqueID)
	}

	request.UserResponse = response
	request.IsCompleted = true

	// Signal waiter
	if waiter, exists := s.waiters[uniqueID]; exists {
		select {
		case waiter <- response:
		default:
		}
	}

	return nil
}

// ErrFeedbackCancelled is returned when the run that asked was stopped.
var ErrFeedbackCancelled = errors.New("feedback request cancelled: the run was stopped")

// WaitForResponse blocks until user responds or timeout occurs
func (s *HumanFeedbackStore) WaitForResponse(uniqueID string, timeout time.Duration) (string, error) {
	return s.WaitForResponseCtx(context.Background(), uniqueID, timeout)
}

// WaitForResponseCtx is WaitForResponse that also ends when ctx is
// cancelled (the run or turn was stopped): the question is removed at once,
// so it stops showing as waiting and a late answer is refused. A deadline on
// ctx does not end the wait; only an explicit cancellation does. Before,
// a stopped workflow kept its question pending and its goroutine blocked
// until the wait timed out (PLAT-368).
func (s *HumanFeedbackStore) WaitForResponseCtx(parent context.Context, uniqueID string, timeout time.Duration) (string, error) {
	s.mu.RLock()
	waiter, exists := s.waiters[uniqueID]
	s.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("feedback request %s not found", uniqueID)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	defer func() {
		// Answers can contain OTPs or other private input. Remove both the answer
		// and waiter as soon as the waiting tool consumes or expires the request.
		// Only this wait's own entry: a later request reusing the ID stays.
		s.mu.Lock()
		if s.waiters[uniqueID] == waiter {
			delete(s.requests, uniqueID)
			delete(s.waiters, uniqueID)
		}
		s.mu.Unlock()
	}()

	var cancelled <-chan struct{}
	if parent != nil && parent.Done() != nil {
		cancelled = parent.Done()
	}
	for {
		select {
		case response, open := <-waiter:
			if !open {
				return "", ErrFeedbackCancelled
			}
			return response, nil
		case <-timer.C:
			return "", fmt.Errorf("timeout waiting for feedback: %w", context.DeadlineExceeded)
		case <-cancelled:
			if errors.Is(parent.Err(), context.Canceled) {
				return "", ErrFeedbackCancelled
			}
			// A deadline elapsed: keep waiting for the requested time.
			cancelled = nil
		}
	}
}

// Cleanup removes old requests (optional cleanup)
func (s *HumanFeedbackStore) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for uniqueID, request := range s.requests {
		if request.CreatedAt.Before(cutoff) {
			delete(s.requests, uniqueID)
			if waiter, exists := s.waiters[uniqueID]; exists {
				close(waiter)
				delete(s.waiters, uniqueID)
			}
		}
	}
}
