package picli

import (
	"context"
	"errors"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-872-20: real native continuity and streamed output through the RPC
// protocol, including process loss and bounded cleanup. No terminal is used.
func TestPiRPCSessionLifecycleLive(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	owner := "pi-rpc-live-" + piRandomHex(4)
	defer ClosePiCLIInteractiveSessionForOwner(owner, "test")
	cwd := piLiveWorkDir(t)
	a := newRealPiCLIAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	system := "Keep the private token in memory. Do not use tools. Answer exactly what the user asks."
	token := "PI_RPC_" + piRandomHex(5)
	run := func(prompt, resume string, persistent bool) *llmtypes.ContentResponse {
		t.Helper()
		stream := make(chan llmtypes.StreamChunk, 4096)
		resp, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, system), llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(persistent), WithResumeSessionID(resume), llmtypes.WithStreamingChan(stream))
		if err != nil {
			t.Fatal(err)
		}
		content := 0
		var streamed strings.Builder
		var status *llmtypes.StatusLine
		for ch := range stream {
			if ch.Type == llmtypes.StreamChunkTypeStatusLine {
				status = ch.StatusLine
			}
			if ch.Type == llmtypes.StreamChunkTypeTerminal {
				t.Fatal("RPC emitted a terminal frame")
			}
			if ch.Type == llmtypes.StreamChunkTypeContent {
				content++
				streamed.WriteString(ch.Content)
				if ch.Metadata[llmtypes.ContentDeltaMetadataKey] != true {
					t.Fatal("native text missing delta semantics")
				}
			}
		}
		if status == nil || status.InputTokens <= 0 {
			t.Fatal("missing native status line")
		}
		if resume != "" && strings.Contains(streamed.String(), "ACK") {
			t.Fatal("replayed prior assistant text")
		}
		if content == 0 {
			t.Fatal("no streamed content")
		}
		h, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
		if !ok || h.Transport != llmtypes.CodingProviderTransportRPC || h.TmuxSession != "" || h.NativeSessionID == "" {
			t.Fatalf("invalid RPC handle: %#v", h)
		}
		if resp.Usage == nil || resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
			t.Fatalf("missing real usage: %#v", resp.Usage)
		}
		return resp
	}
	first := run("Remember "+token+". Reply exactly ACK.", "", true)
	h, _ := llmtypes.ExtractCodingProviderSessionHandleFromResponse(first)
	s := piRPCSessionForOwner(owner)
	process := s.rpc
	second := run("What private token did I give you? Return only that token.", h.NativeSessionID, true)
	if !strings.Contains(second.Choices[0].Content, token) {
		t.Fatal("warm turn lost native memory")
	}
	if s.rpc != process {
		t.Fatal("warm turn replaced process")
	}
	ClosePiCLIInteractiveSessionForOwner(owner, "simulate process loss")
	third := run("What private token did I give you? Return only that token.", h.NativeSessionID, false)
	if !strings.Contains(third.Choices[0].Content, token) {
		t.Fatal("resumed turn lost native memory")
	}
	if piRPCSessionForOwner(owner) != nil {
		t.Fatal("bounded RPC process retained")
	}
	t.Log("native fresh/warm/resumed/bounded turns, deltas and usage passed")
}

// PLAT-872-20: a steer must be both durably recorded and used by the model;
// abort must stop the active run without discarding the native conversation.
func TestPiRPCSteerAndAbortLive(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	cwd := piLiveWorkDir(t)
	owner := "pi-rpc-steer-" + piRandomHex(4)
	defer ClosePiCLIInteractiveSessionForOwner(owner, "test")
	marker := filepath.Join(cwd, "started")
	server := writePiSlowMCPServer(t, "test", marker)
	config := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, server)
	a := newRealPiCLIAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	options := []llmtypes.CallOption{WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithMCPConfig(config), WithBridgeOnlyTools(true)}
	type result struct {
		resp *llmtypes.ContentResponse
		err  error
	}
	start := func() chan result {
		done := make(chan result, 1)
		go func() {
			r, e := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call slow_contract once with token test and delay_ms 6000, then reply DONE. Follow any newer user instruction after the tool finishes.")}, options...)
			done <- result{r, e}
		}()
		deadline := time.After(60 * time.Second)
		tick := time.NewTicker(30 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case r := <-done:
				t.Fatalf("turn ended before slow tool: %v", r.err)
			case <-deadline:
				t.Fatal("slow tool never started")
			case <-tick.C:
				if _, e := os.Stat(marker); e == nil {
					return done
				}
			}
		}
	}
	done := start()
	canary := "STEER_" + piRandomHex(5)
	steer := "Change the final answer. Reply exactly " + canary + " and do not call any more tools."
	if err := SendPiInteractiveInput(ctx, owner, steer); err != nil {
		t.Fatal(err)
	}
	ack, err := AwaitPiInputDurable(ctx, owner, steer, 40*time.Second)
	if err != nil || ack.Outcome != PiDurableAckConfirmed {
		t.Fatalf("steer not durable: %#v %v", ack, err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !strings.Contains(r.resp.Choices[0].Content, canary) {
			t.Fatalf("steer ignored: %q", r.resp.Choices[0].Content)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	done = start()
	if err := SendPiInteractiveControlKey(ctx, owner, "Escape"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if !errors.Is(r.err, errPiRPCAborted) {
			t.Fatalf("abort outcome: %v", r.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	resp, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply exactly RECOVERED, no tools.")}, options...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Choices[0].Content, "RECOVERED") {
		t.Fatal("could not reuse session after abort")
	}
}

func TestPiRPCParallelIsolationLive(t *testing.T) {
	requireRealPiCLIContractE2E(t)
	cwd := piLiveWorkDir(t)
	type result struct {
		err       error
		got, want string
	}
	done := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			owner := fmt.Sprintf("pi-isolation-%d-%s", i, piRandomHex(3))
			defer ClosePiCLIInteractiveSessionForOwner(owner, "test")
			token := "ISOLATED_" + piRandomHex(5)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			a := newRealPiCLIAdapter(t)
			server := writePiEchoMCPServer(t, token)
			config := fmt.Sprintf(`{"mcpServers":{"api-bridge":{"command":"node","args":[%q]}}}`, server)
			opts := []llmtypes.CallOption{WithWorkingDir(cwd), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithMCPConfig(config), WithBridgeOnlyTools(true)}
			_, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Call mcp__api_bridge__echo_contract exactly once with token CHECK, remember the returned token, then reply ACK. Do not call it again.")}, opts...)
			if err != nil {
				done <- result{err: err}
				return
			}
			r, err := a.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply only with the token returned by the previous tool. Do not call any tools.")}, opts...)
			got := ""
			if r != nil {
				got = r.Choices[0].Content
			}
			done <- result{err, got, token + "_CHECK"}
		}(i)
	}
	for i := 0; i < 2; i++ {
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !strings.Contains(r.got, r.want) {
			t.Fatalf("sessions crossed: got %q want %q", r.got, r.want)
		}
	}
}
