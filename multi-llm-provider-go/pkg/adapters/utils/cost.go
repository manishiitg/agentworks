package utils

import (
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// modelMetadataIndex is a one-shot cache that maps model_id -> metadata
// across every adapter the registry knows about. Built lazily on first
// call; cheap enough that we don't bother invalidating since the
// registry itself is static at process start.
var (
	modelMetadataIdx     map[string]*llmtypes.ModelMetadata
	modelMetadataIdxOnce sync.Once
)

func buildModelMetadataIndex() {
	idx := make(map[string]*llmtypes.ModelMetadata)
	for _, m := range GetAllModelMetadata() {
		if m == nil || m.ModelID == "" {
			continue
		}
		// First entry wins; registry order is deterministic so this is
		// stable across calls.
		if _, exists := idx[m.ModelID]; !exists {
			idx[m.ModelID] = m
		}
	}
	modelMetadataIdx = idx
}

// FindModelMetadata returns the registry entry for a model ID (or nil
// if unknown). Lookups are O(1) after the first call.
func FindModelMetadata(modelID string) *llmtypes.ModelMetadata {
	if modelID == "" {
		return nil
	}
	modelMetadataIdxOnce.Do(buildModelMetadataIndex)
	return modelMetadataIdx[modelID]
}

// ComputeUSDCostFromTokens returns the API-equivalent USD cost given a
// model ID and token counts pulled from a GenerationInfo. Uses the
// model registry's per-1M-token rates.
//
// This is the registry-aware convenience wrapper. Callers inside an
// adapter package should prefer their adapter's own
// GetModelMetadata + llmtypes.ComputeUSDCostFromMetadata to avoid the
// import cycle through the registry.
//
// Returns 0 (not an error) when the model is unknown, when no tokens
// were recorded, or when the registry entry has no pricing — cost is
// best-effort and a missing answer should not break the response path.
func ComputeUSDCostFromTokens(modelID string, gi *llmtypes.GenerationInfo) float64 {
	if gi == nil || modelID == "" {
		return 0
	}
	return llmtypes.ComputeUSDCostFromMetadata(FindModelMetadata(modelID), gi)
}
