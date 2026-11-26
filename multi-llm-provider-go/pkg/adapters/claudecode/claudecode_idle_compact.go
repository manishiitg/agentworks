package claudecode

import (
	"context"
	"fmt"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Idle compaction (PLAT-872). The host compacts a long chat that sat idle near
// the end of the prompt-cache lifetime, while the cache is still warm, by
// running Claude Code's own /compact on the resumed conversation. The SDK and
// stream-json transports send it as the turn's input; the tmux transport
// pastes it into the resumed pane and reads the compaction record from the
// native transcript.
const claudeIdleCompactCommand = "/compact"

// claudeIdleCompactTrigger marks idle compaction events so the chat can say
// the chat was tidied while the person was away.
const claudeIdleCompactTrigger = "idle"

// claudeIdleCompactObserved collects the compaction a compaction-only turn ran.
type claudeIdleCompactObserved struct {
	ended         bool
	failed        bool
	before, after int
}

func (o *claudeIdleCompactObserved) observe(c llmtypes.ContextCompaction) llmtypes.ContextCompaction {
	c.Trigger = claudeIdleCompactTrigger
	if c.Phase == llmtypes.ContextCompactionPhaseEnd {
		o.ended = true
		o.failed = c.Outcome != "" && c.Outcome != llmtypes.ContextCompactionOutcomeSuccess
		if c.TokensBefore > 0 {
			o.before = c.TokensBefore
		}
		if c.TokensAfter > 0 {
			o.after = c.TokensAfter
		}
	}
	return c
}

func (o claudeIdleCompactObserved) result(cacheRead, cacheWrite int) llmtypes.IdleCompactResult {
	r := llmtypes.IdleCompactResult{Compacted: o.ended && !o.failed, TokensBefore: o.before, TokensAfter: o.after, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite}
	switch {
	case r.Compacted:
		r.Reason = llmtypes.IdleCompactReasonCompacted
	case o.failed:
		r.Reason = llmtypes.IdleCompactReasonFailed
	default:
		// Claude answered /compact without compacting (nothing to compact).
		r.Reason = llmtypes.IdleCompactReasonNothing
	}
	return r
}

// claudeIdleCompactResponse is a compaction-only turn's response on the SDK and
// stream-json transports: no answer text, the compaction's usage, the handle.
func claudeIdleCompactResponse(sessionID, workingDir, model string, sdk bool, observed claudeIdleCompactObserved, usage llmtypes.Usage, cacheRead, cacheWrite int) *llmtypes.ContentResponse {
	transport := llmtypes.CodingProviderTransportStructured
	mode := "structured"
	if sdk {
		transport, mode = llmtypes.CodingProviderTransportSDK, "sdk"
	}
	additional := map[string]any{"provider": "claude-code", "claude_code_mode": mode, "claude_code_session_id": sessionID, "context_window_usage_known": false}
	info := &llmtypes.GenerationInfo{InputTokens: intPtrIfNonZeroClaude(usage.InputTokens), OutputTokens: intPtrIfNonZeroClaude(usage.OutputTokens), TotalTokens: intPtrIfNonZeroClaude(usage.InputTokens + usage.OutputTokens), Additional: additional}
	if cacheRead > 0 {
		v := cacheRead
		info.CachedContentTokens = &v
		additional["cache_read_input_tokens"] = v
	}
	if cacheWrite > 0 {
		additional["cache_creation_input_tokens"] = cacheWrite
	}
	if cacheRead > 0 || cacheWrite > 0 {
		additional["prompt_tokens_include_cache"] = true
	}
	llmtypes.AttachIdleCompactResult(info, observed.result(cacheRead, cacheWrite))
	llmtypes.AttachCodingProviderSessionHandle(info, llmtypes.CodingProviderSessionHandle{Provider: "claude-code", Transport: transport, NativeSessionID: sessionID, WorkingDir: workingDir, Model: model, Status: llmtypes.CodingProviderSessionStatusIdle})
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{StopReason: "stop", GenerationInfo: info}}, Usage: &usage}
}

// runClaudeTmuxIdleCompact pastes /compact into the resumed pane and waits for
// the compaction record Claude writes to the native transcript. ctx bounds the
// wait (the host gives up and treats it as failed).
func runClaudeTmuxIdleCompact(ctx context.Context, sessionName string, emit func(llmtypes.StreamChunk)) (llmtypes.IdleCompactResult, error) {
	started := time.Now().UTC()
	id := fmt.Sprintf("claude-compact-%d", started.UnixMilli())
	emit(llmtypes.ContextCompactionChunk(llmtypes.ContextCompaction{Provider: "claude-code", Phase: llmtypes.ContextCompactionPhaseStart, ID: id, Trigger: claudeIdleCompactTrigger, StartedAt: started}))
	// The mod asks Claude Code to compact and reports the sizes it got.
	event, err := claudeModRequest(ctx, sessionName, "compact", "")
	if err != nil {
		emitClaudeIdleCompactEnd(emit, id, started, nil, false)
		return llmtypes.IdleCompactResult{}, err
	}
	if event.Delivery != "compacted" {
		emitClaudeIdleCompactEnd(emit, id, started, nil, false)
		return llmtypes.IdleCompactResult{Reason: llmtypes.IdleCompactReasonNoSession}, nil
	}
	meta := &claudeCompactMetadata{PreTokens: int(event.TokensBefore), PostTokens: int(event.TokensAfter)}
	end := emitClaudeIdleCompactEnd(emit, id, started, meta, true)
	return llmtypes.IdleCompactResult{Compacted: true, Reason: llmtypes.IdleCompactReasonCompacted, TokensBefore: end.TokensBefore, TokensAfter: end.TokensAfter}, nil
}

func emitClaudeIdleCompactEnd(emit func(llmtypes.StreamChunk), id string, started time.Time, meta *claudeCompactMetadata, ok bool) llmtypes.ContextCompaction {
	now := time.Now().UTC()
	end := claudeCompactionFromBoundary(id, meta, now)
	end.Trigger = claudeIdleCompactTrigger
	end.StartedAt, end.DurationMs = started, now.Sub(started).Milliseconds()
	if !ok {
		end.Outcome = llmtypes.ContextCompactionOutcomeFailed
	}
	emit(llmtypes.ContextCompactionChunk(end))
	return end
}
