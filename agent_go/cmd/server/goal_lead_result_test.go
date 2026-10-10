package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Drive the actual ask, saved reply, shared watcher and completion dispatcher.
// Only the model turn is replaced; its identity, permission lease and Pulse
// conversation logging use the normal production path.
func TestPulseDelayedBuilderResultUsesCurrentAuthorityOnce(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, ws), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	manifest, _, err := ReadWorkflowManifest(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := ensureGoalLeadConversation(ctx, ws, manifest.ID, time.Now().UTC(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	session := conv.SessionID
	env.api.lastQueryRequests = map[string]QueryRequest{session: {SelectedFolder: ws}}
	env.api.completionLoopStarted[session] = true
	setManifest := func(run string) {
		env.mock.mu.Lock()
		env.mock.files[manifestPath(ws)] = fmt.Sprintf(`{"id":%q,"label":"Reports","access":{"owners":["owner"]},"pulse":{"enabled":true,"autonomy":{"run":%q,"change":"ask"}}}`, manifest.ID, run)
		env.mock.mu.Unlock()
	}
	setManifest("auto")
	previousFind, previousBuilder, previousRunner, previousLimiter := findPulseBuilderChat, pulseBuilderAskTurn, goalLeadTurnRunner, pulseBuilderAsks
	findPulseBuilderChat = func(*StreamingAPI, context.Context, string, *WorkflowManifest, string, string) (string, error) {
		return "builder-result-test", nil
	}
	releaseBuilder := make(chan struct{})
	pulseBuilderAskTurn = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		<-releaseBuilder
		return internalSessionTurnResult{FinalResponse: "Measurement finished; the primary sample is unavailable and repair needs owner approval."}, nil
	}
	var turns atomic.Int32
	turnResult := make(chan error, 2)
	goalLeadTurnRunner = func(_ context.Context, req map[string]interface{}, gotSession, user string) (internalSessionTurnResult, error) {
		turns.Add(1)
		perms, held := goalWorkTurnPermissions(gotSession)
		if gotSession != session || user != "owner" || !held || perms.Run || perms.Change || req["pulse_lifecycle_turn"] != true || req["is_auto_notification"] != true {
			turnResult <- fmt.Errorf("wrong identity or current authority: session=%s user=%s held=%v perms=%+v req=%v", gotSession, user, held, perms, req)
		} else if query := fmt.Sprint(req["query"]); !strings.Contains(query, "primary sample is unavailable") || !strings.Contains(query, "Reconcile this result") || !strings.Contains(query, "does not mean that background work finished") {
			turnResult <- fmt.Errorf("missing evidence/reconciliation instruction: %s", query)
		} else {
			turnResult <- nil
		}
		return internalSessionTurnResult{FinalResponse: "Goal Work is blocked on approval; the unavailable primary sample remains pending verification."}, nil
	}
	pulseBuilderAsks = &hourlyAskLimiter{}
	t.Cleanup(func() {
		findPulseBuilderChat, pulseBuilderAskTurn, goalLeadTurnRunner, pulseBuilderAsks = previousFind, previousBuilder, previousRunner, previousLimiter
	})
	out, err := env.api.askBuilder(ctx, pulseBuilderAskRequest{UserID: "owner", WorkspacePath: ws, PulseSession: session, Message: "Measure the goal and report the result", Perms: stepworkflow.GoalWorkPermissions{Run: true}, SubmissionID: "measurement-result"})
	if err != nil || out["auto_notify"] != true {
		t.Fatalf("ask=%v error=%v", out, err)
	}
	call := lookupCrewFunctionCall(fmt.Sprint(out["call_id"]))
	executionID := fmt.Sprint(out["execution_id"])
	joinedID, err := env.api.startCrewFunctionWatch(QueryRequest{SelectedFolder: ws}, session, "owner", call, triggerTargetDefaultTimeout)
	if err != nil || joinedID != executionID {
		t.Fatalf("duplicate watcher=%s %v; want %s", joinedID, err, executionID)
	}
	// Permissions were reduced while Builder worked; the result must use them.
	setManifest("ask")
	close(releaseBuilder)
	agent := env.api.bgAgentRegistry.Get(session, executionID)
	deadline := time.Now().Add(3 * time.Second)
	for agent.GetStatus() != BGAgentCompleted && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if agent.GetStatus() != BGAgentCompleted {
		t.Fatalf("notification did not complete: %+v", agent.GetSnapshot())
	}
	// Long scheduled receipts still point to the saved call, rather than an
	// execution folder that has no function-call result.
	large := agent.GetSnapshot()
	large.Result = strings.Repeat("measurement evidence ", scheduledAutoNotificationResultMaxRunes)
	if notice := env.api.buildAutoNotificationMessage(session, large); !strings.Contains(notice, call.ID) || !strings.Contains(notice, "get_function_call") {
		t.Fatalf("long reply lost its saved-call reference: %s", notice)
	}
	if env.api.steerBackgroundAgentCompletion(session, executionID) {
		t.Fatal("Pulse result must queue instead of steering into a busy turn")
	}
	releaseLane := env.api.lockSessionInputLane(session)
	if env.api.executePulseResultTurn(session, "result while busy", nil) {
		releaseLane()
		t.Fatal("busy Pulse must retain the completion in the shared queue")
	}
	releaseLane()
	env.api.processBackgroundAgentCompletion(session, executionID)
	select {
	case err := <-turnResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Pulse was not resumed")
	}
	deadline = time.Now().Add(3 * time.Second)
	for !agent.GetSnapshot().CompletionNotified && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !agent.GetSnapshot().CompletionNotified {
		t.Fatal("delivery was not committed after the terminal result")
	}
	env.api.processBackgroundAgentCompletion(session, executionID)
	if turns.Load() != 1 {
		t.Fatalf("got %d Pulse turns, want one", turns.Load())
	}
	messages, err := listGoalLeadMessages(ctx, ws, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range messages {
		if msg.Source == goalLeadTurnFunctionResult && strings.Contains(msg.Text, "Goal Work is blocked") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Pulse reply not recorded: %+v", messages)
	}
}

func TestPulseResultDoesNotReviveDisabledOrRotatedConversation(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, ws), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ReadWorkflowManifest(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := ensureGoalLeadConversation(context.Background(), ws, manifest.ID, time.Now().UTC(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.api.lastQueryRequests = map[string]QueryRequest{conv.SessionID: {SelectedFolder: ws}}
	previous := goalLeadTurnRunner
	var turns atomic.Int32
	goalLeadTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		turns.Add(1)
		return internalSessionTurnResult{}, nil
	}
	t.Cleanup(func() { goalLeadTurnRunner = previous })
	for _, enabled := range []bool{false, true} {
		env.mock.mu.Lock()
		env.mock.files[manifestPath(ws)] = fmt.Sprintf(`{"id":%q,"access":{"owners":["owner"]},"pulse":{"enabled":%t}}`, manifest.ID, enabled)
		env.mock.mu.Unlock()
		expected := conv.SessionID
		if enabled {
			expected = goalLeadSessionID(manifest.ID, conv.Generation+1)
		}
		_, _, err := env.api.runGoalLeadTurn(context.Background(), ws, goalLeadTurn{Kind: goalLeadTurnFunctionResult, Body: "saved result", ExpectedSessionID: expected})
		if err != errPulseResultIneligible {
			t.Fatalf("enabled=%v: got %v", enabled, err)
		}
	}
	if turns.Load() != 0 {
		t.Fatal("ineligible completion ran Pulse")
	}
	env.api.stoppedSessions = map[string]bool{conv.SessionID: true}
	if env.api.executeSyntheticTurnWithOutcome(conv.SessionID, "saved result", "", nil) {
		t.Fatal("result revived a stopped Pulse")
	}
}

func TestBuilderReceivesDelayedPulseReplyInItsOwnChat(t *testing.T) {
	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, ws), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := goalLeadTurnRunner
	release := make(chan struct{})
	finished := make(chan struct{})
	goalLeadTurnRunner = func(context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error) {
		<-release
		close(finished)
		return internalSessionTurnResult{FinalResponse: "Keep the existing measurement window; no owner decision is needed."}, nil
	}
	t.Cleanup(func() { goalLeadTurnRunner = previous })
	out, err := env.api.askGoalLead(context.Background(), "owner", ws, "sess-builder", "the Builder chat", "What should I do next?", 0, "delayed-pulse-result")
	if err != nil || out["auto_notify"] != true {
		t.Fatalf("ask_pulse=%v error=%v", out, err)
	}
	call := lookupCrewFunctionCall(fmt.Sprint(out["call_id"]))
	if call.CallerChatSession != "sess-builder" {
		t.Fatalf("result bound to %q", call.CallerChatSession)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("Pulse did not answer")
	}
	agent := env.api.bgAgentRegistry.Get("sess-builder", fmt.Sprint(out["execution_id"]))
	deadline := time.Now().Add(3 * time.Second)
	for agent.GetStatus() != BGAgentCompleted && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if snap := agent.GetSnapshot(); snap.Status != BGAgentCompleted || !strings.Contains(snap.Result, "Keep the existing measurement window") {
		t.Fatalf("notification=%+v", snap)
	}
}
