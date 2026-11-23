package musecli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

func TestMuseResumeHandleRoundTripTmuxP0(t *testing.T) {
	requireMuseBinary(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	museEchoTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := "resume-identity-" + museRandomSessionSuffix()
	defer KillMusePersistentSession(owner)
	workdir := t.TempDir()
	adapter := NewMuseCLIAdapter("", "", museTestLogger{})
	opts := []llmtypes.CallOption{WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(workdir)}
	launch, err := adapter.GenerateContent(ctx, nil, append(opts, llmtypes.WithCodingProviderLaunchOnly())...)
	if err != nil {
		t.Fatal(err)
	}
	if !pathidentity.Same(launch.Choices[0].GenerationInfo.CodingProviderSessionHandle.WorkingDir, workdir) {
		t.Fatal("launch handle lost working directory")
	}
	message := func(text string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}}
	}
	history := append(message("HISTORY-MARKER-previous-user"), llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "HISTORY-MARKER-previous-answer"}}})
	first, err := adapter.GenerateContent(ctx, append(history, message("resume identity first turn\n"+strings.Repeat("History line retained across terminal restart.\n", 70))...), opts...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.Choices[0].Content, "HISTORY-MARKER-previous-answer") {
		t.Fatal("fresh tmux session discarded fallback history")
	}
	handle := first.Choices[0].GenerationInfo.CodingProviderSessionHandle
	raw, err := json.Marshal(handle)
	if err != nil {
		t.Fatal(err)
	}
	var restored llmtypes.CodingProviderSessionHandle
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !pathidentity.Same(restored.WorkingDir, workdir) {
		t.Fatal("saved completion handle lost working directory")
	}
	if restored.NativeSessionID == "" {
		t.Fatal("missing native session ID")
	}
	if !pathidentity.Same(NativeSessionWorkingDir(restored.NativeSessionID), workdir) {
		t.Fatal("native metadata did not identify workspace")
	}
	KillMusePersistentSession(owner)
	opts = append(opts, WithResumeSessionID(restored.NativeSessionID))
	// Restoring a long conversation scrolls the startup banner out of the
	// newly created terminal. Exercise the application's launch-only restore
	// path before sending another message, with a bounded readiness deadline.
	restoreCtx, restoreCancel := context.WithTimeout(ctx, 20*time.Second)
	defer restoreCancel()
	reopened, err := adapter.GenerateContent(restoreCtx, nil, append(opts, llmtypes.WithCodingProviderLaunchOnly())...)
	if err != nil {
		t.Fatalf("restore long native history: %v", err)
	}
	reopenedHandle := reopened.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if reopenedHandle.NativeSessionID != restored.NativeSessionID || !pathidentity.Same(reopenedHandle.WorkingDir, workdir) {
		t.Fatal("launch-only restore lost native session identity or workspace")
	}
	pane, err := museTmuxCapturePane(ctx, reopenedHandle.TmuxSession)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pane, "Muse Code") || !museTUIAtPrompt(pane) {
		t.Fatalf("regression must exercise an idle restored pane without startup banner: %s", pane)
	}
	second, err := adapter.GenerateContent(ctx, message("resume identity second turn"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	resumed := second.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if resumed.NativeSessionID != restored.NativeSessionID {
		t.Fatalf("started new conversation: %s != %s", resumed.NativeSessionID, restored.NativeSessionID)
	}
	if !pathidentity.Same(resumed.WorkingDir, workdir) {
		t.Fatal("resumed completion lost workspace")
	}
}

func TestMuseNativeWorkspaceMetadataOnlyP0(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path := filepath.Join(museXDGDataHome(), "muse", "sessions", "2026", "09", "12", "native-id", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, payload, want string }{
		{"metadata", `{"payload_type":"runtime.session.metadata","payload":{"record":{"workspace_root":"/tmp/private"}}}`, "/tmp/private"},
		{"tool output", `{"payload_type":"runtime.tool.result","payload":{"record":{"workspace_root":"/tmp/private"}}}`, ""},
		{"relative", `{"payload_type":"runtime.session.metadata","payload":{"record":{"workspace_root":"relative"}}}`, ""},
		{"malformed", `{broken`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.payload+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := NativeSessionWorkingDir("native-id"); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMuseFreshHistoryAndNativeResumeP0(t *testing.T) {
	messages := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "system guidance"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Explain my pending notices"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "These are campaign feedback requests"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "what if we do not reply?"}}},
	}
	fresh, err := museBuildExecPrompt(messages, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Explain my pending notices", "These are campaign feedback requests", "what if we do not reply?"} {
		if !strings.Contains(fresh, text) {
			t.Fatalf("lost fallback context: %q", fresh)
		}
	}
	resumed, err := museBuildExecPrompt(messages, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resumed, "campaign feedback requests") || !strings.Contains(resumed, "what if we do not reply?") {
		t.Fatalf("native resume must send current message only: %q", resumed)
	}
}
