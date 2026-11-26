package mcpagent

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

func TestPiGenerationPricesFreshAndCachedUsageWithoutGuessingOtherRoutes(t *testing.T) {
	for _, tt := range []struct {
		name, selected, effective string
		fresh, output, cached     int
		actual, want              float64
	}{
		{"google", "google/gemini-3.8-flash", "google/gemini-3.8-flash", 2522, 1730, 154264, 0, 0.0199488},
		{"vertex", "google-vertex/gemini-3.7-flash", "google-vertex/gemini-3.7-flash", 2954, 1007, 79943, 0, 0.011987475},
		{"native actual wins", "google/gemini-3.8-flash", "google/gemini-3.8-flash", 2522, 1730, 154264, 0.123, 0.123},
		{"unknown google model", "google/unknown-model", "google/unknown-model", 2522, 1730, 154264, 0, 0},
		{"custom route", "custom/gemini-3.7-flash", "gemini-3.7-flash", 2522, 1730, 154264, 0, 0},
		{"openrouter route", "openrouter/google/gemini-3.8-flash", "openrouter/google/gemini-3.8-flash", 2522, 1730, 154264, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener := &recordingAgentEventListener{}
			a := &Agent{sessionID: "pi-pricing", modelID: tt.selected, provider: llm.ProviderPiCLI,
				llmModel: picli.NewPiCLIAdapter("", tt.selected, nil), logger: loggerv2.NewNoop(), listeners: []AgentEventListener{listener}}
			additional := map[string]interface{}{"pi_model": tt.effective, "cache_read_input_tokens": tt.cached, "prompt_tokens_include_cache": false}
			if tt.actual > 0 {
				additional["cost_usd"] = tt.actual
			}
			resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{
				InputTokens: &tt.fresh, OutputTokens: &tt.output, CachedContentTokens: &tt.cached, Additional: additional,
			}}}}
			a.endLLMGeneration(context.Background(), "", 1, 0, time.Second, events.UsageMetrics{PromptTokens: tt.fresh, CompletionTokens: tt.output, TotalTokens: tt.fresh + tt.output + tt.cached}, resp)
			var completed *events.LLMGenerationEndEvent
			for _, event := range listener.events {
				if value, ok := event.Data.(*events.LLMGenerationEndEvent); ok {
					completed = value
				}
			}
			if completed == nil {
				t.Fatal("missing cost event")
			}
			got, _ := completed.Metadata["cost_usd_estimated"].(float64)
			if math.Abs(got-tt.want) > 1e-12 || math.Abs(a.lastTurnCost-tt.want) > 1e-12 {
				t.Fatalf("cost event=%v runtime=%v, want %v", got, a.lastTurnCost, tt.want)
			}
			if tt.actual > 0 && completed.Metadata["cost_usd"] != tt.actual {
				t.Fatal("native cost was overwritten")
			}
		})
	}
}

type pricedEventTestModel struct{}

func (pricedEventTestModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return nil, nil
}

func (pricedEventTestModel) GetModelID() string { return "claude-opus-5" }

func (pricedEventTestModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{
		ModelID:                         "claude-opus-5",
		InputCostPer1MTokens:            5,
		OutputCostPer1MTokens:           25,
		CachedInputCostPer1MTokens:      0.5,
		CachedInputCostWritePer1MTokens: 6.25,
	}, nil
}

