package codexcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-19: the live agentic capture joined distinct messages into
// "now.I found...content.ZEBRA". Preserve boundaries without replaying deltas.
func TestCodexAppServerSeparatesMessagesWithoutDuplicatingCompletedText(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 10), done: make(chan struct{})}
	send := func(method, fields string) {
		r.events <- codexRPCMessage{Method: method, Params: json.RawMessage(`{"threadId":"thread","turnId":"turn",` + fields + `}`)}
	}
	send("item/agentMessage/delta", `"itemId":"a","delta":"I will "`)
	send("item/agentMessage/delta", `"itemId":"a","delta":"read it."`)
	send("item/completed", `"item":{"id":"a","type":"agentMessage","phase":"commentary","text":"I will read it."}`)
	// A message received whole must get the same boundary as streamed deltas.
	send("item/completed", `"item":{"id":"b","type":"agentMessage","phase":"commentary","text":"Read complete.\n\n"}`)
	send("item/agentMessage/delta", `"itemId":"c","delta":"done\n\n"`)
	send("item/agentMessage/delta", `"itemId":"c","delta":"`+"```text\\nx\\n```"+`"`)
	send("item/completed", `"item":{"id":"c","type":"agentMessage","phase":"final_answer","text":"done\n\n`+"```text\\nx\\n```"+`"}`)
	send("turn/completed", `"turn":{"id":"turn","status":"completed"}`)
	var output strings.Builder
	response, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{threadID: "thread"}, r, "thread", "turn", func(ch llmtypes.StreamChunk) {
		if ch.Type == llmtypes.StreamChunkTypeContent {
			if ch.Metadata[llmtypes.ContentDeltaMetadataKey] != true {
				t.Fatal("token deltas are mislabeled as whole messages")
			}
			output.WriteString(ch.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	final := "done\n\n```text\nx\n```"
	if output.String() != "I will read it.\n\nRead complete.\n\n"+final || response.Choices[0].Content != final {
		t.Fatalf("message boundaries or final changed: %q / %q", output.String(), response.Choices[0].Content)
	}
}

// Server B 2026-10-28: a steer the turn did not consume ran as Codex's own next turn, which the adapter ignored, so
// the chat showed nothing running and Stop had no turn id. The call must follow that turn to its end.
func TestCodexAppServerFollowsUnconsumedSteerIntoNextTurn(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 10), done: make(chan struct{})}
	send := func(method, fields string) {
		r.events <- codexRPCMessage{Method: method, Params: json.RawMessage(fields)}
	}
	send("item/completed", `{"threadId":"thread","turnId":"turn","item":{"id":"m0","type":"agentMessage","phase":"final_answer","text":"first answer"}}`)
	send("turn/completed", `{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)
	send("turn/started", `{"threadId":"thread","turn":{"id":"turn2","status":"inProgress"}}`)
	send("item/completed", `{"threadId":"thread","turnId":"turn2","item":{"id":"m","type":"agentMessage","phase":"final_answer","text":"steered answer"}}`)
	send("turn/completed", `{"threadId":"thread","turn":{"id":"turn2","status":"completed"}}`)
	s := &codexAppSession{threadID: "thread", turnID: "turn", steers: map[string]string{"id": "more"}}
	response, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), s, r, "thread", "turn", func(llmtypes.StreamChunk) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Choices[0].Content; got != "first answer\n\nsteered answer" || s.turnID != "turn2" {
		t.Fatalf("follow-up turn not followed: %q turn=%s", got, s.turnID)
	}
}

func TestCodexNativeTextBlocksUseStartsAndCompletedSnapshots(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 8), done: make(chan struct{})}
	for _, frame := range []struct{ method, body string }{
		{"item/started", `"item":{"id":"a","type":"agentMessage"}`},
		{"item/agentMessage/delta", `"itemId":"a","delta":"before "`},
		{"item/agentMessage/delta", `"itemId":"a","delta":"finishes"`},
		{"item/completed", `"item":{"id":"a","type":"agentMessage","phase":"commentary","text":"before finishes"}`},
		{"item/started", `"item":{"id":"b","type":"agentMessage"}`},
		{"item/agentMessage/delta", `"itemId":"b","delta":"after"`},
		{"item/completed", `"item":{"id":"b","type":"agentMessage","phase":"final_answer","text":"after"}`},
		{"turn/completed", `"turn":{"id":"turn","status":"completed"}`},
	} {
		r.events <- codexRPCMessage{Method: frame.method, Params: json.RawMessage(`{"threadId":"thread","turnId":"turn",` + frame.body + `}`)}
	}
	var blocks []llmtypes.StreamChunk
	var text strings.Builder
	_, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{}, r, "thread", "turn", func(c llmtypes.StreamChunk) {
		if c.Type == llmtypes.StreamChunkTypeAssistantBlock {
			blocks = append(blocks, c)
		}
		if c.Type == llmtypes.StreamChunkTypeContent {
			text.WriteString(c.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 4 || blocks[1].Content != "before finishes" || blocks[3].Content != "after" || blocks[0].Metadata[llmtypes.AssistantBlockIDMetadataKey] == blocks[2].Metadata[llmtypes.AssistantBlockIDMetadataKey] {
		t.Fatalf("framing=%+v", blocks)
	}
	if text.String() != "before finishes\n\nafter" {
		t.Fatalf("duplicated stream %q", text.String())
	}
}

// PLAT-746: compaction used to omit counts; only native installed usage is an after count.
func TestCodexAppCompactionReportsNativeCountsAndRefreshesStatus(t *testing.T) {
	for _, late := range []bool{false, true} {
		r := &codexRPC{events: make(chan codexRPCMessage, 12), done: make(chan struct{})}
		send := func(method, fields string) {
			r.events <- codexRPCMessage{Method: method, Params: json.RawMessage(`{"threadId":"thread","turnId":"turn",` + fields + `}`)}
		}
		send("thread/tokenUsage/updated", `"tokenUsage":{"total":{"inputTokens":90000,"totalTokens":91000},"last":{"inputTokens":90000,"totalTokens":91000},"modelContextWindow":200000}`)
		send("item/started", `"item":{"id":"compact1","type":"contextCompaction"}`)
		if late {
			send("item/completed", `"item":{"id":"compact1","type":"contextCompaction"}`)
		}
		send("thread/tokenUsage/updated", `"tokenUsage":{"total":{"inputTokens":90000,"totalTokens":91000},"last":{"totalTokens":12000},"modelContextWindow":200000}`)
		if !late {
			send("item/completed", `"item":{"id":"compact1","type":"contextCompaction"}`)
		}
		send("turn/completed", `"turn":{"id":"turn","status":"completed"}`)
		var end *llmtypes.ContextCompaction
		var status *llmtypes.StatusLine
		_, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{}, r, "thread", "turn", func(c llmtypes.StreamChunk) {
			if c.ContextCompaction != nil && c.ContextCompaction.Phase == llmtypes.ContextCompactionPhaseEnd {
				copy := *c.ContextCompaction
				end = &copy
			}
			if c.StatusLine != nil {
				status = c.StatusLine
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if end == nil || end.TokensBefore != 91000 || end.TokensAfter != 12000 {
			t.Fatalf("late=%v compact=%+v", late, end)
		}
		if status == nil || status.Metadata[llmtypes.ContextUsedTokensMetaKey] != 12000 {
			t.Fatalf("stale status %+v", status)
		}
	}
}

// PLAT-746: absent summary requests left enabled Thinking blank despite the
// existing decoder. Keep request defaults and a provider delta on the real consumer.
func TestCodexAppServerRequestsAndStreamsReasoningSummaries(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	llmtypes.WithProviderAccountEnvironment(map[string]string{"CODEX_HOME": t.TempDir()})(opts)
	config, err := codexAppThreadConfig(opts, "never")
	if err != nil {
		t.Fatal(err)
	}
	if config["features"].(map[string]any)["concurrent_reasoning_summaries"] != true {
		t.Fatal("concurrent summaries not requested")
	}
	if codexAppTurnParams("thread", "hello", opts)["summary"] != "auto" {
		t.Fatal("turn did not request a summary")
	}
	WithReasoningSummary("none")(opts)
	if codexAppTurnParams("thread", "hello", opts)["summary"] != "none" {
		t.Fatal("explicit summary preference lost")
	}
	r := &codexRPC{events: make(chan codexRPCMessage, 2), done: make(chan struct{})}
	r.events <- codexRPCMessage{Method: "item/reasoning/summaryTextDelta", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"reason","delta":"Checking the implementation."}`)}
	r.events <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
	var reasoning strings.Builder
	_, err = (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{threadID: "thread"}, r, "thread", "turn", func(c llmtypes.StreamChunk) {
		if c.Type == llmtypes.StreamChunkTypeReasoning {
			reasoning.WriteString(c.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning.String() != "Checking the implementation." {
		t.Fatalf("thinking stream=%q", reasoning.String())
	}
}

// PLAT-746: the native turn plan updates in place; plan markdown is not todo state.
func TestCodexAppServerEmitsStructuredPlanSnapshots(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 5), done: make(chan struct{})}
	for _, raw := range []struct{ method, params string }{
		{"turn/plan/updated", `{"threadId":"other","turnId":"turn","plan":[{"step":"Wrong chat","status":"completed"}]}`},
		{"item/plan/delta", `{"threadId":"thread","turnId":"turn","itemId":"proposed","delta":"- [x] Guess"}`},
		{"turn/plan/updated", `{"threadId":"thread","turnId":"turn","plan":[{"step":"Inspect","status":"in_progress"}]}`},
		{"turn/plan/updated", `{"threadId":"thread","turnId":"turn","plan":[{"step":"Inspect","status":"completed"}]}`},
		{"turn/completed", `{"threadId":"thread","turnId":"turn","turn":{"id":"turn","status":"completed"}}`},
	} {
		r.events <- codexRPCMessage{Method: raw.method, Params: json.RawMessage(raw.params)}
	}
	var plans []*llmtypes.CodingAgentPlan
	_, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{threadID: "thread"}, r, "thread", "turn", func(ch llmtypes.StreamChunk) {
		if ch.CodingAgentPlan != nil {
			plans = append(plans, ch.CodingAgentPlan)
		}
	})
	if err != nil || len(plans) != 2 || plans[0].PlanID != plans[1].PlanID || plans[0].Items[0].Status != "running" || plans[1].Items[0].Status != "completed" {
		t.Fatalf("native plans lost/crossed chat: %+v %v", plans, err)
	}
}

