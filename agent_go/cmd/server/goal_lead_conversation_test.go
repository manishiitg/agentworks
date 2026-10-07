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

// PLAT-697 phase 4: the Pulse is one persistent conversation per
// workflow. The daily goal check on two consecutive days runs in the same
// conversation (the second day resumes it instead of starting fresh), and a
// workflow chat's ask_goal_lead is answered there with a recommendation.
// The owner talks to Pulse only through their Builder chat (2026-10-08): its
// ask_pulse is an owner relay, so lasting direction goes to goal memory or a
// focus-area proposal; a Run chat or a step only gets a recommendation.
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
		if strings.Contains(fmt.Sprint(reqMap["query"]), "PULSE TURN: [Function call") {
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
	first, second := fmt.Sprint(requests[0]["query"]), fmt.Sprint(requests[1]["query"])
	if !strings.Contains(first, "You are the Reports Pulse") || strings.Contains(second, "You are the Reports Pulse") {
		t.Fatalf("the charter must open the conversation once:\nday 1: %s\nday 2: %s", first, second)
	}
	if !strings.Contains(second, "daily goal check, 2026-10-08") || !strings.Contains(second, `pulse_run_id="run-day-2"`) {
		t.Fatalf("day 2 query = %s", second)
	}
	if requests[1]["restored_conversation_session_id"] != sessions[0] || requests[1]["pulse_lifecycle_turn"] != true {
		t.Fatalf("day 2 must resume the conversation: %v", requests[1])
	}
	mu.Unlock()

	// A chat of the workflow asks; the answer is the Pulse's
	// recommendation, from the same conversation.
	out, err := env.api.askGoalLead(ctx, "owner", ws, "sess-builder", "the Reports Builder chat", "Should I run the growth route again today?", "", false, 5*time.Second, "")
	if err != nil {
		t.Fatalf("ask_goal_lead: %v", err)
	}
	if out["status"] != "completed" || !strings.Contains(fmt.Sprint(out["result"]), "I recommend running the growth route") {
		t.Fatalf("ask_goal_lead = %v", out)
	}
	mu.Lock()
	if len(sessions) != 3 || sessions[2] != sessions[0] {
		t.Fatalf("the ask must run in the Pulse conversation: %v", sessions)
	}
	if ask := fmt.Sprint(requests[2]["query"]); !strings.Contains(ask, "Answer as a recommendation") || !strings.Contains(ask, "You never decide the owner's preferences") {
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
	mu.Unlock()

	// Only a person's Builder chat relays the owner's words.
	env.api.activeSessionsMux.Lock()
	if env.api.activeSessions == nil {
		env.api.activeSessions = map[string]*ActiveSessionInfo{}
	}
	env.api.activeSessions["sess-owner-builder"] = &ActiveSessionInfo{SessionID: "sess-owner-builder", WorkshopMode: "workshop"}
	env.api.activeSessions["sess-owner-run"] = &ActiveSessionInfo{SessionID: "sess-owner-run", WorkshopMode: "run"}
	env.api.activeSessionsMux.Unlock()
	owner := &UserClaims{UserID: "owner", Username: "manish"}
	if label, relay := env.api.goalLeadAskCaller(ctx, ws, "sess-owner-builder", owner); !relay || label != "manish in the Builder chat" {
		t.Fatalf("Builder chat caller = %q relay=%v", label, relay)
	}
	if _, relay := env.api.goalLeadAskCaller(ctx, ws, "sess-owner-run", owner); relay {
		t.Fatal("a Run chat must not relay the owner's words")
	}

	// The Builder chat passes the owner's direction; Pulse's turn is told to
	// record it with its normal tools and the log shows it as the owner's.
	if _, err := env.api.askGoalLead(ctx, "owner", ws, "sess-owner-builder", "manish in the Builder chat", "Tell Pulse to focus on USA subscribers this week.", "", true, 5*time.Second, ""); err != nil {
		t.Fatalf("ask_pulse relay: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	relay := fmt.Sprint(requests[len(requests)-1]["query"])
	for _, want := range []string{"a message from manish in the Builder chat", "record_pulse_goal_memory, source owner_answer", "record_pulse_focus_area action=propose"} {
		if !strings.Contains(relay, want) {
			t.Fatalf("relay turn lacks %q:\n%s", want, relay)
		}
	}
	if strings.Contains(relay, "Answer as a recommendation") {
		t.Fatalf("a relayed owner message is not a recommendation ask:\n%s", relay)
	}
	messages, err = listGoalLeadMessages(ctx, ws, 20)
	if err != nil {
		t.Fatal(err)
	}
	if last := messages[len(messages)-2]; last.Role != goalLeadTurnOwner || last.Source != "manish in the Builder chat" {
		t.Fatalf("relayed message logged as %+v", last)
	}
}

// Owner, 2026-10-08: the Builder and Pulse talk in a bounded thread, not in
// one-off messages, and the Builder trusts Pulse as the goal expert. Round 1:
// Pulse asks the Builder chat for a fact. Round 2 (same thread_id): Pulse
// decides with owner_needed: no, the thread closes, and the Builder is told to
// act without asking the owner; no decision request is created for the owner.
func TestPulseThreadAsksBackThenDecidesWithoutTheOwner(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, "Workflow", "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var queries []string
	previousRunner := goalLeadTurnRunner
	goalLeadTurnRunner = func(_ context.Context, reqMap map[string]interface{}, _ string, _ string) (internalSessionTurnResult, error) {
		mu.Lock()
		defer mu.Unlock()
		query := fmt.Sprint(reqMap["query"])
		queries = append(queries, query)
		if strings.Contains(query, "Round 2 of at most 6") {
			return internalSessionTurnResult{FinalResponse: "With 40 USA sign-ups last week the USA segment is the best bet.\n\ndecision: run the growth route for the USA segment today\nowner_needed: no (within run=auto)"}, nil
		}
		return internalSessionTurnResult{FinalResponse: "I need one number first.\nquestion: how many USA subscribers joined last week?"}, nil
	}
	t.Cleanup(func() { goalLeadTurnRunner = previousRunner })
	ctx := context.Background()

	first, err := env.api.askGoalLead(ctx, "owner", ws, "sess-builder", "manish in the Builder chat", "Which segment should the growth route target today?", "", true, 5*time.Second, "")
	if err != nil {
		t.Fatalf("round 1: %v", err)
	}
	result, _ := first["result"].(map[string]interface{})
	threadID, _ := first["thread_id"].(string)
	if threadID == "" || result["thread_open"] != true || result["pulse_question"] != "how many USA subscribers joined last week?" ||
		!strings.Contains(fmt.Sprint(first["next"]), "thread_id=") {
		t.Fatalf("round 1 = %v", first)
	}

	second, err := env.api.askGoalLead(ctx, "owner", ws, "sess-builder", "manish in the Builder chat", "40 USA subscribers joined last week.", threadID, true, 5*time.Second, "")
	if err != nil {
		t.Fatalf("round 2: %v", err)
	}
	result, _ = second["result"].(map[string]interface{})
	if second["thread_id"] != threadID || second["round"] != 2 || result["thread_open"] != false ||
		result["decision"] != "run the growth route for the USA segment today" || result["owner_needed"] != "no" {
		t.Fatalf("round 2 = %v", second)
	}
	if next := fmt.Sprint(second["next"]); !strings.Contains(next, "do not ask the owner again") {
		t.Fatalf("the Builder must act without asking the owner: %s", next)
	}
	mu.Lock()
	if len(queries) != 2 || !strings.Contains(queries[1], "continues your earlier replies in this thread") || !strings.Contains(queries[0], "owner_needed: yes|no") {
		t.Fatalf("Pulse must see round 2 as a continuation:\n%s", strings.Join(queries, "\n---\n"))
	}
	mu.Unlock()
	if _, err := env.api.askGoalLead(ctx, "owner", ws, "sess-builder", "manish in the Builder chat", "and Dubai?", threadID, true, 5*time.Second, ""); err == nil || !strings.Contains(err.Error(), "concluded") {
		t.Fatalf("a concluded thread must not take another round: %v", err)
	}
	if pending, err := listReportHumanInputs(ctx, ws, "pending", ""); err != nil || len(pending) != 0 {
		t.Fatalf("no decision request for the owner, got %v %v", pending, err)
	}
}
