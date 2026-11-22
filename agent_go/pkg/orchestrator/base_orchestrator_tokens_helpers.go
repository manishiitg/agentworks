package orchestrator

import (
	"strings"

	"github.com/manishiitg/mcpagent/events"
)

// CacheTokens holds separated cache read and write token counts
type CacheTokens struct {
	ReadTokens  int // Tokens read from cache (charged at discount rate)
	WriteTokens int // Tokens written to cache (charged at premium rate, 1.25x)
	Total       int // Total cache tokens (read + write)
}

// extractCacheTokensSeparate extracts cache read and write tokens separately from a TokenUsageEvent
// This is important for accurate pricing: cache reads are discounted, cache writes are premium (1.25x)
// Returns CacheTokens struct with ReadTokens, WriteTokens, and Total
func extractCacheTokensSeparate(tokenEvent *events.TokenUsageEvent) CacheTokens {
	result := CacheTokens{}

	if tokenEvent.GenerationInfo == nil {
		return result
	}

	// Priority 1: Check for cumulative cache tokens (from conversation end event)
	// These are pre-separated cumulative totals
	if cumulativeRead, ok := tokenEvent.GenerationInfo["cumulative_cache_read_tokens"].(int); ok {
		result.ReadTokens = cumulativeRead
	} else if cumulativeRead, ok := tokenEvent.GenerationInfo["cumulative_cache_read_tokens"].(float64); ok {
		result.ReadTokens = int(cumulativeRead)
	}
	if cumulativeWrite, ok := tokenEvent.GenerationInfo["cumulative_cache_write_tokens"].(int); ok {
		result.WriteTokens = cumulativeWrite
	} else if cumulativeWrite, ok := tokenEvent.GenerationInfo["cumulative_cache_write_tokens"].(float64); ok {
		result.WriteTokens = int(cumulativeWrite)
	}

	// If we got cumulative values, calculate total and return
	if result.ReadTokens > 0 || result.WriteTokens > 0 {
		result.Total = result.ReadTokens + result.WriteTokens
		return result
	}

	// Priority 2: Check for legacy cumulative_cache_tokens (backward compatibility)
	// This is the old combined total - we'll treat it as all read tokens (conservative estimate)
	if ct, ok := tokenEvent.GenerationInfo["cumulative_cache_tokens"].(int); ok {
		result.ReadTokens = ct
		result.Total = ct
		return result
	}
	if ct, ok := tokenEvent.GenerationInfo["cumulative_cache_tokens"].(float64); ok {
		result.ReadTokens = int(ct)
		result.Total = int(ct)
		return result
	}

	// Priority 3: Check top-level fields (Additional map fields are copied to top level by ExtractTokenUsageWithCacheInfo)
	// CacheReadInputTokens (Anthropic field) - tokens read from cache (discounted)
	if cacheRead, ok := tokenEvent.GenerationInfo["CacheReadInputTokens"]; ok {
		if cacheReadInt, ok := cacheRead.(int); ok {
			result.ReadTokens += cacheReadInt
		} else if cacheReadFloat, ok := cacheRead.(float64); ok {
			result.ReadTokens += int(cacheReadFloat)
		}
	}

	// CacheCreationInputTokens (Anthropic field) - tokens written to cache (premium, 1.25x)
	if cacheCreate, ok := tokenEvent.GenerationInfo["CacheCreationInputTokens"]; ok {
		if cacheCreateInt, ok := cacheCreate.(int); ok {
			result.WriteTokens += cacheCreateInt
		} else if cacheCreateFloat, ok := cacheCreate.(float64); ok {
			result.WriteTokens += int(cacheCreateFloat)
		}
	}

	// Priority 4: Also check Additional map (in case fields weren't copied to top level)
	if additional, ok := tokenEvent.GenerationInfo["Additional"].(map[string]interface{}); ok {
		// Check CacheReadInputTokens (only if not already found at top level)
		if result.ReadTokens == 0 {
			if cacheRead, ok := additional["CacheReadInputTokens"]; ok {
				if cacheReadInt, ok := cacheRead.(int); ok {
					result.ReadTokens += cacheReadInt
				} else if cacheReadFloat, ok := cacheRead.(float64); ok {
					result.ReadTokens += int(cacheReadFloat)
				}
			}
		}
		// Check CacheCreationInputTokens (only if not already found at top level)
		if result.WriteTokens == 0 {
			if cacheCreate, ok := additional["CacheCreationInputTokens"]; ok {
				if cacheCreateInt, ok := cacheCreate.(int); ok {
					result.WriteTokens += cacheCreateInt
				} else if cacheCreateFloat, ok := cacheCreate.(float64); ok {
					result.WriteTokens += int(cacheCreateFloat)
				}
			}
		}
	}

	// Priority 5: Check for CachedContentTokens (Anthropic field) - treat as read tokens
	if result.ReadTokens == 0 {
		if cachedContent, ok := tokenEvent.GenerationInfo["CachedContentTokens"].(int); ok {
			result.ReadTokens += cachedContent
		} else if cachedContent, ok := tokenEvent.GenerationInfo["CachedContentTokens"].(float64); ok {
			result.ReadTokens += int(cachedContent)
		}
	}

	result.Total = result.ReadTokens + result.WriteTokens
	return result
}

