package anthropic

import (
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Beta token identifiers we know how to ask for. Held as named constants
// so callers/tests don't get stringly-typed and so we can document each.
const (
	// betaInterleavedThinking unlocks the interleaved thinking + tool_use
	// pattern (Claude 4 family). Set when the request both enables
	// extended thinking AND declares tools — Anthropic rejects the
	// combination without this header.
	betaInterleavedThinking = "interleaved-thinking-2025-05-14"

	// betaExtendedCacheTTL enables the 1-hour prompt-cache TTL on
	// cache_control breakpoints (the default is 5m). We opt in
	// automatically when any block in the outgoing request asks for
	// ttl=1h.
	betaExtendedCacheTTL = "extended-cache-ttl-2025-04-11"

	// betaPromptCachingLegacy is the GA-era prompt-caching toggle.
	// Prompt caching is now GA in the Messages API so this header is
	// effectively a no-op on modern model snapshots, but the const stays
	// around for callers who explicitly want to pin the legacy behavior.
	betaPromptCachingLegacy = "prompt-caching-2024-07-31"
)

// buildAnthropicBetaTokens returns the set of `anthropic-beta` tokens that
// should accompany the outgoing Messages API request, deduplicated and in
// stable order. An empty result means no `anthropic-beta` header should be
// attached at all.
//
// The function is pure: it only inspects the already-built MessageNewParams
// plus the caller's CallOptions. That keeps the streaming call site small
// and gives the unit tests something concrete to assert against.
func buildAnthropicBetaTokens(params anthropic.MessageNewParams, opts *llmtypes.CallOptions) []string {
	seen := map[string]struct{}{}
	var ordered []string
	add := func(token string) {
		if token == "" {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		ordered = append(ordered, token)
	}

	// 1. Prompt caching activation. The Messages API treats prompt
	//    caching as GA in the sense that no organization opt-in is
	//    required, but it still gates per-request behavior on the
	//    `prompt-caching-2024-07-31` beta token — without it the API
	//    silently returns cache_creation_input_tokens=0 and never
	//    populates cache_read_input_tokens on follow-up calls.
	//
	//    We attach the token only when at least one cache_control
	//    breakpoint is present in the outgoing request, so calls that
	//    do not use caching keep a minimal request surface.
	if anthropicRequestUsesCacheControl(params) {
		add(betaPromptCachingLegacy)
	}

	// 2. Interleaved thinking + tool use. We require BOTH thinking
	//    enablement AND a non-empty tool list — otherwise the header
	//    does nothing and just inflates the request surface.
	if anthropicRequestEnablesThinking(params) && len(params.Tools) > 0 {
		add(betaInterleavedThinking)
	}

	// 3. Extended cache TTL. Opt in whenever the caller marked any
	//    block with the 1-hour TTL, since the SDK silently downgrades
	//    to 5m otherwise.
	if anthropicRequestUsesExtendedCacheTTL(params) {
		add(betaExtendedCacheTTL)
	}

	// 4. Caller-supplied explicit beta tokens via CallOptions metadata.
	//    This is the escape hatch for users who need a beta we haven't
	//    promoted to a first-class field yet (e.g. a brand new feature
	//    flag). Format: opts.Metadata.Custom["anthropic_beta"] is either
	//    a `string` (single token) or `[]string` (multiple tokens).
	for _, token := range extraAnthropicBetaTokens(opts) {
		add(token)
	}

	return ordered
}

// anthropicRequestUsesCacheControl reports whether any block in the
// outgoing request has a cache_control breakpoint set. We check system
// blocks, message content blocks, and tool definitions — every place
// the Messages API allows a cache_control attachment.
func anthropicRequestUsesCacheControl(params anthropic.MessageNewParams) bool {
	for _, block := range params.System {
		if string(block.CacheControl.TTL) != "" || block.CacheControl.Type != "" {
			return true
		}
	}
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			cc := contentBlockCacheControl(block)
			if cc == nil {
				continue
			}
			if string(cc.TTL) != "" || cc.Type != "" {
				return true
			}
		}
	}
	for _, toolUnion := range params.Tools {
		if toolUnion.OfTool == nil {
			continue
		}
		cc := toolUnion.OfTool.CacheControl
		if string(cc.TTL) != "" || cc.Type != "" {
			return true
		}
	}
	return false
}

