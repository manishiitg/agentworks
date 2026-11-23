package tmuxinput

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const OwnerSessionOption = "@runloop_owner_session_id"

type readinessState struct {
	mu       sync.Mutex
	ready    chan struct{}
	closed   chan struct{}
	isReady  bool
	isClosed bool
}

func (s *readinessState) markReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isReady || s.isClosed {
		return
	}
	s.isReady = true
	close(s.ready)
}

func (s *readinessState) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed || s.isReady {
		return
	}
	s.isClosed = true
	close(s.closed)
}

var readinessRegistry = struct {
	sync.RWMutex
	sessions map[string]*readinessState
}{sessions: make(map[string]*readinessState)}

// MarkStarting publishes a tmux session before the provider begins waiting for
// its first prompt. Normal input to this session blocks until MarkReady; this
// prevents follow-ups, terminal paste, and auto-notifications from overtaking
// the provider's initial prompt transaction.
func MarkStarting(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	state := &readinessState{
		ready:  make(chan struct{}),
		closed: make(chan struct{}),
	}
	readinessRegistry.Lock()
	old := readinessRegistry.sessions[sessionID]
	readinessRegistry.sessions[sessionID] = state
	readinessRegistry.Unlock()
	if old != nil {
		old.markClosed()
	}
}

// MarkStartingForOwner also persists the Runloop owner on the tmux session.
// The app uses this tmux-native metadata to authenticate live reattachment
// after a backend restart, when its in-memory terminal registry is empty.
func MarkStartingForOwner(sessionID, ownerSessionID string) {
	MarkStarting(sessionID)
	sessionID = strings.TrimSpace(sessionID)
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if sessionID == "" || ownerSessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "tmux", "set-option", "-t", sessionID, OwnerSessionOption, ownerSessionID).Run()
}

// MarkReady releases normal input after the provider has confirmed its initial
// prompt (or, for launch-only sessions, confirmed the idle composer is ready).
func MarkReady(sessionID string) {
	readinessRegistry.RLock()
	state := readinessRegistry.sessions[strings.TrimSpace(sessionID)]
	readinessRegistry.RUnlock()
	if state != nil {
		state.markReady()
	}
}

// RemoveReadiness unregisters a tmux session and wakes any callers waiting for
// startup. It is safe to call repeatedly during cleanup paths.
func RemoveReadiness(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	readinessRegistry.Lock()
	state := readinessRegistry.sessions[sessionID]
	delete(readinessRegistry.sessions, sessionID)
	readinessRegistry.Unlock()
	if state != nil {
		state.markClosed()
	}
}

// WaitUntilReady is a no-op for tmux sessions not managed by a coding-provider
// startup lifecycle. Managed sessions wait for confirmed startup or cleanup.
func WaitUntilReady(ctx context.Context, sessionID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	readinessRegistry.RLock()
	state := readinessRegistry.sessions[sessionID]
	readinessRegistry.RUnlock()
	if state == nil {
		return nil
	}
	select {
	case <-state.ready:
		return nil
	case <-state.closed:
		return fmt.Errorf("tmux input session %q closed before its initial prompt became ready", sessionID)
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for tmux input session %q to finish startup: %w", sessionID, ctx.Err())
	}
}
