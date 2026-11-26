package musecli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type museTestLogger struct{}

func (museTestLogger) Infof(string, ...any)  {}
func (museTestLogger) Errorf(string, ...any) {}
func (museTestLogger) Debugf(string, ...any) {}

func requireMuseBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("muse"); err != nil {
		t.Skip("muse CLI not in PATH")
	}
}

// museEchoTestEnv isolates the lane: echo provider (no auth, no spend) and
// per-test XDG homes so session logs never touch the real store.
func museEchoTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvMuseCLIExecProvider, "echo")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestMuseExecLaneEchoFinalText(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	adapter := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "say the word pineapple"}}},
	}, WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if got := resp.Choices[0].Content; !strings.Contains(got, "echo: say the word pineapple") {
		t.Fatalf("content = %q, want echo reply", got)
	}
	if resp.Usage == nil {
		t.Fatal("want usage attached from the session sidecar (echo reports zeros)")
	}
	gi := resp.Choices[0].GenerationInfo
	if gi == nil || gi.CodingProviderSessionHandle == nil {
		t.Fatal("missing coding-provider session handle")
	}
	handle := gi.CodingProviderSessionHandle
	if handle.Provider != "muse-cli" || handle.Transport != llmtypes.CodingProviderTransportStructured {
		t.Fatalf("handle = %+v, want muse-cli/structured", handle)
	}
	if strings.TrimSpace(handle.NativeSessionID) == "" {
		t.Fatal("handle carries no native session id")
	}
}

func TestMuseExecLaneEchoStreamsDeltas(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	adapter := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	stream := make(chan llmtypes.StreamChunk, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "say the word pineapple"}}},
	}, llmtypes.WithStreamingChan(stream), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	var streamed strings.Builder
	for {
		select {
		case chunk := <-stream:
			if chunk.Type == llmtypes.StreamChunkTypeContent {
				streamed.WriteString(chunk.Content)
			}
		default:
			goto drained
		}
	}
drained:
	if got := streamed.String(); !strings.Contains(got, "pineapple") {
		t.Fatalf("streamed deltas = %q, want pineapple; final = %q", got, resp.Choices[0].Content)
	}
}

// TestMuseExecLaneEchoSystemPrompt is the JSON-lane e2e for system handling:
// a system message plus human turn must travel folded into the exec prompt.
// The echo provider returns the prompt it received, so the assertion observes
// real CLI delivery (not just the unit fold). No model cost, no Meta auth.
func TestMuseExecLaneEchoSystemPrompt(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	adapter := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Be brief."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "say the word pineapple"}}},
	}, WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if got := resp.Choices[0].Content; !strings.Contains(got, "echo: Be brief.\n\nsay the word pineapple") {
		t.Fatalf("content = %q, want folded system + human prompt echoed", got)
	}
}

func TestMuseBuildExecPromptFoldsSystem(t *testing.T) {
	got, err := museBuildExecPrompt([]llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Be brief."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}},
	}, false)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if got != "Be brief.\n\nhi" {
		t.Fatalf("prompt = %q", got)
	}
	if _, err := museBuildExecPrompt([]llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "  "}}},
	}, false); err == nil {
		t.Fatal("blank human prompt should fail")
	}
}

func TestMuseExecLaneStructuredMultiTurn(t *testing.T) {
	requireMuseBinary(t)
	museEchoTestEnv(t)
	adapter := NewMuseCLIAdapter("", "muse-cli", museTestLogger{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	human := func(text string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}},
		}
	}
	first, err := adapter.GenerateContent(ctx, human("first turn turnips"), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	sid := strings.TrimSpace(first.Choices[0].GenerationInfo.CodingProviderSessionHandle.NativeSessionID)
	if sid == "" {
		t.Fatal("first turn surfaced no native session id")
	}
	second, err := adapter.GenerateContent(ctx, human("second turn mangoes"), WithResumeSessionID(sid), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if got := strings.TrimSpace(second.Choices[0].GenerationInfo.CodingProviderSessionHandle.NativeSessionID); got != sid {
		t.Fatalf("second turn session = %q, want resumed %q", got, sid)
	}
	// Same on-disk session must hold both prompts: mechanical proof the
	// second turn landed in the first turn's session. (Echo cannot prove
	// the model saw turn one; that needs a Meta-provider run.)
	msgs, ok := readMuseTranscriptMessages(museSessionLogPath(sid), "")
	if !ok {
		t.Fatal("session log unreadable")
	}
	var joined strings.Builder
	for _, m := range msgs {
		joined.WriteString(messageText(m))
		joined.WriteString("\n")
	}
	for _, want := range []string{"first turn turnips", "second turn mangoes"} {
		if !strings.Contains(joined.String(), want) {
			t.Fatalf("session log lacks %q; got %d messages", want, len(msgs))
		}
	}
}
