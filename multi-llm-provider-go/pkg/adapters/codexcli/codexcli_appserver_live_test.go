package codexcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appServerTestOptions(t *testing.T) (string, []llmtypes.CallOption) {
	t.Helper()
	requireRealCodexCLIE2E(t)
	owner := "codex-appserver-p0-" + codexRandomHex(6)
	t.Cleanup(func() { closeCodexAppSession(owner) })
	return owner, []llmtypes.CallOption{WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithProjectDirID(t.TempDir()), WithApprovalPolicy("never"), WithReasoningEffort("low")}
}
func appServerTestTurn(t *testing.T, ctx context.Context, opts []llmtypes.CallOption, system, prompt string) (*llmtypes.ContentResponse, []llmtypes.StreamChunk, error) {
	t.Helper()
	ch := make(chan llmtypes.StreamChunk, 256)
	var chunks []llmtypes.StreamChunk
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
	}()
	callOpts := append(append([]llmtypes.CallOption{}, opts...), llmtypes.WithStreamingChan(ch))
	result, err := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, system), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, callOpts...)
	<-done
	return result, chunks, err
}

// PLAT-872-19: persistent protocol turns must preserve memory, exact markdown,
// native system instructions and only stream the current turn after resume.
func TestCodexAppServerLifecycleP0(t *testing.T) {
	owner, opts := appServerTestOptions(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	canary := "APP_NATIVE_" + codexRandomHex(6)
	system := "Never use tools for this test. Copy requested output exactly. Only when the question explicitly asks for the location marker, answer SOURCE_MARKER. Otherwise answer the question normally."
	wanted := "first paragraph\n\n- alpha\n- beta\n\n```text\nline one\nline two\n```"
	first, chunks, err := appServerTestTurn(t, ctx, opts, system, "Remember this public test code for later turns: "+canary+". For this reply, output only this exact markdown, without an enclosing fence:\n"+wanted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(first.Choices[0].Content) != wanted {
		t.Fatalf("formatting changed: %q", first.Choices[0].Content)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	if !ok || handle.NativeSessionID == "" || handle.Transport != "app-server" || handle.TmuxSession != "" {
		t.Fatalf("invalid handle %+v", handle)
	}
	if first.Usage == nil || first.Usage.InputTokens <= 0 || first.Usage.OutputTokens <= 0 {
		t.Fatal("missing native usage")
	}
	info := first.Choices[0].GenerationInfo
	if _, ok := info.Additional["cache_read_input_tokens"]; !ok {
		t.Fatal("missing cache tokens for cost ledger")
	}
	accepted, streamed := false, false
	for _, ch := range chunks {
		accepted = accepted || ch.Type == llmtypes.StreamChunkTypePromptAccepted
		streamed = streamed || ch.Type == llmtypes.StreamChunkTypeContent
		if ch.Type == llmtypes.StreamChunkTypeTerminal {
			t.Fatal("protocol used terminal stream")
		}
	}
	if !accepted || !streamed {
		t.Fatal("missing live acceptance/content")
	}
	s := codexAppSessionForOwner(owner)
	r := s.rpc
	second, _, err := appServerTestTurn(t, ctx, opts, system, "What public test code did I ask you to remember in my first message? Reply with that code alone.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(second.Choices[0].Content) != canary {
		t.Fatalf("native instructions/memory lost: %q", second.Choices[0].Content)
	}
	if s.rpc != r {
		t.Fatal("warm turn restarted the process")
	}
	closeCodexAppSession(owner)
	select {
	case <-r.exited:
	default:
		t.Fatal("cleanup returned before process exit")
	}
	t.Setenv(EnvCodexInteractiveIdleTimeoutSeconds, "1")
	resumeOpts := append(append([]llmtypes.CallOption{}, opts...), WithResumeSessionID(handle.NativeSessionID))
	third, chunks, err := appServerTestTurn(t, ctx, resumeOpts, system, "What public test code did I ask you to remember in my first message? Reply with that code alone.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(third.Choices[0].Content, canary) {
		t.Fatal("native resume lost context")
	}
	for _, ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeContent && strings.Contains(ch.Content, "first paragraph") {
			t.Fatal("replayed prior turn")
		}
	}
	resumed := codexAppSessionForOwner(owner)
	if resumed == nil {
		t.Fatal("resumed process not registered")
	}
	select {
	case <-resumed.rpc.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("idle process exceeded retention")
	}
	if codexAppSessionForOwner(owner) != nil {
		t.Fatal("idle session retained")
	}
	// PLAT-856: a real cold resume kept the old developer instructions after
	// a same-machine profile edit. The native base override must apply now.
	changedSystem := strings.ReplaceAll(system, "SOURCE_MARKER", "DESTINATION_MARKER")
	changed, _, err := appServerTestTurn(t, ctx, resumeOpts, changedSystem, "What is the current location marker? Reply with only that marker.")
	if err != nil {
		t.Fatal(err)
	}
	changedHandle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(changed)
	if !ok || changedHandle.NativeSessionID != handle.NativeSessionID || strings.TrimSpace(changed.Choices[0].Content) != "DESTINATION_MARKER" {
		t.Fatalf("native resume ignored changed instructions: handle=%+v reply=%+v", changedHandle, changed)
	}
	boundedOpts := append(append([]llmtypes.CallOption{}, opts...), WithPersistentInteractiveSession(false))
	if _, _, err := appServerTestTurn(t, ctx, boundedOpts, system, "Reply with only OK."); err != nil {
		t.Fatal(err)
	}
	if codexAppSessionForOwner(owner) != nil {
		t.Fatal("bounded call retained a process")
	}
	t.Logf("native thread %s: warm reuse, process-loss resume, exact markdown and token usage passed", handle.NativeSessionID)
}

// The slow MCP call is the real boundary: a steer accepted during the call
// cannot finish the foreground turn before its tool and latest reply finish.
func TestCodexAppServerSteerP0(t *testing.T) {
	owner, opts := appServerTestOptions(t)
	config, _ := json.Marshal(map[string]any{"api-bridge": map[string]any{"command": writeCodexContractMCPServer(t)}})
	opts = append(opts, WithMCPServers(string(config)), WithDisableShellTool())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	chunks := make(chan llmtypes.StreamChunk, 256)
	opts = append(opts, llmtypes.WithStreamingChan(chunks))
	token := "STEER_" + codexRandomHex(5)
	type result struct {
		response *llmtypes.ContentResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		r, e := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge slow_contract with token ORIGINAL and delay_ms 12000. Then report the tool result.")}, opts...)
		done <- result{r, e}
	}()
	steered := false
	ended := false
	var ackDone chan error
	for ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeToolCallStart && ch.ToolName == "slow_contract" && !steered {
			message := "For the final answer include the completed slow tool result and also this exact word: " + token
			if err := SendCodexAppServerInput(ctx, owner, message); err != nil {
				t.Fatal(err)
			}
			steered = true
			ackDone = make(chan error, 1)
			go func() {
				ack, err := AwaitCodexInputDurable(ctx, owner, message, time.Minute)
				if err == nil && ack.Outcome != CodexDurableAckConfirmed {
					err = fmt.Errorf("not durable: %s", ack.Outcome)
				}
				ackDone <- err
			}()
		}
		if ch.Type == llmtypes.StreamChunkTypeToolCallEnd && ch.ToolName == "slow_contract" {
			ended = true
		}
	}
	resultValue := <-done
	if resultValue.err != nil {
		t.Fatal(resultValue.err)
	}
	if !steered || !ended {
		t.Fatalf("missing real tool boundaries steer=%v end=%v", steered, ended)
	}
	if err := <-ackDone; err != nil {
		t.Fatal(err)
	}
	answer := resultValue.response.Choices[0].Content
	if !strings.Contains(answer, token) || !strings.Contains(answer, "SLOW_BRIDGE_TOOL_OK_ORIGINAL") {
		t.Fatalf("missing latest steer/tool result: %q", answer)
	}
}

func TestCodexAppServerInterruptP0(t *testing.T) {
	owner, opts := appServerTestOptions(t)
	config, _ := json.Marshal(map[string]any{"api-bridge": map[string]any{"command": writeCodexContractMCPServer(t)}})
	opts = append(opts, WithMCPServers(string(config)), WithDisableShellTool())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ch := make(chan llmtypes.StreamChunk, 256)
	runOpts := append(append([]llmtypes.CallOption{}, opts...), llmtypes.WithStreamingChan(ch))
	done := make(chan error, 1)
	go func() {
		_, err := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call api-bridge slow_contract with token INTERRUPT and delay_ms 60000.")}, runOpts...)
		done <- err
	}()
	interrupted := false
	for event := range ch {
		if !interrupted && event.Type == llmtypes.StreamChunkTypeToolCallStart {
			if err := InterruptCodexAppServer(ctx, owner); err != nil {
				t.Fatal(err)
			}
			interrupted = true
		}
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected native interrupt: %v", err)
	}
	if !interrupted {
		t.Fatal("never reached tool")
	}
	response, _, err := appServerTestTurn(t, ctx, opts, "", "Reply exactly AFTER_INTERRUPT without using tools.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Choices[0].Content, "AFTER_INTERRUPT") {
		t.Fatal("next turn did not complete")
	}
}

func TestCodexAppServerRuntimeP0(t *testing.T) {
	_, opts := appServerTestOptions(t)
	cwd := t.TempDir()
	opts = append(opts, WithProjectDirID(cwd))
	canary := "SKILL_APP_" + codexRandomHex(5)
	opts = append(opts, llmtypes.WithAttachedSkills([]*llmtypes.Skill{{Name: "app-canary", Description: "Use this skill when asked for the app canary.", Content: "The app canary is " + canary + ". Reply with it when asked."}}))
	config, _ := json.Marshal(map[string]any{"api-bridge": map[string]any{"command": writeCodexContractMCPServer(t)}})
	opts = append(opts, WithMCPServers(string(config)), WithNativeTools())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	response, chunks, err := appServerTestTurn(t, ctx, opts, "Do exactly the requested test work.", "Read the app-canary skill and call api-bridge echo_contract with its canary as token. Use your native shell to write the current working directory to cwd.txt. Finally report the tool result.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Choices[0].Content, "BRIDGE_TOOL_OK_"+canary) {
		t.Fatalf("skill/MCP missing: %q", response.Choices[0].Content)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "cwd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	expected, _ := filepath.EvalSymlinks(cwd)
	if actual != expected {
		t.Fatalf("wrong cwd: %q", data)
	}
	mcp, shell := false, false
	for _, ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeToolCallEnd {
			mcp = mcp || ch.ToolName == "echo_contract"
			shell = shell || ch.ToolName == "exec_command"
		}
	}
	if !mcp || !shell {
		t.Fatalf("missing native events mcp=%v shell=%v", mcp, shell)
	}
}

func TestCodexAppServerParallelIsolationP0(t *testing.T) {
	requireRealCodexCLIE2E(t)
	for i := 0; i < 2; i++ {
		t.Run(fmt.Sprintf("session-%d", i), func(t *testing.T) {
			t.Parallel()
			_, opts := appServerTestOptions(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			token := "ISOLATED_" + codexRandomHex(8)
			system := "Remember only this codeword: " + token + ". Do not use tools."
			_, _, err := appServerTestTurn(t, ctx, opts, system, "Reply ACK.")
			if err != nil {
				t.Fatal(err)
			}
			response, _, err := appServerTestTurn(t, ctx, opts, system, "Reply with only the codeword.")
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(response.Choices[0].Content) != token {
				t.Fatalf("session leaked/lost context: %q", response.Choices[0].Content)
			}
		})
	}
}

// The requested native write must be unavailable in bridge-only mode while
// the allowed MCP call still succeeds, not just fail because no tools loaded.
func TestCodexAppServerBridgeOnlyP0(t *testing.T) {
	_, opts := appServerTestOptions(t)
	cwd := t.TempDir()
	config, _ := json.Marshal(map[string]any{"api-bridge": map[string]any{"command": writeCodexContractMCPServer(t)}})
	opts = append(opts, WithProjectDirID(cwd), WithMCPServers(string(config)), WithDisableShellTool(), WithSandbox("read-only"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	response, chunks, err := appServerTestTurn(t, ctx, opts, "Execute only tools available to you. If a tool is unavailable, say so.", "First try using a native shell to write BYPASS to forbidden.txt. Then call api-bridge echo_contract with token ALLOWED. Report the MCP result and whether native shell was available.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Choices[0].Content, "BRIDGE_TOOL_OK_ALLOWED") {
		t.Fatal("allowed MCP call did not succeed")
	}
	if _, err := os.Stat(filepath.Join(cwd, "forbidden.txt")); !os.IsNotExist(err) {
		t.Fatalf("native write escaped bridge-only mode: %v", err)
	}
	for _, ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeToolCallStart && ch.ToolName == "exec_command" {
			t.Fatal("bridge-only session exposed native shell")
		}
	}
}

// PLAT-872-19: selecting a signed-out private account must never fall back to
// the ambient signed-in account or reuse its running process/native thread.
func TestCodexAppServerSignedOutAccountCannotBorrowLogin(t *testing.T) {
	owner, opts := appServerTestOptions(t)
	accountA := os.Getenv("CODEX_HOME")
	if accountA == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		accountA = filepath.Join(home, ".codex")
	}
	if _, err := os.Stat(filepath.Join(accountA, "auth.json")); err != nil {
		t.Fatal("this login-isolation proof requires an existing file-based Codex login")
	}
	accountBHome := t.TempDir()
	accountB := filepath.Join(accountBHome, ".codex")
	if err := os.Mkdir(accountB, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountB, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	selected := func(home, codexHome string) []llmtypes.CallOption {
		result := append([]llmtypes.CallOption{}, opts...)
		env := map[string]string{"CODEX_HOME": codexHome}
		if home != "" {
			env["HOME"] = home
			env["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
			env["XDG_DATA_HOME"] = filepath.Join(home, ".local/share")
			env["XDG_STATE_HOME"] = filepath.Join(home, ".local/state")
		}
		return append(result, llmtypes.WithProviderAccountEnvironment(env))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	first, _, err := appServerTestTurn(t, ctx, selected("", accountA), "Never use tools.", "Reply exactly LOGIN_A_OK.")
	if err != nil {
		t.Fatal(err)
	}
	firstHandle, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	badCtx, badCancel := context.WithTimeout(ctx, 45*time.Second)
	_, _, err = appServerTestTurn(t, badCtx, selected(accountBHome, accountB), "Never use tools.", "Reply exactly SHOULD_NOT_RUN.")
	badCancel()
	if err == nil {
		t.Fatal("signed-out account borrowed a signed-in login")
	}
	message := strings.ToLower(err.Error())
	if errors.Is(err, context.DeadlineExceeded) || (!strings.Contains(message, "auth") && !strings.Contains(message, "logged") && !strings.Contains(message, "401")) {
		t.Fatalf("refusal did not prove the authentication boundary: %v", err)
	}
	t.Log("signed-out account refused by native authentication")
	last, _, err := appServerTestTurn(t, ctx, selected("", accountA), "Never use tools.", "Reply exactly LOGIN_A_OK.")
	if err != nil {
		t.Fatal(err)
	}
	handle, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(last)
	if strings.TrimSpace(last.Choices[0].Content) != "LOGIN_A_OK" || handle.NativeSessionID == firstHandle.NativeSessionID {
		t.Fatal("switching accounts reused the old native thread or lost the allowed login")
	}
	if s := codexAppSessionForOwner(owner); s == nil {
		t.Fatal("allowed account failed to retain its process")
	}
}

// PLAT-872-55: opting into tmux must run and resume the retained native conversation.
func TestCodexTmuxLifecycleLive(t *testing.T) {
	requireRealCodexCLIE2E(t)
	owner := t.Name() + codexRandomHex(4)
	defer CloseCodexCLIInteractiveSessionForOwner(owner, "test complete")
	cwd, runtimeDir := t.TempDir(), t.TempDir()
	opts := []llmtypes.CallOption{WithProjectDirID(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithApprovalPolicy("never"), WithReasoningEffort("low"), llmtypes.WithCLIRuntimeDir(runtimeDir)}
	a := NewCodexCLIAdapter("", codexCLIRealContractModel, &MockLogger{})
	a.SetTmuxTransport(true)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	word := "TERMINAL_" + codexRandomHex(5)
	first, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Remember the codeword "+word+". Do not use tools. Reply READY.")}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	if !ok || h.Transport != "tmux" || h.NativeSessionID == "" || h.TmuxSession == "" || h.RuntimeDir != runtimeDir {
		t.Fatalf("missing terminal continuation: %+v", h)
	}
	CloseCodexCLIInteractiveSessionForOwner(owner, "restart proof")
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
