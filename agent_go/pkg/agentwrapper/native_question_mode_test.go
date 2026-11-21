package agent

import (
	"github.com/manishiitg/mcpagent/llm"
	"testing"
)

// PLAT-746-941: automatic turns lost the attended mode on a fresh context.
// Reuse only the current wrapper's server-admitted capability, not trigger JSON.
func TestSyntheticQuestionModeUsesAdmittedWrapper(t *testing.T) {
	for _, tc := range []struct {
		name             string
		admitted, closed bool
		want             bool
	}{
		{"ordinary chat followup", true, false, true},
		{"unattended schedule bot API or child", false, false, false},
		{"closed wrapper", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &LLMAgentWrapper{config: LLMAgentConfig{Provider: llm.Provider("codex-cli"), CodingAgentUserAnswersNativeQuestions: tc.admitted}, closed: tc.closed}
			provider, got := a.NativeQuestionMode()
			if provider != "codex-cli" || got != tc.want {
				t.Fatalf("provider=%s attended=%t want=%t", provider, got, tc.want)
			}
		})
	}
}
