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

// PLAT-697 phase 4: the Goal Lead is one persistent conversation per
// workflow. The daily goal check on two consecutive days runs in the same
// conversation (the second day resumes it instead of starting fresh), and a
// workflow chat's ask_goal_lead is answered there with a recommendation.
func TestGoalLeadCheckContinuesItsConversationAndAnswersAsks(t *testing.T) {
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
		if strings.Contains(fmt.Sprint(reqMap["query"]), "GOAL LEAD TURN: [Function call") {
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
		t.Fatalf("sessions = %v, want one Goal Lead conversation on both days", sessions)
	}
	first, second := fmt.Sprint(requests[0]["query"]), fmt.Sprint(requests[1]["query"])
	if !strings.Contains(first, "You are the Reports Goal Lead") || strings.Contains(second, "You are the Reports Goal Lead") {
		t.Fatalf("the charter must open the conversation once:\nday 1: %s\nday 2: %s", first, second)
	}
	if !strings.Contains(second, "daily goal check, 2026-10-08") || !strings.Contains(second, `pulse_run_id="run-day-2"`) {
		t.Fatalf("day 2 query = %s", second)
	}
	if requests[1]["restored_conversation_session_id"] != sessions[0] || requests[1]["pulse_lifecycle_turn"] != true {
		t.Fatalf("day 2 must resume the conversation: %v", requests[1])
	}
	mu.Unlock()

	// A chat of the workflow asks; the answer is the Goal Lead's
	// recommendation, from the same conversation.
	out, err := env.api.askGoalLead(ctx, "owner", ws, "sess-builder", "the Reports Builder chat", "Should I run the growth route again today?", 5*time.Second, "")
	if err != nil {
		t.Fatalf("ask_goal_lead: %v", err)
	}
	if out["status"] != "completed" || !strings.Contains(fmt.Sprint(out["result"]), "I recommend running the growth route") {
		t.Fatalf("ask_goal_lead = %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 3 || sessions[2] != sessions[0] {
		t.Fatalf("the ask must run in the Goal Lead conversation: %v", sessions)
	}
	if ask := fmt.Sprint(requests[2]["query"]); !strings.Contains(ask, "Answer as a recommendation") || !strings.Contains(ask, "You do not decide for the owner") {
		t.Fatalf("ask turn = %s", ask)
	}

	messages, err := listGoalLeadMessages(ctx, ws, 20)
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{}
	for _, msg := range messages {
		roles = append(roles, msg.Role)
	}
	if got := strings.Join(roles, ","); got != "check,check,ask,goal_lead" {
		t.Fatalf("conversation log roles = %s", got)
	}
}
