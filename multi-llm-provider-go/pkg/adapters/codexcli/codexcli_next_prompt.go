package codexcli

import (
	"context"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const codexNextPromptInstruction = "Predict the next message the user will send in this conversation. Reply with only that message, written as the user would type it, in under 20 words. Do not use tools. If there is no clear next step, reply with nothing."

// Uses Codex >=0.165's native cached sampler, NOT turn/start or a separate
// model/tool conversation. expectedTurnId rejects a stale result if new input
// reached the thread. Unsupported native versions fail closed, without fallback.
func sampleCodexNextPrompt(ctx context.Context, s *codexAppSession, rpc *codexRPC, thread, turn string) {
	scope := llmtypes.NextPromptFor(ctx, "codex-cli", s.owner)
	if scope.Complete == nil {
		return
	}
	s.mu.Lock()
	epoch := s.promptEpoch
	model := s.model
	nearLimit := llmtypes.NextPromptNearLimit(codexStructuredRateLimitWindows(s.rateLimits))
	s.mu.Unlock()
	if !scope.Eligible || nearLimit {
		reason := "first_or_unattended_turn"
		if nearLimit {
			reason = "near_limit"
		}
		scope.Finish(llmtypes.NextPromptResult{Reason: reason, NativeThread: thread, NativeTurn: turn})
		return
	}
	// Completion must not wait for the optional prediction. The result remains
	// fenced by the exact native turn and the host's captured platform turn.
	go func() {
		if !scope.Current() {
			scope.Finish(llmtypes.NextPromptResult{Reason: "newer_input_or_stopped"})
			return
		}
		sampleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var reply struct {
			Text        *string `json:"text"`
			SourceTurn  string  `json:"sourceTurnId"`
			ResponseID  string  `json:"responseId"`
			Input       *int64  `json:"inputTokens"`
			CachedInput *int64  `json:"cachedInputTokens"`
			Output      *int64  `json:"outputTokens"`
		}
		err := rpc.call(sampleCtx, "thread/sample", map[string]any{"threadId": thread, "expectedTurnId": turn, "prompt": codexNextPromptInstruction, "effort": "low"}, &reply)
		result := llmtypes.NextPromptResult{NativeThread: thread, NativeTurn: turn}
		if err != nil {
			result.Reason = "unsupported_or_stale"
		} else {
			// An ephemeral sample is absent from the parent's native ledger.
			// Retain its measured usage even when its hint is empty or stale.
			if reply.ResponseID != "" && reply.Input != nil && reply.CachedInput != nil && reply.Output != nil && *reply.Input >= 0 && *reply.CachedInput >= 0 && *reply.CachedInput <= *reply.Input && *reply.Output >= 0 {
				result.Usage = &llmtypes.NextPromptUsage{ResponseID: reply.ResponseID, Model: model, Input: *reply.Input, CachedInput: *reply.CachedInput, Output: *reply.Output}
			}
			if reply.SourceTurn != "" && reply.SourceTurn != turn {
				result.Reason = "newer_turn"
			} else if reply.Text != nil {
				result.Text = *reply.Text
			}
		}
		s.mu.Lock()
		stale := s.threadID != thread || s.promptEpoch != epoch || s.rpc != rpc || (s.turnID != "" && s.turnID != turn)
		s.mu.Unlock()
		if stale {
			result.Text = ""
			result.Reason = "newer_turn"
		}
		scope.Finish(result)
	}()
}
