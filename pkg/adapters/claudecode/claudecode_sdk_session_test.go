package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872: a kept session may take the next turn only with the same launch
// and the same conversation; anything else starts a fresh session. A changed
// launch is named by part (never by value) so the log shows what changed.
func TestClaudeSDKReuseDecision(t *testing.T) {
	launch := func(model, system string, mcp string) claudeSDKLaunch {
		// mcpagent also passes the instructions as a launch option (#2886).
		opts := &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{MetadataKeyMCPConfig: mcp, MetadataKeyResumeSessionID: model + system, llmtypes.CodingProviderLaunchSystemPromptMetadataKey: system}}}
		return claudeSDKLaunchParts(model, system, "", "default", []string{"SECRET_X=v"}, opts, false)
	}
	base := launch("m1", "sys", "{}")
	for _, tc := range []struct {
		name           string
		wanted         claudeSDKLaunch
		keptID, resume string
		reuse          bool
		reason         string
	}{
		{"changed model and current instructions", launch("m2", "new rules", "{}"), "s1", "s1", true, ""},
		{"model and MCP servers changed", launch("m2", "sys", "{\"x\":1}"), "s1", "s1", false, "launch_changed:option:" + MetadataKeyMCPConfig},
		{"new conversation", launch("m1", "sys", "{}"), "s1", "", false, "new_conversation"},
		{"other conversation", launch("m1", "sys", "{}"), "s1", "s2", false, "other_conversation"},
	} {
		reuse, reason := claudeSDKReuseDecision(base, tc.wanted, tc.keptID, tc.resume)
		if reuse != tc.reuse || reason != tc.reason {
			t.Errorf("%s: got %t %q, want %t %q", tc.name, reuse, reason, tc.reuse, tc.reason)
		}
	}
}

// The background line is shown in chat: inline NAME=value assignments can carry
// secret values and must never reach it.
func TestClaudeSDKBackgroundCommandLabelHidesInlineValues(t *testing.T) {
	for in, want := range map[string]string{
		"API_KEY=abc123 PORT=3000 npm run dev": "npm run dev",
		"export TOKEN='x y' && npm start":      "&& npm start",
		"DB_URL=postgres://u:p@h/db":           "",
		"npm run dev\nsecond line":             "npm run dev",
		strings.Repeat("a", 200):               strings.Repeat("a", 119) + "…",
	} {
		if got := claudeBackgroundCommandLabel(in); got != want {
			t.Errorf("label(%q) = %q, want %q", in, got, want)
		}
	}
}

