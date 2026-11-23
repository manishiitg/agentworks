package llmtypes

import (
	"context"
	"strings"
	"testing"
)

// PLAT-746-992: inherited child work must not deliver a prediction to another
// chat/provider, and a delayed native event must not complete its turn twice.
func TestNextPromptScopeFencesOwnerProviderAndOneCompletion(t *testing.T) {
	calls := 0
	ctx := WithNextPrompt(context.Background(), NextPromptScope{Owner: "chat-a", Provider: "codex-cli", TurnID: "turn-a", Eligible: true, Complete: func(result NextPromptResult) {
		calls++
		if result.Text != "Check the diff" {
			t.Fatalf("prediction: %+v", result)
		}
	}})
	if NextPromptFor(ctx, "codex-cli", "chat-b").Complete != nil || NextPromptFor(ctx, "claude-code", "chat-a").Complete != nil {
		t.Fatal("prediction scope crossed chat/provider")
	}
	scope := NextPromptFor(ctx, "codex-cli", "chat-a")
	scope.Finish(NextPromptResult{Text: " Check the diff "})
	scope.Finish(NextPromptResult{Text: "late duplicate"})
	if calls != 1 {
		t.Fatalf("completion count=%d", calls)
	}
}

func TestNextPromptRejectsReplySizedOutputAndNearLimitSampling(t *testing.T) {
	got := NextPromptResult{}
	scope := NextPromptScope{Eligible: true, Complete: func(r NextPromptResult) { got = r }}
	scope.Finish(NextPromptResult{Text: strings.Repeat("x", 501)})
	if got.Text != "" || got.Reason != "too_long" {
		t.Fatalf("reply-sized prediction: %+v", got)
	}
	scope.StillCurrent = func(string) bool { return false }
	scope.Finish(NextPromptResult{Text: "late hint", Usage: &NextPromptUsage{ResponseID: "paid-sample", Input: 100, CachedInput: 80, Output: 5}})
	if got.Text != "" || got.Reason != "newer_input_or_stopped" || got.Usage == nil || got.Usage.ResponseID != "paid-sample" {
		t.Fatalf("late delivery escaped the host fence or lost accounting: %+v", got)
	}
	if !NextPromptNearLimit([]RateLimitWindow{{UsedPercent: 90}}) || NextPromptNearLimit([]RateLimitWindow{{UsedPercent: 50}}) {
		t.Fatal("near-limit gate")
	}
}
