package kimi

import (
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	ModelKimiCode    = "kimi-code"
	ModelKimiK26     = "kimi-k2.6"
	ModelKimiK27Code = "kimi-k2.7-code"
)

func GetDefaultVisibleKimiModelIDs() []string {
	return []string{ModelKimiK26, ModelKimiK27Code}
}

func GetAllKimiModels() []*llmtypes.ModelMetadata {
	return []*llmtypes.ModelMetadata{
		{
			Provider:          "kimi",
			ModelID:           ModelKimiK26,
			ModelName:         "Kimi K2.6 Vision",
			ContextWindow:     262144,
			SupportsToolCalls: true,
			SupportsJSONMode:  true,
		},
		{
			Provider:          "kimi",
			ModelID:           ModelKimiK27Code,
			ModelName:         "Kimi K2.7 Code",
			ContextWindow:     262144,
			SupportsToolCalls: true,
			SupportsJSONMode:  true,
		},
	}
}

func GetKimiModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	if modelID == "" {
		modelID = ModelKimiK26
	}
	for _, metadata := range GetAllKimiModels() {
		if metadata.ModelID == modelID {
			result := *metadata
			return &result, nil
		}
	}
	return nil, fmt.Errorf("unknown Kimi model: %s", modelID)
}
