package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
)

// estimateCursorAutoCost prices a Cursor Auto call recorded without a cost at
// the provider library's estimated average Auto rate. Auto bills whichever
// model each request is routed to, and the CLI does not say which, so this is
// a plan-equivalent estimate; other providers and models are left alone.
func estimateCursorAutoCost(e costledger.Entry) (float64, string) {
	provider := strings.ToLower(strings.TrimSpace(costobserver.FirstNonEmpty(e.EffectiveProvider, e.Provider)))
	model := strings.ToLower(strings.TrimSpace(costobserver.FirstNonEmpty(e.EffectiveModelID, e.ModelID)))
	if (provider != "cursor-cli" && provider != "cursor_cli") || model != "auto" {
		return 0, ""
	}
	meta, err := (&cursorcli.CursorCLIAdapter{}).GetModelMetadata("auto")
	if err != nil || meta == nil {
		return 0, ""
	}
	prompt, completion, cacheRead := e.PromptTokens, e.CompletionTokens, e.CacheReadTokens
	cost := llmtypes.ComputeUSDCostFromMetadata(meta, &llmtypes.GenerationInfo{
		PromptTokens:        &prompt,
		CompletionTokens:    &completion,
		CachedContentTokens: &cacheRead,
		Additional: map[string]interface{}{
			// Cursor reports fresh input apart from its cache buckets.
			"prompt_tokens_include_cache": false,
			"cache_creation_input_tokens": e.CacheWriteTokens,
		},
	})
	return cost, "cursor_auto_estimate"
}
