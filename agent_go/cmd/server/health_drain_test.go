package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// The server A deploy (./deploy.sh <server>) polls /health .drain.idle before swapping releases.
func TestHealthReportsDrainStatus(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}
	rec := httptest.NewRecorder()
	api.handleHealth(rec, httptest.NewRequest("GET", "/health", nil))
	var body struct {
		Drain struct {
			ActiveSessions   int   `json:"active_sessions"`
			InFlightRequests int64 `json:"in_flight_requests"`
			Idle             bool  `json:"idle"`
		} `json:"drain"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Drain.Idle || body.Drain.ActiveSessions != 0 {
		t.Fatalf("idle server must report idle: %+v", body.Drain)
	}

	// Finished sessions stay in the tracker for a day; they must not block.
	api.activeSessions["done"] = &ActiveSessionInfo{Status: string(sessionLifecycleCompleted)}
	rec = httptest.NewRecorder()
	api.handleHealth(rec, httptest.NewRequest("GET", "/health", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Drain.Idle {
		t.Fatalf("a completed session must not block the drain: %+v", body.Drain)
	}

	api.activeSessions["s1"] = &ActiveSessionInfo{Status: string(sessionLifecycleRunning)}
	rec = httptest.NewRecorder()
	api.handleHealth(rec, httptest.NewRequest("GET", "/health", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Drain.Idle || body.Drain.ActiveSessions != 1 {
		t.Fatalf("a tracked session must block the drain: %+v", body.Drain)
	}
}
