package bedrock

import (
	"fmt"
	"regexp"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
)

// normalizeBedrockModelID normalizes Bedrock model IDs to base Anthropic model names
// Bedrock model IDs have prefixes like "us.anthropic.", "global.anthropic.", or "anthropic."
// and version suffixes like "-v1:0", "-v2:0"
// Examples:
//   - "us.anthropic.claude-sonnet-4-20250514-v1:0" -> "claude-sonnet-4"
//   - "global.anthropic.claude-sonnet-4-5-20250929-v1:0" -> "claude-sonnet-4-5"
//   - "anthropic.claude-3-5-sonnet-20241022-v2:0" -> "claude-3-5-sonnet"
//   - "us.anthropic.claude-3-7-sonnet-20250219-v1:0" -> "claude-3-7-sonnet"
func normalizeBedrockModelID(modelID string) string {
	// Remove region/provider prefixes: "us.anthropic.", "global.anthropic.", "anthropic."
	prefixPattern := regexp.MustCompile(`^(us\.|global\.)?anthropic\.`)
	baseModelID := prefixPattern.ReplaceAllString(modelID, "")

	// Remove version suffixes like "-v1:0", "-v2:0"
	versionPattern := regexp.MustCompile(`-v\d+:\d+$`)
	baseModelID = versionPattern.ReplaceAllString(baseModelID, "")

	// Remove date suffixes (format: -YYYYMMDD)
	datePattern := regexp.MustCompile(`-\d{8}$`)
	baseModelID = datePattern.ReplaceAllString(baseModelID, "")

	return baseModelID
}

// GetBedrockModelMetadata returns model metadata for a given Bedrock model ID
// Bedrock uses Anthropic Claude models, so we normalize the Bedrock model ID
// to the base Anthropic model name and reuse Anthropic metadata
func GetBedrockModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	// Normalize Bedrock model ID to base Anthropic model name
	normalizedID := normalizeBedrockModelID(modelID)

	// Get metadata from Anthropic (Bedrock uses the same models)
	metadata, err := anthropic.GetAnthropicModelMetadata(normalizedID)
	if err != nil {
		return nil, fmt.Errorf("failed to get Bedrock model metadata for %s (normalized from %s): %w", normalizedID, modelID, err)
	}

	// Update provider to "bedrock" to indicate this is accessed via Bedrock
	metadata.Provider = "bedrock"
	// Preserve the original Bedrock model ID
	metadata.ModelID = modelID

	return metadata, nil
}

// GetAllBedrockModels returns a list of common Bedrock models
func GetAllBedrockModels() []*llmtypes.ModelMetadata {
	// List of common Bedrock model IDs
	commonIDs := []string{
		"us.anthropic.claude-3-7-sonnet-20250219-v1:0",
		"us.anthropic.claude-3-5-sonnet-20241022-v2:0",
		"us.anthropic.claude-3-5-haiku-20241022-v1:0",
		"us.anthropic.claude-3-opus-20240229-v1:0",
		"us.anthropic.claude-3-sonnet-20240229-v1:0",
		"us.anthropic.claude-3-haiku-20240307-v1:0",
	}

	var models []*llmtypes.ModelMetadata
	for _, id := range commonIDs {
		if meta, err := GetBedrockModelMetadata(id); err == nil {
			models = append(models, meta)
		}
	}
	return models
}
