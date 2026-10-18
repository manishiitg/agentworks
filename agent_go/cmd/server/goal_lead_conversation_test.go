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
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	manifest.CreatedBy = "owner"
	manifest.Pulse = &WorkflowPulseConfig{Enabled: true, OutwardActions: "ask"}
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
		if !saveReport {
			// A concurrent Builder save has no bound Pulse cycle and must not count.
			_, _ = recordPulseGoalCheckFromToolArgs(context.Background(), map[string]interface{}{"workspace_path": ws, "status": "not_measured", "summary": "Builder saved another observation."})
		}
		if saveReport {
			if _, err := recordPulseGoalCheckFromToolArgs(ctx, map[string]interface{}{"workspace_path": ws, "status": "not_measured", "summary": "Outcome readings are missing. Measure replies before changing the approach.", "next_check_in_hours": float64(1), "next_check_reason": "An earlier question"}); err != nil {
				return internalSessionTurnResult{}, err
			}
		}
		return internalSessionTurnResult{FinalResponse: "Goal check saved."}, nil
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
	if err != nil || stored == nil {
		t.Fatalf("goal check missing: %+v %v", stored, err)
	}
	if gap := parseStoredTime(stored.NextCheckAt).Sub(parseStoredTime(stored.CheckedAt)); gap != pulseCheckMinGap {
		t.Fatalf("check bounds lost: %s", gap)
	}
	// A successful provider exit without a newly saved check is incomplete;
	// neither the previous report nor a final chat sentence can complete it.
	saveReport = false
	result := env.api.scheduler.runGoalLeadPassStep(ctx, sctx, pulseLifecycleStep{label: "goal-check", goalLead: true, query: "Save this week's report"})
	if result.outcome == pulseLifecycleStepCompleted || result.err == nil || !strings.Contains(result.err.Error(), "without saving") {
		t.Fatalf("old report completed a new cycle: %+v", result)
	}

	// OFF rejects all entry kinds before the runner and preserves the conversation.
	manifest.Pulse.Enabled = false
	raw, _ = json.Marshal(manifest)
	env.mock.mu.Lock()
	env.mock.files[manifestPath(ws)] = string(raw)
	env.mock.mu.Unlock()
	before := len(sessions)
	for _, kind := range []string{goalLeadTurnCheck, goalLeadTurnGoalWork, goalLeadTurnRunFailed, goalLeadTurnOwner, goalLeadTurnAsk, goalLeadTurnSlack, goalLeadTurnFunctionResult, goalLeadTurnAgentMessage} {
		if _, _, err := env.api.runGoalLeadTurn(ctx, ws, goalLeadTurn{Kind: kind, Body: "Do not start"}); err == nil {
			t.Fatalf("OFF admitted %s", kind)
		}
	}
	if len(sessions) != before {
		t.Fatal("OFF reached the provider")
	}
	if next, _ := nextPulseCheckAt(ctx, ws, manifest, stored, time.Time{}); !next.IsZero() {
		t.Fatalf("OFF kept a check: %s", next)
	}
	if run, err := env.api.scheduler.TriggerGoalCheck(ws); err == nil || run != "" {
		t.Fatalf("OFF queued a schedule: %s %v", run, err)
	}
	// A saved inbox address cannot acknowledge messages to the disabled Pulse.
	// Call the same send boundary used by explicit replies, not ask_pulse's gate.
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, Label: "External agent"}
	target := triggerTarget{Kind: triggerCallerWorkflow, Path: ws, Manifest: manifest, Chat: &codeChat{Key: pulseBuilderChatKey, SessionID: sessions[0]}}
	if receipt, err := env.api.sendAgentMessage(internalBotRequestContext(ctx, "owner"), "owner", caller, target, "Do not deliver", "", "pulse-off-send"); err == nil || receipt != nil || !strings.Contains(err.Error(), "Pulse is off") {
		t.Fatalf("OFF accepted inbox mail: %v %v", receipt, err)
	}

}
