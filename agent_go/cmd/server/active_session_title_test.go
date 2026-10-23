package server

import "testing"

func TestTrackActiveSessionUsesAndPreservesShortSessionTitle(t *testing.T) {
	const sessionID = "schedule-cron--daily_123"
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}

	api.trackActiveSession(
		sessionID,
		"simple",
		"NORMAL CHIEF OF STAFF TASK RUN.\n\nVery long scheduler envelope",
		"default",
		"",
		"cron",
		"Daily Financial Compliance Monitor",
		"",
		"",
	)
	if got := api.activeSessions[sessionID].Title; got != "Daily Financial Compliance Monitor" {
		t.Fatalf("title = %q, want concise schedule name", got)
	}

	// Later turns may omit session_title. They must not erase the short title
	// and make activity surfaces fall back to the complete query again.
	api.trackActiveSession(sessionID, "simple", "report update envelope", "default", "", "cron", "", "", "")
	if got := api.activeSessions[sessionID].Title; got != "Daily Financial Compliance Monitor" {
		t.Fatalf("title after follow-up = %q, want preserved schedule name", got)
	}
}

func TestTrackActiveSessionPreservesInternalParentMetadata(t *testing.T) {
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}
	const sessionID = "pulse-reviewer-1"

	api.trackActiveSession(sessionID, "workflow_phase", "review", "default", "", "cron", "Autopilot review", "pulse-root-1", "pulse_reviewer")
	api.trackActiveSession(sessionID, "workflow_phase", "follow-up", "default", "", "cron", "", "", "")

	got := api.activeSessions[sessionID]
	if got.ParentSessionID != "pulse-root-1" || got.SessionKind != "pulse_reviewer" {
		t.Fatalf("child metadata was not preserved: %#v", got)
	}
}