// effectiveModelIDFromTokenEvent returns the model the CLI/provider
// actually served the turn with, as recorded in the GenerationInfo
// Additional blob by the multi-llm-provider-go CLI adapters. Falls
// back to "" when none of the known keys are present, in which case
// the caller should keep the requested ModelID.
//
// Keys checked, in priority order:
//   - cost_model_id         (canonical key all CLI adapters now emit)
//   - claude_code_model     (claude-code legacy, structured + tmux)
//   - codex_effective_model (codex tmux + JSON)
//   - gemini_effective_model (gemini tmux); gemini_model (structured)
//   - cursor_model          (cursor structured)
func effectiveModelIDFromTokenEvent(tokenEvent *events.TokenUsageEvent) string {
	if tokenEvent == nil || tokenEvent.GenerationInfo == nil {
		return ""
	}
	for _, key := range []string{
		"cost_model_id",
		"claude_code_model",
		"codex_effective_model",
		"gemini_effective_model",
		"gemini_model",
		"cursor_model",
	} {
		if v, ok := tokenEvent.GenerationInfo[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// extractLLMCallCount extracts LLM call count from a TokenUsageEvent's GenerationInfo
// Returns 1 if not available (fallback for single-call events).
// For conversation end events, this returns the cumulative call count
func extractLLMCallCount(tokenEvent *events.TokenUsageEvent) int {
	if tokenEvent.GenerationInfo == nil {
		return 1 // Default to 1 for single-call events.
	}

	// Check for cumulative_llm_call_count (from conversation end event)
	if count, ok := tokenEvent.GenerationInfo["llm_call_count"].(int); ok {
		return count
	}
	if count, ok := tokenEvent.GenerationInfo["llm_call_count"].(float64); ok {
		return int(count)
	}

	// Fallback: return 1 for single-call events.
	return 1
}

// freshPromptTokens returns the event's prompt tokens without the cache
// buckets, so run files hold fresh input the way the ledger formula prices it.
// Adapters report prompt either including cache (Claude Code, Cursor SDK,
// Codex app-server) or apart from it (Pi, Codex transcripts) and mark which on
// each call; unmarked prompt includes cache, the provider library's default.
func (c *ContextAwareEventBridge) freshPromptTokens(tokenEvent *events.TokenUsageEvent, cacheTokens int) int {
	includes, marked := tokenEvent.GenerationInfo["prompt_tokens_include_cache"].(bool)
	if !marked {
		c.mu.RLock()
		includes, marked = c.promptIncludesCache[strings.ToLower(strings.TrimSpace(tokenEvent.Provider))]
		c.mu.RUnlock()
	}
	if marked && !includes {
		return tokenEvent.PromptTokens
	}
	return max(tokenEvent.PromptTokens-cacheTokens, 0)
}
