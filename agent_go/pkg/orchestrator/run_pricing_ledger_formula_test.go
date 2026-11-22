package orchestrator

import (
	"context"
	"math"
	"testing"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Real bug: run files priced prompt as if it always included cache, so a
// provider that reports input apart from cache (Pi, Codex transcripts) lost
// its fresh input cost and the run's dollars disagreed with the ledger.
func TestRunPricingMatchesLedgerFormulaWhetherPromptIncludesCache(t *testing.T) {
	const fresh, output, cacheRead, cacheWrite = 1000, 200, 4000, 500
	for _, tc := range []struct {
		name          string
		includesCache bool
		prompt        int
	}{
		{"prompt apart from cache", false, fresh},
		{"prompt includes cache", true, fresh + cacheRead + cacheWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := NewContextAwareEventBridge(&noopListener{}, &silentLoggerV2{})
			persister := &recordingTokenPersister{}
			bridge.SetTokenPersister(persister)
			bridge.SetIterationFolder("runs/test-run-001")
			bridge.PushContext("execution", 1, "fetch-data", "worker-agent")
			ctx := context.Background()
			_ = bridge.HandleEvent(ctx, &unifiedevents.AgentEvent{
				Type: unifiedevents.LLMGenerationEnd, Timestamp: time.Now(),
				Data: &unifiedevents.LLMGenerationEndEvent{BaseEventData: unifiedevents.BaseEventData{Metadata: map[string]interface{}{
					"provider": "anthropic", "prompt_tokens_include_cache": tc.includesCache,
				}}},
			})
			_ = bridge.HandleEvent(ctx, &unifiedevents.AgentEvent{
				Type: unifiedevents.TokenUsage, Timestamp: time.Now(),
				Data: &unifiedevents.TokenUsageEvent{
					ModelID: "claude-sonnet-4-6", Provider: "anthropic",
					PromptTokens: tc.prompt, CompletionTokens: output,
					GenerationInfo: map[string]interface{}{
						"cumulative_cache_read_tokens":  cacheRead,
						"cumulative_cache_write_tokens": cacheWrite,
					},
				},
			})
			if err := bridge.WaitForTokenPersistence(ctx); err != nil {
				t.Fatalf("persist: %v", err)
			}
			calls := persister.snapshot()
			if len(calls) == 0 || calls[0].modelTokenData == nil {
				t.Fatal("no model token data persisted")
			}
			data := calls[0].modelTokenData
			if data.InputTokens != fresh {
				t.Fatalf("stored input = %d, want fresh %d", data.InputTokens, fresh)
			}

			meta, err := getModelMetadata("anthropic", "claude-sonnet-4-6")
			if err != nil {
				t.Fatalf("metadata: %v", err)
			}
			prompt, completion, cached := fresh, output, cacheRead
			want := llmtypes.ComputeUSDCostFromMetadata(meta, &llmtypes.GenerationInfo{
				PromptTokens: &prompt, CompletionTokens: &completion, CachedContentTokens: &cached,
				Additional: map[string]interface{}{"prompt_tokens_include_cache": false, "cache_creation_input_tokens": cacheWrite},
			})
			got := buildModelTokenUsage(data)
			if want <= 0 || math.Abs(got.TotalCost-want) > 1e-12 {
				t.Fatalf("run cost = %v, ledger formula = %v", got.TotalCost, want)
			}
			if parts := got.InputCost + got.OutputCost + got.CacheCost; math.Abs(parts-got.TotalCost) > 1e-12 {
				t.Fatalf("parts %v do not add up to total %v", parts, got.TotalCost)
			}
		})
	}
}