// PLAT-746-990: plan updates must reach subscribers even without a token event.
func TestCodexAccountLimitsEmitWithoutTokenUsage(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 3), done: make(chan struct{})}
	for _, percent := range []string{"0", "42"} {
		r.events <- codexRPCMessage{Method: "account/rateLimits/updated", Params: json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":` + percent + `,"windowDurationMins":300}}}`)}
	}
	r.events <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
	initial := &llmtypes.StatusLine{Provider: "codex-cli", InputTokens: 100, Metadata: map[string]any{"context_used_tokens": 123}}
	var statuses []*llmtypes.StatusLine
	_, err := (&CodexCLIAdapter{}).consumeAppTurn(context.Background(), &codexAppSession{status: initial}, r, "thread", "turn", func(c llmtypes.StreamChunk) {
		if c.StatusLine != nil {
			statuses = append(statuses, c.StatusLine)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected two immediate quota updates, got %d", len(statuses))
	}
	for i, status := range statuses {
		windows := status.RateLimitWindows()
		if len(windows) != 1 || windows[0].UsedPercent != float64(i*42) || status.InputTokens != 100 || status.Metadata["context_used_tokens"] != 123 {
			t.Fatalf("lost or mutated quota/context: %+v", status)
		}
	}
	if _, changed := initial.Metadata[llmtypes.RateLimitWindowsMetaKey]; changed {
		t.Fatal("quota update mutated prior status")
	}
}

// A Codex app-server chat showed its async question (request_user_input_async) as plain text and got its card only
// after the turn ended, while Codex slept for minutes waiting for the answer. The card is shown when it is asked.
func TestCodexAppServerShowsAsyncQuestionCardWhenAsked(t *testing.T) {
	r := &codexRPC{events: make(chan codexRPCMessage, 10), done: make(chan struct{})}
	send := func(method, fields string) {
		r.events <- codexRPCMessage{Method: method, Params: json.RawMessage(`{"threadId":"thread","turnId":"turn",` + fields + `}`)}
	}
	send("item/completed", `"item":{"id":"call_q","type":"agentMessage","phase":"final_answer","text":"Which colour?\n- Red","delivery":"async","questions":[{"title":"Which colour?","options":["Red"]}]}`)
	send("item/completed", `"item":{"id":"m","type":"agentMessage","phase":"commentary","text":"Waiting.","delivery":null,"questions":null}`)
	send("turn/completed", `"turn":{"id":"turn","status":"completed"}`)
	var shown []llmtypes.NativeRecord
	ctx := llmtypes.WithNativeAsyncQuestionHandler(context.Background(), func(r llmtypes.NativeRecord) bool { shown = append(shown, r); return true })
	var output strings.Builder
	if _, err := (&CodexCLIAdapter{}).consumeAppTurn(ctx, &codexAppSession{threadID: "thread"}, r, "thread", "turn", func(ch llmtypes.StreamChunk) {
		if ch.Type == llmtypes.StreamChunkTypeContent {
			output.WriteString(ch.Content)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || shown[0].ID != "call_q" || shown[0].SessionID != "thread" || shown[0].Text != `{"questions":[{"title":"Which colour?","options":["Red"]}]}` {
		t.Fatalf("async question card = %+v", shown)
	}
	if strings.TrimSpace(output.String()) != "Waiting." {
		t.Fatalf("the carded question was repeated as text, or a reply was lost: %q", output.String())
	}
}