// A kept session closes itself when its last background task ends, and a
// kept session whose launch no longer matches is closed (its tasks stopped)
// instead of taking the turn. A fake bridge stands in for the SDK.
func TestClaudeSDKKeptSessionLifecycle(t *testing.T) {
	var mu sync.Mutex
	var closed, woke []string
	SetSDKBackgroundListener(func(u SDKBackgroundUpdate) {
		mu.Lock()
		defer mu.Unlock()
		if u.Closed != "" {
			closed = append(closed, u.Owner+":"+u.Closed)
		}
		if u.Wake {
			woke = append(woke, u.Owner)
		}
	})
	defer SetSDKBackgroundListener(nil)
	grace, hold := claudeSDKWakeGrace, claudeSDKWakeHoldLimit
	claudeSDKWakeGrace, claudeSDKWakeHoldLimit = 300*time.Millisecond, 2*time.Second
	defer func() { claudeSDKWakeGrace, claudeSDKWakeHoldLimit = grace, hold }()

	// The fake bridge runs its opening (for example: wait for a trigger line,
	// then report an empty task list), then exits on stop_background like the
	// real bridge between turns.
	start := func(owner, opening string) *claudeSDKSession {
		life, kill := context.WithCancel(context.Background())
		cmd := exec.CommandContext(life, "sh", "-c", opening+` while read line; do case "$line" in *stop_background*) exit 0;; esac; done`)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		s, err := startClaudeSDKSession(owner, claudeSDKLaunch{"model": {7}}, cmd, stdin, stdout, &claudeLockedBuffer{}, kill, func() {}, nil)
		if err != nil {
			t.Fatal(err)
		}
		s.setNativeID("native-1")
		s.setTasks([]SDKBackgroundTask{{TaskID: "t1", Command: "npm run dev"}})
		s.park()
		return s
	}
	waitExit := func(s *claudeSDKSession) {
		select {
		case <-s.exited:
		case <-time.After(10 * time.Second):
			t.Fatal("kept session did not exit")
		}
	}

	finished := start("chat-finished", `read trigger; echo '{"type":"sdk_background","background":[]}';`)
	if err := finished.input.write(map[string]any{"type": "go"}); err != nil {
		t.Fatal(err)
	}
	waitExit(finished)

	// PLAT-746-899: Claude starts its own turn when its last task finishes. That turn was
	// dropped and the session closed under it; now the session waits and holds the turn
	// for the chat turn that adopts it.
	wake := start("chat-wake", `read trigger; echo '{"type":"sdk_background","background":[]}'; echo '{"type":"assistant","sdk_parked_turn":true,"message":{"content":[{"type":"text","text":"It finished"}]}}';`)
	if err := wake.input.write(map[string]any{"type": "go"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !ClaudeSDKWakePending("chat-wake") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	nativeID, wakeID := ClaudeSDKWakeIdentity("chat-wake")
	if nativeID != "native-1" || wakeID == 0 {
		t.Fatal("wake lacks exact identity")
	}
	if QueueClaudeSDKWake("chat-wake", nativeID, wakeID+1) {
		t.Fatal("a stale wake claimed another hold")
	}
	if !QueueClaudeSDKWake("chat-wake", nativeID, wakeID) {
		t.Fatal("host could not queue the exact wake")
	}
	time.Sleep(claudeSDKWakeHoldLimit + 50*time.Millisecond) // queued behind a longer foreground or schedule turn
	if !ClaudeSDKWakePending("chat-wake") {
		t.Fatal("queued wake expired before the lane became free")
	}
	adopted, first := takeClaudeSDKWake("chat-wake")
	if adopted != wake || !strings.Contains(first, "It finished") {
		t.Fatalf("Claude's own turn was not held for adoption: session=%v first=%q", adopted != nil, first)
	}
	wake.kill()
	waitExit(wake)

	replaced := start("chat-replaced", "")
	if s := takeKeptClaudeSDKSession("chat-replaced", claudeSDKLaunch{"model": {8}}, "native-1"); s != nil {
		t.Fatal("a kept session with another launch must not take the turn")
	}
	waitExit(replaced)

	kept := start("chat-kept", "")
	if s := takeKeptClaudeSDKSession("chat-kept", claudeSDKLaunch{"model": {7}}, "native-1"); s != kept {
		t.Fatal("an unchanged launch of the same conversation must reuse the kept session")
	}
	// A session that dies while it still lists tasks clears the chat's line.
	kept.kill()
	waitExit(kept)

	// A held wake nobody adopts (lost adopt, crash, stuck chat) is let go after the hold
	// limit, never left paused with nobody reading; with no tasks left the session ends.
	lost := start("chat-wake-lost", `read trigger; echo '{"type":"sdk_background","background":[]}'; echo '{"type":"assistant","sdk_parked_turn":true,"message":{"content":[]}}';`)
	if err := lost.input.write(map[string]any{"type": "go"}); err != nil {
		t.Fatal(err)
	}
	waitExit(lost)

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(closed, ","); got != "chat-finished:finished,chat-replaced:replaced,chat-kept:finished,chat-wake-lost:stopped" {
		t.Fatalf("closed updates = %q", got)
	}
	if got := strings.Join(woke, ","); got != "chat-wake,chat-wake-lost" {
		t.Fatalf("wake updates = %q", got)
	}
}

// "Allow for this chat" must never write a lasting rule into the project or the account: every rule Claude
// suggests is kept to this chat's session, and only allow rules pass.
func TestClaudeSDKAlwaysAllowStaysInThisChat(t *testing.T) {
	raw := json.RawMessage(`[{"type":"addRules","behavior":"allow","destination":"localSettings","rules":[{"toolName":"Bash","ruleContent":"git commit:*"}]},{"type":"addRules","behavior":"deny","destination":"userSettings","rules":[]},{"type":"setMode","mode":"bypassPermissions","destination":"session"}]`)
	rules := claudeSDKChatScopedRules(raw)
	if len(rules) != 1 || rules[0]["destination"] != "session" || rules[0]["behavior"] != "allow" {
		t.Fatalf("rules = %+v, want one allow rule scoped to the session", rules)
	}
	if claudeSDKChatScopedRules() != nil || claudeSDKChatScopedRules(json.RawMessage(`not json`)) != nil {
		t.Fatal("no suggestion must offer no always-allow")
	}
}

// PLAT-872: a crashed native helper must stop showing Running, while an
// already-completed helper and an older resumed run keep their terminal state.
func TestClaudeSDKHelperExitAndParentIdentity(t *testing.T) {
	var updates []SDKHelperUpdate
	SetSDKHelperListener(func(update SDKHelperUpdate) { updates = append(updates, update) })
	defer SetSDKHelperListener(nil)
	session := &claudeSDKSession{owner: "parent-chat", nativeID: "native-parent"}
	session.observeHelper(SDKHelperTask{TaskID: "done", RunID: "2", State: "started", Description: "Review parser"})
	session.observeHelper(SDKHelperTask{TaskID: "done", RunID: "2", State: "finished", Description: "Review parser"})
	session.observeHelper(SDKHelperTask{TaskID: "done", RunID: "1", State: "started"})
	session.observeHelper(SDKHelperTask{TaskID: "running", RunID: "1", State: "started"})
	tokens := int64(18400)
	session.observeHelper(SDKHelperTask{TaskID: "running", RunID: "1", State: "progress", TotalTokens: &tokens, DurationMS: 134000})
	session.interruptHelpers()
	session.interruptHelpers()
	if len(updates) != 5 || updates[4].Task.State != "interrupted" || session.helpers["done"].State != "finished" {
		t.Fatalf("wrong helper lifecycle: %+v", updates)
	}
	if updates[4].Task.TotalTokens == nil || *updates[4].Task.TotalTokens != 18400 {
		t.Fatal("terminal state lost the last native task count")
	}
	for _, update := range updates {
		if update.Owner != "parent-chat" || update.NativeSessionID != "native-parent" {
			t.Fatalf("helper escaped its parent: %+v", update)
		}
	}
}

// PLAT-746-941: exercise the actual shipped JS bridge, including its native
// task progress and async lifecycle fixtures, without a model or credentials.
func TestClaudeSDKBackgroundTaskProgressBridge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "node", "--test", "claude_sdk_bridge.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("SDK task bridge: %v\n%s", err, output)
	}
}

