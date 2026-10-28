package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"

	"github.com/gorilla/mux"
)

// sseEventMessage mirrors GetEventsResponse for SSE "event" messages.
type sseEventMessage struct {
	Events                     []events.Event   `json:"events"`
	SessionStatus              string           `json:"session_status,omitempty"`
	DisplayStatus              string           `json:"display_status,omitempty"`
	LastProcessedIndex         int              `json:"last_processed_index"`
	HasRunningBackgroundAgents bool             `json:"has_running_background_agents,omitempty"`
	IsSyntheticTurn            bool             `json:"is_synthetic_turn,omitempty"` // True when running auto-notification turn (frontend should not block input)
	CanSteer                   bool             `json:"can_steer,omitempty"`
	RuntimeState               *RuntimeSnapshot `json:"runtime_state,omitempty"`
}

// sseStatusMessage is sent on the "status" SSE event.
type sseStatusMessage struct {
	SessionStatus              string           `json:"session_status,omitempty"`
	DisplayStatus              string           `json:"display_status,omitempty"`
	HasRunningBackgroundAgents bool             `json:"has_running_background_agents,omitempty"`
	IsSyntheticTurn            bool             `json:"is_synthetic_turn,omitempty"`
	CanSteer                   bool             `json:"can_steer,omitempty"`
	RuntimeState               *RuntimeSnapshot `json:"runtime_state,omitempty"`
}

