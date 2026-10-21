package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// HeaderSummaryResponse merges the app header's three independent polls
// (active sessions, workflow schedule counts) into one response. The
// report-human-inputs poll is intentionally not included here — that panel
// only needs to load when a user opens it, not on a background timer.
type HeaderSummaryResponse struct {
	ActiveSessions  []*ActiveSessionInfo    `json:"active_sessions"`
	Total           int                     `json:"total"`
	ScheduleSummary WorkflowScheduleSummary `json:"schedule_summary"`
}

// handleGetHeaderSummary backs the single poll GlobalActivityMonitor and
// ModePresetBar share, replacing what used to be two separate requests
// (/api/sessions/active and /api/scheduler/jobs?entity_type=workflow) on two
// separate timers.
func (api *StreamingAPI) handleGetHeaderSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	activeSessions := api.collectActiveSessions(r.Context())

	var scheduleSummary WorkflowScheduleSummary
	if api.scheduler != nil {
		summary, err := api.scheduler.LatestWorkflowScheduleSummary(r.Context())
		if err == nil {
			scheduleSummary = summary
		}
	}

	response := HeaderSummaryResponse{
		ActiveSessions:  activeSessions,
		Total:           len(activeSessions),
		ScheduleSummary: scheduleSummary,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}
