package claudecode

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeGenerationInfo maps Anthropic usage components onto GenerationInfo the way every Claude transcript reader
// must, so a normal turn and a retained turn price identically.
func claudeGenerationInfo(input, output, cacheCreate, cacheRead int) *llmtypes.GenerationInfo {
	// GenerationInfo's cross-provider contract defines PromptTokens as all
	// input consumed by the model, including the cache-served subset. Keep the
	// raw Anthropic components in Additional below. Returning only fresh/cache-
	// creation input here made downstream pricing subtract cacheRead from a
	// smaller number, clamp fresh input to zero, and silently omit input cost.
	prompt := input + cacheCreate + cacheRead
	total := prompt + output
	gi := &llmtypes.GenerationInfo{
		PromptTokens:     intRef(prompt),
		CompletionTokens: intRef(output),
		TotalTokens:      intRef(total),
		// Transcript usage is an aggregate over CLI model activity, not a
		// reliable current-context snapshot for mcpagent's percentage meter.
		Additional: map[string]interface{}{
			"context_window_usage_known": false,
		},
	}
	if cacheRead > 0 {
		gi.CachedContentTokens = intRef(cacheRead)
	}
	// Mirror cache numbers into Additional under the raw Anthropic
	// key names so the downstream cost ledger pipeline (which keys
	// off `cache_read_input_tokens` / `cache_creation_input_tokens`
	// rather than the typed CachedContentTokens field) can populate
	// CacheReadTokens / CacheWriteTokens per turn. Without this,
	// claude-code chat ledger entries showed cache_read/write=0
	// even though the turn was largely cache-served.
	if cacheRead > 0 || cacheCreate > 0 {
		// PromptTokens above is intentionally the complete input footprint.
		// Tell the shared pricing helper to remove these cache buckets before
		// charging the fresh-input rate, then charge each at its own rate.
		gi.Additional["prompt_tokens_include_cache"] = true
		if cacheRead > 0 {
			gi.Additional["cache_read_input_tokens"] = cacheRead
		}
		if cacheCreate > 0 {
			gi.Additional["cache_creation_input_tokens"] = cacheCreate
		}
	}
	return gi
}

func intRef(v int) *int { return &v }
