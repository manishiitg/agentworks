package orchestrator

import (
	"testing"

	"github.com/manishiitg/mcpagent/events"
)

// TestEffectiveModelIDFromTokenEventPicksRightKey locks in the
// priority order of the keys we read from GenerationInfo. The CLI
// adapters emit one or more of these per turn; the helper must pick
// the most-specific one available.
func TestEffectiveModelIDFromTokenEventPicksRightKey(t *testing.T) {
	cases := []struct {
		name string
		gi   map[string]interface{}
		want string
	}{
		{
			name: "cost_model_id wins (canonical key)",
			gi: map[string]interface{}{
				"cost_model_id":     "gpt-5.4",
				"claude_code_model": "claude-opus-4-7",
				"cursor_model":      "composer-2.5",
			},
			want: "gpt-5.4",
		},
		{
			name: "claude_code_model when cost_model_id missing",
			gi: map[string]interface{}{
				"claude_code_model": "claude-haiku-4-5",
			},
			want: "claude-haiku-4-5",
		},
		{
			name: "codex_effective_model",
			gi: map[string]interface{}{
				"codex_effective_model": "gpt-5.4",
			},
			want: "gpt-5.4",
		},
		{
			name: "gemini_effective_model (tmux)",
			gi: map[string]interface{}{
				"gemini_effective_model": "gemini-3.1-flash-lite",
			},
			want: "gemini-3.1-flash-lite",
		},
		{
			name: "gemini_model (structured)",
			gi: map[string]interface{}{
				"gemini_model": "gemini-3-flash-preview",
			},
			want: "gemini-3-flash-preview",
		},
		{
			name: "cursor_model",
			gi: map[string]interface{}{
				"cursor_model": "composer-2.5",
			},
			want: "composer-2.5",
		},
		{
			name: "empty when no known key present",
			gi: map[string]interface{}{
				"unrelated_field": "x",
			},
			want: "",
		},
		{
			name: "empty when GenerationInfo nil",
			gi:   nil,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := &events.TokenUsageEvent{GenerationInfo: tc.gi}
			if got := effectiveModelIDFromTokenEvent(ev); got != tc.want {
				t.Fatalf("effectiveModelIDFromTokenEvent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePricingProviderAndModelUsesPiDefault(t *testing.T) {
	gotProvider, gotModel := resolvePricingProviderAndModel("pi-cli", "")
	if gotProvider != "pi-cli" || gotModel != "google/gemini-3.8-flash" {
		t.Fatalf("resolvePricingProviderAndModel(pi-cli, empty) = (%q, %q), want (pi-cli, google/gemini-3.8-flash)", gotProvider, gotModel)
	}

	gotProvider, gotModel = resolvePricingProviderAndModel("pi-cli", "google/gemini-2.5-flash")
	if gotProvider != "pi-cli" || gotModel != "google/gemini-2.5-flash" {
		t.Fatalf("resolvePricingProviderAndModel(pi-cli, explicit) = (%q, %q)", gotProvider, gotModel)
	}
}

func TestResolvePricingProviderAndModelUsesClaudeCodeAliases(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{model: "", want: "claude-opus-5"},
		{model: "auto", want: "claude-opus-5"},
		{model: "claude-code", want: "claude-opus-5"},
		{model: "opus", want: "claude-opus-5"},
		{model: "Opus 5", want: "claude-opus-5"},
		{model: "Opus 4.8", want: "claude-opus-4-8"},
		{model: "claude-4.8-opus", want: "claude-opus-4-8"},
		{model: "claude-opus-4-7", want: "claude-opus-4-7"},
		{model: "Claude Sonnet 4.6", want: "claude-sonnet-4-6"},
		{model: "sonnet", want: "claude-sonnet-5-5"},
		{model: "fable", want: "claude-fable-5-1"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			gotProvider, gotModel := resolvePricingProviderAndModel("claude-code", tc.model)
			if gotProvider != "claude-code" || gotModel != tc.want {
				t.Fatalf("resolvePricingProviderAndModel(claude-code, %q) = (%q, %q), want (claude-code, %q)", tc.model, gotProvider, gotModel, tc.want)
			}
		})
	}
}

func TestResolvePricingProviderAndModelUsesAnthropicAliases(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{model: "opus", want: "claude-opus-4-8"},
		{model: "Claude 4.8 Opus", want: "claude-opus-4-8"},
		{model: "claude-opus-4-6", want: "claude-opus-4-6"},
		{model: "Claude Sonnet 4.6", want: "claude-sonnet-4-6"},
		{model: "sonnet", want: "claude-sonnet-4-6"},
		{model: "haiku", want: "claude-haiku-4-5"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			gotProvider, gotModel := resolvePricingProviderAndModel("anthropic", tc.model)
			if gotProvider != "anthropic" || gotModel != tc.want {
				t.Fatalf("resolvePricingProviderAndModel(anthropic, %q) = (%q, %q), want (anthropic, %q)", tc.model, gotProvider, gotModel, tc.want)
			}
		})
	}
}

func TestPricingMetadataCoversCodingAgentFrontierAliases(t *testing.T) {
	codexMeta, err := getModelMetadata("codex-cli", "gpt-5.5 xhigh")
	if err != nil {
		t.Fatalf("getModelMetadata(codex-cli/gpt-5.5 xhigh): %v", err)
	}
	if codexMeta.InputCostPer1MTokens != 5.00 || codexMeta.OutputCostPer1MTokens != 30.00 || codexMeta.CachedInputCostPer1MTokens != 0.50 {
		t.Fatalf("codex GPT-5.5 pricing = in %.2f cached %.2f out %.2f, want 5.00/0.50/30.00",
			codexMeta.InputCostPer1MTokens, codexMeta.CachedInputCostPer1MTokens, codexMeta.OutputCostPer1MTokens)
	}

	opusMeta, err := getModelMetadata("claude-code", "opus")
	if err != nil {
		t.Fatalf("getModelMetadata(claude-code/opus): %v", err)
	}
	if opusMeta.ModelID != "claude-opus-5" || opusMeta.InputCostPer1MTokens != 5.00 || opusMeta.OutputCostPer1MTokens != 25.00 {
		t.Fatalf("opus metadata = id %q in %.2f out %.2f, want claude-opus-5 5.00/25.00",
			opusMeta.ModelID, opusMeta.InputCostPer1MTokens, opusMeta.OutputCostPer1MTokens)
	}

	fableMeta, err := getModelMetadata("claude-code", "fable")
	if err != nil {
		t.Fatalf("getModelMetadata(claude-code/fable): %v", err)
	}
	if fableMeta.InputCostPer1MTokens <= 0 || fableMeta.OutputCostPer1MTokens <= 0 {
		t.Fatalf("fable pricing missing: in %.2f out %.2f", fableMeta.InputCostPer1MTokens, fableMeta.OutputCostPer1MTokens)
	}
}
