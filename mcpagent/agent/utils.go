// utils.go
//
// This file contains shared helper functions for the Agent, including tool choice conversion and usage metrics extraction.
//
// Exported:
//   - BuildSystemPrompt
//   - ConvertToolChoice
//   - extractUsageMetrics
//   - castToInt

package mcpagent

import (
	"os"
	"strconv"

	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetDefaultMaxTurns returns the configured default turn limit.
// Checks MAX_TURNS environment variable, falls back to 500 if not set or invalid.
func GetDefaultMaxTurns() int {
	// Check MAX_TURNS environment variable
	if envVal := os.Getenv("MAX_TURNS"); envVal != "" {
		if maxTurns, err := strconv.Atoi(envVal); err == nil && maxTurns > 0 {
			return maxTurns
		}
	}
	// Default to 500 if env var not set or invalid
	return 500
}

// ConvertToolChoice converts a tool choice string to *llmtypes.ToolChoice.
// Returns nil if choice is empty, otherwise returns a properly constructed ToolChoice.
func ConvertToolChoice(choice string) *llmtypes.ToolChoice {
	if choice == "" {
		return nil
	}

	switch choice {
	case "auto", "none", "required":
		return &llmtypes.ToolChoice{Type: choice}
	default:
		// Specific tool name - create function-specific tool choice
		return &llmtypes.ToolChoice{
			Type:     "function",
			Function: &llmtypes.FunctionName{Name: choice},
		}
	}
}

// extractUsageMetrics extracts token usage metrics from an LLM response.
// It uses the unified llmtypes.ExtractUsageFromGenerationInfo for comprehensive extraction.
// This ensures all token types (input, output, cache, thoughts, reasoning) are properly extracted.
func extractUsageMetrics(resp *llmtypes.ContentResponse) observability.UsageMetrics {
	if resp == nil || len(resp.Choices) == 0 {
		return observability.UsageMetrics{}
	}

	m := observability.UsageMetrics{Unit: "TOKENS"}

	// Priority 1: Use unified Usage field (if available) - already extracted by adapters
	if resp.Usage != nil {
		m.InputTokens = resp.Usage.InputTokens
		m.OutputTokens = resp.Usage.OutputTokens
		m.TotalTokens = resp.Usage.TotalTokens

		// If we got actual token usage from unified field, return it
		if m.InputTokens > 0 || m.OutputTokens > 0 || m.TotalTokens > 0 {
			// Ensure total is calculated if not provided
			if m.TotalTokens == 0 {
				m.TotalTokens = m.InputTokens + m.OutputTokens
			}
			return m
		}
	}

	// Priority 2: Fall back to GenerationInfo using unified extraction
	if resp.Choices[0].GenerationInfo != nil {
		// Use unified extraction function from multi-llm-provider-go
		usage := llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
		if usage != nil {
			m.InputTokens = usage.InputTokens
			m.OutputTokens = usage.OutputTokens
			m.TotalTokens = usage.TotalTokens

			// Ensure total is calculated if not provided
			if m.TotalTokens == 0 && m.InputTokens > 0 && m.OutputTokens > 0 {
				m.TotalTokens = m.InputTokens + m.OutputTokens
			}
			return m
		}
	}

	// No actual token usage available - return zeros
	// Character-based estimation has been removed - we only use actual values from LLM responses
	return m
}

// extractUsageMetricsWithMessages extracts token usage from LLM response.
// Returns actual token values only - does not estimate.
// If no actual values are available, returns 0 (caller should handle this appropriately).
func extractUsageMetricsWithMessages(resp *llmtypes.ContentResponse, messages []llmtypes.MessageContent) observability.UsageMetrics {
	// Get base usage metrics (extracts actual values from resp)
	usage := extractUsageMetrics(resp)

	// Only return actual values - do not estimate
	// If InputTokens is 0, it means LLM didn't return actual values
	// Caller should handle this case (e.g., don't accumulate estimated values)
	// Recalculate total if we have both input and output
	if usage.TotalTokens == 0 && usage.InputTokens > 0 && usage.OutputTokens > 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return usage
}

// extractAllTokenTypes extracts all token types (cache, thoughts, reasoning) from ContentResponse.
// Uses unified llmtypes.ExtractUsageFromGenerationInfo for comprehensive extraction.
// Returns: (cacheTokens, thoughtsTokens, reasoningTokens)
func extractAllTokenTypes(resp *llmtypes.ContentResponse) (cacheTokens, thoughtsTokens, reasoningTokens int) {
	if resp == nil {
		return 0, 0, 0
	}

	// Priority 1: Use unified Usage field (if available)
	if resp.Usage != nil {
		if resp.Usage.CacheTokens != nil {
			cacheTokens = *resp.Usage.CacheTokens
		}
		if resp.Usage.ThoughtsTokens != nil {
			thoughtsTokens = *resp.Usage.ThoughtsTokens
		}
		if resp.Usage.ReasoningTokens != nil {
			reasoningTokens = *resp.Usage.ReasoningTokens
		}
		// If we got values from Usage, return them
		if cacheTokens > 0 || thoughtsTokens > 0 || reasoningTokens > 0 {
			return cacheTokens, thoughtsTokens, reasoningTokens
		}
	}

	// Priority 2: Fall back to GenerationInfo using unified extraction
	if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		usage := llmtypes.ExtractUsageFromGenerationInfo(resp.Choices[0].GenerationInfo)
		if usage != nil {
			if usage.CacheTokens != nil {
				cacheTokens = *usage.CacheTokens
			}
			if usage.ThoughtsTokens != nil {
				thoughtsTokens = *usage.ThoughtsTokens
			}
			if usage.ReasoningTokens != nil {
				reasoningTokens = *usage.ReasoningTokens
			}
		}
	}

	return cacheTokens, thoughtsTokens, reasoningTokens
}

// estimateInputTokens has been removed - we now use only actual token values from LLM responses.
// For token counting needs, use CountTokensForModel() (tiktoken-based) which is available in tool_output_handler.go
