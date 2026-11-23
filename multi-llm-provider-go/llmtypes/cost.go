package llmtypes

// ComputeUSDCostFromMetadata returns the API-equivalent USD cost given
// a ModelMetadata entry and token counts from a GenerationInfo. Pure
// math, no registry lookup — callers must resolve the model metadata
// themselves (typically via their adapter's GetModelMetadata).
//
// Returns 0 when either argument is nil, when no tokens were recorded,
// or when the metadata has zero rates — cost is best-effort and a
// missing answer should not break the response path.
//
// For subscription-based CLIs (Cursor, Codex Pro) the result is a
// SHADOW cost: what the same workload would cost via the underlying
// per-token API, NOT what the user is billed for the flat plan.
func ComputeUSDCostFromMetadata(meta *ModelMetadata, gi *GenerationInfo) float64 {
	if meta == nil || gi == nil {
		return 0
	}
	prompt := firstNonZeroIntPtr(gi.PromptTokens, gi.InputTokens, gi.PromptTokensCap, gi.InputTokensCap)
	completion := firstNonZeroIntPtr(gi.CompletionTokens, gi.OutputTokens, gi.CompletionTokensCap, gi.OutputTokensCap)
	cached := firstNonZeroIntPtr(gi.CachedContentTokens)
	cacheWrite := generationInfoAdditionalInt(gi.Additional, "cache_creation_input_tokens", "CacheCreationInputTokens")
	if prompt == 0 && completion == 0 && cached == 0 && cacheWrite == 0 {
		return 0
	}

	// Prompt token conventions differ across providers. API usage objects
	// traditionally report prompt as the complete input footprint, including
	// cache buckets, so preserve that as the compatibility default. Coding
	// adapters that normalize prompt to fresh-only input explicitly set this
	// marker false; inclusive coding transports set it true for clarity.
	freshPrompt := prompt
	promptIncludesCache := true
	if markedValue, marked := gi.Additional["prompt_tokens_include_cache"].(bool); marked {
		promptIncludesCache = markedValue
	}
	if promptIncludesCache {
		freshPrompt -= cached + cacheWrite
		if freshPrompt < 0 {
			freshPrompt = 0
		}
	}
	inputMultiplier, outputMultiplier := 1.0, 1.0
	if meta.LongContextThresholdTokens > 0 && freshPrompt+cached+cacheWrite > meta.LongContextThresholdTokens {
		if meta.LongContextInputMultiplier > 0 {
			inputMultiplier = meta.LongContextInputMultiplier
		}
		if meta.LongContextOutputMultiplier > 0 {
			outputMultiplier = meta.LongContextOutputMultiplier
		}
	}

	var cost float64
	cost += float64(freshPrompt) * meta.InputCostPer1MTokens * inputMultiplier / 1_000_000
	cost += float64(completion) * meta.OutputCostPer1MTokens * outputMultiplier / 1_000_000
	if cached > 0 {
		rate := meta.CachedInputCostPer1MTokens
		if rate == 0 {
			// Most providers charge ~10% of the input rate on cache reads
			// (Anthropic, OpenAI prompt caching). Fall back to that
			// convention when the registry doesn't carry an explicit
			// cache rate.
			rate = meta.InputCostPer1MTokens * 0.10
		}
		cost += float64(cached) * rate * inputMultiplier / 1_000_000
	}
	if cacheWrite > 0 {
		rate := meta.CachedInputCostWritePer1MTokens
		if rate == 0 {
			rate = meta.InputCostPer1MTokens
		}
		cost += float64(cacheWrite) * rate * inputMultiplier / 1_000_000
	}
	return cost
}

func firstNonZeroIntPtr(ptrs ...*int) int {
	for _, p := range ptrs {
		if p != nil && *p > 0 {
			return *p
		}
	}
	return 0
}

func generationInfoAdditionalInt(additional map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		valueInt := 0
		switch value := additional[key].(type) {
		case int:
			valueInt = value
		case int64:
			valueInt = int(value)
		case float64:
			valueInt = int(value)
		case float32:
			valueInt = int(value)
		}
		if valueInt > 0 {
			return valueInt
		}
	}
	return 0
}
