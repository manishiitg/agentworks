package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type goalRPCWriter struct {
	rpc      *codexRPC
	requests []codexRPCMessage
	reply    func(codexRPCMessage) codexRPCMessage
}

func (w *goalRPCWriter) Write(raw []byte) (int, error) {
	var request codexRPCMessage
	if err := json.Unmarshal(raw, &request); err != nil {
		return 0, err
	}
	w.requests = append(w.requests, request)
	response := w.reply(request)
	w.rpc.mu.Lock()
	ch := w.rpc.pending[string(request.ID)]
	w.rpc.mu.Unlock()
	if ch != nil {
		ch <- response
	}
	return len(raw), nil
}
func (*goalRPCWriter) Close() error { return nil }

var _ io.WriteCloser = (*goalRPCWriter)(nil)

func goalFixtureRPC(reply func(codexRPCMessage) codexRPCMessage) (*codexRPC, *goalRPCWriter) {
	exited := make(chan struct{})
	close(exited)
	rpc := &codexRPC{events: make(chan codexRPCMessage, 32), done: make(chan struct{}), exited: exited, stop: func() {}, pending: map[string]chan codexRPCMessage{}}
	writer := &goalRPCWriter{rpc: rpc, reply: reply}
	rpc.stdin = writer
	return rpc, writer
}
func goalWire(status string) json.RawMessage {
	return json.RawMessage(`{"goal":{"objective":"Finish the task","status":"` + status + `","tokensUsed":42,"tokenBudget":1000,"timeUsedSeconds":12}}`)
}

