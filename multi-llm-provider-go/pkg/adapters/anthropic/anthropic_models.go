package anthropic

import (
	"fmt"
	"regexp"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/modelspecs"
)

// Anthropic model name constants
const (
	ModelClaudeOpus55 = "claude-opus-5-5"

	// Claude 4.8 Series
	ModelClaudeOpus48 = "claude-opus-4-8"

	// Claude 4.6 Series
	ModelClaudeOpus46   = "claude-opus-4-6"
	ModelClaudeSonnet46 = "claude-sonnet-4-6"

	// Claude 4.5 Series
	ModelClaudeSonnet45 = "claude-sonnet-4-5"
	ModelClaudeHaiku45  = "claude-haiku-4-5"
	ModelClaudeOpus45   = "claude-opus-4-5"
)

// normalizeToBaseModel normalizes Anthropic model IDs to base model names
// Anthropic models use date suffixes like "-20241022" or "-20250219"
// Examples:
//   - "claude-3-5-sonnet-20241022" -> "claude-3-5-sonnet"
//   - "claude-sonnet-4-5-20250929" -> "claude-sonnet-4-5"
//   - "claude-3-7-sonnet-20250219" -> "claude-3-7-sonnet"
func normalizeToBaseModel(modelID string) string {
	// Remove date suffixes (format: -YYYYMMDD)
	datePattern := regexp.MustCompile(`-\d{8}$`)
	baseModelID := datePattern.ReplaceAllString(modelID, "")

	// Also handle version suffixes like "-v1:0" (used in Bedrock)
	versionPattern := regexp.MustCompile(`-v\d+:\d+$`)
	baseModelID = versionPattern.ReplaceAllString(baseModelID, "")

	return baseModelID
}

// anthropicModels are the models the Anthropic API serves here; their facts
// (name, context window, prices) come from the shared table in modelspecs.
var anthropicModels = []string{
	"claude-fable-5-2", "claude-fable-5-1", ModelClaudeOpus55, "claude-sonnet-5-5", "claude-haiku-5-5",
	ModelClaudeOpus48, ModelClaudeOpus46, ModelClaudeSonnet46, ModelClaudeSonnet45, ModelClaudeHaiku45, ModelClaudeOpus45,
}

// getAnthropicModels returns the map of Anthropic model metadata
func getAnthropicModels() map[string]*llmtypes.ModelMetadata {
	models := make(map[string]*llmtypes.ModelMetadata, len(anthropicModels))
	for _, id := range anthropicModels {
		if m, ok := modelspecs.For(id, "anthropic"); ok {
			models[id] = m
		}
	}
	return models
}

// GetAllAnthropicModels returns a list of all available Anthropic models
func GetAllAnthropicModels() []*llmtypes.ModelMetadata {
	models := getAnthropicModels()
	result := make([]*llmtypes.ModelMetadata, 0, len(models))
	for _, m := range models {
		result = append(result, m)
	}
	return result
}

// GetAnthropicModelMetadata returns metadata for Anthropic models including token limits and pricing
// Pricing includes input, output, cached input read tokens, and cached input write tokens
// Cache read cost varies by model (Opus 5.5 is 5% of base input).
// Cache write cost: 125% of base input cost (25% more than base)
// Accepts both base model names (e.g., "claude-3-5-sonnet") and versioned names (e.g., "claude-3-5-sonnet-20241022")
// Versioned names are normalized to base model names
func GetAnthropicModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	// Normalize model ID to base model name (remove date/version suffixes)
	baseModelID := normalizeToBaseModel(modelID)

	// Anthropic model metadata with comprehensive pricing
	// Only base model names are stored - versioned names are normalized to base names
	models := getAnthropicModels()

	// Look up the model by base model ID
	metadata, exists := models[baseModelID]
	if !exists {
		return nil, fmt.Errorf("unknown Anthropic model: %s (normalized from: %s)", baseModelID, modelID)
	}

	// Return a copy with the original model ID preserved (in case caller needs it)
	result := *metadata
	result.ModelID = modelID // Preserve original model ID if versioned
	return &result, nil
}