// anthropicRequestEnablesThinking reports whether the assembled
// MessageNewParams has the `thinking` field populated to a non-disabled
// value. The SDK exposes the field as a union with separate enabled /
// disabled variants; we treat anything other than the explicit "disabled"
// shape as a thinking-enabled request.
func anthropicRequestEnablesThinking(params anthropic.MessageNewParams) bool {
	if params.Thinking.OfEnabled != nil {
		return true
	}
	return false
}

// anthropicRequestUsesExtendedCacheTTL reports whether any cache_control
// breakpoint in the request asks for the non-default 1-hour TTL. The SDK
// represents TTL as a string param ("5m" or "1h"); we match on the
// stringified form so this stays correct as the SDK adds new TTL options.
func anthropicRequestUsesExtendedCacheTTL(params anthropic.MessageNewParams) bool {
	matchesExtended := func(ttl anthropic.CacheControlEphemeralTTL) bool {
		return strings.EqualFold(strings.TrimSpace(string(ttl)), "1h")
	}
	for _, block := range params.System {
		if matchesExtended(block.CacheControl.TTL) {
			return true
		}
	}
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			cc := contentBlockCacheControl(block)
			if cc == nil {
				continue
			}
			if matchesExtended(cc.TTL) {
				return true
			}
		}
	}
	for _, toolUnion := range params.Tools {
		if toolUnion.OfTool == nil {
			continue
		}
		if matchesExtended(toolUnion.OfTool.CacheControl.TTL) {
			return true
		}
	}
	return false
}

// contentBlockCacheControl reaches into the various ContentBlockParamUnion
// variants and returns a pointer to the variant's CacheControl, or nil if
// the active variant doesn't carry one. The SDK doesn't expose a shared
// accessor for this so we inline the dispatch.
func contentBlockCacheControl(block anthropic.ContentBlockParamUnion) *anthropic.CacheControlEphemeralParam {
	switch {
	case block.OfText != nil:
		return &block.OfText.CacheControl
	case block.OfImage != nil:
		return &block.OfImage.CacheControl
	case block.OfDocument != nil:
		return &block.OfDocument.CacheControl
	case block.OfToolUse != nil:
		return &block.OfToolUse.CacheControl
	case block.OfToolResult != nil:
		return &block.OfToolResult.CacheControl
	}
	return nil
}

// resolveAnthropicThinkingBudget computes the budget_tokens to send for
// extended thinking based on the caller's CallOptions. Returns 0 when
// thinking should NOT be enabled (the caller didn't ask for it, or the
// computed budget would violate Anthropic's constraints).
//
// Anthropic requires:
//   - budget_tokens >= 1024
//   - budget_tokens < max_tokens
//
// We round budgets that would violate either rule down to zero (i.e.
// skip thinking) rather than silently picking a budget the caller didn't
// authorize.
func resolveAnthropicThinkingBudget(opts *llmtypes.CallOptions, maxTokens int64) int {
	if opts == nil {
		return 0
	}
	budget := opts.ThinkingBudget
	if budget <= 0 && opts.ThinkingLevel != "" {
		switch strings.ToLower(strings.TrimSpace(opts.ThinkingLevel)) {
		case "off", "none", "disabled":
			return 0
		case "low":
			budget = 1024
		case "medium":
			budget = 4096
		case "high":
			budget = 16384
		}
	}
	if budget < 1024 {
		return 0
	}
	if maxTokens > 0 && int64(budget) >= maxTokens {
		return 0
	}
	return budget
}

// extraAnthropicBetaTokens reads opt-in beta tokens the caller supplied
// through Metadata.Custom. Accepted forms:
//
//	Custom["anthropic_beta"] = "extended-cache-ttl-2025-04-11"
//	Custom["anthropic_beta"] = []string{"a", "b"}
//
// Anything else is ignored.
func extraAnthropicBetaTokens(opts *llmtypes.CallOptions) []string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return nil
	}
	raw, ok := opts.Metadata.Custom["anthropic_beta"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case string:
		token := strings.TrimSpace(v)
		if token == "" {
			return nil
		}
		return []string{token}
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if t := strings.TrimSpace(item); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}
