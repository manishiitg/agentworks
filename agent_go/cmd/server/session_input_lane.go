package server

import (
	"strings"
	"sync"
	"time"
)

type sessionInputLane struct {
	sem  chan struct{} // capacity 1: a waiter holds the token while its turn owns the lane
	refs int
}

// sessionTurnInProgress reports whether a turn currently holds — or is already
// queued for — this session's input lane.
//
// The lane is not a signal ABOUT occupancy; it IS occupancy. A turn occupies a
// session exactly when it holds this lane token, because the token is what serializes
// access to the one tmux pane, the conversation history, and runningAgents.
//
// PLAT-113: callers needing "is a turn running" previously read sessionBusy,
// which is a user-facing display flag deliberately NOT set for workflow turns
// during headless execution. Auto-notifications
// therefore skipped their queue during a workflow run and blocked on this lane
// instead, piling 25 never-started synthetic turns behind one 5-hour turn until
// the idle-wait watchdog killed a healthy run. Read the authority, not the
// display flag.
func (api *StreamingAPI) sessionTurnInProgress(sessionID string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	api.sessionInputLanesMu.Lock()
	defer api.sessionInputLanesMu.Unlock()
	lane := api.sessionInputLanes[sessionID]
	return lane != nil && lane.refs > 0
}

func (api *StreamingAPI) lockSessionInputLane(sessionID string) func() {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return func() {}
	}
	lane := api.sessionInputLaneFor(sessionID)
	lane.sem <- struct{}{}
	return api.releaseSessionInputLaneFunc(sessionID, lane)
}

// sessionInputLaneInteractiveWait bounds how long an interactive send waits
// for a running turn to release the session lane before the backend answers
// 409 turn_running (nothing was sent; the frontend retries). Synthetic and
// scheduled turns keep the unbounded wait: they are served by internal
// queue/retry machinery, not by an impatient user-facing POST.
const sessionInputLaneInteractiveWait = 10 * time.Second

// tryLockSessionInputLane acquires the session input lane, waiting at most
// timeout. It reports false (holding nothing) when the lane stays busy; the
// caller must answer without starting a turn.
func (api *StreamingAPI) tryLockSessionInputLane(sessionID string, timeout time.Duration) (func(), bool) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return func() {}, true
	}
	lane := api.sessionInputLaneFor(sessionID)
	if timeout <= 0 {
		select {
		case lane.sem <- struct{}{}:
			return api.releaseSessionInputLaneFunc(sessionID, lane), true
		default:
			api.dropSessionInputLaneRef(sessionID, lane)
			return nil, false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case lane.sem <- struct{}{}:
		return api.releaseSessionInputLaneFunc(sessionID, lane), true
	case <-timer.C:
		api.dropSessionInputLaneRef(sessionID, lane)
		return nil, false
	}
}

func (api *StreamingAPI) sessionInputLaneFor(sessionID string) *sessionInputLane {
	api.sessionInputLanesMu.Lock()
	defer api.sessionInputLanesMu.Unlock()
	if api.sessionInputLanes == nil {
		api.sessionInputLanes = make(map[string]*sessionInputLane)
	}
	lane := api.sessionInputLanes[sessionID]
	if lane == nil {
		lane = &sessionInputLane{sem: make(chan struct{}, 1)}
		api.sessionInputLanes[sessionID] = lane
	}
	lane.refs++
	return lane
}

func (api *StreamingAPI) releaseSessionInputLaneFunc(sessionID string, lane *sessionInputLane) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-lane.sem
			api.dropSessionInputLaneRef(sessionID, lane)
			// The persisted queue is the authority for what runs next. Kicking
			// after release closes the accept-vs-completion race.
			api.kickConversationTurnQueue(sessionID)
		})
	}
}

func (api *StreamingAPI) dropSessionInputLaneRef(sessionID string, lane *sessionInputLane) {
	api.sessionInputLanesMu.Lock()
	defer api.sessionInputLanesMu.Unlock()
	lane.refs--
	if lane.refs == 0 && api.sessionInputLanes[sessionID] == lane {
		delete(api.sessionInputLanes, sessionID)
	}
}
