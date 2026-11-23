package utils

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"google.golang.org/genai"
)

// ExtractGenerationInfoFromVertexUsage extracts token usage information from Vertex/Gemini UsageMetadata
// and converts it to llmtypes.GenerationInfo. This function preserves all token types including
// ThoughtsTokens for gemini-3-pro models.
//
// Parameters:
//   - usage: Pointer to genai.GenerateContentResponseUsageMetadata from Vertex/Gemini API response
//
// Returns:
//   - Pointer to llmtypes.GenerationInfo with all extracted token information, or nil if usage is nil
func ExtractGenerationInfoFromVertexUsage(usage *genai.GenerateContentResponseUsageMetadata) *llmtypes.GenerationInfo {
	if usage == nil {
		return nil
	}

	// Extract basic token counts
	inputTokens := int(usage.PromptTokenCount)
	outputTokens := int(usage.CandidatesTokenCount)
	var totalTokens int
	if usage.TotalTokenCount > 0 {
		totalTokens = int(usage.TotalTokenCount)
	} else {
		totalTokens = int(usage.PromptTokenCount + usage.CandidatesTokenCount)
	}

	genInfo := &llmtypes.GenerationInfo{
		InputTokens:  &inputTokens,
		OutputTokens: &outputTokens,
		TotalTokens:  &totalTokens,
	}

	// Cache token information
	if usage.CachedContentTokenCount > 0 {
		cachedTokens := int(usage.CachedContentTokenCount)
		genInfo.CachedContentTokens = &cachedTokens

		// Calculate cache discount percentage (0.0 to 1.0)
		if usage.PromptTokenCount > 0 {
			cacheDiscount := float64(usage.CachedContentTokenCount) / float64(usage.PromptTokenCount)
			genInfo.CacheDiscount = &cacheDiscount
		}
	}

	// Additional token counts if available
	if usage.ToolUsePromptTokenCount > 0 {
		toolUseTokens := int(usage.ToolUsePromptTokenCount)
		genInfo.ToolUsePromptTokens = &toolUseTokens
	}

	// Extract ThoughtsTokens for gemini-3-pro and similar models
	if usage.ThoughtsTokenCount > 0 {
		thoughtsTokens := int(usage.ThoughtsTokenCount)
		genInfo.ThoughtsTokens = &thoughtsTokens
	}

	return genInfo
}
