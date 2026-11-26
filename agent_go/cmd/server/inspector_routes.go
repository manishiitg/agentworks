package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/inspector"
)

// inspectorSummary is the JSON shape the GET /api/inspector/<session>
// endpoint returns. Keeping it small + flat so the frontend can render
// a live timeline without extra translation.
type inspectorSummary struct {
	SessionID string                  `json:"session_id"`
	LatestSeq int                     `json:"latest_seq"`
	Count     int                     `json:"count"`
	Events    []inspector.StoredEvent `json:"events"`
}

// handleInspectorEvents serves
//
//	GET /api/inspector/{session_id}?since=<n>&max=<m>
//
// Returns the stored InspectorEvents for the session whose
// GlobalSeq is strictly greater than `since` (default 0). `max`
// caps the returned slice (default: all). The response also
// carries the latest sequence number so the frontend can use it as
// the next polling cursor.
//
// 404 is returned when the inspector store has no record of the
// session — i.e. the panel was never opened or the session has been
// cleared. An empty 200 with count=0 is returned when the session
// is tracked but has no events newer than `since`.
func (api *StreamingAPI) handleInspectorEvents(w http.ResponseWriter, r *http.Request) {
	if api.inspectorStore == nil {
		http.Error(w, `{"error":"inspector store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	sessionID := mux.Vars(r)["session_id"]
	if sessionID == "" {
		http.Error(w, `{"error":"session_id is required"}`, http.StatusBadRequest)
		return
	}
	// The timeline holds the session's prompts, tool arguments and results:
	// the same people who may open its terminal (a view for GET, control for DELETE).
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, `{"error":"session not tracked by inspector"}`, http.StatusNotFound)
		return
	}

	since := 0
	if raw := r.URL.Query().Get("since"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			since = n
		}
	}
	maxN := 0 // 0 = unlimited
	if raw := r.URL.Query().Get("max"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxN = n
		}
	}

	latest := api.inspectorStore.LatestSeq(sessionID)
	if latest == 0 {
		http.Error(w, `{"error":"session not tracked by inspector"}`, http.StatusNotFound)
		return
	}

	events := api.inspectorStore.Events(sessionID, since)
	if maxN > 0 && len(events) > maxN {
		events = events[:maxN]
	}

	resp := inspectorSummary{
		SessionID: sessionID,
		LatestSeq: latest,
		Count:     len(events),
		Events:    events,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleInspectorClear serves
//
//	DELETE /api/inspector/{session_id}
//
// Drops all stored events for the session. Useful when the frontend
// panel is closed and the user wants to free memory or reset the view.
func (api *StreamingAPI) handleInspectorClear(w http.ResponseWriter, r *http.Request) {
	if api.inspectorStore == nil {
		http.Error(w, `{"error":"inspector store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	sessionID := mux.Vars(r)["session_id"]
	if sessionID == "" {
		http.Error(w, `{"error":"session_id is required"}`, http.StatusBadRequest)
		return
	}
	// The timeline holds the session's prompts, tool arguments and results:
	// the same people who may open its terminal (a view for GET, control for DELETE).
	if !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, `{"error":"session not tracked by inspector"}`, http.StatusNotFound)
		return
	}
	api.inspectorStore.Clear(sessionID)
	w.WriteHeader(http.StatusNoContent)
}

// handleInspectorSessions serves
//
//	GET /api/inspector
//
// Returns the list of session IDs currently tracked. Diagnostic
// endpoint for administrators (registered behind requireAdmin); not needed
// for normal panel operation.
func (api *StreamingAPI) handleInspectorSessions(w http.ResponseWriter, r *http.Request) {
	if api.inspectorStore == nil {
		http.Error(w, `{"error":"inspector store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": api.inspectorStore.Sessions(),
	})
}
