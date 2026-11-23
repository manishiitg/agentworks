package musecli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func skipMuseMSPLive(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	t.Setenv(MuseTransportEnv, "msp")
}

func museMSPText(resp *llmtypes.ContentResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Content
}

// A chat on `muse serve`: two turns in one session, the second remembering the
// first, and the native session id the transfer and resume paths key on.
func TestMuseMSPRealMultiTurn(t *testing.T) {
	skipMuseMSPLive(t)
	owner := "msp-live-" + museCommandID()
	t.Cleanup(func() { CloseMuseMSPSession(owner) })
	adapter := NewMuseCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	options := []llmtypes.CallOption{WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner)}

	first, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Remember the code word MSP_ZEBRA. Reply with only: noted")}, options...)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	if !ok || handle.NativeSessionID == "" || handle.Transport != llmtypes.CodingProviderTransportMSP {
		t.Fatalf("first turn handle = %+v, %v; want a native session on msp", handle, ok)
	}
	second, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "What was the code word? Reply with only the word.")}, options...)
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if !strings.Contains(strings.ToUpper(museMSPText(second)), "MSP_ZEBRA") {
		t.Fatalf("second answer = %q, want the code word from the first turn", museMSPText(second))
	}
	if h, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(second); h.NativeSessionID != handle.NativeSessionID {
		t.Fatalf("session changed between turns: %s -> %s", handle.NativeSessionID, h.NativeSessionID)
	}
}

func museMSPHuman(text string) []llmtypes.MessageContent {
	return []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, text)}
}

func museMSPLiveChat(t *testing.T) (string, []llmtypes.CallOption) {
	t.Helper()
	owner := "msp-live-" + museCommandID()
	t.Cleanup(func() { CloseMuseMSPSession(owner) })
	return owner, []llmtypes.CallOption{WithWorkingDir(t.TempDir()), WithInteractiveSessionID(owner), llmtypes.WithReasoningEffort("low")}
}

// Streaming, live input and stop on one chat: text streams as deltas, a tool
// call is reported at its start and end, a message sent mid-turn reaches the
// running turn, a stop ends the turn at once, and the next message works.
func TestMuseMSPRealStreamSteerStop(t *testing.T) {
	skipMuseMSPLive(t)
	owner, options := museMSPLiveChat(t)
	adapter := NewMuseCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	stream := make(chan llmtypes.StreamChunk, 4096)
	var deltas, starts, ends int
	toolStarted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range stream {
			switch chunk.Type {
			case llmtypes.StreamChunkTypeContent:
				deltas++
			case llmtypes.StreamChunkTypeToolCallStart:
				starts++
				select {
				case toolStarted <- struct{}{}:
				default:
				}
			case llmtypes.StreamChunkTypeToolCallEnd:
				ends++
			}
		}
	}()
	go func() {
		select {
		case <-toolStarted:
			if err := SendMuseMSPInput(ctx, owner, "Also put the word KIWI_STEER at the end of your final reply."); err != nil {
				t.Errorf("steer: %v", err)
			}
		case <-ctx.Done():
		}
	}()
	resp, err := adapter.GenerateContent(ctx, museMSPHuman("Run the shell command `sleep 8 && echo slept` once, then reply with a numbered list of the first 30 prime numbers, one per line, then a sentence about what the command printed."), append(options, llmtypes.WithStreamingChan(stream))...)
	close(stream)
	<-done
	if err != nil {
		t.Fatalf("steered turn: %v", err)
	}
	if !strings.Contains(museMSPText(resp), "KIWI_STEER") {
		t.Fatalf("the mid-turn message did not reach the turn: %q", museMSPText(resp))
	}
	if deltas < 2 || starts == 0 || ends == 0 {
		t.Fatalf("stream: %d text deltas, %d tool starts, %d tool ends; want streamed text and a reported tool call", deltas, starts, ends)
	}
	gi := resp.Choices[0].GenerationInfo
	if gi == nil || gi.TotalTokens == nil || *gi.TotalTokens == 0 {
		t.Fatalf("no token usage on the response: %+v", gi)
	}

	stopCtx, stop := context.WithCancel(ctx)
	go func() { time.Sleep(12 * time.Second); stop() }()
	began := time.Now()
	_, err = adapter.GenerateContent(stopCtx, museMSPHuman("Run the shell command `sleep 120` and then reply done."), options...)
	if err == nil || time.Since(began) > 40*time.Second {
		t.Fatalf("stop: err=%v after %s; want a prompt cancellation", err, time.Since(began))
	}
	after, err := adapter.GenerateContent(ctx, museMSPHuman("Reply with only: AFTER_STOP_OK"), options...)
	if err != nil || !strings.Contains(museMSPText(after), "AFTER_STOP_OK") {
		t.Fatalf("message after stop: %q, %v", museMSPText(after), err)
	}
}

