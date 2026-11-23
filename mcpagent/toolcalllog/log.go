// Package toolcalllog provides a session-scoped registry of completed tool calls.
// It is written to by the HTTP tool execution layer (executor/handlers.go) and
// read by LLMAgentWrapper on cancellation to reconstruct conversation history.
package toolcalllog

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// StartedCall records a single tool call when it starts.
type StartedCall struct {
	SessionID string
	ID        string
	Name      string
	ArgsJSON  string
	StartedAt time.Time
}

// CompletedCall records a single tool call that finished.
type CompletedCall struct {
	SessionID   string
	ID          string // synthetic ID — consistent between ToolUse and ToolResult messages
	Name        string
	ArgsJSON    string // raw JSON string of the arguments
	Result      string
	StartedAt   time.Time
	CompletedAt time.Time
}

// SnapshotCall is a non-destructive view of a tool call, used by live monitors.
type SnapshotCall struct {
	SessionID   string
	ID          string
	Name        string
	ArgsJSON    string
	Result      string
	Status      string // "running" or "done"
	StartedAt   time.Time
	CompletedAt time.Time
}

// Hook receives real-time notifications for HTTP-level tool calls.
type Hook struct {
	OnStart func(StartedCall)
	OnEnd   func(CompletedCall)
}

var (
	mu       sync.Mutex
	registry = make(map[string][]CompletedCall) // sessionID → calls
	running  = make(map[string]map[string]StartedCall)
	hooks    = make(map[string]Hook) // sessionID → real-time event hook
	idSeq    uint64
)

// RegisterHook installs a session-scoped hook and returns an unregister function.
func RegisterHook(sessionID string, hook Hook) func() {
	mu.Lock()
	hooks[sessionID] = hook
	mu.Unlock()
	return func() {
		mu.Lock()
		if current, ok := hooks[sessionID]; ok {
			if fmt.Sprintf("%p", current.OnStart) == fmt.Sprintf("%p", hook.OnStart) &&
				fmt.Sprintf("%p", current.OnEnd) == fmt.Sprintf("%p", hook.OnEnd) {
				delete(hooks, sessionID)
			}
		}
		mu.Unlock()
	}
}

// RecordStart records a tool start and notifies any session hook.
func RecordStart(sessionID, name, argsJSON string) string {
	id := fmt.Sprintf("toolu_%d", atomic.AddUint64(&idSeq, 1))
	start := StartedCall{
		SessionID: sessionID,
		ID:        id,
		Name:      name,
		ArgsJSON:  argsJSON,
		StartedAt: time.Now(),
	}
	mu.Lock()
	if running[sessionID] == nil {
		running[sessionID] = make(map[string]StartedCall)
	}
	running[sessionID][id] = start
	hook := hooks[sessionID]
	mu.Unlock()
	if hook.OnStart != nil {
		hook.OnStart(start)
	}
	return id
}

// Record appends a completed tool call for the given session.
// Returns the generated ID (used to match ToolUse ↔ ToolResult in history).
func Record(sessionID, name, argsJSON, result string) string {
	id := fmt.Sprintf("toolu_%d", atomic.AddUint64(&idSeq, 1))
	RecordEnd(sessionID, id, name, argsJSON, result, time.Time{})
	return id
}

// RecordEnd records a completed tool call and notifies any session hook.
func RecordEnd(sessionID, id, name, argsJSON, result string, startedAt time.Time) {
	completed := CompletedCall{
		SessionID:   sessionID,
		ID:          id,
		Name:        name,
		ArgsJSON:    argsJSON,
		Result:      result,
		StartedAt:   startedAt,
		CompletedAt: time.Now(),
	}
	mu.Lock()
	if byID := running[sessionID]; byID != nil {
		if started, ok := byID[id]; ok {
			if completed.StartedAt.IsZero() {
				completed.StartedAt = started.StartedAt
			}
			if completed.Name == "" {
				completed.Name = started.Name
			}
			if completed.ArgsJSON == "" {
				completed.ArgsJSON = started.ArgsJSON
			}
			delete(byID, id)
			if len(byID) == 0 {
				delete(running, sessionID)
			}
		}
	}
	registry[sessionID] = append(registry[sessionID], completed)
	hook := hooks[sessionID]
	mu.Unlock()
	if hook.OnEnd != nil {
		hook.OnEnd(completed)
	}
}

// Snapshot returns a non-destructive view of completed and currently running
// calls for a session.
func Snapshot(sessionID string) []SnapshotCall {
	mu.Lock()
	defer mu.Unlock()
	return snapshotSessionLocked(sessionID)
}

// SnapshotBySessionPrefix returns a non-destructive view of calls for every
// session whose id starts with prefix.
func SnapshotBySessionPrefix(prefix string) []SnapshotCall {
	if prefix == "" {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()

	sessionIDs := make(map[string]struct{})
	for sessionID := range registry {
		if strings.HasPrefix(sessionID, prefix) {
			sessionIDs[sessionID] = struct{}{}
		}
	}
	for sessionID := range running {
		if strings.HasPrefix(sessionID, prefix) {
			sessionIDs[sessionID] = struct{}{}
		}
	}

	calls := make([]SnapshotCall, 0)
	for sessionID := range sessionIDs {
		calls = append(calls, snapshotSessionLocked(sessionID)...)
	}
	sortSnapshotCalls(calls)
	return calls
}

func snapshotSessionLocked(sessionID string) []SnapshotCall {
	calls := make([]SnapshotCall, 0, len(registry[sessionID])+len(running[sessionID]))
	for _, call := range registry[sessionID] {
		calls = append(calls, SnapshotCall{
			SessionID:   sessionID,
			ID:          call.ID,
			Name:        call.Name,
			ArgsJSON:    call.ArgsJSON,
			Result:      call.Result,
			Status:      "done",
			StartedAt:   call.StartedAt,
			CompletedAt: call.CompletedAt,
		})
	}
	for _, call := range running[sessionID] {
		calls = append(calls, SnapshotCall{
			SessionID: sessionID,
			ID:        call.ID,
			Name:      call.Name,
			ArgsJSON:  call.ArgsJSON,
			Status:    "running",
			StartedAt: call.StartedAt,
		})
	}
	sortSnapshotCalls(calls)
	return calls
}

func sortSnapshotCalls(calls []SnapshotCall) {
	sort.SliceStable(calls, func(i, j int) bool {
		left := calls[i].StartedAt
		if left.IsZero() {
			left = calls[i].CompletedAt
		}
		right := calls[j].StartedAt
		if right.IsZero() {
			right = calls[j].CompletedAt
		}
		if left.Equal(right) {
			return calls[i].ID < calls[j].ID
		}
		return left.Before(right)
	})
}

// GetAndClear returns all recorded calls for the session and removes them.
func GetAndClear(sessionID string) []CompletedCall {
	mu.Lock()
	calls := registry[sessionID]
	delete(registry, sessionID)
	delete(running, sessionID)
	mu.Unlock()
	return calls
}

// Clear discards all recorded calls for the session (call at start of a new run).
func Clear(sessionID string) {
	mu.Lock()
	delete(registry, sessionID)
	delete(running, sessionID)
	mu.Unlock()
}
