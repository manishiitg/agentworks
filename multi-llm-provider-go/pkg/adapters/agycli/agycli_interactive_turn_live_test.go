package agycli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestAgyCLIRealInteractiveTurnContract is the CertTmuxTurnExecution P0
// proof: with persistent interactive set, GenerateContent runs the turn
// inside the sidecar's own TUI conversation (not headless exec). Two turns
// under one owner share the native conversation id, and both report real
// token usage metered from the conversation .db.
// agyKeyModeForTest flips to Gemini API key mode when AGY_P0_KEY_MODE=1
// (subscription quota exhausted); otherwise runs stay on stored-login truth.
func agyKeyModeForTest(t *testing.T) {
	t.Helper()
	restore, err := AgyEnsureKeyMode()
	if err != nil {
		t.Fatalf("agy key mode: %v", err)
	}
	t.Cleanup(restore)
	t.Logf("agy auth mode under test: %s", AgyTestAuthMode())
}

func TestAgyCLIRealInteractiveTurnContract(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	agyKeyModeForTest(t)
	workDir := t.TempDir()
	agyTrustWorkdirForTest(t, workDir)
	owner := "agy-sidecar-turn-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	canary1 := "AGY_SIDECAR_TURN_" + agyRandomHex(t, 4)
	resp1, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not use tools."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+canary1),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner))
	if err != nil {
		t.Fatalf("sidecar turn 1 error = %v", err)
	}
	text1 := agySidecarChoiceText(t, resp1)
	if !strings.Contains(text1, canary1) {
		t.Fatalf("sidecar turn 1 reply missing canary %q: %q", canary1, text1)
	}
	if gi := resp1.Choices[0].GenerationInfo; gi == nil || gi.Additional["agy_intake_source"] != "sqlite_user_step" || gi.Additional["agy_completion_source"] != "sqlite_assistant_settled" {
		t.Fatalf("sidecar turn lacks structured source receipts: %+v", gi)
	}
	usage1 := agySidecarUsage(t, resp1)
	t.Logf("sidecar turn 1 usage: %+v", usage1)
	handle1, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp1)
	if !ok || handle1.NativeSessionID == "" {
		t.Fatalf("sidecar turn 1 missing native session id: %#v ok=%v", handle1, ok)
	}

	canary2 := "AGY_SIDECAR_REUSE_" + agyRandomHex(t, 4)
	resp2, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+canary2),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner))
	if err != nil {
		t.Fatalf("sidecar turn 2 error = %v", err)
	}
	text2 := agySidecarChoiceText(t, resp2)
	if !strings.Contains(text2, canary2) {
		t.Fatalf("sidecar turn 2 reply missing canary %q: %q", canary2, text2)
	}
	_ = agySidecarUsage(t, resp2)
	handle2, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp2)
	if !ok || handle2.NativeSessionID != handle1.NativeSessionID {
		t.Fatalf("sidecar turn 2 conversation = %#v, want same conversation %s", handle2, handle1.NativeSessionID)
	}
}

