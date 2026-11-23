package cursorcli

import (
	"context"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCursorSDKCancelAndReuseLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	owner := t.Name()
	defer closeCursorSDKInstance(owner, nil)
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "started.json")
	script := writeCursorSlowContractMCPServer(t, marker)
	config := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, script)
	opts := []llmtypes.CallOption{WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithMCPConfig(config), WithDenyBuiltinTools(true)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := NewCursorCLIAdapter("", "composer-2.5", nil)
	done := make(chan error, 1)
	go func() {
		_, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge contract_echo_token with token CANCEL_TEST. Wait for the result and report it.")}, opts...)
		done <- err
	}()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("turn ended before tool started: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		t.Fatalf("slow tool reported completion early: %v", err)
	case <-time.After(2 * time.Second):
	}
	if err := SendCursorInteractiveControlKey(ctx, owner, "Escape"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled turn reported success")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cancel did not settle")
	}
	next, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Do not use tools. Reply exactly AFTER_CANCEL.")}, opts...)
	if err != nil || strings.TrimSpace(next.Choices[0].Content) != "AFTER_CANCEL" {
		t.Fatalf("reuse failed: %v %v", next, err)
	}
}

// A message sent while the turn waits on a slow tool joins that same turn (steer); nothing is dropped.
func TestCursorSDKSteerDuringSlowToolLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	owner := t.Name()
	defer closeCursorSDKInstance(owner, nil)
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "started.json")
	script := writeCursorSlowContractMCPServer(t, marker)
	config := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, script)
	opts := []llmtypes.CallOption{WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithMCPConfig(config), WithDenyBuiltinTools(true)}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	a := NewCursorCLIAdapter("", "composer-2.5", nil)
	steerToken := "STEER_" + cursorRandomHex(4)
	type result struct {
		resp *llmtypes.ContentResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		r, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge contract_echo_token with token STEER_TEST. Wait for the result and report it.")}, opts...)
		done <- result{r, err}
	}()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("turn ended before tool started: %v", r.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := SendCursorSDKInput(ctx, owner, "Also, at the end of your reply, write the word "+steerToken+" on its own line."); err != nil {
		t.Fatalf("steer not taken: %v", err)
	}
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	text := r.resp.Choices[0].Content
	if !strings.Contains(text, "SLOW_BRIDGE_TOOL_OK_") || !strings.Contains(text, steerToken) {
		t.Fatalf("steered turn must answer both the tool and the steer: %q", text)
	}
	t.Logf("steered answer: %q", text)
}

func TestCursorSDKParallelMCPIsolationLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	cwd := t.TempDir()
	type result struct {
		text  string
		err   error
		owner string
	}
	results := make(chan result, 2)
	for _, label := range []string{"one", "two"} {
		owner := t.Name() + label
		defer closeCursorSDKInstance(owner, nil)
		script := writeCursorIsolationMCPServer(t, label, filepath.Join(cwd, label+".json"))
		config := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, script)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			a := NewCursorCLIAdapter("", "composer-2.5", &MockLogger{})
			r, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge write_contract with token isolated. Reply only with its result.")}, WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithMCPConfig(config), WithDenyBuiltinTools(true))
			text := ""
			if r != nil && len(r.Choices) > 0 {
				text = r.Choices[0].Content
			}
			results <- result{text, err, label}
		}()
	}
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil || strings.TrimSpace(r.text) != "ISOLATED_OK_"+r.owner+"_isolated" {
			t.Fatalf("owner %s: %q %v", r.owner, r.text, r.err)
		}
	}
}

// The SDK merges the MCP servers of every project folder it loads. A server another chat in the same
// folder was given must never be callable from this chat.
func TestCursorSDKOtherChatServersStayPrivateLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	cwd := t.TempDir()
	a := NewCursorCLIAdapter("", "composer-2.5", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	other := t.Name() + "other"
	defer closeCursorSDKInstance(other, nil)
	otherScript := writeCursorIsolationMCPServer(t, "other", filepath.Join(cwd, "other.json"))
	otherConfig := fmt.Sprintf(`{"mcpServers":{"other-server":{"command":"node","args":[%q]}}}`, otherScript)
	if _, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Do not use tools. Reply exactly READY.")}, WithWorkingDir(cwd), WithInteractiveSessionID(other), WithPersistentInteractiveSession(true), WithMCPConfig(otherConfig), WithDenyBuiltinTools(true)); err != nil {
		t.Fatal(err)
	}
	own := t.Name() + "own"
	defer closeCursorSDKInstance(own, nil)
	ownScript := writeCursorIsolationMCPServer(t, "own", filepath.Join(cwd, "own.json"))
	ownConfig := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, ownScript)
	r, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "If an MCP server named other-server is available, call its write_contract tool with token leak and reply with its result. Otherwise reply exactly NO_OTHER_SERVER.")}, WithWorkingDir(cwd), WithInteractiveSessionID(own), WithPersistentInteractiveSession(true), WithMCPConfig(ownConfig), WithDenyBuiltinTools(true))
	if err != nil {
		t.Fatal(err)
	}
	if text := r.Choices[0].Content; strings.Contains(text, "ISOLATED_OK_other") {
		t.Fatalf("this chat reached another chat's MCP server: %q", text)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".cursor", "mcp.json")); err == nil {
		t.Fatal("a chat wrote its MCP config into the shared folder")
	}
}

