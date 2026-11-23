package mcpagent

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCacheTokenBreakdownReadsClaudeRawComponents(t *testing.T) {
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{Additional: map[string]interface{}{
			"cache_read_input_tokens":     200.0,
			"cache_creation_input_tokens": 300,
		}},
	}}}
	read, write := cacheTokenBreakdown(resp)
	if read != 200 || write != 300 {
		t.Fatalf("cache breakdown = %d/%d, want 200/300", read, write)
	}
}

func TestEffectiveModelIDFromResponsePrefersImmutableCostModel(t *testing.T) {
	resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
		GenerationInfo: &llmtypes.GenerationInfo{Additional: map[string]interface{}{
			"claude_code_model": "claude-sonnet-5",
			"cost_model_id":     "claude-opus-5",
		}},
	}}}
	if got := effectiveModelIDFromResponse(resp, "auto"); got != "claude-opus-5" {
		t.Fatalf("effective model = %q, want claude-opus-5", got)
	}
}
