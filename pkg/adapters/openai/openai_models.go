package openai

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/modelspecs"
)

// OpenAI model name constants
const (
	ModelGPT6Sol  = "gpt-6-sol"
	ModelGPT6Luna = "gpt-6-luna"

	// GPT-5.2 family
	ModelGPT52 = "gpt-5.2"
	// GPT-5.2 specialized variants
	ModelGPT52Instant  = "gpt-5.2-instant"
	ModelGPT52Thinking = "gpt-5.2-thinking"
	// GPT-5.5 family
	ModelGPT55 = "gpt-5.5"
	// GPT-5.4 family
	ModelGPT54     = "gpt-5.4"
	ModelGPT54Mini = "gpt-5.4-mini"
	// GPT-4.1 family
	ModelGPT41     = "gpt-4.1"
	ModelGPT41Mini = "gpt-4.1-mini"
	ModelGPT41Nano = "gpt-4.1-nano"
)

// openAIModels are the models the OpenAI API serves here; their facts
// (name, context window, prices) come from the shared table in modelspecs.
var openAIModels = []string{ModelGPT6Sol, ModelGPT6Luna, ModelGPT52, ModelGPT52Instant, ModelGPT52Thinking, ModelGPT55, ModelGPT54, ModelGPT54Mini, ModelGPT41, ModelGPT41Mini, ModelGPT41Nano}

// getOpenAIModels returns the map of OpenAI model metadata
func getOpenAIModels() map[string]*llmtypes.ModelMetadata {
	models := make(map[string]*llmtypes.ModelMetadata, len(openAIModels))
	for _, id := range openAIModels {
		if m, ok := modelspecs.For(id, "openai"); ok {
			models[id] = m
		}
	}
	return models
}

// GetAllOpenAIModels returns a list of all available OpenAI models
func GetAllOpenAIModels() []*llmtypes.ModelMetadata {
	models := getOpenAIModels()
	result := make([]*llmtypes.ModelMetadata, 0, len(models))
	for _, m := range models {
		result = append(result, m)
	}
	return result
}

// GetOpenAIModelMetadata returns metadata for OpenAI models including token limits and pricing
// Pricing includes input, output, reasoning (for reasoning models), and cached input tokens
// Accepts both base model names (e.g., "gpt-4o") and versioned names (e.g., "gpt-4o-2024-11-20")
// Versioned names are normalized to base model names
func GetOpenAIModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	// Normalize model ID to base model name (remove version suffixes)
	baseModelID := normalizeToBaseModel(modelID)

	// OpenAI model metadata with comprehensive pricing
	// Only base model names are stored - versioned names are normalized to base names
	models := getOpenAIModels()

	// Look up by base model name (versioned names are normalized)
	if metadata, exists := models[baseModelID]; exists {
		// Create a copy with the original modelID (preserve versioned name if provided)
		result := *metadata
		result.ModelID = modelID // Keep original modelID (may be versioned)
		return &result, nil
	}

	return nil, fmt.Errorf("model metadata not found for OpenAI model: %s (normalized: %s)", modelID, baseModelID)
}

// normalizeToBaseModel normalizes model IDs to base model names by removing version suffixes
// Examples:
//   - "gpt-4o-2024-08-06" -> "gpt-4o"
//   - "gpt-4o-2024-11-20" -> "gpt-4o"
//   - "gpt-4-turbo-preview" -> "gpt-4-turbo"
//   - "gpt-4o" -> "gpt-4o" (no change)
func normalizeToBaseModel(modelID string) string {
	normalized := strings.ToLower(strings.TrimSpace(modelID))

	// Remove version date patterns (YYYY-MM-DD) using regex to match trailing date suffix
	// e.g., "gpt-4o-2024-08-06" -> "gpt-4o"
	// The regex matches "-YYYY-MM-DD" at the end of the string
	datePattern := regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)
	normalized = datePattern.ReplaceAllString(normalized, "")

	// Remove common suffixes like "-preview"
	normalized = strings.TrimSuffix(normalized, "-preview")

	return normalized
}
