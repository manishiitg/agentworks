package server

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// startLoginFlowCall starts Alpha's call of Beta.run_login_flow with a short
// timeout, bypassing call_function's one-minute minimum.
func startLoginFlowCall(t *testing.T, env crewFunctionEnv, timeout time.Duration) *crewFunctionCall {
	t.Helper()
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := env.alpha["define_function"].exec(ctx, loginFlowArgs); err != nil {
		t.Fatal(err)
	}
	target, err := resolveTriggerTarget(ctx, &UserClaims{UserID: "owner"}, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	caller, err := crewTriggerLinkCaller(linkAlphaPath)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	functions, err := callableFunctions(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := findCrewFunction(functions, "run_login_flow")
	if !ok {
		t.Fatal("run_login_flow not declared")
	}
	call, err := env.api.startCrewFunctionCall(ctx, "owner", caller, target, fn, map[string]interface{}{"build": "7"}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func callClosed(call *crewFunctionCall) bool {
	select {
	case <-call.done:
		return true
	default:
		return false
	}
}

// Progress reports keep a call alive past its timeout; once the target goes
// quiet the caller stops waiting, and the target's later answer is still
// accepted and delivered to the caller's chat.
func TestCrewFunctionTimeoutFollowsActivityAndAcceptsLateAnswer(t *testing.T) {
	env := newCrewFunctionEnv(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env.svc.heldConversation = nil
	started := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	env.svc.automationTurnRunner = func(ctx context.Context, req map[string]interface{}, session, user string) (internalSessionTurnResult, error) {
		started <- session
		<-release
		vars := common.GetSessionShellEnv(session)
		if err := os.MkdirAll(vars["FUNCTION_OUTPUT_DIR"], 0700); err != nil {
			return internalSessionTurnResult{}, err
		}
		if err := os.WriteFile(vars["FUNCTION_RESULT_FILE"], []byte(`{"passed":true}`), 0600); err != nil {
			return internalSessionTurnResult{}, err
		}
		return internalSessionTurnResult{FinalResponse: "Completed login check."}, nil
	}
	ctx := context.Background()
	timeout := 500 * time.Millisecond
	call := startLoginFlowCall(t, env, timeout)
	var receiver map[string]recordedTool
	select {
	case session := <-started:
		receiver = env.functionTools(t, linkBetaPath, session, nil)
	case <-time.After(3 * time.Second):
		t.Fatal("isolated execution never started")
	}
	executionID, err := env.api.startCrewFunctionWatch(QueryRequest{SelectedFolder: linkAlphaPath}, "sess-caller", "owner", call, timeout)
	if err != nil {
		t.Fatal(err)
	}

	// This exercises supervision while the live run record still omits its
	// session: progress must retain the worker's exact receiving identity.
	if _, err := env.beta["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": call.ID, "message": "wrong chat"}); err == nil {
		t.Fatal("another chat reported progress for an isolated execution")
	}
	for i := 0; i < 8; i++ {
		if _, err := receiver["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": call.ID, "message": "still testing"}); err != nil {
			t.Fatalf("progress %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if callClosed(call) {
		t.Fatalf("a call reporting progress timed out after %s: %v", timeout, call.snapshot())
	}

	select {
	case <-call.done:
	case <-time.After(3 * time.Second):
		t.Fatal("a quiet call never timed out")
	}
	timedOut := call.snapshot()
	if timedOut["status"] != "failed" || timedOut["timed_out"] != true || !strings.Contains(timedOut["error"].(string), "late answer") {
		t.Fatalf("timed-out call = %v", timedOut)
	}
	waitForNotification(t, env, executionID, "late answer is sent to you automatically")

	if _, err := receiver["report_function_progress"].exec(ctx, map[string]interface{}{"call_id": call.ID, "message": "almost done"}); err != nil {
		t.Fatalf("progress after the timeout was refused: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	lateDeadline := time.Now().Add(3 * time.Second)
	for call.snapshot()["late"] != true {
		if time.Now().After(lateDeadline) {
			t.Fatal("late terminal answer never arrived")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if late := call.snapshot(); late["status"] != "completed" {
		t.Fatalf("late call = %v", late)
	}
	if call.settle("completed", nil, "") {
		t.Fatal("a second terminal result was accepted")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		var delivered string
		for _, agent := range env.api.bgAgentRegistry.GetAll("sess-caller") {
			snapshot := agent.GetSnapshot()
			if agent.ID != executionID && snapshot.Status == BGAgentCompleted {
				delivered = snapshot.Result
			}
		}
		if delivered != "" {
			if !strings.Contains(delivered, "late answer") || !strings.Contains(delivered, `"passed": true`) {
				t.Fatalf("late notification = %q", delivered)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the late answer never reached the caller's chat")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForNotification(t *testing.T, env crewFunctionEnv, executionID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot := env.api.bgAgentRegistry.Get("sess-caller", executionID).GetSnapshot()
		if snapshot.Status != BGAgentRunning {
			if text := snapshot.Result + snapshot.Error; !strings.Contains(text, want) {
				t.Fatalf("notification = %+v, want %q", snapshot, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("notification not delivered: %+v", snapshot)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// After a restart a call is found from its saved record; one that was still
// open is settled as interrupted with its progress kept.
func TestCrewFunctionCallSurvivesRestartAsInterrupted(t *testing.T) {
	env := newCrewFunctionEnv(t)
	call := &crewFunctionCall{
		ID: "fn-restart-interrupted", UserID: "owner", Function: "run_login_flow",
		CallerKind: triggerCallerCrew, CallerProfileID: "work", CallerID: "alpha", CallerPath: linkAlphaPath,
		TargetKind: triggerCallerCrew, TargetProfileID: "work", TargetID: "beta", TargetPath: linkBetaPath,
		IsolatedExecution: true, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{}),
		Progress: []crewFunctionProgress{{At: time.Now(), Message: "halfway"}},
	}
	if err := persistStructuredFunctionAdmission(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	record := call.recordPath()

	polled, err := env.alpha["get_function_call"].exec(context.Background(), map[string]interface{}{"call_id": call.ID})
	if err != nil {
		t.Fatalf("lookup after restart: %v", err)
	}
	snapshot := decodeToolJSON(t, polled)
	if snapshot["status"] != "failed" || !strings.Contains(snapshot["error"].(string), "server restarted") || !strings.Contains(polled, "halfway") {
		t.Fatalf("after restart = %s", polled)
	}
	env.mock.mu.Lock()
	persisted := env.mock.files[record]
	env.mock.mu.Unlock()
	if !strings.Contains(persisted, "server restarted") {
		t.Fatalf("interrupted status not saved: %s", persisted)
	}
	if lookupCrewFunctionCall("fn-does-not-exist") != nil || lookupCrewFunctionCall("fn-../../etc") != nil {
		t.Fatal("unknown or unsafe IDs must not resolve")
	}
}
