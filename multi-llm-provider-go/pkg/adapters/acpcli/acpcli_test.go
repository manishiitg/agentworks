package acpcli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type replayRun struct {
	resp   *llmtypes.ContentResponse
	err    error
	chunks []llmtypes.StreamChunk
}

func human(text string) []llmtypes.MessageContent {
	return []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Be brief."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}},
	}
}

// runReplay plays one transcript through the real adapter path.
func runReplay(t *testing.T, ctx context.Context, scenario, model string, onChunk func(llmtypes.StreamChunk), options ...llmtypes.CallOption) replayRun {
	t.Helper()
	workdir := t.TempDir()
	owner := "chat-" + scenario
	peer := installReplay(t, scenario, workdir)
	adapter, err := NewAdapter(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream := make(chan llmtypes.StreamChunk, 256)
	options = append(options, WithInteractiveSessionID(owner), WithWorkingDir(workdir), llmtypes.WithStreamingChan(stream))
	done := make(chan replayRun, 1)
	go func() {
		resp, err := adapter.GenerateContent(ctx, human("hello"), options...)
		done <- replayRun{resp: resp, err: err}
	}()
	var chunks []llmtypes.StreamChunk
	var run replayRun
	for finished := false; !finished; {
		select {
		case c := <-stream:
			chunks = append(chunks, c)
			if onChunk != nil {
				onChunk(c)
			}
		case run = <-done:
			finished = true
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: turn did not finish", scenario)
		}
	}
	for len(stream) > 0 {
		chunks = append(chunks, <-stream)
	}
	run.chunks = chunks
	CloseSession(owner)
	peer.wait(t)
	return run
}

func chunksOf(chunks []llmtypes.StreamChunk, kind llmtypes.StreamChunkType) []llmtypes.StreamChunk {
	var out []llmtypes.StreamChunk
	for _, c := range chunks {
		if c.Type == kind {
			out = append(out, c)
		}
	}
	return out
}

func TestACPReplayTurns(t *testing.T) {
	for _, tc := range []struct {
		scenario, model string
		options         []llmtypes.CallOption
		check           func(t *testing.T, run replayRun)
	}{
		{
			// Text, thinking and context usage stream; the chosen model is
			// selected with session/set_model; unknown update kinds are ignored.
			scenario: "text_turn", model: "opencode/openai/gpt-5",
			check: func(t *testing.T, run replayRun) {
				if got := run.resp.Choices[0].Content; got != "Hello world" {
					t.Fatalf("answer = %q", got)
				}
				if len(chunksOf(run.chunks, llmtypes.StreamChunkTypeReasoning)) != 1 || len(chunksOf(run.chunks, llmtypes.StreamChunkTypeStatusLine)) != 1 {
					t.Fatalf("missing thinking or status line: %+v", run.chunks)
				}
				if run.resp.Usage == nil || run.resp.Usage.TotalTokens != 13 {
					t.Fatalf("usage = %+v", run.resp.Usage)
				}
				handle, ok := llmtypes.ExtractCodingProviderSessionHandle(run.resp.Choices[0].GenerationInfo)
				if !ok || handle.NativeSessionID != "sess_text" || handle.Model != "opencode/openai/gpt-5" {
					t.Fatalf("session handle = %+v", handle)
				}
				if models := AgentModels("opencode"); len(models) != 2 {
					t.Fatalf("agent models = %+v", models)
				}
			},
		},
		{
			// Tool calls open and close once each, with text or diff results; a plan streams. A step the agent
			// never reports finished (agy leaves some open) is closed when the turn ends, not left "running".
			scenario: "tool_call", model: "grok",
			check: func(t *testing.T, run replayRun) {
				starts, ends := chunksOf(run.chunks, llmtypes.StreamChunkTypeToolCallStart), chunksOf(run.chunks, llmtypes.StreamChunkTypeToolCallEnd)
				if len(starts) != 3 || len(ends) != 3 {
					t.Fatalf("starts=%d ends=%d", len(starts), len(ends))
				}
				if ends[2].ToolCallID != "call-bg" || ends[2].Metadata["tool_unfinished"] != true {
					t.Fatalf("the unfinished step must close at turn end: %+v", ends[2])
				}
				if ends[0].ToolCallID != "call-read" || ends[0].ToolResult != "line one" || !strings.Contains(ends[1].ToolResult, "notes.txt (+2 -1") {
					t.Fatalf("tool results: %q / %q", ends[0].ToolResult, ends[1].ToolResult)
				}
				if plans := chunksOf(run.chunks, llmtypes.StreamChunkTypeCodingAgentPlan); len(plans) != 1 || len(plans[0].CodingAgentPlan.Items) != 2 {
					t.Fatalf("plan chunks: %+v", plans)
				}
			},
		},
		{
			// The chat's own session is loaded; its replayed history is not this turn's answer.
			scenario: "resume_load", model: "opencode",
			options: []llmtypes.CallOption{WithResumeSessionID("sess_old")},
			check: func(t *testing.T, run replayRun) {
				if got := run.resp.Choices[0].Content; got != "New answer" {
					t.Fatalf("answer = %q (history leaked into the turn?)", got)
				}
				handle, _ := llmtypes.ExtractCodingProviderSessionHandle(run.resp.Choices[0].GenerationInfo)
				if handle.NativeSessionID != "sess_old" {
					t.Fatalf("resumed session = %q", handle.NativeSessionID)
				}
			},
		},
		{
			// The chat's MCP servers reach session/new as ACP stdio entries.
			scenario: "mcp_servers", model: "opencode",
			options: []llmtypes.CallOption{WithMCPConfig(`{"mcpServers":{"docs":{"command":"docs-server"},"api-bridge":{"command":"/opt/bridge/bin/bridge","args":["--stdio"],"env":{"BRIDGE_URL":"http://127.0.0.1:9000"}}}}`)},
			check: func(t *testing.T, run replayRun) {
				if got := run.resp.Choices[0].Content; got != "Tools ready" {
					t.Fatalf("answer = %q", got)
				}
			},
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			run := runReplay(t, context.Background(), tc.scenario, tc.model, nil, tc.options...)
			if run.err != nil {
				t.Fatal(run.err)
			}
			tc.check(t, run)
		})
	}
}