func TestLLMGenerationEndCarriesRuntimeCalculatedCost(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "pulse-cost-test",
		modelID:   "selected-alias",
		provider:  llm.ProviderClaudeCode,
		llmModel:  pricedEventTestModel{},
		logger:    loggerv2.NewNoop(),
		listeners: []AgentEventListener{listener},
	}
	prompt, completion, cacheRead := 1_273_577, 34_200, 1_273_527
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{
			PromptTokens:        &prompt,
			CompletionTokens:    &completion,
			CachedContentTokens: &cacheRead,
			Additional: map[string]interface{}{
				"claude_code_model":           "claude-opus-5",
				"cache_read_input_tokens":     cacheRead,
				"prompt_tokens_include_cache": true,
			},
		},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		CacheTokens:      cacheRead,
	}, resp)

	var completed *events.LLMGenerationEndEvent
	for _, event := range listener.events {
		if value, ok := event.Data.(*events.LLMGenerationEndEvent); ok {
			completed = value
		}
	}
	if completed == nil {
		t.Fatal("LLMGenerationEnd event was not emitted")
	}

	// 50 fresh input tokens * $5/M + 34,200 output * $25/M +
	// 1,273,527 cache-read tokens * $0.50/M.
	want := 0.00025 + 0.855 + 0.6367635
	got, ok := completed.Metadata["cost_usd_estimated"].(float64)
	if !ok || got != want {
		t.Fatalf("cost_usd_estimated = %#v, want %.7f", completed.Metadata["cost_usd_estimated"], want)
	}
	if completed.Metadata["provider"] != string(llm.ProviderClaudeCode) || completed.Metadata["model_id"] != "claude-opus-5" {
		t.Fatalf("generation identity must use the resolved model rather than the selected alias: %#v", completed.Metadata)
	}
	if gotModel := completed.Metadata["cost_model_id"]; gotModel != "claude-opus-5" {
		t.Fatalf("cost_model_id = %#v, want claude-opus-5", gotModel)
	}
}

func TestRuntimePricingDoesNotSubtractSeparateCacheTwice(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "codex-cost-test",
		modelID:   "claude-opus-5",
		llmModel:  pricedEventTestModel{},
		logger:    loggerv2.NewNoop(),
		listeners: []AgentEventListener{listener},
	}
	freshPrompt, completion, cacheRead := 50, 100, 1_000
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{
			PromptTokens:        &freshPrompt,
			CompletionTokens:    &completion,
			CachedContentTokens: &cacheRead,
			Additional: map[string]interface{}{
				"cache_read_input_tokens":     cacheRead,
				"prompt_tokens_include_cache": false,
			},
		},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens:     freshPrompt,
		CompletionTokens: completion,
		TotalTokens:      freshPrompt + completion,
		CacheTokens:      cacheRead,
	}, resp)

	var got float64
	for _, event := range listener.events {
		if completed, ok := event.Data.(*events.LLMGenerationEndEvent); ok {
			got, _ = completed.Metadata["cost_usd_estimated"].(float64)
		}
	}
	want := 0.00025 + 0.0025 + 0.0005
	if got != want {
		t.Fatalf("cost_usd_estimated = %.9f, want %.9f", got, want)
	}
}

func TestSubscriptionRuntimeKeepsItsShadowEstimateWithNativeDollarUsage(t *testing.T) {
	a := &Agent{sessionID: "subscription-pricing", modelID: "claude-opus-5", provider: llm.ProviderClaudeCode,
		llmModel: pricedEventTestModel{}, logger: loggerv2.NewNoop()}
	fresh, output := 100, 10
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{
		InputTokens: &fresh, OutputTokens: &output, Additional: map[string]interface{}{"cost_usd": 0.123},
	}}}}
	a.endLLMGeneration(context.Background(), "", 1, 0, time.Second, events.UsageMetrics{PromptTokens: fresh, CompletionTokens: output}, resp)
	if math.Abs(a.lastTurnCost-0.00075) > 1e-12 {
		t.Fatalf("subscription shadow estimate changed to native dollars: %v", a.lastTurnCost)
	}
}

func TestCumulativeUsageRetainsEstimatedProviderMarker(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "estimated-usage-test",
		modelID:   "cursor-auto",
		llmModel:  pricedEventTestModel{},
		logger:    loggerv2.NewNoop(),
		listeners: []AgentEventListener{listener},
	}
	prompt, completion := 10, 5
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{
			PromptTokens:     &prompt,
			CompletionTokens: &completion,
			Additional:       map[string]interface{}{"token_usage_estimated": true},
		},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
	}, resp)
	agent.emitTotalTokenUsageEvent(context.Background(), time.Second)

	for _, event := range listener.events {
		total, ok := event.Data.(*events.TokenUsageEvent)
		if !ok || total.Context != "conversation_total" {
			continue
		}
		if got, _ := total.GenerationInfo["token_usage_estimated"].(bool); !got {
			t.Fatalf("cumulative token_usage_estimated = %#v, want true", total.GenerationInfo["token_usage_estimated"])
		}
		return
	}
	t.Fatal("conversation_total token event was not emitted")
}

