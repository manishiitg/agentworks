package claudecode

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The consumer contract (mcpagent agent.go, "Accumulate tokens") is:
//
//	PromptTokens: total input tokens (includes cached portion)
//	CacheTokens:  subset of PromptTokens that were cached
//
// and it prices fresh input as PromptTokens - CacheTokens. Anthropic's wire
// format is the opposite -- input_tokens excludes cache read/write tokens --
// so the adapter has to reconcile the three buckets. Reporting the raw Anthropic split
// drives that subtraction negative, where a clamp turns the whole fresh-input
// charge into zero: a real production conversation recorded prompt=18 against
// cache=54717, billing every one of those fresh tokens as free.
func TestAccumulateClaudeUsageKeepsCacheASubsetOfInput(t *testing.T) {
	var got llmtypes.Usage
	accumulateClaudeUsage(&got, &claudeStreamUsage{
		InputTokens:              18,
		OutputTokens:             909,
		CacheReadInputTokens:     54717,
		CacheCreationInputTokens: 11,
	})

	if got.CacheTokens == nil {
		t.Fatal("cache reads were dropped entirely")
	}
	cache := *got.CacheTokens
	if cache != 54717+11 {
		t.Errorf("CacheTokens = %d, want %d", cache, 54717+11)
	}
	if got.InputTokens != 18+54717+11 {
		t.Errorf("InputTokens = %d, want %d (fresh + cache read/write, so cache is a subset)",
			got.InputTokens, 18+54717+11)
	}
	if fresh := got.InputTokens - cache; fresh != 18 {
		t.Errorf("InputTokens-CacheTokens = %d, want 18 fresh tokens; a negative or "+
			"zero result is what silently dropped the fresh-input charge", fresh)
	}
	if got.TotalTokens != got.InputTokens+got.OutputTokens {
		t.Errorf("TotalTokens = %d, want %d", got.TotalTokens,
			got.InputTokens+got.OutputTokens)
	}
}

// Accumulation runs once per stream usage event, so the subset relationship
// has to survive multiple folds, not just the first.
func TestAccumulateClaudeUsageSubsetHoldsAcrossEvents(t *testing.T) {
	var got llmtypes.Usage
	for _, ev := range []claudeStreamUsage{
		{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 1000},
		{InputTokens: 7, OutputTokens: 3, CacheReadInputTokens: 2000},
	} {
		e := ev
		accumulateClaudeUsage(&got, &e)
	}

	if got.CacheTokens == nil || *got.CacheTokens != 3000 {
		t.Fatalf("CacheTokens = %v, want 3000", got.CacheTokens)
	}
	if got.InputTokens != 17+3000 {
		t.Errorf("InputTokens = %d, want %d", got.InputTokens, 17+3000)
	}
	if fresh := got.InputTokens - *got.CacheTokens; fresh != 17 {
		t.Errorf("fresh input = %d, want 17", fresh)
	}
}

// A turn with no cache hit must be unaffected -- the fold only adds what the
// provider actually reported.
func TestAccumulateClaudeUsageWithoutCacheIsUnchanged(t *testing.T) {
	var got llmtypes.Usage
	accumulateClaudeUsage(&got, &claudeStreamUsage{InputTokens: 500, OutputTokens: 200})

	if got.InputTokens != 500 || got.OutputTokens != 200 {
		t.Errorf("got in=%d out=%d, want 500/200", got.InputTokens, got.OutputTokens)
	}
	if got.CacheTokens != nil {
		t.Errorf("CacheTokens = %v, want nil when the provider reported no cache read", *got.CacheTokens)
	}
}
