package agycli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Exercise the actual launch-only adapter and real tmux capture with an
// already retained pane. No model request or account is needed for warmup.
func TestAgyLaunchOnlyPublishesRetainedTerminal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := "agy-warmup-" + agyRandomHex(t, 4)
	name := agySanitizeTmuxName(owner)
	dir := t.TempDir()
	if out, err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", name,
		`sh -c 'printf "AGY_WARMUP_READY\n> "; exec sleep 30'`).CombinedOutput(); err != nil {
		t.Fatalf("start pane: %v %s", err, out)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "tmux", "kill-session", "-t", name).Run()
	})
	options := []llmtypes.CallOption{WithWorkingDir(dir), WithInteractiveSessionID(owner),
		WithPersistentInteractiveSession(true), llmtypes.WithCodingProviderLaunchOnly()}
	opts := &llmtypes.CallOptions{}
	for _, option := range options {
		option(opts)
	}
	session := &agyInteractiveSession{ownerSessionID: owner, tmuxSessionName: name,
		workingDir: dir, model: "gemini-3.8-flash-high", conversationID: "retained-conversation",
		mountFingerprint: agyToolModeFingerprint("", "") + ":" + llmtypes.CodingAgentScopeFingerprint(opts)}
	agyInteractiveRegistry.Lock()
	agyInteractiveRegistry.sessions[owner] = session
	agyInteractiveRegistry.Unlock()
	t.Cleanup(func() {
		agyInteractiveRegistry.Lock()
		delete(agyInteractiveRegistry.sessions, owner)
		agyInteractiveRegistry.Unlock()
	})
	// Wait for the shell's first frame, rather than assuming tmux rendered it.
	for {
		pane, _ := captureAgyPane(ctx, name)
		if strings.Contains(pane, "AGY_WARMUP_READY") {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("pane did not render")
		}
		time.Sleep(10 * time.Millisecond)
	}
	chunks := make(chan llmtypes.StreamChunk, 2)
	options = append(options, llmtypes.WithStreamingChan(chunks))
	response, err := NewAgyCLIAdapter("", session.model, nil).GenerateContent(ctx, nil, options...)
	if err != nil {
		t.Fatal(err)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(response)
	if !ok || handle.TmuxSession != name || handle.NativeSessionID != session.conversationID || handle.Status != llmtypes.CodingProviderSessionStatusIdle {
		t.Fatalf("warmup handle = %#v", handle)
	}
	select {
	case chunk := <-chunks:
		if chunk.Type != llmtypes.StreamChunkTypeTerminal || chunk.Metadata["tmux_session"] != name || !strings.Contains(chunk.Content, "AGY_WARMUP_READY") {
			t.Fatalf("warmup chunk = %#v", chunk)
		}
	default:
		t.Fatal("launch-only returned a live session without its terminal frame")
	}
	if len(chunks) != 0 || response.Choices[0].Content != "" {
		t.Fatal("warmup emitted a generation response")
	}
}

func TestAgyLaunchOnlyDoesNotRequireHumanPrompt(t *testing.T) {
	adapter := NewAgyCLIAdapter("", "gemini-3.8-flash-high", nil)
	_, err := adapter.GenerateContent(t.Context(), nil,
		WithInteractiveSessionID("restored-owner"),
		WithPersistentInteractiveSession(true),
		WithNativeToolsMode("invalid-test-mode"),
		llmtypes.WithReasoningEffort("high"),
		llmtypes.WithCodingProviderLaunchOnly(),
	)
	// The invalid tool mode stops before any CLI process is started. Reaching it
	// proves launch-only skipped the normal human-prompt requirement and accepted
	// a redundant effort matching the selected model's baked-in level.
	if err == nil || !strings.Contains(err.Error(), "native tools mode") {
		t.Fatalf("launch-only error = %v, want invalid tool mode after prompt and effort checks", err)
	}
}

func TestAgyInteractiveEffortMustMatchModel(t *testing.T) {
	adapter := NewAgyCLIAdapter("", "gemini-3.8-flash-high", nil)
	_, err := adapter.GenerateContent(t.Context(), []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hi"),
	},
		WithInteractiveSessionID("owner"),
		WithPersistentInteractiveSession(true),
		llmtypes.WithReasoningEffort("low"),
	)
	if err == nil || !strings.Contains(err.Error(), "does not match the booted model") {
		t.Fatalf("interactive effort mismatch error = %v", err)
	}
}
