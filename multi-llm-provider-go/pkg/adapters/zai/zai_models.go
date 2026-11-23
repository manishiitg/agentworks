package zai

import (
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	ModelGLM51           = "glm-5.1"
	ModelGLM5            = "glm-5"
	ModelGLM5Turbo       = "glm-5-turbo"
	ModelGLM5VTurbo      = "glm-5v-turbo"
	ModelGLM46V          = "glm-4.6v"
	ModelGLM47           = "glm-4.7"
	ModelGLM47Flash      = "glm-4.7-flash"
	ModelGLM47FlashX     = "glm-4.7-flashx"
	ModelGLM46           = "glm-4.6"
	ModelGLM45           = "glm-4.5"
	ModelGLM45Air        = "glm-4.5-air"
	ModelGLM45X          = "glm-4.5-x"
	ModelGLM45AirX       = "glm-4.5-airx"
	ModelGLM45Flash      = "glm-4.5-flash"
	ModelGLM432B128K     = "glm-4-32b-0414-128k"
	defaultContextWindow = 200000
)

func getZAIModels() map[string]*llmtypes.ModelMetadata {
	return map[string]*llmtypes.ModelMetadata{
		ModelGLM51: {
			ModelID:                    ModelGLM51,
			ModelName:                  "GLM-5.1",
			Provider:                   "z-ai",
			ContextWindow:              200000,
			InputCostPer1MTokens:       1.00,
			OutputCostPer1MTokens:      3.20,
			CachedInputCostPer1MTokens: 0.20,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM5: {
			ModelID:                    ModelGLM5,
			ModelName:                  "GLM-5",
			Provider:                   "z-ai",
			ContextWindow:              200000,
			InputCostPer1MTokens:       1.00,
			OutputCostPer1MTokens:      3.20,
			CachedInputCostPer1MTokens: 0.20,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM5Turbo: {
			ModelID:                    ModelGLM5Turbo,
			ModelName:                  "GLM-5 Turbo",
			Provider:                   "z-ai",
			ContextWindow:              200000,
			InputCostPer1MTokens:       1.20,
			OutputCostPer1MTokens:      4.00,
			CachedInputCostPer1MTokens: 0.24,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM5VTurbo: {
			ModelID:                    ModelGLM5VTurbo,
			ModelName:                  "GLM-5V Turbo",
			Provider:                   "z-ai",
			ContextWindow:              200000,
			InputCostPer1MTokens:       1.20,
			OutputCostPer1MTokens:      4.00,
			CachedInputCostPer1MTokens: 0.24,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM46V: {
			ModelID:                    ModelGLM46V,
			ModelName:                  "GLM-4.6V",
			Provider:                   "z-ai",
			ContextWindow:              200000,
			InputCostPer1MTokens:       0.30,
			OutputCostPer1MTokens:      0.90,
			CachedInputCostPer1MTokens: 0.05,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM47: {
			ModelID:                    ModelGLM47,
			ModelName:                  "GLM-4.7",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.60,
			OutputCostPer1MTokens:      2.20,
			CachedInputCostPer1MTokens: 0.11,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM47Flash: {
			ModelID:                    ModelGLM47Flash,
			ModelName:                  "GLM-4.7 Flash",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.00,
			OutputCostPer1MTokens:      0.00,
			CachedInputCostPer1MTokens: 0.00,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM47FlashX: {
			ModelID:                    ModelGLM47FlashX,
			ModelName:                  "GLM-4.7 FlashX",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.07,
			OutputCostPer1MTokens:      0.40,
			CachedInputCostPer1MTokens: 0.01,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM46: {
			ModelID:                    ModelGLM46,
			ModelName:                  "GLM-4.6",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.60,
			OutputCostPer1MTokens:      2.20,
			CachedInputCostPer1MTokens: 0.11,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM45: {
			ModelID:                    ModelGLM45,
			ModelName:                  "GLM-4.5",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.60,
			OutputCostPer1MTokens:      2.20,
			CachedInputCostPer1MTokens: 0.11,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM45Air: {
			ModelID:                    ModelGLM45Air,
			ModelName:                  "GLM-4.5 Air",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.20,
			OutputCostPer1MTokens:      1.10,
			CachedInputCostPer1MTokens: 0.03,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM45X: {
			ModelID:                    ModelGLM45X,
			ModelName:                  "GLM-4.5 X",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       2.20,
			OutputCostPer1MTokens:      8.90,
			CachedInputCostPer1MTokens: 0.45,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM45AirX: {
			ModelID:                    ModelGLM45AirX,
			ModelName:                  "GLM-4.5 AirX",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       1.10,
			OutputCostPer1MTokens:      4.50,
			CachedInputCostPer1MTokens: 0.22,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM45Flash: {
			ModelID:                    ModelGLM45Flash,
			ModelName:                  "GLM-4.5 Flash",
			Provider:                   "z-ai",
			ContextWindow:              defaultContextWindow,
			InputCostPer1MTokens:       0.00,
			OutputCostPer1MTokens:      0.00,
			CachedInputCostPer1MTokens: 0.00,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
		ModelGLM432B128K: {
			ModelID:                    ModelGLM432B128K,
			ModelName:                  "GLM-4-32B-0414-128K",
			Provider:                   "z-ai",
			ContextWindow:              128000,
			InputCostPer1MTokens:       0.10,
			OutputCostPer1MTokens:      0.10,
			CachedInputCostPer1MTokens: 0.00,
			SupportsToolCalls:          true,
			SupportsJSONMode:           true,
		},
	}
}

func GetAllZAIModels() []*llmtypes.ModelMetadata {
	models := getZAIModels()
	result := make([]*llmtypes.ModelMetadata, 0, len(models))
	for _, m := range models {
		result = append(result, m)
	}
	return result
}

// GetDefaultVisibleZAIModelIDs returns the small curated set surfaced in
// provider defaults and UI catalogs. We keep this intentionally short to avoid
// exposing every historical GLM variant in selectors.
func GetDefaultVisibleZAIModelIDs() []string {
	return []string{
		ModelGLM51,
		ModelGLM47,
	}
}

// GetDefaultVisibleZAIModels returns metadata only for the curated selector set.
// Direct metadata lookup still supports the full model catalog via GetZAIModelMetadata.
func GetDefaultVisibleZAIModels() []*llmtypes.ModelMetadata {
	models := getZAIModels()
	ids := GetDefaultVisibleZAIModelIDs()
	result := make([]*llmtypes.ModelMetadata, 0, len(ids))
	for _, id := range ids {
		if metadata, ok := models[id]; ok {
			result = append(result, metadata)
		}
	}
	return result
}

func GetZAIModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	models := getZAIModels()
	if metadata, ok := models[normalized]; ok {
		result := *metadata
		result.ModelID = modelID
		return &result, nil
	}
	return nil, fmt.Errorf("model metadata not found for Z.AI model: %s", modelID)
}