// PLAT-746: ordinary turn completion must not end a native goal, and the
// host must never manufacture the next turn. Only the owned thread updates it.
func TestCodexGoalRunsNativeContinuationUntilTerminalState(t *testing.T) {
	for _, status := range []string{"complete", "blocked", "budgetLimited", "usageLimited", "cleared"} {
		t.Run(status, func(t *testing.T) {
			rpc, wire := goalFixtureRPC(func(codexRPCMessage) codexRPCMessage { return codexRPCMessage{} })
			session := &codexAppSession{owner: "chat", threadID: "thread", turnID: "first", goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"}, goalActivationPending: true}
			send := func(method, body string) {
				rpc.events <- codexRPCMessage{Method: method, Params: json.RawMessage(body)}
			}
			// Ignore the transient old clear/paused acknowledgements during activation.
			send("thread/goal/cleared", `{"threadId":"thread"}`)
			send("thread/goal/updated", `{"threadId":"thread","goal":{"objective":"Finish the task","status":"paused"}}`)
			send("thread/goal/updated", `{"threadId":"another-thread","goal":{"objective":"private other task","status":"complete"}}`)
			send("item/completed", `{"threadId":"thread","turnId":"first","item":{"id":"a","type":"agentMessage","text":"first step"}}`)
			send("turn/completed", `{"threadId":"thread","turn":{"id":"first","status":"completed"}}`)
			send("thread/goal/updated", `{"threadId":"thread","goal":{"objective":"Finish the task","status":"active"}}`)
			if status == "complete" {
				send("turn/started", `{"threadId":"thread","turn":{"id":"second","status":"inProgress"}}`)
				send("item/completed", `{"threadId":"thread","turnId":"second","item":{"id":"b","type":"agentMessage","text":"task finished"}}`)
			}
			if status == "cleared" {
				send("thread/goal/cleared", `{"threadId":"thread"}`)
			} else {
				send("thread/goal/updated", `{"threadId":"thread","goal":{"objective":"Finish the task","status":"`+status+`","tokensUsed":42,"tokenBudget":1000,"timeUsedSeconds":12}}`)
			}
			if status == "complete" {
				send("turn/completed", `{"threadId":"thread","turn":{"id":"second","status":"completed"}}`)
			}
			updates := []llmtypes.NativeGoalUpdate{}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			response, err := (&CodexCLIAdapter{}).consumeAppTurn(ctx, session, rpc, "thread", "first", func(chunk llmtypes.StreamChunk) {
				if chunk.NativeGoal != nil {
					updates = append(updates, *chunk.NativeGoal)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(wire.requests) != 0 {
				t.Fatal("host started a replacement goal turn")
			}
			if status == "complete" && !strings.Contains(response.Choices[0].Content, "task finished") {
				t.Fatal("native continuation was lost at first completion")
			}
			if len(updates) == 0 {
				t.Fatal("goal updates did not reach the host")
			}
			for _, update := range updates {
				if update.NativeSessionID != "thread" || (update.Goal != nil && update.Goal.Objective != "Finish the task") {
					t.Fatal("another thread's goal leaked")
				}
			}
			last := updates[len(updates)-1]
			if status == "cleared" {
				if last.Goal != nil {
					t.Fatal("clear did not remove goal")
				}
			} else {
				if last.Goal == nil || last.Goal.TokensUsed == nil || *last.Goal.TokensUsed != 42 || last.Goal.TokenBudget == nil || *last.Goal.TokenBudget != 1000 {
					t.Fatal("native goal counters lost")
				}
			}
		})
	}
}

// Invalid objectives leave the native goal untouched. Unsupported native
// methods are different from a transient failure; neither starts model work.
func TestCodexGoalControlDoesNotFallbackToAnOrdinaryPrompt(t *testing.T) {
	for _, scenario := range []string{"too_long", "show", "show_empty", "unsupported", "read_failed"} {
		t.Run(scenario, func(t *testing.T) {
			rpc, wire := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
				switch scenario {
				case "unsupported":
					return codexRPCMessage{Error: &codexRPCError{Code: -32601, Message: "Method not found"}}
				case "show_empty":
					return codexRPCMessage{Result: json.RawMessage(`{"goal":null}`)}
				case "read_failed":
					return codexRPCMessage{Error: &codexRPCError{Code: -32000, Message: "temporarily unavailable"}}
				default:
					return codexRPCMessage{Result: goalWire("paused")}
				}
			})
			session := &codexAppSession{owner: "chat", threadID: "thread"}
			command := llmtypes.NativeGoalCommand{Action: "show"}
			if scenario == "too_long" {
				command = llmtypes.NativeGoalCommand{Action: "set", Objective: strings.Repeat("x", 4001)}
			}
			snapshots := 0
			for i := 0; i < 2; i++ {
				_, activate, _, err := (&CodexCLIAdapter{}).prepareCodexGoalCommand(context.Background(), session, rpc, "thread", command, func(chunk llmtypes.StreamChunk) {
					if chunk.NativeGoal != nil {
						snapshots++
					}
				})
				if activate {
					t.Fatal("control started model work")
				}
				var unavailable *llmtypes.NativeGoalUnavailableError
				if scenario == "unsupported" && !errors.As(err, &unavailable) {
					t.Fatal("unsupported native goal was not reported plainly")
				}
				if scenario == "read_failed" && (err == nil || errors.As(err, &unavailable)) {
					t.Fatal("transient read was mislabeled unsupported")
				}
				if scenario == "too_long" && (err == nil || len(wire.requests) != 0) {
					t.Fatal("invalid objective touched native goal")
				}
				if scenario == "show" && err != nil {
					t.Fatal(err)
				}
			}
			if (scenario == "show" || scenario == "show_empty") && snapshots != 2 {
				t.Fatal("explicit unchanged/unset read lost its snapshot")
			}
			for _, request := range wire.requests {
				if request.Method == "turn/start" || request.Method == "thread/goal/clear" {
					t.Fatal("read/invalid goal control changed work")
				}
			}
		})
	}
}

// Stop and missing native continuation pause the native goal rather than
// creating an AgentWorks continuation loop or abandoning an active goal.
func TestCodexGoalStopAndNativeContinuationGapPauseTheOwnedThread(t *testing.T) {
	previous := codexGoalFollowupWait
	codexGoalFollowupWait = 20 * time.Millisecond
	defer func() { codexGoalFollowupWait = previous }()
	for _, scenario := range []string{"native_gap", "native_gap_late_start", "stop"} {
		t.Run(scenario, func(t *testing.T) {
			var rpc *codexRPC
			var wire *goalRPCWriter
			rpc, wire = goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
				if request.Method == "thread/goal/set" {
					if scenario == "native_gap_late_start" {
						rpc.events <- codexRPCMessage{Method: "turn/started", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"late","status":"inProgress"}}`)}
					}
					return codexRPCMessage{Result: goalWire("paused")}
				}
				return codexRPCMessage{Result: json.RawMessage(`{}`)}
			})
			session := &codexAppSession{owner: "chat", threadID: "thread", goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: "active"}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if scenario == "stop" {
				cancel()
			} else {
				rpc.events <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
			}
			_, err := (&CodexCLIAdapter{}).consumeAppTurn(ctx, session, rpc, "thread", "turn", func(llmtypes.StreamChunk) {})
			if scenario != "stop" && err != nil {
				t.Fatal(err)
			}
			if scenario == "stop" && !errors.Is(err, context.Canceled) {
				t.Fatal("Stop did not cancel the owned turn")
			}
			if session.goal == nil || session.goal.Status != "paused" {
				t.Fatal("native goal still active")
			}
			paused := false
			for _, request := range wire.requests {
				var params struct {
					ThreadID string `json:"threadId"`
					Status   string `json:"status"`
				}
				if json.Unmarshal(request.Params, &params) != nil {
					t.Fatal("invalid native params")
				}
				if params.ThreadID != "thread" {
					t.Fatal("control reached another thread")
				}
				if request.Method == "thread/goal/set" && params.Status == "paused" {
					paused = true
				}
				if request.Method == "turn/start" {
					t.Fatal("host invented a goal continuation")
				}
			}
			if !paused {
				t.Fatal("native pause command missing")
			}
			// PR166: a continuation racing the pause cannot outlive ownership.
			select {
			case <-rpc.done:
			default:
				t.Fatal("native gap/Stop left an unobserved process running")
			}
		})
	}
}

// PR166: uncertain activation must not leave an autonomous goal unobserved;
// failed first-turn startup and explicit clear must discard the setup guard.
func TestCodexGoalAbandonedStartupCannotLeaveAnActiveGoal(t *testing.T) {
	for _, scenario := range []string{"turn_failed", "activation_canceled", "cleanup_failed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rpc, wire := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
				if request.Method == "turn/start" {
					if scenario == "turn_failed" {
						return codexRPCMessage{Error: &codexRPCError{Code: -32000, Message: "start failed"}}
					}
					return codexRPCMessage{Result: json.RawMessage(`{"turn":{"id":"first"}}`)}
				}
				if request.Method == "thread/goal/set" {
					var params struct {
						Status string `json:"status"`
					}
					_ = json.Unmarshal(request.Params, &params)
					if params.Status == "active" {
						cancel()
						return codexRPCMessage{Error: &codexRPCError{Code: -32000, Message: "lost activation reply"}}
					}
					if scenario == "cleanup_failed" {
						return codexRPCMessage{Error: &codexRPCError{Code: -32000, Message: "pause failed"}}
					}
					return codexRPCMessage{Result: goalWire("paused")}
				}
				return codexRPCMessage{Result: json.RawMessage(`{}`)}
			})
			session := &codexAppSession{owner: "chat", threadID: "thread", goalActivationPending: true, goal: &llmtypes.NativeGoal{Objective: "Finish the task", Status: "paused"}}
			_, err := startCodexGoalTurn(ctx, session, rpc, "thread", map[string]any{"threadId": "thread"}, true, func(llmtypes.StreamChunk) {})
			if err == nil || session.goalActivationPending {
				t.Fatal("abandoned startup retained its activation guard")
			}
			if scenario == "activation_canceled" {
				if session.goal.Status != "paused" {
					t.Fatal("uncertain active goal was not paused")
				}
				interrupted := false
				for _, request := range wire.requests {
					if request.Method == "turn/interrupt" {
						interrupted = true
					}
				}
				if !interrupted {
					t.Fatal("cleanup reused canceled context instead of interrupting")
				}
			}
			if scenario == "cleanup_failed" || scenario == "turn_failed" {
				select {
				case <-rpc.done:
				default:
					t.Fatal("failed startup/cleanup left the process running")
				}
			}
		})
	}
	rpc, _ := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
		if request.Method == "thread/goal/get" {
			return codexRPCMessage{Result: goalWire("paused")}
		}
		return codexRPCMessage{Result: json.RawMessage(`{}`)}
	})
	session := &codexAppSession{owner: "chat", threadID: "thread", goalActivationPending: true}
	_, _, _, err := (&CodexCLIAdapter{}).prepareCodexGoalCommand(context.Background(), session, rpc, "thread", llmtypes.NativeGoalCommand{Action: "clear"}, func(llmtypes.StreamChunk) {})
	if err != nil || session.goalActivationPending {
		t.Fatal("explicit clear retained activation guard")
	}
}

// PLAT-746-988: non-persistent cleanup belongs to the complete Generate call,
// not each native goal turn. Use a subprocess wire fixture, not a real AI CLI.
func TestCodexGoalNonPersistentCallKeepsProcessAcrossNativeTurns(t *testing.T) {
	if os.Getenv("AGENTWORKS_GOAL_RPC_FIXTURE") == "1" {
		var mu sync.Mutex
		send := func(v any) { mu.Lock(); defer mu.Unlock(); _ = json.NewEncoder(os.Stdout).Encode(v) }
		notify := func(method, body string) { send(codexRPCMessage{Method: method, Params: json.RawMessage(body)}) }
		wakeFixture := os.Getenv("AGENTWORKS_GOAL_WAKE_FIXTURE") == "1"
		goalStatus := ""
		turnCount := 0
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			var req codexRPCMessage
			if json.Unmarshal(scan.Bytes(), &req) != nil {
				os.Exit(2)
			}
			if len(req.ID) == 0 {
				continue
			}
			result := json.RawMessage(`{}`)
			activate := false
			pausedFirst := false
			switch req.Method {
			case "thread/start":
				result = json.RawMessage(`{"thread":{"id":"fixture-thread"}}`)
			case "thread/goal/get":
				result = json.RawMessage(`{"goal":null}`)
				if goalStatus != "" {
					result = goalWire(goalStatus)
				}
			case "turn/start":
				turnCount++
				id := "first"
				if wakeFixture && turnCount == 2 {
					id = "second"
				}
				if wakeFixture && turnCount == 3 {
					id = "third"
				}
				result = json.RawMessage(`{"turn":{"id":"` + id + `"}}`)
			case "thread/goal/set":
				var params struct {
					Status string `json:"status"`
				}
				_ = json.Unmarshal(req.Params, &params)
				pausedFirst = wakeFixture && goalStatus == "active" && params.Status == "paused" && turnCount == 1
				goalStatus = params.Status
				result = goalWire(params.Status)
				activate = params.Status == "active"
			}
			send(codexRPCMessage{ID: req.ID, Result: result})
			if wakeFixture {
				if pausedFirst {
					notify("thread/goal/updated", `{"threadId":"fixture-thread","goal":{"objective":"Finish the task","status":"paused"}}`)
					notify("turn/completed", `{"threadId":"fixture-thread","turn":{"id":"first","status":"completed"}}`)
				}
				if req.Method == "turn/start" && turnCount == 2 {
					notify("item/completed", `{"threadId":"fixture-thread","turnId":"second","item":{"id":"wake-answer","type":"agentMessage","text":"wake done"}}`)
					notify("turn/completed", `{"threadId":"fixture-thread","turn":{"id":"second","status":"completed"}}`)
				}
				if activate && turnCount == 3 {
					goalStatus = "complete"
					notify("item/completed", `{"threadId":"fixture-thread","turnId":"third","item":{"id":"goal-answer","type":"agentMessage","text":"goal done"}}`)
					notify("thread/goal/updated", `{"threadId":"fixture-thread","goal":{"objective":"Finish the task","status":"complete"}}`)
					notify("turn/completed", `{"threadId":"fixture-thread","turn":{"id":"third","status":"completed"}}`)
				}
				continue
			}
			if activate {
				go func() {
					notify("turn/completed", `{"threadId":"fixture-thread","turn":{"id":"first","status":"completed"}}`)
					// Leave an actual gap after first completion, while the parent still
					// owns this non-persistent process. No host second turn is requested.
					time.Sleep(30 * time.Millisecond)
					notify("turn/started", `{"threadId":"fixture-thread","turn":{"id":"second","status":"inProgress"}}`)
					notify("item/completed", `{"threadId":"fixture-thread","turnId":"second","item":{"id":"answer","type":"agentMessage","text":"native second turn"}}`)
					notify("thread/goal/updated", `{"threadId":"fixture-thread","goal":{"objective":"Finish the task","status":"complete"}}`)
					notify("turn/completed", `{"threadId":"fixture-thread","turn":{"id":"second","status":"completed"}}`)
				}()
			}
		}
		os.Exit(0)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// The selected process is this test's protocol fixture; no installed Codex,
	// login, model, network or sandbox-policy changes participate.
	wrapper := "#!/bin/sh\nexec " + shellQuoteGoalFixture(binary) + " -test.run '^TestCodexGoalNonPersistentCallKeepsProcessAcrossNativeTurns$'\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENTWORKS_GOAL_RPC_FIXTURE", "1")
	owner := "goal-nonpersistent-fixture"
	t.Cleanup(func() { closeCodexAppSession(owner) })
	opts := &llmtypes.CallOptions{}
	WithInteractiveSessionID(owner)(opts)
	WithProjectDirID(dir)(opts)
	// Leave persistence unspecified: this is the default non-persistent lane.
	if codexPersistentInteractiveFromOptions(opts) {
		t.Fatal("fixture accidentally persistent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = llmtypes.WithNativeGoalCommand(ctx, llmtypes.NativeGoalCommand{SessionID: owner, Action: "set", Objective: "Finish the task"})
	response, err := NewCodexCLIAdapter("", "codex-cli", &MockLogger{}).generateContentAppServer(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "/goal Finish the task")}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) == 0 || !strings.Contains(response.Choices[0].Content, "native second turn") {
		t.Fatal("non-persistent call ended at the first native goal turn")
	}
	if codexAppSessionForOwner(owner) != nil {
		t.Fatal("non-persistent call did not clean up after terminal goal")
	}
}

func shellQuoteGoalFixture(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// PLAT-746-988: background work must not inherit an active goal, and a
// cancelled restore must not resume a changed objective or an active goal.
func TestCodexGoalWakePauseAndRestoreStayOnTheOwnedGoal(t *testing.T) {
	rpc, wire := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
		status := "active"
		if request.Method == "thread/goal/set" {
			status = "paused"
		}
		return codexRPCMessage{Result: goalWire(status)}
	})
	session := &codexAppSession{owner: "chat", threadID: "thread"}
	updates := []llmtypes.NativeGoalUpdate{}
	err := pauseCodexGoalForTurn(context.Background(), session, rpc, "thread", llmtypes.NativeGoalTurnPause{SessionID: "chat", Reason: "background_wake"}, func(chunk llmtypes.StreamChunk) {
		if chunk.NativeGoal != nil {
			updates = append(updates, *chunk.NativeGoal)
		}
	})
	if err != nil || len(updates) != 1 || updates[0].Reason != "background_wake" || updates[0].Goal.Status != "paused" {
		t.Fatal("wake did not confirm a native pause", err)
	}
	for _, req := range wire.requests {
		if req.Method == "turn/start" {
			t.Fatal("pause started or replaced model work")
		}
	}
	for _, status := range []string{"paused", "active"} {
		rpc, wire := goalFixtureRPC(func(codexRPCMessage) codexRPCMessage { return codexRPCMessage{Result: goalWire(status)} })
		objective := "Different old goal"
		if status == "active" {
			objective = "Finish the task"
		}
		_, activate, _, err := (&CodexCLIAdapter{}).prepareCodexGoalCommand(context.Background(), session, rpc, "thread", llmtypes.NativeGoalCommand{Action: "resume", Objective: objective}, func(llmtypes.StreamChunk) {})
		if err == nil || activate || len(wire.requests) != 1 || wire.requests[0].Method != "thread/goal/get" {
			t.Fatal("stale restore changed the goal")
		}
	}
	// A rejected pause cannot retain active autonomous work without its consumer.
	for _, mode := range []string{"turn", "controller"} {
		failedRPC, _ := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
			if request.Method == "thread/goal/set" {
				return codexRPCMessage{Error: &codexRPCError{Code: -32000, Message: "pause refused"}}
			}
			return codexRPCMessage{Result: goalWire("active")}
		})
		failedSession := &codexAppSession{owner: "failed-pause-" + mode, threadID: "thread", rpc: failedRPC}
		if mode == "turn" {
			err = pauseCodexGoalForTurn(context.Background(), failedSession, failedRPC, "thread", llmtypes.NativeGoalTurnPause{Reason: "background_wake"}, nil)
		} else {
			codexAppSessions.Lock()
			codexAppSessions.sessions[failedSession.owner] = failedSession
			codexAppSessions.Unlock()
			_, err = ControlCodexNativeGoal(context.Background(), failedSession.owner, "wake_pause")
			closeCodexAppSession(failedSession.owner)
		}
		if err == nil || failedSession.goalPausedForWake {
			t.Fatal("rejected pause retained the active native goal")
		}
		select {
		case <-failedRPC.done:
		default:
			t.Fatal("rejected pause left autonomous process open")
		}
	}

	owner := "native-goal-control-" + codexRandomHex(4)
	session.owner = owner
	session.rpc = rpc
	codexAppSessions.Lock()
	codexAppSessions.sessions[owner] = session
	codexAppSessions.Unlock()
	defer closeCodexAppSession(owner)
	update, err := ControlCodexNativeGoal(context.Background(), owner, "show")
	if err != nil || update == nil || update.NativeSessionID != "thread" || update.Provider != "codex-cli" {
		t.Fatal("control result lost exact native identity", err)
	}
	for _, action := range []string{"pause", "wake_pause"} {
		quotaRPC, _ := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
			status := "usageLimited"
			if request.Method == "thread/goal/set" {
				status = "paused"
			}
			return codexRPCMessage{Result: goalWire(status)}
		})
		session.mu.Lock()
		session.rpc = quotaRPC
		session.mu.Unlock()
		update, err := ControlCodexNativeGoal(context.Background(), owner, action)
		if err != nil || update.Goal == nil || update.Goal.Status != "paused" || session.goalPausedForWake != (action == "wake_pause") {
			t.Fatal("usage-limited goal pause was not confirmed", action, err)
		}
		quotaRPC.close()
	}

}

// A wake pause must not kill native background work merely because the caller
// uses the non-persistent default. The same process serves pause/wake/resume.
func TestCodexGoalNonPersistentWakeRetainsItsOwnedProcessUntilRestore(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wrapper := "#!/bin/sh\nexec " + shellQuoteGoalFixture(binary) + " -test.run '^TestCodexGoalNonPersistentCallKeepsProcessAcrossNativeTurns$'\n"
	if err = os.WriteFile(filepath.Join(dir, "codex"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENTWORKS_GOAL_RPC_FIXTURE", "1")
	t.Setenv("AGENTWORKS_GOAL_WAKE_FIXTURE", "1")
	owner := "goal-wake-nonpersistent"
	t.Cleanup(func() { closeCodexAppSession(owner) })
	opts := &llmtypes.CallOptions{}
	WithInteractiveSessionID(owner)(opts)
	WithProjectDirID(dir)(opts)
	chunks := make(chan llmtypes.StreamChunk, 64)
	opts.StreamChan = chunks
	active := make(chan struct{}, 1)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for chunk := range chunks {
			if chunk.NativeGoal != nil && chunk.NativeGoal.Goal != nil && chunk.NativeGoal.Goal.Status == "active" {
				select {
				case active <- struct{}{}:
				default:
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := NewCodexCLIAdapter("", "codex-cli", &MockLogger{})
	done := make(chan error, 1)
	go func() {
		_, err := adapter.generateContentAppServer(llmtypes.WithNativeGoalCommand(ctx, llmtypes.NativeGoalCommand{SessionID: owner, Action: "set", Objective: "Finish the task"}), []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "/goal Finish the task")}, opts)
		done <- err
	}()
	select {
	case <-active:
	case <-ctx.Done():
		t.Fatal("goal did not start")
	}
	if _, err = ControlCodexNativeGoal(ctx, owner, "wake_pause"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	<-drained
	opts.StreamChan = nil
	session := codexAppSessionForOwner(owner)
	if session == nil {
		t.Fatal("wake pause closed non-persistent native process")
	}
	session.mu.Lock()
	rpc := session.rpc
	session.mu.Unlock()
	wakeCtx := llmtypes.WithNativeGoalTurnPause(ctx, llmtypes.NativeGoalTurnPause{SessionID: owner, Reason: "background_wake"})
	response, err := adapter.generateContentAppServer(wakeCtx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Background result")}, opts)
	if err != nil || !strings.Contains(response.Choices[0].Content, "wake done") {
		t.Fatal("paused wake failed", err)
	}
	session = codexAppSessionForOwner(owner)
	if session == nil || session.rpc != rpc {
		t.Fatal("wake did not retain the same owned native process")
	}
	response, err = adapter.generateContentAppServer(llmtypes.WithNativeGoalCommand(ctx, llmtypes.NativeGoalCommand{SessionID: owner, Action: "resume", Objective: "Finish the task"}), []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Resume after wake")}, opts)
	if err != nil || !strings.Contains(response.Choices[0].Content, "goal done") {
		t.Fatal("goal restore failed", err)
	}
	if codexAppSessionForOwner(owner) != nil {
		t.Fatal("non-persistent restored goal did not clean up at completion")
	}
}