// PLAT-746-941: task-free queries must survive two turns, but never leave the
// laptop retaining all 15–20 chats. Active work and exact pending wakes are not
// disposable cache entries.
func TestClaudeSDKWarmSessionBoundAndCleanup(t *testing.T) {
	start := func(owner string, tasks bool) *claudeSDKSession {
		life, kill := context.WithCancel(context.Background())
		cmd := exec.CommandContext(life, "sh", "-c", `while read line; do case "$line" in *stop_background*) exit 0;; esac; done`)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		s, err := startClaudeSDKSession(owner, claudeSDKLaunch{"model": {7}}, cmd, stdin, stdout, &claudeLockedBuffer{}, kill, func() {}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.closeIfParked("stopped", "stopped", "test_cleanup")
			s.kill()
			select {
			case <-s.exited:
			case <-time.After(5 * time.Second):
				t.Error("warm process cleanup stalled")
			}
		})
		s.setNativeID("native-" + owner)
		s.warm = true
		if tasks {
			s.setTasks([]SDKBackgroundTask{{TaskID: "running"}})
		}
		s.park()
		return s
	}
	waitExit := func(s *claudeSDKSession) {
		t.Helper()
		select {
		case <-s.exited:
		case <-time.After(5 * time.Second):
			t.Fatal("warm query did not close")
		}
	}
	background := start("warm-background", true)
	if background.closeIfParked("stopped", "stopped", "idle_compaction") {
		t.Fatal("idle compaction stopped real background work")
	}
	var queries []*claudeSDKSession
	for i := 0; i < claudeSDKWarmLimit; i++ {
		queries = append(queries, start(fmt.Sprintf("warm-%d", i), false))
	}
	// Touch the oldest: the same native query, not a cold resume, becomes MRU.
	first := queries[0]
	if takeKeptClaudeSDKSession(first.owner, first.launch, first.nativeID) != first {
		t.Fatal("the second ordinary turn lost its warm query")
	}
	first.park()
	queries = append(queries, start("warm-new", false))
	waitExit(queries[1])
	if first.hasExited() || background.hasExited() || queries[2].hasExited() {
		t.Fatal("LRU evicted a recent query or running background work")
	}
	// Last-task completion used to close its query after the wake grace. A
	// warm query stays available without losing that grace or wake identity.
	grace := claudeSDKWakeGrace
	claudeSDKWakeGrace = 30 * time.Millisecond
	defer func() { claudeSDKWakeGrace = grace }()
	if takeKeptClaudeSDKSession(background.owner, background.launch, background.nativeID) != background {
		t.Fatal("background query could not be reused")
	}
	background.park()
	background.handleKeptLine(`{"type":"sdk_background","background":[]}`)
	time.Sleep(100 * time.Millisecond)
	if background.hasExited() {
		t.Fatal("last-task completion closed the warm query instead of applying the LRU cap")
	}
	closeAllKeptClaudeSDKSessions()
	for _, s := range append(queries, background) {
		waitExit(s)
	}

	idle := claudeSDKWarmIdle
	claudeSDKWarmIdle = 25 * time.Millisecond
	defer func() { claudeSDKWarmIdle = idle }()
	expired := start("warm-idle", false)
	waitExit(expired)
	stopped := start("warm-stop", false)
	if !closeKeptClaudeSDKSession(stopped.owner, "stop") {
		t.Fatal("Stop missed a task-free warm query")
	}
	waitExit(stopped)
}

