package claudecode

import (
	"math"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeCodeCachePricedModels lists every model ID the two claude-code
// adapters price per token. Cache reads are the largest token bucket in a
// resumed coding session (tens of thousands of tokens per turn against a
// handful of fresh prompt tokens), so a model that prices input and output
// but leaves CachedInputCostPer1MTokens at zero reports a cost that is not
// merely imprecise -- it can understate the real spend by more than the
// entire reported amount, and it always errs low.
var claudeCodeCachePricedModels = []string{
	"claude-opus-5",
	"claude-haiku-5-5",
}

// These older Anthropic models price a cache read at 10% of the base input
// rate. Opus 5.5 and Sonnet 5.5 have different rates and are checked
// separately below.
func TestClaudeCodeModelsPriceCacheReads(t *testing.T) {
	interactive := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{})

	for _, modelID := range claudeCodeCachePricedModels {
		t.Run(modelID, func(t *testing.T) {
			interactiveMeta, err := interactive.GetModelMetadata(modelID)
			if err != nil {
				t.Fatalf("interactive metadata error: %v", err)
			}
			compat := NewClaudeCodeAdapter("", modelID, &MockLogger{})
			compatMeta, err := compat.GetModelMetadata(modelID)
			if err != nil {
				t.Fatalf("compat metadata error: %v", err)
			}

			for adapterName, meta := range map[string]struct {
				input, cached float64
			}{
				"interactive": {interactiveMeta.InputCostPer1MTokens, interactiveMeta.CachedInputCostPer1MTokens},
				"compat":      {compatMeta.InputCostPer1MTokens, compatMeta.CachedInputCostPer1MTokens},
			} {
				if meta.input <= 0 {
					t.Fatalf("%s: %s has no input pricing; update this test's model list", adapterName, modelID)
				}
				// mcpagent skips cache cost entirely on a zero rate
				// (agent.go: cacheTokens > 0 && CachedInputCostPer1MTokens > 0),
				// so zero here silently drops the charge rather than
				// approximating it.
				if meta.cached <= 0 {
					t.Errorf("%s: %s prices input at %v but cache reads at %v; cache tokens would be billed as free",
						adapterName, modelID, meta.input, meta.cached)
					continue
				}
				if want := meta.input / 10; math.Abs(meta.cached-want) > 1e-9 {
					t.Errorf("%s: %s cache read = %v, want %v (10%% of the %v input rate)",
						adapterName, modelID, meta.cached, want, meta.input)
				}
			}
		})
	}
}

func TestClaudeCodeOpus55Pricing(t *testing.T) {
	for name, adapter := range map[string]func() (float64, float64, float64, float64, error){
		"interactive": func() (float64, float64, float64, float64, error) {
			meta, err := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{}).GetModelMetadata("claude-opus-5-5")
			if err != nil {
				return 0, 0, 0, 0, err
			}
			return meta.InputCostPer1MTokens, meta.OutputCostPer1MTokens, meta.CachedInputCostPer1MTokens, meta.CachedInputCostWritePer1MTokens, nil
		},
		"compat": func() (float64, float64, float64, float64, error) {
			meta, err := NewClaudeCodeAdapter("", "claude-opus-5-5", &MockLogger{}).GetModelMetadata("claude-opus-5-5")
			if err != nil {
				return 0, 0, 0, 0, err
			}
			return meta.InputCostPer1MTokens, meta.OutputCostPer1MTokens, meta.CachedInputCostPer1MTokens, meta.CachedInputCostWritePer1MTokens, nil
		},
	} {
		input, output, cacheRead, cacheWrite, err := adapter()
		if err != nil {
			t.Fatalf("%s metadata: %v", name, err)
		}
		if input != 4 || output != 20 || cacheRead != 0.2 || cacheWrite != 5 {
			t.Errorf("%s Opus 5.5 pricing = (%v, %v, %v, %v), want (4, 20, 0.2, 5)", name, input, output, cacheRead, cacheWrite)
		}
	}
}

