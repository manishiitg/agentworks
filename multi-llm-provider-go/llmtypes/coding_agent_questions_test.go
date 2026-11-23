package llmtypes

import (
	"context"
	"testing"
)

// PLAT-872-21: a child or another provider must not inherit its parent's answer UI.
func TestCodingAgentQuestionScopeRequiresExactOwnerAndProvider(t *testing.T) {
	ctx := WithCodingAgentQuestionHandler(context.Background(), "codex-cli", "owner", func(context.Context, map[string]any) (string, error) { return "ok", nil })
	for _, tc := range []struct {
		provider, owner string
		allowed         bool
	}{{"codex-cli", "owner", true}, {"pi-cli", "owner", false}, {"codex-cli", "child", false}} {
		_, err := AskCodingAgentQuestion(ScopeCodingAgentQuestions(ctx, tc.provider, tc.owner), nil)
		if (err == nil) != tc.allowed {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
