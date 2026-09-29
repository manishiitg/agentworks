package virtualtools

import (
	"context"
	"errors"
	"sort"
	"time"
)

var ErrFeedbackNotPending = errors.New("feedback request is not pending for this session")
var ErrFeedbackInvalidChoice = errors.New("response must match one of the input request's options")

// PendingForSession avoids exposing requests from other sessions, and never
// returns submitted response values (which may contain OTPs or secrets).
func (s *HumanFeedbackStore) PendingForSession(sessionID string, now time.Time) []HumanFeedbackRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []HumanFeedbackRequest{}
	if sessionID == "" {
		return rows
	}
	for _, request := range s.requests {
		if request == nil || request.SessionID != sessionID || request.IsCompleted || (!request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt)) {
			continue
		}
		copy := *request
		copy.Options = append([]string(nil), request.Options...)
		copy.UserResponse = ""
		rows = append(rows, copy)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
	return rows
}

// PendingForOperation follows trusted turn provenance across a root session
// and any child sessions it starts. A shared conversation can contain several
// calls, so a session lookup alone is not sufficient for function calls.
func (s *HumanFeedbackStore) PendingForOperation(operationID string, now time.Time) []HumanFeedbackRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := []HumanFeedbackRequest{}
	if operationID == "" {
		return rows
	}
	for _, request := range s.requests {
		if request == nil || request.OperationID != operationID || request.IsCompleted || (!request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt)) {
			continue
		}
		copy := *request
		copy.Options = append([]string(nil), request.Options...)
		copy.UserResponse = ""
		rows = append(rows, copy)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.Before(rows[j].CreatedAt) })
	return rows
}

// SubmitResponseForSession adds an atomic session binding to the same feedback
// store/waiter protocol used by the UI. Checking a public ListPending snapshot
// before SubmitResponse would race IDs reused by another session.
func (s *HumanFeedbackStore) SubmitResponseForSession(sessionID, requestID, response string, now time.Time) error {
	return s.submitScopedResponse(sessionID, "", requestID, response, now)
}

func (s *HumanFeedbackStore) submitScopedResponse(sessionID, operationID, requestID, response string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := s.requests[requestID]
	if request == nil || sessionID == "" || request.SessionID != sessionID || (operationID != "" && request.OperationID != operationID) || request.IsCompleted || (!request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt)) {
		return ErrFeedbackNotPending
	}
	if !request.AllowFeedback && len(request.Options) > 0 {
		valid := false
		for _, option := range request.Options {
			if option == response {
				valid = true
				break
			}
		}
		if !valid {
			return ErrFeedbackInvalidChoice
		}
	}
	request.UserResponse = response
	request.IsCompleted = true
	if waiter, exists := s.waiters[requestID]; exists {
		select {
		case waiter <- response:
		default:
		}
	}
	return nil
}

// Feedback operation provenance is attached by authenticated tool dispatch.
type feedbackOperationContextKey struct{}

func WithFeedbackOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, feedbackOperationContextKey{}, id)
}
func FeedbackOperationFromContext(ctx context.Context) string {
	return feedbackOperationFromContext(ctx)
}
func feedbackOperationFromContext(ctx context.Context) string {
	id, _ := ctx.Value(feedbackOperationContextKey{}).(string)
	return id
}

func (s *HumanFeedbackStore) WithdrawOperation(operationID string) {
	if operationID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, request := range s.requests {
		if request != nil && request.OperationID == operationID {
			delete(s.requests, id)
			if waiter, ok := s.waiters[id]; ok {
				close(waiter)
				delete(s.waiters, id)
			}
		}
	}
}

func (s *HumanFeedbackStore) SubmitResponseForOperation(sessionID, operationID, requestID, response string, now time.Time) error {
	return s.submitScopedResponse(sessionID, operationID, requestID, response, now)
}

// SubmitResponseForOperationID is used when a call may have spawned questions
// in several sessions. OperationID is stamped only by authenticated dispatch.
func (s *HumanFeedbackStore) SubmitResponseForOperationID(operationID, requestID, response string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := s.requests[requestID]
	if request == nil || operationID == "" || request.OperationID != operationID || request.IsCompleted || (!request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt)) {
		return ErrFeedbackNotPending
	}
	if !request.AllowFeedback && len(request.Options) > 0 {
		valid := false
		for _, option := range request.Options {
			if option == response {
				valid = true
				break
			}
		}
		if !valid {
			return ErrFeedbackInvalidChoice
		}
	}
	request.UserResponse = response
	request.IsCompleted = true
	if waiter, exists := s.waiters[requestID]; exists {
		select {
		case waiter <- response:
		default:
		}
	}
	return nil
}
