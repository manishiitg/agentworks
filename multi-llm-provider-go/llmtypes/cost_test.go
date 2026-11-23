package llmtypes

import (
	"math"
	"testing"
)

func TestComputeUSDCostFromMetadataWithInclusiveCacheTokens(t *testing.T) {
	prompt, completion, cacheRead := 1_273_577, 34_200, 1_273_527
	metadata := &ModelMetadata{
		InputCostPer1MTokens:            5,
		OutputCostPer1MTokens:           25,
		CachedInputCostPer1MTokens:      0.5,
		CachedInputCostWritePer1MTokens: 6.25,
	}
	info := &GenerationInfo{
		PromptTokens:        &prompt,
		CompletionTokens:    &completion,
		CachedContentTokens: &cacheRead,
		Additional: map[string]interface{}{
			"prompt_tokens_include_cache": true,
		},
	}

	want := 0.00025 + 0.855 + 0.6367635
	if got := ComputeUSDCostFromMetadata(metadata, info); math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %.9f, want %.9f", got, want)
	}
}

func TestComputeUSDCostFromMetadataDefaultsAPIUsageToInclusiveCache(t *testing.T) {
	prompt, completion, cacheRead := 1_050, 100, 1_000
	metadata := &ModelMetadata{
		InputCostPer1MTokens:       5,
		OutputCostPer1MTokens:      25,
		CachedInputCostPer1MTokens: 0.5,
	}
	info := &GenerationInfo{
		PromptTokens:        &prompt,
		CompletionTokens:    &completion,
		CachedContentTokens: &cacheRead,
	}

	want := float64(50)*5/1_000_000 + float64(completion)*25/1_000_000 + float64(cacheRead)*0.5/1_000_000
	if got := ComputeUSDCostFromMetadata(metadata, info); math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %.9f, want %.9f", got, want)
	}
}

func TestComputeUSDCostFromMetadataKeepsProviderFreshInputSeparate(t *testing.T) {
	for _, codingAgent := range []string{"codex-cli", "cursor-cli", "pi-cli"} {
		t.Run(codingAgent, func(t *testing.T) {
			prompt, completion, cacheRead := 50, 100, 1_000
			metadata := &ModelMetadata{
				InputCostPer1MTokens:       5,
				OutputCostPer1MTokens:      25,
				CachedInputCostPer1MTokens: 0.5,
			}
			info := &GenerationInfo{
				PromptTokens:        &prompt,
				CompletionTokens:    &completion,
				CachedContentTokens: &cacheRead,
				Additional: map[string]interface{}{
					"prompt_tokens_include_cache": false,
				},
			}

			want := float64(prompt)*5/1_000_000 + float64(completion)*25/1_000_000 + float64(cacheRead)*0.5/1_000_000
			if got := ComputeUSDCostFromMetadata(metadata, info); math.Abs(got-want) > 1e-12 {
				t.Fatalf("cost = %.9f, want %.9f", got, want)
			}
		})
	}
}

func TestComputeUSDCostFromMetadataPricesCacheWriteOnce(t *testing.T) {
	prompt, completion, cacheRead, cacheWrite := 1_150, 100, 1_000, 100
	metadata := &ModelMetadata{
		InputCostPer1MTokens:            5,
		OutputCostPer1MTokens:           25,
		CachedInputCostPer1MTokens:      0.5,
		CachedInputCostWritePer1MTokens: 6.25,
	}
	info := &GenerationInfo{
		PromptTokens:        &prompt,
		CompletionTokens:    &completion,
		CachedContentTokens: &cacheRead,
		Additional: map[string]interface{}{
			"cache_creation_input_tokens": cacheWrite,
			"prompt_tokens_include_cache": true,
		},
	}

	want := float64(50)*5/1_000_000 + float64(completion)*25/1_000_000 +
		float64(cacheRead)*0.5/1_000_000 + float64(cacheWrite)*6.25/1_000_000
	if got := ComputeUSDCostFromMetadata(metadata, info); math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %.9f, want %.9f", got, want)
	}
}

func TestComputeUSDCostFromMetadataLongContextRates(t *testing.T) {
	metadata := &ModelMetadata{
		InputCostPer1MTokens:            2,
		OutputCostPer1MTokens:           10,
		CachedInputCostPer1MTokens:      0.2,
		CachedInputCostWritePer1MTokens: 2.5,
		LongContextThresholdTokens:      272000,
		LongContextInputMultiplier:      2,
		LongContextOutputMultiplier:     1.5,
	}
	completion, cacheRead, cacheWrite := 20000, 100000, 10000
	for _, tt := range []struct {
		prompt int
		want   float64
	}{
		{272000, 0.569},
		{300000, 1.15},
	} {
		info := &GenerationInfo{
			PromptTokens:        &tt.prompt,
			CompletionTokens:    &completion,
			CachedContentTokens: &cacheRead,
			Additional: map[string]interface{}{
				"cache_creation_input_tokens": cacheWrite,
				"prompt_tokens_include_cache": true,
			},
		}
		if got := ComputeUSDCostFromMetadata(metadata, info); math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("prompt %d: cost = %.9f, want %.9f", tt.prompt, got, tt.want)
		}
	}
}

func TestExtractUsageFromGenerationInfoDeduplicatesCacheAliases(t *testing.T) {
	cacheRead, cacheWrite := 1_000, 100
	for _, test := range []struct {
		name       string
		additional map[string]interface{}
	}{
		{
			name: "claude typed and raw read aliases",
			additional: map[string]interface{}{
				"cache_read_input_tokens": cacheRead,
				"CacheReadInputTokens":    cacheRead,
			},
		},
		{
			name: "codex typed and raw read alias",
			additional: map[string]interface{}{
				"cache_read_input_tokens": cacheRead,
			},
		},
		{
			name: "cursor and pi read plus write aliases",
			additional: map[string]interface{}{
				"cache_read_input_tokens":     cacheRead,
				"CacheReadInputTokens":        cacheRead,
				"cache_creation_input_tokens": cacheWrite,
				"CacheCreationInputTokens":    cacheWrite,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompt, completion := 1_150, 100
			info := &GenerationInfo{
				PromptTokens:        &prompt,
				CompletionTokens:    &completion,
				CachedContentTokens: &cacheRead,
				Additional:          test.additional,
			}

			usage := ExtractUsageFromGenerationInfo(info)
			if usage == nil || usage.CacheTokens == nil {
				t.Fatal("expected cache usage")
			}
			want := cacheRead
			if _, hasWrite := test.additional["cache_creation_input_tokens"]; hasWrite {
				want += cacheWrite
			}
			if got := *usage.CacheTokens; got != want {
				t.Fatalf("cache tokens = %d, want %d", got, want)
			}
		})
	}
}