func TestCodingCLIAggregateUsageDoesNotPretendToBeContextPercentage(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID:          "coding-cli-context-test",
		modelID:            "gpt-5.6",
		modelContextWindow: 200_000,
		logger:             loggerv2.NewNoop(),
		listeners:          []AgentEventListener{listener},
	}
	prompt := 643_364
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{Additional: map[string]interface{}{
			"context_window_usage_known": false,
		}},
	}}}

	agent.endLLMGeneration(context.Background(), "done", 1, 0, time.Second, events.UsageMetrics{
		PromptTokens: prompt,
		TotalTokens:  prompt,
	}, resp)

	if agent.contextWindowUsageKnown {
		t.Fatal("aggregate coding-CLI usage must not be treated as a context snapshot")
	}
	if agent.currentContextWindowUsage != 0 {
		t.Fatalf("currentContextWindowUsage = %d, want 0 when unknown", agent.currentContextWindowUsage)
	}
	for _, event := range listener.events {
		completed, ok := event.Data.(*events.LLMGenerationEndEvent)
		if !ok {
			continue
		}
		if got := completed.Metadata["context_usage_percent"]; got != float64(0) {
			t.Fatalf("context_usage_percent = %#v, want 0", got)
		}
		if _, found := completed.Metadata["model_context_window"]; found {
			t.Fatal("unknown context usage must not publish a model-context percentage denominator")
		}
		return
	}
	t.Fatal("LLMGenerationEnd event was not emitted")
}

// PLAT-872-21: missing native ACP usage must not render as measured zero tokens.
func TestNativeMissingUsageDoesNotEmitZeroTotal(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{sessionID: "missing-usage", provider: llm.ProviderCursorCLI, modelID: "composer-2.5", logger: loggerv2.NewNoop(), listeners: []AgentEventListener{listener}}
	a.emitTotalTokenUsageEvent(context.Background(), time.Second)
	if len(listener.events) != 0 {
		t.Fatal("unavailable usage emitted as zero")
	}
	a.cumulativePromptTokens = 10
	a.cumulativeCompletionTokens = 5
	a.cumulativeTotalTokens = 15
	a.emitTotalTokenUsageEvent(context.Background(), time.Second)
	if len(listener.events) != 1 {
		t.Fatal("reported usage was hidden")
	}
}

// A CLI's reported capacity overrides model tables without treating a billed
// per-turn prompt total as the current context occupancy.
func TestReportedContextWindowOverridesFallbackWithoutInventingOccupancy(t *testing.T) {
	agent := &Agent{sessionID: "reported-context", modelID: "main-model", modelContextWindow: 200000, logger: loggerv2.NewNoop()}
	input := 1
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{InputTokens: &input, Additional: map[string]interface{}{llmtypes.ContextWindowTokensMetaKey: 327680, "context_window_usage_known": false}}}}}
	agent.accumulateTokenUsage(context.Background(), events.UsageMetrics{}, resp, 1)
	if agent.modelContextWindow != 327680 {
		t.Fatalf("native capacity lost to fallback: %d", agent.modelContextWindow)
	}
	for _, bad := range []interface{}{nil, 0, -1, 1.5, "1000000"} {
		resp.Choices[0].GenerationInfo.Additional[llmtypes.ContextWindowTokensMetaKey] = bad
		agent.applyReportedContextWindow(resp)
		if agent.modelContextWindow != 327680 {
			t.Fatalf("invalid capacity replaced native value: %v", bad)
		}
	}
	if agent.currentContextWindowUsage != 0 {
		t.Fatal("capacity fabricated context occupancy")
	}
}