// TestAgyCLIRealInteractiveResumeContract proves sidecar session-loss
// recovery at the adapter level: turn 1 states a code word, the live tmux
// session is killed, and turn 2 — carrying only the resume conversation id
// — rebirths the sidecar with --conversation and recalls the word. The
// word is never written to disk, so recall proves genuine native attach,
// not a fresh conversation. Both turns' handles carry the tmux session
// name the caller needs to detect and simulate loss.
func TestAgyCLIRealInteractiveResumeContract(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	agyKeyModeForTest(t)
	workDir := t.TempDir()
	agyTrustWorkdirForTest(t, workDir)
	owner := "agy-sidecar-resume-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	codeWord := "AGY_RESUME_" + agyRandomHex(t, 6)
	resp1, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "Do not use tools."),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Please remember this code word for later: "+codeWord+". Just reply OK — do not write it anywhere."),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner))
	if err != nil {
		t.Fatalf("resume turn 1 error = %v", err)
	}
	handle1, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp1)
	if !ok || handle1.NativeSessionID == "" {
		t.Fatalf("resume turn 1 missing native session id: %#v ok=%v", handle1, ok)
	}
	if handle1.TmuxSession == "" {
		t.Fatalf("resume turn 1 missing tmux session name: %#v", handle1)
	}
	t.Logf("resume turn 1 conversation=%s tmux=%s", handle1.NativeSessionID, handle1.TmuxSession)

	if out, err := exec.CommandContext(t.Context(), "tmux", "kill-session", "-t", handle1.TmuxSession).CombinedOutput(); err != nil {
		t.Fatalf("kill sidecar tmux session: %v\n%s", err, out)
	}
	if err := exec.CommandContext(t.Context(), "tmux", "has-session", "-t", handle1.TmuxSession).Run(); err == nil {
		t.Fatalf("tmux session %q still alive after kill", handle1.TmuxSession)
	}

	resp2, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "What was the exact code word I asked you to remember earlier? Reply with ONLY that word."),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner),
		WithResumeSessionID(handle1.NativeSessionID))
	if err != nil {
		t.Fatalf("resume turn 2 error = %v", err)
	}
	text2 := agySidecarChoiceText(t, resp2)
	if !strings.Contains(text2, codeWord) {
		t.Fatalf("resume turn 2 recall %q does not contain the code word %q (sidecar did not re-attach to the conversation)", text2, codeWord)
	}
	handle2, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp2)
	if !ok || handle2.NativeSessionID != handle1.NativeSessionID {
		t.Fatalf("resume turn 2 conversation = %#v, want re-attached conversation %s", handle2, handle1.NativeSessionID)
	}
	if handle2.TmuxSession == "" {
		t.Fatalf("resume turn 2 missing tmux session name: %#v", handle2)
	}
	_ = agySidecarUsage(t, resp2)
}

// TestAgyCLIRealInteractiveMCPBridgeContract is the
// CertInteractiveMCPBridge P0 proof: a persistent sidecar turn with an MCP
// config routes a tool call through the bridge with no approval stall
// (permissions.allow covers the mount), and closing the session removes
// both the mount and the permission entries.
func TestAgyCLIRealInteractiveMCPBridgeContract(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	agyKeyModeForTest(t)
	workDir := t.TempDir()
	agyTrustWorkdirForTest(t, workDir)
	serverPath, logPath := agyWriteCanaryServer(t, workDir, "sidecar-bridge-server.js")
	owner := "agy-sidecar-bridge-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })
	beforeAllows := agySidecarPermissionAllows(t)

	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	stream := make(chan llmtypes.StreamChunk, 256)
	canary := "AGY_SIDECAR_BRIDGE_" + agyRandomHex(t, 4)
	started := time.Now()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use the MCP gateway only. Do not use terminal or file tools. Call the MCP tool bridge_canary with no arguments, then reply with exactly this token and nothing else: "+canary),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner),
		WithMCPConfig(agyCanaryMCPConfig(serverPath, logPath, "25000")), llmtypes.WithStreamingChan(stream))
	if err != nil {
		t.Fatalf("sidecar bridge turn error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Second {
		t.Fatalf("sidecar turn finished in %s before its 25s tool, want no false idle", elapsed)
	}
	if text := agySidecarChoiceText(t, resp); !strings.Contains(text, canary) {
		t.Fatalf("sidecar bridge reply missing canary %q: %q", canary, text)
	}
	_ = agySidecarUsage(t, resp)
	// Post-hoc tool events: the bridge call surfaces as a Start,End pair
	// under its INNER tool name (the call_mcp_tool wrapper is transport),
	// ahead of the final content chunk.
	close(stream)
	var starts, ends int
	var startNames []string
	startIDs := map[string]bool{}
	endIDs := map[string]bool{}
	for chunk := range stream {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeToolCallStart:
			starts++
			startNames = append(startNames, chunk.ToolName)
			startIDs[chunk.ToolCallID] = true
			if strings.TrimSpace(chunk.ToolName) == "" {
				t.Fatalf("tool Start chunk has an empty name (call %q)", chunk.ToolCallID)
			}
		case llmtypes.StreamChunkTypeToolCallEnd:
			ends++
			endIDs[chunk.ToolCallID] = true
		}
	}
	for id := range startIDs {
		if !endIDs[id] {
			t.Fatalf("tool Start %q has no matching End", id)
		}
	}
	t.Logf("sidecar bridge tool chunks: starts=%d ends=%d names=%q", starts, ends, startNames)
	sawCanary := false
	for _, name := range startNames {
		if strings.Contains(name, "bridge_canary") {
			sawCanary = true
		}
	}
	if !sawCanary {
		t.Fatalf("no tool Start chunk for the bridge_canary call; names=%q", startNames)
	}
	if starts == 0 || ends != starts {
		t.Fatalf("unpaired post-hoc tool events: starts=%d ends=%d", starts, ends)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil || len(logged) == 0 {
		t.Fatalf("bridge canary not called through sidecar turn (log %q err %v)", logPath, err)
	}

	CloseAgyCLIInteractiveSessionForOwner(owner, "test done")
	agyAssertNoMountLeak(t)
	afterAllows := agySidecarPermissionAllows(t)
	if len(afterAllows) != len(beforeAllows) {
		t.Fatalf("permissions.allow changed across sidecar session: before=%q after=%q", beforeAllows, afterAllows)
	}
	for i := range beforeAllows {
		if beforeAllows[i] != afterAllows[i] {
			t.Fatalf("permissions.allow changed across sidecar session: before=%q after=%q", beforeAllows, afterAllows)
		}
	}
}