// PLAT-872-98: a Full CLI chat's Task subagent becomes a child chat with its own tool calls; a bridge-only
// chat has no Task tool at all.
func TestCursorSDKSubagentChildChatLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	config := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"type":"stdio","command":"node","args":[%q]}}}`, writeCursorTmuxContractMCPServer(t))
	ask := "Use your Task tool to launch exactly one subagent. The subagent must call the api-bridge MCP tool contract_echo_token with token CHILD_CHAT and report the result. If you have no Task tool, reply exactly NO_TASK_TOOL."
	run := func(owner string, extra ...llmtypes.CallOption) (*llmtypes.ContentResponse, []llmtypes.StreamChunk) {
		t.Helper()
		defer closeCursorSDKInstance(owner, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		stream := make(chan llmtypes.StreamChunk, 4096)
		opts := append([]llmtypes.CallOption{WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithMCPConfig(config), llmtypes.WithStreamingChan(stream)}, extra...)
		type res struct {
			r   *llmtypes.ContentResponse
			err error
		}
		done := make(chan res, 1)
		go func() {
			r, err := NewCursorCLIAdapter("", "composer-2.5", nil).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, ask)}, opts...)
			done <- res{r, err}
		}()
		var chunks []llmtypes.StreamChunk
		for ch := range stream {
			chunks = append(chunks, ch)
		}
		out := <-done
		if out.err != nil {
			t.Fatal(out.err)
		}
		return out.r, chunks
	}
	r, chunks := run(t.Name()+"full", WithFullNativeTools())
	var child *llmtypes.NativeSubagentTranscript
	for _, ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeNativeSubagentTranscript {
			children, _ := ch.Metadata["native_subagents"].([]llmtypes.NativeSubagentTranscript)
			if len(children) > 0 {
				child = &children[len(children)-1]
			}
		}
		if ch.Type == llmtypes.StreamChunkTypeToolCallEnd && ch.ToolName == "task" && strings.Contains(ch.ToolResult, "conversationSteps") {
			t.Fatal("the task row shows the raw SDK result")
		}
	}
	if child == nil {
		t.Fatalf("no child chat; answer %q", r.Choices[0].Content)
	}
	sawTool := false
	for _, m := range child.Messages {
		for _, p := range m.Parts {
			if out, ok := p.(llmtypes.ToolCallResponse); ok && strings.Contains(out.Content, "BRIDGE_TOOL_OK_CHILD_CHAT") && out.Name == "mcp__api-bridge__contract_echo_token" {
				sawTool = true
			}
		}
	}
	if !sawTool || child.Status != "completed" || child.Provider != "cursor-cli" {
		t.Fatalf("child chat lacks its tool call: %+v", child)
	}
	if _, ok := r.Choices[0].GenerationInfo.Additional["native_subagents"]; !ok {
		t.Fatal("final answer does not carry the child chat")
	}
	t.Logf("child %q: %d messages", child.Name, len(child.Messages))

	_, chunks = run(t.Name()+"bridge", WithDenyBuiltinTools(true))
	for _, ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeNativeSubagentTranscript || (ch.Type == llmtypes.StreamChunkTypeToolCallStart && ch.ToolName == "task") {
			t.Fatal("a bridge-only chat started a subagent")
		}
	}
}

// PLAT-872-21: same-process continuity, restart resume, no replay and scoped cleanup.
func TestCursorSDKLifecycleLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	owner := t.Name()
	defer closeCursorSDKInstance(owner, nil)
	cwd := t.TempDir()
	// PLAT-872: unavailable selectors used to become EOF/network retries.
	rejected := NewCursorCLIAdapter("", "agentworks-unavailable-model", nil)
	rejectCtx, rejectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer rejectCancel()
	_, rejectErr := rejected.GenerateContent(rejectCtx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply OK.")},
		WithWorkingDir(cwd), WithInteractiveSessionID(owner))
	if llmerrors.KindOf(rejectErr) != llmerrors.KindModelNotFound || llmerrors.IsRetryable(rejectErr) {
		t.Fatalf("unavailable model must be actionable without retry: %v", rejectErr)
	}
	t.Logf("unavailable model rejected without retry: %v", rejectErr)
	a := NewCursorCLIAdapter("", "composer-2.5", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	token := "SDK_MEMORY_" + cursorRandomHex(5)
	options := []llmtypes.CallOption{WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithForce()}
	run := func(prompt, resume string) *llmtypes.ContentResponse {
		t.Helper()
		stream := make(chan llmtypes.StreamChunk, 4096)
		opts := append(append([]llmtypes.CallOption{}, options...), WithResumeSessionID(resume), llmtypes.WithStreamingChan(stream))
		r, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not read files or use tools. Follow the requested response exactly."), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, opts...)
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for ch := range stream {
			if ch.Type == llmtypes.StreamChunkTypeTerminal {
				t.Fatal("terminal frame")
			}
			if ch.Type == llmtypes.StreamChunkTypeContent {
				text.WriteString(ch.Content)
			}
		}
		if text.String() != r.Choices[0].Content {
			t.Fatal("stream/final mismatch")
		}
		return r
	}
	first := run("Remember "+token+". Reply exactly ACK.", "")
	h, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	if !ok || h.Transport != "sdk" || h.NativeSessionID == "" || h.TmuxSession != "" {
		t.Fatalf("invalid handle %+v", h)
	}
	cursorSDKSessions.Lock()
	s := cursorSDKSessions.sessions[owner]
	cursorSDKSessions.Unlock()
	proc := s.proc
	pid := proc.cmd.Process.Pid
	second := run("What token did I ask you to remember? Reply only that token.", "")
	if strings.TrimSpace(second.Choices[0].Content) != token {
		t.Fatal("lost context: " + second.Choices[0].Content)
	}
	if s.proc == nil || s.proc.cmd.Process.Pid != pid {
		t.Fatal("warm process replaced")
	}
	closeCursorSDKInstance(owner, nil)
	select {
	case <-proc.exited:
	default:
		t.Fatal("cleanup left native process running")
	}
	third := run("Again reply only the token I asked you to remember.", h.NativeSessionID)
	if strings.TrimSpace(third.Choices[0].Content) != token {
		t.Fatal("resume lost context: " + third.Choices[0].Content)
	}
}

// PLAT-872-55: the explicit terminal path must retain its own conversation after restart.
func TestCursorTmuxLifecycleLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	owner := t.Name() + cursorRandomHex(4)
	defer CloseCursorCLIInteractiveSessionForOwner(owner, "test complete")
	cwd, runtimeDir := t.TempDir(), t.TempDir()
	opts := []llmtypes.CallOption{WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), llmtypes.WithCLIRuntimeDir(runtimeDir)}
	a := NewCursorCLIAdapter("", "composer-2.5", &MockLogger{})
	a.SetTmuxTransport(true)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	word := "TERMINAL_" + cursorRandomHex(5)
	first, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Remember the codeword "+word+". Do not use tools. Reply READY.")}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	if !ok || h.Transport != "tmux" || h.NativeSessionID == "" || h.TmuxSession == "" || h.RuntimeDir != runtimeDir {
		t.Fatalf("missing terminal continuation: %+v", h)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("generated runtime leaked into real project")
	}
	t.Logf("first terminal turn native=%s runtime=%s cwd=%s", h.NativeSessionID, runtimeDir, cwd)
	CloseCursorCLIInteractiveSessionForOwner(owner, "restart proof")
	resumed, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "What was the codeword? Reply with only the codeword. Do not use tools.")}, append(opts, WithResumeSessionID(h.NativeSessionID))...)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Choices) == 0 || strings.TrimSpace(resumed.Choices[0].Content) != word {
		t.Fatalf("terminal lost its native conversation: %+v", resumed)
	}
	next, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resumed)
	if next.NativeSessionID != h.NativeSessionID {
		t.Fatal("restart changed native conversation")
	}
	t.Logf("tmux native resume passed: %s", h.NativeSessionID)
}

// agentworks#3135 (SDK 1.3): a model change keeps the same native agent (it still knows the first message's word),
// and idle compaction runs Cursor's own summarize on it.
func TestCursorSDKModelSwitchAndSummarizeLive(t *testing.T) {
	requireRealCursorCLIE2E(t)
	owner := t.Name()
	defer closeCursorSDKInstance(owner, nil)
	opts := []llmtypes.CallOption{WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithDenyBuiltinTools(true)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ask := func(model, text string, extra ...llmtypes.CallOption) *llmtypes.ContentResponse {
		resp, err := NewCursorCLIAdapter("", model, nil).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, text)}, append(append([]llmtypes.CallOption{}, opts...), extra...)...)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		return resp
	}
	first := ask("composer-2.5", "Remember the word MAPLE7. Reply exactly OK.")
	second := ask("auto", "Which word did I ask you to remember? Reply with the word only.")
	if !strings.Contains(second.Choices[0].Content, "MAPLE7") {
		t.Fatalf("the model switch lost the conversation: %q", second.Choices[0].Content)
	}
	handle := func(r *llmtypes.ContentResponse) string {
		h, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(r)
		return h.NativeSessionID
	}
	if handle(first) == "" || handle(first) != handle(second) {
		t.Fatalf("native agent changed with the model: %q then %q", handle(first), handle(second))
	}
	compacted := ask("auto", "", llmtypes.WithCodingProviderIdleCompact())
	if result, ok := llmtypes.IdleCompactResultFromResponse(compacted); !ok || !result.Compacted {
		t.Fatalf("summarize did not compact: %#v", result)
	}
}