// Two chats at once keep their own sessions and answers.
func TestMuseMSPRealParallelIsolation(t *testing.T) {
	skipMuseMSPLive(t)
	adapter := NewMuseCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	words := []string{"ALPHA_PARALLEL", "BRAVO_PARALLEL"}
	type result struct {
		text, session string
		err           error
	}
	results := make([]result, len(words))
	finished := make(chan int, len(words))
	for i, word := range words {
		_, options := museMSPLiveChat(t)
		go func(i int, word string, options []llmtypes.CallOption) {
			resp, err := adapter.GenerateContent(ctx, museMSPHuman("Reply with only: "+word), options...)
			h, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
			results[i] = result{museMSPText(resp), h.NativeSessionID, err}
			finished <- i
		}(i, word, options)
	}
	for range words {
		<-finished
	}
	for i, word := range words {
		if results[i].err != nil || !strings.Contains(results[i].text, word) || strings.Contains(results[i].text, words[1-i]) {
			t.Fatalf("chat %d: %q, %v; want only %s", i, results[i].text, results[i].err, word)
		}
	}
	if results[0].session == "" || results[0].session == results[1].session {
		t.Fatalf("chats shared or lacked a session: %q %q", results[0].session, results[1].session)
	}
}

// A chat whose serve process ended resumes the exact native session (never a
// fresh one) and still remembers; Compact now compacts that same session.
func TestMuseMSPRealResumeAndCompact(t *testing.T) {
	skipMuseMSPLive(t)
	owner, options := museMSPLiveChat(t)
	adapter := NewMuseCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	first, err := adapter.GenerateContent(ctx, museMSPHuman("Remember the code word RESUME_OTTER. Reply with only: noted"), options...)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	handle, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	CloseMuseMSPSession(owner)
	resumed := append(append([]llmtypes.CallOption{}, options...), WithResumeSessionID(handle.NativeSessionID))
	second, err := adapter.GenerateContent(ctx, museMSPHuman("What was the code word? Reply with only the word."), resumed...)
	if err != nil {
		t.Fatalf("resumed turn: %v", err)
	}
	if h, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(second); h.NativeSessionID != handle.NativeSessionID || !strings.Contains(museMSPText(second), "RESUME_OTTER") {
		t.Fatalf("resume: session %s -> %s, answer %q", handle.NativeSessionID, h.NativeSessionID, museMSPText(second))
	}
	compact, err := adapter.GenerateContent(ctx, nil, append(resumed, llmtypes.WithCodingProviderIdleCompact())...)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	result, ok := llmtypes.IdleCompactResultFromResponse(compact)
	if !ok {
		t.Fatal("compact returned no result")
	}
	if !result.Compacted || result.TokensAfter >= result.TokensBefore {
		t.Fatalf("compact: %+v; want the session compacted", result)
	}
	if _, _, _, ok := MuseMSPUsage(owner); !ok {
		t.Fatal("no usage for a live chat")
	}
}

// The account's plan windows reach the chat's usage line from muse serve.
func TestMuseMSPRealPlanUsage(t *testing.T) {
	skipMuseMSPLive(t)
	_, options := museMSPLiveChat(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := NewMuseCLIAdapter("", "", nil).GenerateContent(ctx, museMSPHuman("Reply with only: ok"), options...)
	if err != nil {
		t.Fatal(err)
	}
	windows, _ := resp.Choices[0].GenerationInfo.Additional[llmtypes.RateLimitWindowsMetaKey].([]llmtypes.RateLimitWindow)
	if len(windows) == 0 {
		t.Fatal("no plan windows on the response")
	}
	for _, w := range windows {
		if w.Name == "" || w.ResetsAt.IsZero() || w.UsedPercent < 0 {
			t.Fatalf("plan window not well formed: %+v", w)
		}
	}
	t.Logf("plan windows: %+v", windows)
}
