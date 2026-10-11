package server

import (
	"context"
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

	var mu sync.Mutex
	var sessions []string
	var requests []map[string]interface{}
	previousRunner, previousNow := goalLeadTurnRunner, goalLeadNow
	goalLeadTurnRunner = func(_ context.Context, reqMap map[string]interface{}, sessionID, _ string) (internalSessionTurnResult, error) {
		mu.Lock()
		defer mu.Unlock()
		sessions = append(sessions, sessionID)
		requests = append(requests, reqMap)
		if strings.HasPrefix(fmt.Sprint(reqMap["query"]), "the Builder chat (manish): ") {
			return internalSessionTurnResult{FinalResponse: "I recommend running the growth route today: it has not run for 11 days. Posting more often is the owner's call."}, nil
		}
		return internalSessionTurnResult{FinalResponse: "Not measured for 20 days; asked the owner to resume the growth runs."}, nil
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

}