// Sonnet 5.5 prices cache hits at 0.05x input and 5-minute cache writes at
// 1.25x (Anthropic pricing page, 2026-11-13). It was priced at 10% before,
// carried over from Sonnet 5, overstating cache reads twofold.
func TestClaudeCodeSonnet55Pricing(t *testing.T) {
	for name, adapter := range map[string]func() (float64, float64, float64, float64, error){
		"interactive": func() (float64, float64, float64, float64, error) {
			meta, err := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{}).GetModelMetadata("claude-sonnet-5-5")
			if err != nil {
				return 0, 0, 0, 0, err
			}
			return meta.InputCostPer1MTokens, meta.OutputCostPer1MTokens, meta.CachedInputCostPer1MTokens, meta.CachedInputCostWritePer1MTokens, nil
		},
		"compat": func() (float64, float64, float64, float64, error) {
			meta, err := NewClaudeCodeAdapter("", "claude-sonnet-5-5", &MockLogger{}).GetModelMetadata("claude-sonnet-5-5")
			if err != nil {
				return 0, 0, 0, 0, err
			}
			return meta.InputCostPer1MTokens, meta.OutputCostPer1MTokens, meta.CachedInputCostPer1MTokens, meta.CachedInputCostWritePer1MTokens, nil
		},
	} {
		input, output, cacheRead, cacheWrite, err := adapter()
		if err != nil {
			t.Fatalf("%s metadata: %v", name, err)
		}
		if input != 2 || output != 10 || cacheRead != 0.1 || cacheWrite != 2.5 {
			t.Errorf("%s Sonnet 5.5 pricing = (%v, %v, %v, %v), want (2, 10, 0.1, 2.5)", name, input, output, cacheRead, cacheWrite)
		}
	}
}

// Fable 5.2 and 5.1 price cache hits at 0.025x input and 5-minute cache
// writes at 1.25x (Anthropic pricing page, 2026-11-10). Fable 5.1 was priced
// at 10% before, overstating cache reads fourfold.
func TestClaudeCodeFablePricing(t *testing.T) {
	for modelID, want := range map[string][4]float64{
		"claude-fable-5-2": {8, 40, 0.20, 10},
		"claude-fable-5-1": {10, 50, 0.25, 12.50},
	} {
		for name, meta := range map[string]func() (*llmtypes.ModelMetadata, error){
			"interactive": func() (*llmtypes.ModelMetadata, error) {
				return NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{}).GetModelMetadata(modelID)
			},
			"compat": func() (*llmtypes.ModelMetadata, error) {
				return NewClaudeCodeAdapter("", modelID, &MockLogger{}).GetModelMetadata(modelID)
			},
		} {
			m, err := meta()
			if err != nil {
				t.Fatalf("%s %s metadata: %v", name, modelID, err)
			}
			got := [4]float64{m.InputCostPer1MTokens, m.OutputCostPer1MTokens, m.CachedInputCostPer1MTokens, m.CachedInputCostWritePer1MTokens}
			if got != want {
				t.Errorf("%s %s pricing = %v, want %v", name, modelID, got, want)
			}
		}
	}
}

// A retired model keeps working: saved configs run and are priced as its
// replacement (Sonnet 5 -> 5.5; Haiku 4.5 -> 5.5, 2026-10-08).
func TestRetiredClaudeModelsRunAsTheirReplacement(t *testing.T) {
	for retired, want := range map[string]struct {
		id    string
		input float64
	}{
		"claude-sonnet-5":           {"claude-sonnet-5-5", 2.00},
		"claude-haiku-4-5-20251001": {"claude-haiku-5-5", 0.10},
		"claude-haiku-4-5":          {"claude-haiku-5-5", 0.10},
	} {
		if got := NewClaudeCodeInteractiveAdapter(retired, &MockLogger{}).modelID; got != want.id {
			t.Fatalf("%s: interactive adapter model = %q, want %s", retired, got, want.id)
		}
		if got := NewClaudeCodeAdapter("", retired, &MockLogger{}).modelID; got != want.id {
			t.Fatalf("%s: compat adapter model = %q, want %s", retired, got, want.id)
		}
		meta, err := NewClaudeCodeInteractiveAdapter("claude-code", &MockLogger{}).GetModelMetadata(retired)
		if err != nil || meta.ModelID != want.id || meta.InputCostPer1MTokens != want.input {
			t.Fatalf("%s metadata = %+v, %v; want %s pricing", retired, meta, err, want.id)
		}
	}
}
