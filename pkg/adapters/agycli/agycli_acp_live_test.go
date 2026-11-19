package agycli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func agyACPText(resp *llmtypes.ContentResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Content
}

// A chat on `agy acp` through the generic ACP connector, signed in with
// agy's own stored login: a short turn, a turn with a shell tool call, then
// the same conversation resumed by its exact id after the process ended.
// Run with AGY_ACP_LIVE=1 (a few cheap turns on the account).
func TestAgyACPRealMultiTurnAndResume(t *testing.T) {
	if os.Getenv("AGY_ACP_LIVE") != "1" && !*codingCLIP0Live {
		t.Skip("set AGY_ACP_LIVE=1 to run against the real agy")
	}
	t.Setenv(AgyTransportEnv, "acp")
	owner := "agy-acp-live-" + time.Now().Format("150405.000")
	t.Cleanup(func() { CloseAgyACPSession(owner) })
	adapter := NewAgyCLIAdapter("", "gemini-3.8-flash-low", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	work := t.TempDir()
	options := []llmtypes.CallOption{WithWorkingDir(work), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true)}
	human := func(text string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, text)}
	}
	turn := func(label, text string, extra ...llmtypes.CallOption) (*llmtypes.ContentResponse, []llmtypes.StreamChunk) {
		t.Helper()
		stream := make(chan llmtypes.StreamChunk, 1024)
		var chunks []llmtypes.StreamChunk
		done := make(chan struct{})
		go func() {
			defer close(done)
			for chunk := range stream {
				chunks = append(chunks, chunk)
			}
		}()
		start := time.Now()
		resp, err := adapter.GenerateContent(ctx, human(text), append(append(options, extra...), llmtypes.WithStreamingChan(stream))...)
		close(stream)
		<-done
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		handle, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
		tools := 0
		for _, c := range chunks {
			if c.Type == llmtypes.StreamChunkTypeToolCallEnd {
				tools++
			}
		}
		t.Logf("%s: %s answer=%q session=%s provider=%s model=%s tool_calls=%d", label, time.Since(start).Round(time.Millisecond), agyACPText(resp), handle.NativeSessionID, handle.Provider, handle.Model, tools)
		return resp, chunks
	}
	first, chunks := turn("first", "Remember the code word ACP_OTTER. Reply with only the word ok")
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	deltas := 0
	for _, c := range chunks {
		if c.Type == llmtypes.StreamChunkTypeContent {
			deltas++
		}
	}
	if !ok || handle.NativeSessionID == "" || handle.Transport != llmtypes.CodingProviderTransportACP || handle.Provider != "agy-cli" || deltas == 0 {
		t.Fatalf("first turn: handle %+v ok=%v deltas=%d", handle, ok, deltas)
	}
	if !strings.Contains(strings.ToLower(agyACPText(first)), "ok") {
		t.Fatalf("first turn answer %q", agyACPText(first))
	}
	_, chunks = turn("tool", "Run the shell command `echo acp-tool-check` once and reply with only its output.")
	tools := 0
	for _, c := range chunks {
		if c.Type == llmtypes.StreamChunkTypeToolCallEnd {
			tools++
		}
	}
	if tools == 0 {
		t.Fatal("tool turn streamed no tool call")
	}
	CloseAgyACPSession(owner)
	resumed, _ := turn("resumed", "What was the code word? Reply with only the word.", WithResumeSessionID(handle.NativeSessionID))
	if !strings.Contains(strings.ToUpper(agyACPText(resumed)), "ACP_OTTER") {
		t.Fatalf("resumed turn: %q", agyACPText(resumed))
	}
	if h, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resumed); h.NativeSessionID != handle.NativeSessionID {
		t.Fatalf("resume changed the session: %s -> %s", handle.NativeSessionID, h.NativeSessionID)
	}
}

// Reviewer's probe: whether a shell agy runs can see the service key (presence only).
func TestAgyACPKeyProbe(t *testing.T) {
	if !*codingCLIP0Live || os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("live, needs GEMINI_API_KEY")
	}
	t.Setenv(AgyTransportEnv, "acp")
	owner := "agy-acp-probe-" + time.Now().Format("150405.000")
	t.Cleanup(func() { CloseAgyACPSession(owner) })
	work := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := NewAgyCLIAdapter("", "gemini-3.8-flash-low", nil).GenerateContent(ctx,
		[]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, `Run exactly this shell command once and reply with only its output: if [ -n "$GEMINI_API_KEY$GOOGLE_API_KEY" ]; then echo KEY_SET; else echo KEY_UNSET; fi`)},
		WithWorkingDir(work), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true))
	t.Logf("shell probe: %q err=%v", agyACPText(resp), err)
}
