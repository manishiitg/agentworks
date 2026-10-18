package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pulse is one persistent conversation per workflow: the daily goal check on
// two days runs in the same conversation (day 2 resumes it), and the Builder
// chat talks to it like a colleague: its message arrives as plain text with
// its sender, and Pulse's reply comes back (owner, 2026-10-08).
func TestGoalLeadCheckContinuesItsConversation(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, "Workflow", "reports"), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := NewWorkflowManifest("Reports")
	manifest.Pulse = &WorkflowPulseConfig{Enabled: false, OutwardActions: "ask"}
	raw, _ := json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath(ws)] = string(raw)
	env.mock.mu.Unlock()

	var mu sync.Mutex
	var sessions []string
	var requests []map[string]interface{}
	previousRunner, previousNow := goalLeadTurnRunner, goalLeadNow
	saveReport := true
	goalLeadTurnRunner = func(ctx context.Context, reqMap map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		mu.Lock()
		defer mu.Unlock()
		sessions = append(sessions, sessionID)
		requests = append(requests, reqMap)
		if strings.HasPrefix(fmt.Sprint(reqMap["query"]), "the Builder chat (manish): ") {
			return internalSessionTurnResult{FinalResponse: "I recommend running the growth route today: it has not run for 11 days. Posting more often is the owner's call."}, nil
		}
		if saveReport {
			if _, err := recordPulseGoalCheckFromToolArgs(ctx, map[string]interface{}{"workspace_path": ws, "status": "not_measured", "summary": "Outcome readings are missing. Measure replies before changing the approach.", "next_check_in_hours": float64(1), "next_check_reason": "An earlier question"}); err != nil {
				return internalSessionTurnResult{}, err
			}
		}
		return internalSessionTurnResult{FinalResponse: "Weekly suggestions saved."}, nil
	}
	day := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	goalLeadNow = func() time.Time { return day }
	t.Cleanup(func() { goalLeadTurnRunner, goalLeadNow = previousRunner, previousNow })

	ctx := context.Background()
	sctx := &ScheduleContext{WorkspacePath: ws}
	check := func(runID string) {
		t.Helper()
		step := pulseLifecycleStep{label: "goal-check", goalLead: true, query: fmt.Sprintf("PULSE DAILY GOAL CHECK. pulse_run_id=%q.", runID)}
		if result := env.api.scheduler.runGoalLeadPassStep(ctx, sctx, step); result.outcome != pulseLifecycleStepCompleted {
			t.Fatalf("goal check %s: %v %v", runID, result.outcome, result.err)
		}
	}
	check("run-day-1")
	day = day.Add(24 * time.Hour)
	check("run-day-2")

	mu.Lock()
	if len(sessions) != 2 || sessions[0] != sessions[1] || !isGoalLeadSessionID(sessions[0]) {
		t.Fatalf("sessions = %v, want one Pulse conversation on both days", sessions)
	}
	if requests[1]["restored_conversation_session_id"] != sessions[0] || requests[1]["pulse_lifecycle_turn"] != true {
		t.Fatalf("day 2 must resume the conversation: %v", requests[1])
	}
	mu.Unlock()
	stored, _, err := latestPulseGoalCheck(ctx, ws)
	if err != nil || stored == nil || !stored.ReportOnly {
		t.Fatalf("weekly report missing: %+v %v", stored, err)
	}
	if gap := parseStoredTime(stored.NextCheckAt).Sub(parseStoredTime(stored.CheckedAt)); gap != 7*24*time.Hour {
		t.Fatalf("OFF allowed an early reminder: %s", gap)
	}
	if due := nextPulseCheckForSetting(true, stored); !due.IsZero() {
		t.Fatalf("enabling Pulse must allow work after a report: %s", due)
	}
	// A successful provider exit without a newly saved check is incomplete;
	// neither the previous report nor a final chat sentence can complete it.
	saveReport = false
	result := env.api.scheduler.runGoalLeadPassStep(ctx, sctx, pulseLifecycleStep{label: "goal-check", goalLead: true, query: "Save this week's report"})
	if result.outcome == pulseLifecycleStepCompleted || result.err == nil || !strings.Contains(result.err.Error(), "without saving") {
		t.Fatalf("old report completed a new cycle: %+v", result)
	}

}
