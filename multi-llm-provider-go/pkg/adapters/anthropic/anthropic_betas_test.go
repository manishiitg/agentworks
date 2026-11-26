package anthropic

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestBuildAnthropicBetaTokensEmptyByDefault: a bare request with no
// thinking, no tools, no extended cache, and no custom beta opt-in must
// produce an empty token list so we do NOT attach the `anthropic-beta`
// header at all. Modern model snapshots have prompt caching GA, so the
// adapter no longer sends the legacy beta token by default.
func TestBuildAnthropicBetaTokensEmptyByDefault(t *testing.T) {
	got := buildAnthropicBetaTokens(anthropic.MessageNewParams{}, nil)
	if len(got) != 0 {
		t.Fatalf("expected no beta tokens for default request, got %v", got)
	}
}

// TestBuildAnthropicBetaTokensInterleavedThinking: when the request both
// enables thinking and declares tools, the adapter must opt into the
// interleaved-thinking beta. Anthropic rejects thinking + tools without
// it on Claude 4-class models.
func TestBuildAnthropicBetaTokensInterleavedThinking(t *testing.T) {
	params := anthropic.MessageNewParams{
		Thinking: anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: 2048},
		},
		Tools: []anthropic.ToolUnionParam{
			anthropic.ToolUnionParamOfTool(anthropic.ToolInputSchemaParam{}, "noop"),
		},
	}
	got := buildAnthropicBetaTokens(params, nil)
	if !contains(got, betaInterleavedThinking) {
		t.Fatalf("expected %s in tokens %v", betaInterleavedThinking, got)
	}
}

// TestBuildAnthropicBetaTokensSkipsInterleavedWhenNoTools: thinking on its
// own does NOT need the interleaved-thinking beta — it only matters when
// thinking and tool_use have to alternate inside a single turn. Including
// the header unnecessarily inflates the request surface.
func TestBuildAnthropicBetaTokensSkipsInterleavedWhenNoTools(t *testing.T) {
	params := anthropic.MessageNewParams{
		Thinking: anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: 2048},
		},
	}
	got := buildAnthropicBetaTokens(params, nil)
	if contains(got, betaInterleavedThinking) {
		t.Fatalf("interleaved-thinking should not be set without tools: %v", got)
	}
}

// TestBuildAnthropicBetaTokensPromptCachingAttachesWhenCacheControlPresent:
// the Messages API silently returns zero cache_creation/cache_read
// tokens unless the `prompt-caching-2024-07-31` beta header is sent on
// requests that carry a cache_control breakpoint. The adapter must
// auto-attach the token whenever the request has any cache_control,
// even after prompt caching went GA.
func TestBuildAnthropicBetaTokensPromptCachingAttachesWhenCacheControlPresent(t *testing.T) {
	systemBlock := anthropic.TextBlockParam{
		Text:         "system",
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	systemBlock.CacheControl.TTL = anthropic.CacheControlEphemeralTTLTTL5m
	params := anthropic.MessageNewParams{System: []anthropic.TextBlockParam{systemBlock}}
	got := buildAnthropicBetaTokens(params, nil)
	if !contains(got, betaPromptCachingLegacy) {
		t.Fatalf("expected %s when cache_control is present; got %v", betaPromptCachingLegacy, got)
	}
}

// TestBuildAnthropicBetaTokensPromptCachingOmittedWithoutCacheControl:
// the prompt-caching beta is a no-op (and unnecessary noise on the
// wire) when the request has no cache_control. The adapter must NOT
// attach it in that case so we keep the request surface minimal.
func TestBuildAnthropicBetaTokensPromptCachingOmittedWithoutCacheControl(t *testing.T) {
	params := anthropic.MessageNewParams{
		System: []anthropic.TextBlockParam{
			{Text: "system without cache_control"},
		},
	}
	got := buildAnthropicBetaTokens(params, nil)
	if contains(got, betaPromptCachingLegacy) {
		t.Fatalf("prompt-caching beta attached unnecessarily for cache-less request: %v", got)
	}
}

// TestBuildAnthropicBetaTokensExtendedCacheFromSystem: when the caller
// marked the system block with the 1-hour TTL, the adapter must opt into
// extended-cache-ttl. Without the beta, the SDK silently downgrades to
// the 5-minute default and the caller's intent is lost.
func TestBuildAnthropicBetaTokensExtendedCacheFromSystem(t *testing.T) {
	systemBlock := anthropic.TextBlockParam{
		Text:         "system",
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	systemBlock.CacheControl.TTL = anthropic.CacheControlEphemeralTTL("1h")
	params := anthropic.MessageNewParams{
		System: []anthropic.TextBlockParam{systemBlock},
	}
	got := buildAnthropicBetaTokens(params, nil)
	if !contains(got, betaExtendedCacheTTL) {
		t.Fatalf("expected %s when system block ttl=1h; got %v", betaExtendedCacheTTL, got)
	}
}

// TestBuildAnthropicBetaTokensExtendedCacheIgnoresDefaultTTL: a 5-minute
// cache breakpoint is the SDK default, so the extended-cache beta must
// NOT be attached. This prevents accidental opt-in when a caller sets
// cache_control but leaves TTL at its default.
func TestBuildAnthropicBetaTokensExtendedCacheIgnoresDefaultTTL(t *testing.T) {
	systemBlock := anthropic.TextBlockParam{
		Text:         "system",
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
	systemBlock.CacheControl.TTL = anthropic.CacheControlEphemeralTTL("5m")
	params := anthropic.MessageNewParams{
		System: []anthropic.TextBlockParam{systemBlock},
	}
	got := buildAnthropicBetaTokens(params, nil)
	if contains(got, betaExtendedCacheTTL) {
		t.Fatalf("extended-cache should not be set for 5m TTL: %v", got)
	}
}

// TestBuildAnthropicBetaTokensExtraFromOptionsString: callers can opt into
// arbitrary betas via Metadata.Custom["anthropic_beta"] = "token".
func TestBuildAnthropicBetaTokensExtraFromOptionsString(t *testing.T) {
	opts := &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
		"anthropic_beta": "files-api-2025-01-01",
	}}}
	got := buildAnthropicBetaTokens(anthropic.MessageNewParams{}, opts)
	if !contains(got, "files-api-2025-01-01") {
		t.Fatalf("expected caller-supplied beta to be honored; got %v", got)
	}
}

