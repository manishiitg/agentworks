package server

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// RuntimePhase is the coordinator's normalized lifecycle vocabulary exposed by
// every session-facing runtime API.
type RuntimePhase string

const (
	runtimePhaseStarting  RuntimePhase = "starting"
	runtimePhaseRunning   RuntimePhase = "running"
	runtimePhaseWaiting   RuntimePhase = "waiting"
	runtimePhaseIdle      RuntimePhase = "idle"
	runtimePhaseCompleted RuntimePhase = "completed"
	runtimePhaseFailed    RuntimePhase = "failed"
	runtimePhaseCanceled  RuntimePhase = "canceled"
)

type RuntimeForegroundSnapshot struct {
	Busy      bool `json:"busy"`
	HasCancel bool `json:"has_cancel"`
	CanSteer  bool `json:"can_steer"`
	Synthetic bool `json:"synthetic"`
}

type RuntimeChildSnapshot struct {
	ExecutionID string     `json:"execution_id"`
	Kind        string     `json:"kind,omitempty"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type RuntimeBackgroundSnapshot struct {
	AgentID     string     `json:"agent_id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type RuntimeTerminalSnapshot struct {
	TerminalID  string    `json:"terminal_id"`
	ExecutionID string    `json:"execution_id,omitempty"`
	State       string    `json:"state"`
	Active      bool      `json:"active"`
	HasTmux     bool      `json:"has_tmux"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RuntimeSnapshot is the immutable, revisioned session lifecycle read model.
// It is assembled from the domain stores and callers always receive deep copies.
type RuntimeSnapshot struct {
	SessionID        string                      `json:"session_id"`
	Generation       uint64                      `json:"generation"`
	Revision         uint64                      `json:"revision"`
	Phase            RuntimePhase                `json:"phase"`
	Reason           string                      `json:"reason,omitempty"`
	RawSessionStatus string                      `json:"raw_session_status,omitempty"`
	ForegroundTurn   RuntimeForegroundSnapshot   `json:"foreground_turn"`
	ChildExecutions  []RuntimeChildSnapshot      `json:"child_executions,omitempty"`
	BackgroundAgents []RuntimeBackgroundSnapshot `json:"background_agents,omitempty"`
	BackgroundLive   bool                        `json:"background_live"`
	Terminals        []RuntimeTerminalSnapshot   `json:"terminals,omitempty"`
	TerminalBusy     bool                        `json:"terminal_busy"`
	WaitingForUser   bool                        `json:"waiting_for_user"`
	WaitingMessage   string                      `json:"waiting_message,omitempty"`
	LastProgressAt   time.Time                   `json:"last_progress_at"`
	StartedAt        time.Time                   `json:"started_at"`
	CompletedAt      *time.Time                  `json:"completed_at,omitempty"`
	ObservedAt       time.Time                   `json:"observed_at"`
}

type runtimeCoordinatorRecord struct {
	snapshot            RuntimeSnapshot
	terminalBoundary    bool
	generationStartedAt time.Time
}

// RuntimeCoordinator is the authoritative read model for session lifecycle.
// Runtime stores still own their domain mutations; all session-facing reads are
// normalized through this immutable, revisioned snapshot.
type RuntimeCoordinator struct {
	mu           sync.RWMutex
	records      map[string]runtimeCoordinatorRecord
	nextRevision uint64
}

func NewRuntimeCoordinator() *RuntimeCoordinator {
	return &RuntimeCoordinator{records: make(map[string]runtimeCoordinatorRecord)}
}

func (c *RuntimeCoordinator) Observe(candidate RuntimeSnapshot) (RuntimeSnapshot, bool) {
	if c == nil || strings.TrimSpace(candidate.SessionID) == "" {
		return RuntimeSnapshot{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	previous, exists := c.records[candidate.SessionID]
	if exists && candidate.Generation > 0 && candidate.Generation < previous.snapshot.Generation {
		return cloneRuntimeSnapshot(previous.snapshot), false
	}
	if exists && previous.terminalBoundary {
		return cloneRuntimeSnapshot(previous.snapshot), false
	}
	if exists && !previous.generationStartedAt.IsZero() {
		evidenceAt := latestRuntimeTime(candidate.StartedAt, candidate.LastProgressAt)
		if evidenceAt.IsZero() || evidenceAt.Before(previous.generationStartedAt) {
			return cloneRuntimeSnapshot(previous.snapshot), false
		}
	}
	candidate.Generation = runtimeSnapshotGeneration(previous.snapshot, candidate, exists)
	if exists && runtimeSnapshotsSemanticallyEqual(previous.snapshot, candidate) {
		return cloneRuntimeSnapshot(previous.snapshot), false
	}

	c.nextRevision++
	candidate.Revision = c.nextRevision
	candidate.ObservedAt = time.Now().UTC()
	previous.snapshot = cloneRuntimeSnapshot(candidate)
	c.records[candidate.SessionID] = previous
	return cloneRuntimeSnapshot(candidate), true
}

// MarkTerminalBoundary makes Stop/failure authoritative immediately. Late
// observations from the previous generation cannot reopen the session; only
// StartGeneration clears this boundary.
func (c *RuntimeCoordinator) MarkTerminalBoundary(sessionID string, phase RuntimePhase, reason string) (RuntimeSnapshot, bool) {
	if c == nil || strings.TrimSpace(sessionID) == "" || !runtimePhaseIsTerminal(phase) {
		return RuntimeSnapshot{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	record := c.records[sessionID]
	if record.terminalBoundary {
		return cloneRuntimeSnapshot(record.snapshot), false
	}
	snapshot := cloneRuntimeSnapshot(record.snapshot)
	snapshot.SessionID = sessionID
	if snapshot.Generation == 0 {
		snapshot.Generation = 1
	}
	c.nextRevision++
	snapshot.Revision = c.nextRevision
	snapshot.Phase = phase
	snapshot.Reason = strings.TrimSpace(reason)
	snapshot.ForegroundTurn.Busy = false
	snapshot.ForegroundTurn.HasCancel = false
	snapshot.ForegroundTurn.CanSteer = false
	snapshot.BackgroundLive = false
	snapshot.TerminalBusy = false
	snapshot.WaitingForUser = false
	snapshot.WaitingMessage = ""
	now := time.Now().UTC()
	snapshot.LastProgressAt = latestRuntimeTime(snapshot.LastProgressAt, now)
	snapshot.CompletedAt = &now
	snapshot.ObservedAt = now
	record.snapshot = cloneRuntimeSnapshot(snapshot)
	record.terminalBoundary = true
	c.records[sessionID] = record
	return cloneRuntimeSnapshot(snapshot), true
}

// StartGeneration explicitly reopens a stopped session for a new user turn.
// It is the only operation that clears an explicit terminal boundary.
func (c *RuntimeCoordinator) StartGeneration(sessionID, reason string) (RuntimeSnapshot, bool) {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return RuntimeSnapshot{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	record := c.records[sessionID]
	snapshot := cloneRuntimeSnapshot(record.snapshot)
	snapshot.SessionID = sessionID
	if snapshot.Generation == 0 {
		snapshot.Generation = 1
	} else {
		snapshot.Generation++
	}
	c.nextRevision++
	snapshot.Revision = c.nextRevision
	snapshot.Phase = runtimePhaseStarting
	snapshot.Reason = strings.TrimSpace(reason)
	snapshot.StartedAt = time.Time{}
	snapshot.LastProgressAt = time.Time{}
	snapshot.CompletedAt = nil
	now := time.Now().UTC()
	snapshot.ObservedAt = now
	record.snapshot = cloneRuntimeSnapshot(snapshot)
	record.terminalBoundary = false
	record.generationStartedAt = now
	c.records[sessionID] = record
	return cloneRuntimeSnapshot(snapshot), true
}

// BusyCount reports how many sessions have a generation in flight (starting
// or running). It is the drain signal the server A deploy waits on: the
// session tracker's "running" status and the in-flight HTTP request count
// both read idle during a steered coding-agent turn (the request returns
// at once and the model runs in the background), which let a deploy restart
// the agent mid-turn and cut a Cursor conversation off with a tool error as
// its last event (server A, 2026-09-03 16:30Z).
func (c *RuntimeCoordinator) BusyCount() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	busy := 0
	for _, record := range c.records {
		switch record.snapshot.Phase {
		case runtimePhaseStarting, runtimePhaseRunning:
			busy++
		}
	}
	return busy
}

func (c *RuntimeCoordinator) Snapshot(sessionID string) (RuntimeSnapshot, bool) {
	if c == nil {
		return RuntimeSnapshot{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.records[sessionID]
	if !ok {
		return RuntimeSnapshot{}, false
	}
	return cloneRuntimeSnapshot(record.snapshot), true
}

// Evict removes runtime state when the authoritative session retention window
// expires, so the read model never outlives the session it represents.
func (c *RuntimeCoordinator) Evict(sessionID string) {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	c.mu.Lock()
	delete(c.records, sessionID)
	c.mu.Unlock()
}

func runtimeSnapshotGeneration(previous, candidate RuntimeSnapshot, exists bool) uint64 {
	if !exists {
		return 1
	}
	generation := previous.Generation
	if generation == 0 {
		generation = 1
	}
	newStart := !previous.StartedAt.IsZero() && !candidate.StartedAt.IsZero() &&
		!previous.StartedAt.Equal(candidate.StartedAt)
	restartedAfterQuiescence := (runtimePhaseIsTerminal(previous.Phase) || previous.Phase == runtimePhaseIdle) &&
		(candidate.Phase == runtimePhaseStarting || candidate.Phase == runtimePhaseRunning)
	if newStart || restartedAfterQuiescence {
		generation++
	}
	return generation
}

func runtimeSnapshotsSemanticallyEqual(a, b RuntimeSnapshot) bool {
	a.Revision, b.Revision = 0, 0
	a.ObservedAt, b.ObservedAt = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func cloneRuntimeSnapshot(snapshot RuntimeSnapshot) RuntimeSnapshot {
	copy := snapshot
	copy.ChildExecutions = append([]RuntimeChildSnapshot(nil), snapshot.ChildExecutions...)
	copy.BackgroundAgents = append([]RuntimeBackgroundSnapshot(nil), snapshot.BackgroundAgents...)
	copy.Terminals = append([]RuntimeTerminalSnapshot(nil), snapshot.Terminals...)
	if snapshot.CompletedAt != nil {
		completedAt := *snapshot.CompletedAt
		copy.CompletedAt = &completedAt
	}
	for i := range copy.ChildExecutions {
		if snapshot.ChildExecutions[i].CompletedAt != nil {
			completedAt := *snapshot.ChildExecutions[i].CompletedAt
			copy.ChildExecutions[i].CompletedAt = &completedAt
		}
	}
	for i := range copy.BackgroundAgents {
		if snapshot.BackgroundAgents[i].CompletedAt != nil {
			completedAt := *snapshot.BackgroundAgents[i].CompletedAt
			copy.BackgroundAgents[i].CompletedAt = &completedAt
		}
	}
	return copy
}

func runtimePhaseDisplayStatus(phase RuntimePhase) string {
	switch phase {
	case runtimePhaseStarting, runtimePhaseRunning:
		return sessionExecutionDisplayBusy
	case runtimePhaseCompleted, runtimePhaseFailed, runtimePhaseCanceled:
		return sessionExecutionDisplayStopped
	default:
		return sessionExecutionDisplayIdle
	}
}

func runtimePhaseIsTerminal(phase RuntimePhase) bool {
	return phase == runtimePhaseCompleted || phase == runtimePhaseFailed || phase == runtimePhaseCanceled
}

func runtimePhaseIsLive(phase RuntimePhase) bool {
	return phase == runtimePhaseStarting || phase == runtimePhaseRunning || phase == runtimePhaseWaiting
}

func sortRuntimeSnapshot(snapshot *RuntimeSnapshot) {
	sort.Slice(snapshot.ChildExecutions, func(i, j int) bool {
		return snapshot.ChildExecutions[i].ExecutionID < snapshot.ChildExecutions[j].ExecutionID
	})
	sort.Slice(snapshot.BackgroundAgents, func(i, j int) bool {
		return snapshot.BackgroundAgents[i].AgentID < snapshot.BackgroundAgents[j].AgentID
	})
	sort.Slice(snapshot.Terminals, func(i, j int) bool {
		return snapshot.Terminals[i].TerminalID < snapshot.Terminals[j].TerminalID
	})
}

func (api *StreamingAPI) observeRuntimeSnapshot(sessionID string) (RuntimeSnapshot, bool) {
	if api == nil || api.runtimeCoordinator == nil || strings.TrimSpace(sessionID) == "" {
		return RuntimeSnapshot{}, false
	}
	snapshot, changed := api.runtimeCoordinator.Observe(api.collectRuntimeSnapshot(sessionID))
	return snapshot, changed
}

// authoritativeRuntimeSnapshot is the sole lifecycle read entry point. It
// refreshes the normalized snapshot from current domain stores and returns the
// coordinator-owned immutable revision.
func (api *StreamingAPI) authoritativeRuntimeSnapshot(sessionID string) (RuntimeSnapshot, bool) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return RuntimeSnapshot{}, false
	}
	if api.runtimeCoordinator == nil {
		return api.collectRuntimeSnapshot(sessionID), true
	}
	snapshot, _ := api.runtimeCoordinator.Observe(api.collectRuntimeSnapshot(sessionID))
	return snapshot, true
}

// authoritativeRuntimeSnapshotForSession supplies lifecycle metadata when a
// historical/event-backed execution tree has no in-memory active-session row.
func (api *StreamingAPI) authoritativeRuntimeSnapshotForSession(session *ActiveSessionInfo) (RuntimeSnapshot, bool) {
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return RuntimeSnapshot{}, false
	}
	candidate := api.collectRuntimeSnapshot(session.SessionID)
	if candidate.RawSessionStatus == "" {
		candidate.RawSessionStatus = session.Status
		candidate.StartedAt = earliestRuntimeTime(candidate.StartedAt, session.CreatedAt)
		candidate.LastProgressAt = latestRuntimeTime(candidate.LastProgressAt, session.LastActivity)
		candidate.WaitingForUser = session.NeedsUserInput
		candidate.WaitingMessage = session.WaitingMessage
		candidate.Phase, candidate.Reason = deriveRuntimePhase(candidate, time.Now().UTC())
		if runtimePhaseIsTerminal(candidate.Phase) {
			completedAt := candidate.LastProgressAt
			if completedAt.IsZero() {
				completedAt = time.Now().UTC()
			}
			candidate.CompletedAt = &completedAt
		}
	}
	if api.runtimeCoordinator == nil {
		return candidate, true
	}
	snapshot, _ := api.runtimeCoordinator.Observe(candidate)
	return snapshot, true
}

func (api *StreamingAPI) collectRuntimeSnapshot(sessionID string) RuntimeSnapshot {
	snapshot := RuntimeSnapshot{SessionID: strings.TrimSpace(sessionID)}
	now := time.Now().UTC()

	if active, ok := api.getActiveSession(sessionID); ok && active != nil {
		snapshot.RawSessionStatus = active.Status
		snapshot.StartedAt = active.CreatedAt
		snapshot.LastProgressAt = active.LastActivity
		snapshot.WaitingForUser = active.NeedsUserInput
		snapshot.WaitingMessage = active.WaitingMessage
		snapshot.ForegroundTurn.Synthetic = active.IsSyntheticTurn
	}
	if api.eventStore != nil {
		waiting, _, _, message := api.deriveSessionUserInputState(sessionID)
		snapshot.WaitingForUser = waiting
		snapshot.WaitingMessage = message
	}

	snapshot.ForegroundTurn.Busy = api.isSessionBusy(sessionID)
	snapshot.ForegroundTurn.HasCancel = api.hasActiveTurnCancel(sessionID)
	snapshot.ForegroundTurn.CanSteer = api.canSteerSession(sessionID)

	for _, execution := range api.trackedExecutionsForSession(sessionID) {
		if execution == nil {
			continue
		}
		snapshot.ChildExecutions = append(snapshot.ChildExecutions, RuntimeChildSnapshot{
			ExecutionID: execution.ExecutionID,
			Kind:        execution.Kind,
			Status:      execution.Status,
			StartedAt:   execution.StartedAt,
			CompletedAt: cloneRuntimeTime(execution.CompletedAt),
		})
		snapshot.StartedAt = earliestRuntimeTime(snapshot.StartedAt, execution.StartedAt)
		snapshot.LastProgressAt = latestRuntimeTime(snapshot.LastProgressAt, execution.StartedAt)
		if execution.CompletedAt != nil {
			snapshot.LastProgressAt = latestRuntimeTime(snapshot.LastProgressAt, *execution.CompletedAt)
		}
	}

	if api.bgAgentRegistry != nil {
		snapshot.BackgroundLive = api.bgAgentRegistry.HasRunningAgents(sessionID)
		for _, backgroundAgent := range api.bgAgentRegistry.GetAll(sessionID) {
			if backgroundAgent == nil {
				continue
			}
			agent := backgroundAgent.GetSnapshot()
			snapshot.BackgroundAgents = append(snapshot.BackgroundAgents, RuntimeBackgroundSnapshot{
				AgentID:     agent.ID,
				Status:      string(agent.Status),
				CreatedAt:   agent.CreatedAt,
				CompletedAt: cloneRuntimeTime(agent.CompletedAt),
			})
			snapshot.StartedAt = earliestRuntimeTime(snapshot.StartedAt, agent.CreatedAt)
			snapshot.LastProgressAt = latestRuntimeTime(snapshot.LastProgressAt, agent.CreatedAt)
			if agent.CompletedAt != nil {
				snapshot.LastProgressAt = latestRuntimeTime(snapshot.LastProgressAt, *agent.CompletedAt)
			}
		}
	}

	if api.terminalStore != nil {
		snapshot.TerminalBusy = api.terminalStore.SessionHasBusyCodingTmux(sessionID)
		for _, terminal := range api.terminalStore.ListMetadata(sessionID) {
			snapshot.Terminals = append(snapshot.Terminals, RuntimeTerminalSnapshot{
				TerminalID:  terminal.TerminalID,
				ExecutionID: terminal.ExecutionID,
				State:       terminal.State,
				Active:      terminal.Active,
				HasTmux:     strings.TrimSpace(terminal.TmuxSession) != "",
				UpdatedAt:   terminal.UpdatedAt,
			})
			snapshot.StartedAt = earliestRuntimeTime(snapshot.StartedAt, terminal.CreatedAt)
			snapshot.LastProgressAt = latestRuntimeTime(snapshot.LastProgressAt, terminal.UpdatedAt)
		}
	}

	snapshot.Phase, snapshot.Reason = deriveRuntimePhase(snapshot, now)
	if runtimePhaseIsTerminal(snapshot.Phase) {
		completedAt := snapshot.LastProgressAt
		if completedAt.IsZero() {
			completedAt = now
		}
		snapshot.CompletedAt = &completedAt
	}
	sortRuntimeSnapshot(&snapshot)
	return snapshot
}

func deriveRuntimePhase(snapshot RuntimeSnapshot, now time.Time) (RuntimePhase, string) {
	if snapshot.WaitingForUser {
		return runtimePhaseWaiting, "session requires user input"
	}
	if snapshot.ForegroundTurn.Busy || snapshot.ForegroundTurn.HasCancel || snapshot.ForegroundTurn.CanSteer {
		return runtimePhaseRunning, "foreground turn is active"
	}
	for _, execution := range snapshot.ChildExecutions {
		if execution.Status == trackedExecutionStatusRunning {
			return runtimePhaseRunning, "tracked child execution is active"
		}
	}
	if snapshot.BackgroundLive {
		return runtimePhaseRunning, "background agent is active or in completion grace"
	}
	if snapshot.TerminalBusy {
		return runtimePhaseRunning, "coding-agent terminal is busy"
	}

	switch normalizeSessionLifecycleStatus(snapshot.RawSessionStatus) {
	case sessionLifecycleFailed:
		return runtimePhaseFailed, "session reported failure"
	case sessionLifecycleStopped:
		return runtimePhaseCanceled, "session was stopped"
	case sessionLifecycleCompleted:
		return runtimePhaseCompleted, "session completed"
	case sessionLifecycleRunning:
		if !snapshot.StartedAt.IsZero() && now.Sub(snapshot.StartedAt) < 10*time.Second {
			return runtimePhaseStarting, "session started and runtime work is not registered yet"
		}
		return runtimePhaseIdle, "session reports running but no live work is registered"
	default:
		return runtimePhaseIdle, "no live runtime work is registered"
	}
}

func cloneRuntimeTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func earliestRuntimeTime(current, candidate time.Time) time.Time {
	if candidate.IsZero() || (!current.IsZero() && !candidate.Before(current)) {
		return current
	}
	return candidate
}

func latestRuntimeTime(current, candidate time.Time) time.Time {
	if candidate.After(current) {
		return candidate
	}
	return current
}
