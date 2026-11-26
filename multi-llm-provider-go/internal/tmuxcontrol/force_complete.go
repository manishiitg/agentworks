package tmuxcontrol

import (
	"errors"
	"strings"
	"sync"
)

// ErrForceComplete is returned by tmux-backed adapters when an operator marks
// the visible terminal complete and wants the normal generation path to resume.
var ErrForceComplete = errors.New("tmux session force-completed by operator")

var forceCompleteSessions = struct {
	sync.Mutex
	values map[string]struct{}
}{
	values: make(map[string]struct{}),
}

// RequestForceComplete records that the next poll of sessionName should return
// through the adapter's normal response path instead of waiting for more TUI
// state changes.
func RequestForceComplete(sessionName string) bool {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return false
	}
	forceCompleteSessions.Lock()
	forceCompleteSessions.values[sessionName] = struct{}{}
	forceCompleteSessions.Unlock()
	return true
}

// ConsumeForceComplete returns true once per requested tmux session.
func ConsumeForceComplete(sessionName string) bool {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return false
	}
	forceCompleteSessions.Lock()
	_, ok := forceCompleteSessions.values[sessionName]
	if ok {
		delete(forceCompleteSessions.values, sessionName)
	}
	forceCompleteSessions.Unlock()
	return ok
}
