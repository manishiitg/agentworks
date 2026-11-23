package llmtypes

// Idle compaction (PLAT-872): the host asks a coding CLI to compact a long
// conversation that has sat idle near the end of the provider's prompt-cache
// lifetime, using the CLI's own native trigger, so the person's next message
// does not pay a full cache write of the long history. The call sends no
// person message: an adapter that sees WithCodingProviderIdleCompact resumes
// the native session, runs only the compaction and returns a response whose
// GenerationInfo carries an IdleCompactResult (and the compaction's usage).

const (
	// CodingProviderIdleCompactMetadataKey marks a call as compaction-only.
	CodingProviderIdleCompactMetadataKey = "coding_provider_idle_compact"
	// IdleCompactAdditionalKey holds the IdleCompactResult on GenerationInfo.Additional.
	IdleCompactAdditionalKey = "idle_compact"
)

// Reasons an idle compaction did or did not happen.
const (
	IdleCompactReasonCompacted      = "compacted"
	IdleCompactReasonUnsupported    = "unsupported"
	IdleCompactReasonFailed         = "failed"
	IdleCompactReasonNothing        = "nothing_to_compact"
	IdleCompactReasonBackgroundWork = "background_work"
	IdleCompactReasonNoSession      = "no_native_session"
)

// IdleCompactResult is what one compaction-only call did. Token counts are 0
// when the CLI did not report them (unknown, never "zero tokens").
type IdleCompactResult struct {
	Compacted        bool   `json:"compacted"`
	Reason           string `json:"reason,omitempty"`
	TokensBefore     int    `json:"tokens_before,omitempty"`
	TokensAfter      int    `json:"tokens_after,omitempty"`
	CacheReadTokens  int    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int    `json:"cache_write_tokens,omitempty"`
}

// WithCodingProviderIdleCompact asks a coding-agent adapter to resume the
// native session and run only the CLI's own compaction (no person message).
func WithCodingProviderIdleCompact() CallOption {
	return func(opts *CallOptions) {
		if opts.Metadata == nil {
			opts.Metadata = &Metadata{Custom: make(map[string]interface{})}
		}
		if opts.Metadata.Custom == nil {
			opts.Metadata.Custom = make(map[string]interface{})
		}
		opts.Metadata.Custom[CodingProviderIdleCompactMetadataKey] = true
	}
}

// CodingProviderIdleCompactFromOptions reports whether the call is compaction-only.
func CodingProviderIdleCompactFromOptions(opts *CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, _ := opts.Metadata.Custom[CodingProviderIdleCompactMetadataKey].(bool)
	return enabled
}

// AttachIdleCompactResult stores the result on a response's GenerationInfo.
func AttachIdleCompactResult(info *GenerationInfo, result IdleCompactResult) {
	if info == nil {
		return
	}
	if info.Additional == nil {
		info.Additional = map[string]interface{}{}
	}
	info.Additional[IdleCompactAdditionalKey] = result
}

// IdleCompactResultFromResponse reads the result an adapter attached.
func IdleCompactResultFromResponse(resp *ContentResponse) (IdleCompactResult, bool) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0] == nil || resp.Choices[0].GenerationInfo == nil {
		return IdleCompactResult{}, false
	}
	result, ok := resp.Choices[0].GenerationInfo.Additional[IdleCompactAdditionalKey].(IdleCompactResult)
	return result, ok
}

// IdleCompactSkipResponse is the response for a compaction-only call that did
// not compact (unsupported transport, nothing to compact, live background work).
func IdleCompactSkipResponse(provider, reason string) *ContentResponse {
	info := &GenerationInfo{Additional: map[string]interface{}{"provider": provider}}
	AttachIdleCompactResult(info, IdleCompactResult{Reason: reason})
	return &ContentResponse{Choices: []*ContentChoice{{StopReason: "stop", GenerationInfo: info}}}
}
