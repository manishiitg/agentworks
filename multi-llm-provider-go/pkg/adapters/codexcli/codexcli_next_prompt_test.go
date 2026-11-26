package codexcli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-992: the native sampler must never start a user/tool turn. A stale
// native turn, old program, first turn or near-limit account gets no prediction.
func TestCodexNextPromptUsesOnlyFencedNativeSampling(t *testing.T) {
	for _, scenario := range []string{"prediction", "prediction_idle", "first", "near_limit", "old_program", "newer_turn"} {
		t.Run(scenario, func(t *testing.T) {
			s := &codexAppSession{owner: "chat", threadID: "thread", turnID: "turn", model: "native-model"}
			if scenario == "near_limit" {
				s.rateLimits = &codexRateLimits{Primary: &codexRateLimitWindow{UsedPercent: 90}}
			}
			rpc, wire := goalFixtureRPC(func(request codexRPCMessage) codexRPCMessage {
				if scenario == "prediction_idle" {
					s.mu.Lock()
					s.turnID = ""
					s.mu.Unlock()
				}
				if scenario == "newer_turn" {
					s.mu.Lock()
					s.turnID = "next-turn"
					s.mu.Unlock()
				}
				if scenario == "old_program" {
					return codexRPCMessage{Error: &codexRPCError{Code: -32601, Message: "unsupported"}}
				}
				return codexRPCMessage{Result: json.RawMessage(`{"sourceTurnId":"turn","responseId":"sample","text":"Check the diff","inputTokens":100,"cachedInputTokens":80,"outputTokens":5}`)}
			})
			s.rpc = rpc
			done := make(chan llmtypes.NextPromptResult, 1)
			ctx := llmtypes.WithNextPrompt(context.Background(), llmtypes.NextPromptScope{Owner: "chat", Provider: "codex-cli", TurnID: "host-turn", Eligible: scenario != "first", Complete: func(r llmtypes.NextPromptResult) { done <- r }})
			sampleCodexNextPrompt(ctx, s, rpc, "thread", "turn")
			var result llmtypes.NextPromptResult
			select {
			case result = <-done:
			case <-time.After(time.Second):
				t.Fatal("prediction did not settle")
			}
			if (scenario == "prediction" || scenario == "prediction_idle") && result.Text != "Check the diff" {
				t.Fatalf("prediction=%+v", result)
			}
			if scenario != "prediction" && scenario != "prediction_idle" && result.Text != "" {
				t.Fatalf("unsafe prediction=%+v", result)
			}
			if scenario == "first" || scenario == "near_limit" {
				if len(wire.requests) != 0 {
					t.Fatal("skipped prediction still spent a request")
				}
				return
			}
			if scenario != "old_program" && (result.Usage == nil || result.Usage.ResponseID != "sample" || result.Usage.Model != "native-model" || result.Usage.Input != 100 || result.Usage.CachedInput != 80 || result.Usage.Output != 5) {
				t.Fatalf("native sample usage lost (even a stale sample costs tokens): %+v", result.Usage)
			}
			if len(wire.requests) != 1 || wire.requests[0].Method != "thread/sample" {
				t.Fatalf("sampling created other work: %+v", wire.requests)
			}
			var params struct {
				Thread string `json:"threadId"`
				Turn   string `json:"expectedTurnId"`
				Effort string `json:"effort"`
			}
			if json.Unmarshal(wire.requests[0].Params, &params) != nil || params.Thread != "thread" || params.Turn != "turn" || params.Effort != "low" {
				t.Fatalf("unfenced sample: %+v", params)
			}
		})
	}
}
