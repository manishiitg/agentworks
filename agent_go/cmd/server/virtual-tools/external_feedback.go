package virtualtools

import (
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

// SubmitResponseForSession adds an atomic session binding to the same feedback
// store/waiter protocol used by the UI. Checking a public ListPending snapshot
// before SubmitResponse would race IDs reused by another session.
func (s *HumanFeedbackStore) SubmitResponseForSession(sessionID, requestID, response string, now time.Time) error {
	return s.submitResponseForSession(sessionID, requestID, response, now, nil)
}

// SubmitResponseForSessionCreatedAt also pins the pending request instance.
// Call-scoped replies first discover a question, then submit it; a request ID
// reused between those operations must not answer a replacement question.
func (s *HumanFeedbackStore) SubmitResponseForSessionCreatedAt(sessionID, requestID, response string, now, createdAt time.Time) error {
	return s.submitResponseForSession(sessionID, requestID, response, now, &createdAt)
}

func (s *HumanFeedbackStore) submitResponseForSession(sessionID, requestID, response string, now time.Time, createdAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := s.requests[requestID]
	if request == nil || sessionID == "" || request.SessionID != sessionID || request.IsCompleted || (createdAt != nil && !request.CreatedAt.Equal(*createdAt)) || (!request.ExpiresAt.IsZero() && !now.Before(request.ExpiresAt)) {
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