// TestBuildAnthropicBetaTokensExtraFromOptionsSlice covers the
// []string variant for callers that need multiple opt-in betas at once.
func TestBuildAnthropicBetaTokensExtraFromOptionsSlice(t *testing.T) {
	opts := &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
		"anthropic_beta": []string{"files-api-2025-01-01", "computer-use-2025-04-29"},
	}}}
	got := buildAnthropicBetaTokens(anthropic.MessageNewParams{}, opts)
	if !contains(got, "files-api-2025-01-01") || !contains(got, "computer-use-2025-04-29") {
		t.Fatalf("expected both caller-supplied betas; got %v", got)
	}
}

// TestBuildAnthropicBetaTokensDedupes: if a caller passes a beta token
// the builder would already include automatically (e.g. interleaved-
// thinking when thinking+tools are present), it should appear once.
func TestBuildAnthropicBetaTokensDedupes(t *testing.T) {
	params := anthropic.MessageNewParams{
		Thinking: anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{BudgetTokens: 2048},
		},
		Tools: []anthropic.ToolUnionParam{
			anthropic.ToolUnionParamOfTool(anthropic.ToolInputSchemaParam{}, "noop"),
		},
	}
	opts := &llmtypes.CallOptions{Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{
		"anthropic_beta": betaInterleavedThinking,
	}}}
	got := buildAnthropicBetaTokens(params, opts)
	count := 0
	for _, token := range got {
		if token == betaInterleavedThinking {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected %s exactly once in %v, got %d occurrences", betaInterleavedThinking, got, count)
	}
}

func contains(set []string, want string) bool {
	for _, s := range set {
		if s == want {
			return true
		}
	}
	return false
}

// TestResolveAnthropicThinkingBudget locks in the budget resolver
// behavior. We deliberately fail-closed: callers who pass a budget that
// would violate Anthropic's constraints (sub-1024, or >= max_tokens) get
// no thinking at all rather than a silently-clamped value they didn't
// authorize.
func TestResolveAnthropicThinkingBudget(t *testing.T) {
	cases := []struct {
		name      string
		opts      *llmtypes.CallOptions
		maxTokens int64
		want      int
	}{
		{
			name: "nil opts produces no thinking",
			opts: nil,
			want: 0,
		},
		{
			name: "no level, no budget → no thinking",
			opts: &llmtypes.CallOptions{},
			want: 0,
		},
		{
			name: "explicit budget passes through when above 1024 and below max",
			opts: &llmtypes.CallOptions{ThinkingBudget: 4096},
			maxTokens: 32768,
			want: 4096,
		},
		{
			name: "explicit budget below 1024 fails closed",
			opts: &llmtypes.CallOptions{ThinkingBudget: 512},
			maxTokens: 32768,
			want: 0,
		},
		{
			name: "explicit budget at or above max fails closed",
			opts: &llmtypes.CallOptions{ThinkingBudget: 32768},
			maxTokens: 32768,
			want: 0,
		},
		{
			name: "level low → 1024",
			opts: &llmtypes.CallOptions{ThinkingLevel: "low"},
			maxTokens: 32768,
			want: 1024,
		},
		{
			name: "level medium → 4096",
			opts: &llmtypes.CallOptions{ThinkingLevel: "Medium"}, // case-insensitive
			maxTokens: 32768,
			want: 4096,
		},
		{
			name: "level high → 16384",
			opts: &llmtypes.CallOptions{ThinkingLevel: "high"},
			maxTokens: 32768,
			want: 16384,
		},
		{
			name: "level off short-circuits to zero",
			opts: &llmtypes.CallOptions{ThinkingLevel: "off"},
			maxTokens: 32768,
			want: 0,
		},
		{
			name: "explicit budget wins over level",
			opts: &llmtypes.CallOptions{ThinkingBudget: 2048, ThinkingLevel: "high"},
			maxTokens: 32768,
			want: 2048,
		},
		{
			name: "unrecognized level falls through to zero",
			opts: &llmtypes.CallOptions{ThinkingLevel: "ultra"},
			maxTokens: 32768,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAnthropicThinkingBudget(tc.opts, tc.maxTokens)
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