// Stop sends session/cancel (with the agent's cancel _meta) and the turn
// ends as cancelled once the agent answers its prompt.
func TestACPCancelEndsTurnAsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := runReplay(t, ctx, "cancel", "grok", func(c llmtypes.StreamChunk) {
		if c.Type == llmtypes.StreamChunkTypeContent {
			cancel()
		}
	})
	if !errors.Is(run.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", run.err)
	}
}

// Permission requests follow the chat's native tools mode, like the other
// coding agents: Full tools allow at once; otherwise the person is asked and
// an unanswered request never allows.
func TestACPPermissionFollowsToolsMode(t *testing.T) {
	confined := func(opts *llmtypes.CallOptions) {
		opts.CLISecurity = &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Seatbelt: true, LandlockRunner: "/usr/bin/true", PrivateHome: t.TempDir()}
	}
	for _, tc := range []struct {
		scenario string
		options  []llmtypes.CallOption
		answer   string // "" = nobody attending
		asked    bool
	}{
		{scenario: "permission_full_tools", options: []llmtypes.CallOption{WithNativeToolsMode("full"), confined}},
		{scenario: "permission_asked", options: []llmtypes.CallOption{WithNativeToolsMode("mcp_only")}, answer: "Allow for this chat", asked: true},
		{scenario: "permission_unanswered", options: []llmtypes.CallOption{WithNativeToolsMode("mcp_only")}},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			ctx := context.Background()
			asked := false
			if tc.answer != "" {
				ctx = llmtypes.WithCodingAgentQuestionHandler(ctx, ProviderID, "chat-"+tc.scenario, func(_ context.Context, args map[string]any) (string, error) {
					asked = args["native_request_kind"] == "command_approval"
					return `{"status":"answered","answers":[{"id":"command_permission","selected_labels":["` + tc.answer + `"]}]}`, nil
				})
			}
			run := runReplay(t, ctx, tc.scenario, "kiro", nil, tc.options...)
			if run.err != nil {
				t.Fatal(run.err)
			}
			if asked != tc.asked {
				t.Fatalf("asked the person = %t, want %t", asked, tc.asked)
			}
		})
	}
}

// An agent's refusal to start a session for lack of a login becomes a clear sign-in error.
func TestACPAuthRequiredIsSignInError(t *testing.T) {
	s := &session{agent: registry["kiro"], rpc: newConn("chat", "kiro", nil)}
	err := s.setupError(&RPCError{Code: -32000, Message: "Authentication required"})
	if !llmtypes.IsCodingAgentAuthRequiredError(err) || !strings.HasPrefix(err.Error(), "Sign in to Kiro first") {
		t.Fatalf("err = %v", err)
	}
}

func TestACPFoldMessagesDeduplicatesLaunchSystemPrompt(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	llmtypes.WithCodingProviderLaunchSystemPrompt("Be brief.")(opts)
	system, prompt := foldMessages(human("hello"), opts)
	if system != "Be brief." || prompt != "hello" {
		t.Fatalf("duplicate launch system prompt was not deduplicated: system=%q prompt=%q", system, prompt)
	}
	llmtypes.WithCodingProviderLaunchSystemPrompt("Launch rules.")(opts)
	system, _ = foldMessages(human("hello"), opts)
	if system != "Launch rules.\n\nBe brief." {
		t.Fatalf("distinct launch system prompt was not kept: system=%q", system)
	}
	system, _ = foldMessages(nil, opts)
	if system != "Launch rules." {
		t.Fatalf("launch-only system prompt lost: system=%q", system)
	}
}