func TestAgyCLIRealInteractiveNativeToolMode(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	agyKeyModeForTest(t)
	workDir := t.TempDir()
	agyTrustWorkdirForTest(t, workDir)
	serverPath, logPath := agyWriteCanaryServer(t, workDir, "sidecar-native-mode-server.js")
	owner := "agy-sidecar-native-mode-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })
	config := agyCanaryMCPConfig(serverPath, logPath, "0")
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	bridge, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use the MCP gateway only. Call bridge_canary, then reply with its result."),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithMCPConfig(config), WithNativeToolsMode("mcp_only"))
	if err != nil || !strings.Contains(agySidecarChoiceText(t, bridge), "AGY_MCP_BRIDGE_OK") {
		t.Fatalf("sidecar MCP-only bridge turn: err=%v", err)
	}
	target := filepath.Join(workDir, "sidecar-native-must-not-write.txt")
	_, _ = adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use only your native write_to_file tool to create "+target+" with exactly HI. Do not call MCP or use a command. If blocked, say so."),
	}, WithWorkingDir(workDir), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithMCPConfig(config), WithNativeToolsMode("mcp_only"))
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("MCP-only sidecar allowed native write: stat err = %v", err)
	}
	CloseAgyCLIInteractiveSessionForOwner(owner, "test done")
	if _, err := os.Stat(filepath.Join(workDir, ".agents", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("sidecar hook file survived cleanup: %v", err)
	}
	agyAssertNoMountLeak(t)
}

func agySidecarPermissionAllows(t *testing.T) []string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	var allows []string
	if perms, ok := settings["permissions"].(map[string]interface{}); ok {
		if list, ok := perms["allow"].([]interface{}); ok {
			for _, item := range list {
				if s, ok := item.(string); ok {
					allows = append(allows, s)
				}
			}
		}
	}
	sort.Strings(allows)
	return allows
}

func agySidecarChoiceText(t *testing.T, resp *llmtypes.ContentResponse) string {
	t.Helper()
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatalf("sidecar turn returned no choices: %+v", resp)
	}
	return resp.Choices[0].Content
}

func agySidecarUsage(t *testing.T, resp *llmtypes.ContentResponse) llmtypes.Usage {
	t.Helper()
	if resp == nil || resp.Usage == nil {
		t.Fatalf("sidecar turn returned no usage: %+v", resp)
	}
	usage := *resp.Usage
	if usage.InputTokens <= 1000 || usage.OutputTokens <= 0 {
		t.Fatalf("sidecar turn usage not real metering: %+v", usage)
	}
	return usage
}
