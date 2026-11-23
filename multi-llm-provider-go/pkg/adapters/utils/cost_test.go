package utils

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestComputeUSDCostFromTokensAgainstRegistry uses real registry rates
// for a single known model to lock in the math.
func TestComputeUSDCostFromTokensAgainstRegistry(t *testing.T) {
	meta := FindModelMetadata("claude-haiku-4-5")
	if meta == nil {
		t.Skip("claude-haiku-4-5 not in registry; skipping cost-math test")
	}
	prompt := 1_000_000
	completion := 1_000_000
	gi := &llmtypes.GenerationInfo{
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
	}
	got := ComputeUSDCostFromTokens("claude-haiku-4-5", gi)
	want := meta.InputCostPer1MTokens + meta.OutputCostPer1MTokens
	if got != want {
		t.Fatalf("cost = %v, want %v (in_rate=%v + out_rate=%v)", got, want, meta.InputCostPer1MTokens, meta.OutputCostPer1MTokens)
	}
}

// TestComputeUSDCostHonorsCacheRate exercises the cache discount
// branch and the 10%-of-input fallback when the registry entry has
// no explicit cached rate.
func TestComputeUSDCostHonorsCacheRate(t *testing.T) {
	meta := FindModelMetadata("claude-haiku-4-5")
	if meta == nil {
		t.Skip("claude-haiku-4-5 not in registry; skipping cache-rate test")
	}
	cached := 1_000_000
	gi := &llmtypes.GenerationInfo{CachedContentTokens: &cached}
	got := ComputeUSDCostFromTokens("claude-haiku-4-5", gi)
	wantRate := meta.CachedInputCostPer1MTokens
	if wantRate == 0 {
		wantRate = meta.InputCostPer1MTokens * 0.10
	}
	if got != wantRate {
		t.Fatalf("cache cost = %v, want %v", got, wantRate)
	}
}

func TestComputeUSDCostReturnsZeroForUnknownModel(t *testing.T) {
	prompt := 100
	gi := &llmtypes.GenerationInfo{PromptTokens: &prompt}
	if got := ComputeUSDCostFromTokens("this-model-does-not-exist", gi); got != 0 {
		t.Fatalf("expected 0 for unknown model; got %v", got)
	}
}

func TestComputeUSDCostReturnsZeroForNilGenInfo(t *testing.T) {
	if got := ComputeUSDCostFromTokens("claude-haiku-4-5", nil); got != 0 {
		t.Fatalf("expected 0 for nil GenerationInfo; got %v", got)
	}
}