// handleSSEStream serves a Server-Sent Events stream of session events.
//
// Query params:
//   - session_id (path)  — required
//   - since              — last event index the client has seen
//   - event_mode         — "advanced" | "tiny" | "micro" (default "micro")
//   - working_set=session — omit child transcript detail loaded by terminal API
//
// The handler subscribes to the EventStore first, then backfills catch-up
// events so no events are lost between subscription and the initial fetch.
func (api *StreamingAPI) handleSSEStream(w http.ResponseWriter, r *http.Request) {
	// 1. Validate streaming support
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// 2. Extract parameters
	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	// Auth / ownership check
	sessionStatus, deny := api.getSessionStatusString(r, sessionID)
	if deny {
		http.Error(w, "Session not found or access denied", http.StatusNotFound)
		return
	}

	sinceIndex := -1
	// Support both ?since= and the standard Last-Event-ID header (EventSource auto-reconnect)
	sinceStr := r.URL.Query().Get("since")
	if sinceStr == "" {
		sinceStr = r.Header.Get("Last-Event-ID")
	}
	if sinceStr != "" {
		parsed, err := strconv.Atoi(sinceStr)
		if err != nil {
			http.Error(w, "since / Last-Event-ID must be a valid integer", http.StatusBadRequest)
			return
		}
		sinceIndex = parsed
	}
	workingSetOnly := r.URL.Query().Get("working_set") == "session"
	durableChat := r.URL.Query().Get("durable_chat") == "1"
	if durableChat {
		if _, active := api.getActiveSession(sessionID); active {
			if !api.eventStore.IsDurableChatSession(sessionID) {
				http.Error(w, "Session is not an interactive chat", http.StatusConflict)
				return
			}
		} else {
			ownerID, ownerErr := api.eventStore.DurableChatOwner(sessionID)
			if ownerErr != nil {
				http.Error(w, "Failed to authorize durable chat", http.StatusInternalServerError)
				return
			}
			if ownerID == "" || !durableChatReadAllowed(r, sessionID, ownerID, r.URL.Query().Get("workspace_path")) && !api.crewSessionTabView(r, sessionID) {
				http.Error(w, "Session not found or access denied", http.StatusNotFound)
				return
			}
			if err := api.eventStore.SetSessionPersistenceClass(sessionID, events.SessionPersistenceInteractiveChat); err != nil {
				http.Error(w, "Session is not an interactive chat", http.StatusConflict)
				return
			}
		}
	}

	// 3. Disable write timeout for SSE connections — they are long-lived streams.
	// The server's global WriteTimeout (30s) would kill the connection prematurely.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		log.Printf("[SSE] Warning: could not disable write deadline: %v", err)
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	flusher.Flush()

	// 4. Subscribe FIRST so no events are lost, then backfill
	sub := api.eventStore.Subscribe(sessionID)
	defer api.eventStore.Unsubscribe(sessionID, sub)
	log.Printf("[SSE] Subscribed to session %s (since=%d)", sessionID, sinceIndex)

	ctx := r.Context()

	// Backfill: subscribe first, then stream every missing durable page. A
	// one-page read must never advertise the journal tip as its cursor: doing
	// so silently drops all remaining pages on reconnect.
	if durableChat && sinceIndex >= 0 {
		requestedIndex := sinceIndex
		for {
			page, err := api.eventStore.ReadDurableChatPage(sessionID, events.DurableEventPageOptions{
				Limit: events.MaxPollingLimit, AfterSequence: int64(sinceIndex), FromStart: sinceIndex == 0,
			})
			if err != nil {
				log.Printf("[SSE] durable backfill failed for session %s: %v", sessionID, err)
				return
			}
			if len(page.Events) == 0 {
				if page.JournalLatestSequence < int64(requestedIndex) {
					// A previous live-only cursor (or an older server instance)
					// can be ahead of the durable tip. Rewind before live delivery.
					sinceIndex = int(page.JournalLatestSequence)
					if err := writeSSEEvent(w, "cursor", sinceIndex, struct{}{}); err != nil {
						return
					}
					flusher.Flush()
				}
				break
			}
			cursor := int(page.LatestSequence)
			batch := page.Events
			if workingSetOnly {
				batch = events.FilterSessionWorkingSet(sessionID, batch)
			}
			if len(batch) == 0 {
				if err := writeSSEEvent(w, "cursor", cursor, struct{}{}); err != nil {
					return
				}
			} else {
				runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
				runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
				msg := sseEventMessage{
					Events: batch, SessionStatus: sessionStatus,
					DisplayStatus: runtimeStatus.Status, LastProcessedIndex: cursor,
					HasRunningBackgroundAgents: runtimeStatus.HasRunningBackgroundAgents,
					IsSyntheticTurn:            api.isSyntheticTurn(sessionID), CanSteer: runtimeStatus.CanSteer,
					RuntimeState: &runtimeState,
				}
				if err := writeSSEEvent(w, "event", cursor, api.nativeUsageResponse(r, sessionID, msg)); err != nil {
					return
				}
			}
			sinceIndex = cursor
			flusher.Flush()
			if !page.HasNewer || ctx.Err() != nil {
				break
			}
		}
	} else if sinceIndex >= 0 {
		result := api.eventStore.GetEvents(sessionID, events.GetEventsOptions{SinceIndex: sinceIndex, IncludeStreaming: true})
		backfillEvents := result.Events
		resultExists := result.Exists
		resultLastIndex := result.LastProcessedIndex
		rawBackfillCount := len(backfillEvents)
		if workingSetOnly {
			backfillEvents = events.FilterSessionWorkingSet(sessionID, backfillEvents)
		}
		if workingSetOnly && rawBackfillCount > 0 && len(backfillEvents) == 0 {
			if err := writeSSEEvent(w, "cursor", resultLastIndex, struct{}{}); err != nil {
				return
			}
			sinceIndex = resultLastIndex
			flusher.Flush()
		}
		if len(backfillEvents) > 0 {
			runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
			runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
			msg := sseEventMessage{
				Events:                     backfillEvents,
				SessionStatus:              sessionStatus,
				DisplayStatus:              runtimeStatus.Status,
				LastProcessedIndex:         resultLastIndex,
				HasRunningBackgroundAgents: runtimeStatus.HasRunningBackgroundAgents,
				IsSyntheticTurn:            api.isSyntheticTurn(sessionID),
				CanSteer:                   runtimeStatus.CanSteer,
				RuntimeState:               &runtimeState,
			}
			if err := writeSSEEvent(w, "event", resultLastIndex, api.nativeUsageResponse(r, sessionID, msg)); err != nil {
				return
			}
			sinceIndex = resultLastIndex
			flusher.Flush()
		} else if !resultExists {
			// The in-memory EventStore has no record of this session at all —
			// almost always because the process restarted and this ring
			// buffer is memory-only (the durable transcript on disk is
			// unaffected). A reconnecting EventSource (native browser
			// auto-reconnect, or our own client's since=/Last-Event-ID) is
			// still asking for events after its old, now-meaningless index.
			//
			// Two things go wrong if we silently do nothing here, confirmed
			// live on Dominion 2026-08-31 (a chat answer was fully generated
			// and persisted, but never appeared in an already-open tab until
			// a manual refresh):
			//  1. The client is never told to resync, so it just sits there
			//     believing it's caught up.
			//  2. Below, `sinceIndex` (and the event loop's `lastIndex`) stays
			//     pinned to the stale, large pre-restart value. The fresh
			//     store numbers new events starting near 1 again, so every
			//     genuinely-new event's index is `<= lastIndex` and gets
			//     silently discarded by the "already covered by backfill"
			//     guard in the event loop below — permanently, for the life
			//     of this connection.
			//
			// Sending an explicit LastProcessedIndex: -1 reuses the exact
			// sentinel the REST-polling path already sends in this same
			// situation (see getSessionStatusString / polling.go), which the
			// frontend already knows how to treat as "resync me". Resetting
			// sinceIndex here fixes problem 2 for the rest of this
			// connection's lifetime.
			msg := sseEventMessage{
				Events:             []events.Event{},
				SessionStatus:      sessionStatus,
				LastProcessedIndex: -1,
			}
			if err := writeSSEEvent(w, "event", -1, api.nativeUsageResponse(r, sessionID, msg)); err != nil {
				return
			}
			sinceIndex = -1
			flusher.Flush()
		}
	}

	// 5. Event loop
	statusTicker := time.NewTicker(2 * time.Second)
	defer statusTicker.Stop()
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	// lastIndex is the newest index sent (the SSE id). backfilledThrough is
	// only what the backfill above already delivered. A durable event can reach
	// this subscriber after a newer one: a store callback appends while the
	// older event is still being published (a retained turn's completion adds
	// reply_file_changes before its own completion is broadcast). Skipping by
	// lastIndex dropped that completion and left the chat's spinner running
	// until the tab was reopened (PLAT-746-158).
	lastIndex := sinceIndex
	backfilledThrough := sinceIndex

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return

		case event, ok := <-sub.Ch:
			if !ok {
				// Channel closed (unsubscribed)
				return
			}
			if durableChat && !events.IsDurableChatEvent(event) {
				// Live-only updates still drive the streaming UI, but never become
				// chat history or advance the journal cursor. No SSE id means a
				// reconnect resumes from the last persisted event instead.
				if workingSetOnly && !events.RetainEventInSessionWorkingSet(sessionID, event) {
					continue
				}
				runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
				runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
				// -2 is the client's "no cursor information" sentinel; -1 would
				// be read as "server lost this session" and trigger a resync.
				msg := sseEventMessage{
					Events: []events.Event{event}, SessionStatus: sessionStatus,
					DisplayStatus: runtimeStatus.Status, LastProcessedIndex: -2,
					HasRunningBackgroundAgents: runtimeStatus.HasRunningBackgroundAgents,
					IsSyntheticTurn:            api.isSyntheticTurn(sessionID), CanSteer: runtimeStatus.CanSteer,
					RuntimeState: &runtimeState,
				}
				if err := writeSSEEvent(w, "event", -1, api.nativeUsageResponse(r, sessionID, msg)); err != nil {
					return
				}
				flusher.Flush()
				continue
			}

			// Skip events already covered by the backfill
			if event.Sequence > 0 && event.Sequence <= int64(backfilledThrough) {
				continue
			}

			// Determine the event index to use as SSE id
			eventIndex := lastIndex + 1
			if event.Sequence > 0 {
				eventIndex = int(event.Sequence)
			} else if event.Data != nil && event.Data.EventIndex > 0 {
				eventIndex = event.Data.EventIndex
			}
			if eventIndex > lastIndex {
				lastIndex = eventIndex
			}
			if workingSetOnly && !events.RetainEventInSessionWorkingSet(sessionID, event) {
				// Advance EventSource's Last-Event-ID without serializing the heavy
				// child payload. The browser has no listener for this tiny cursor
				// event, but will reconnect after the correct global index.
				if err := writeSSEEvent(w, "cursor", lastIndex, struct{}{}); err != nil {
					return
				}
				flusher.Flush()
				continue
			}

			currentStatus, _ := api.getSessionStatusString(r, sessionID)
			if currentStatus == "" {
				currentStatus = sessionStatus
			}

			runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
			runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
			msg := sseEventMessage{
				Events:                     []events.Event{event},
				SessionStatus:              currentStatus,
				DisplayStatus:              runtimeStatus.Status,
				LastProcessedIndex:         lastIndex,
				HasRunningBackgroundAgents: runtimeStatus.HasRunningBackgroundAgents,
				IsSyntheticTurn:            api.isSyntheticTurn(sessionID),
				CanSteer:                   runtimeStatus.CanSteer,
				RuntimeState:               &runtimeState,
			}
			if err := writeSSEEvent(w, "event", lastIndex, api.nativeUsageResponse(r, sessionID, msg)); err != nil {
				return
			}
			flusher.Flush()

		case <-statusTicker.C:
			currentStatus, _ := api.getSessionStatusString(r, sessionID)
			if currentStatus == "" {
				currentStatus = sessionStatus
			} else {
				sessionStatus = currentStatus // update cached status
			}
			runtimeState, _ := api.authoritativeRuntimeSnapshot(sessionID)
			runtimeStatus := sessionDisplayStatusFromRuntime(runtimeState)
			msg := sseStatusMessage{
				SessionStatus:              currentStatus,
				DisplayStatus:              runtimeStatus.Status,
				HasRunningBackgroundAgents: runtimeStatus.HasRunningBackgroundAgents,
				IsSyntheticTurn:            api.isSyntheticTurn(sessionID),
				CanSteer:                   runtimeStatus.CanSteer,
				RuntimeState:               &runtimeState,
			}
			if err := writeSSEEvent(w, "status", -1, msg); err != nil {
				return
			}
			flusher.Flush()

		case <-heartbeatTicker.C:
			// SSE comment heartbeat to keep connection alive
			if _, err := fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().Format(time.RFC3339)); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSEEvent writes a single SSE message to the writer.
// If id >= 0, an "id:" line is included (allows EventSource auto-reconnect).
func writeSSEEvent(w http.ResponseWriter, eventType string, id int, data interface{}) (writeErr error) {
	// Recover from concurrent map access panics during JSON marshaling.
	// The primary fix is in ContextAwareBridge (new map per event), but this
	// acts as a safety net to prevent the whole server from crashing.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SSE] WARNING: recovered from panic during event serialization: %v", r)
			writeErr = fmt.Errorf("serialization panic: %v", r)
		}
	}()

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if id >= 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", eventType); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", jsonBytes); err != nil {
		return err
	}
	return nil
}