// A changed model/effort/mode or instruction refresh must not kill native
// background work; actual auth/tool/environment/program changes still must.
func TestClaudeSDKLaunchIdentityUsesEffectiveEnvironmentAndConfig(t *testing.T) {
	opts := &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{MetadataKeyMCPConfig: `{"b":2,"a":1}`}}}
	base := claudeSDKLaunchParts("first", "rules", "", "default", []string{"A=old", "A=current", "B=ok"}, opts, false, "binary", "sdk", "cwd")
	opts.Metadata.Custom[MetadataKeyMCPConfig] = ` { "a":1, "b":2 } `
	opts.Metadata.Custom[MetadataKeyEffort] = "low"
	opts.Metadata.Custom[MetadataKeyPermissionMode] = "plan"
	wanted := claudeSDKLaunchParts("second", "new rules", "", "plan", []string{"B=ok", "A=current"}, opts, false, "binary", "sdk", "cwd")
	if diff := base.changed(wanted); len(diff) != 0 {
		t.Fatalf("equivalent process identity changed: %v", diff)
	}
	for _, tc := range []struct {
		name   string
		env    []string
		binary string
	}{
		{"environment", []string{"A=other", "B=ok"}, "binary"},
		{"binary", []string{"B=ok", "A=current"}, "new-binary"},
	} {
		changed := claudeSDKLaunchParts("second", "new rules", "", "plan", tc.env, opts, false, tc.binary, "sdk", "cwd")
		if diff := base.changed(changed); len(diff) != 1 || diff[0] != tc.name {
			t.Fatalf("%s change not fenced: %v", tc.name, diff)
		}
	}
	opts.Metadata.Custom["future_tool_policy"] = true
	if diff := base.changed(claudeSDKLaunchParts("second", "rules", "", "plan", []string{"B=ok", "A=current"}, opts, false, "binary", "sdk", "cwd")); len(diff) != 1 || diff[0] != "option:future_tool_policy" {
		t.Fatalf("unknown policy silently ignored: %v", diff)
	}
}
