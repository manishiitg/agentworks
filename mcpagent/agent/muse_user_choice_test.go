package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
)

// A scheduled run keeps Muse's native session alive (persistent) but has no
// person to answer, so a native question must be auto-answered there. Before
// PLAT-354's fix, persistence alone switched on user choice and such a run
// waited forever.
func TestMuseWaitsForUserChoiceNeedsAnAttendingUser(t *testing.T) {
	cases := []struct {
		name       string
		persistent bool
		attending  bool
		structured bool
		want       bool
	}{
		{"interactive chat", true, true, false, true},
		{"scheduled run keeps session alive", true, false, false, false},
		{"one-shot turn", false, true, false, false},
		{"structured transport", true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{musePersistentInteractiveSession: tc.persistent, userAnswersNativeQuestions: tc.attending}
			if tc.structured {
				a.codingAgentTransport = llm.CodingAgentTransportStructured
			}
			if got := a.museWaitsForUserChoice(); got != tc.want {
				t.Fatalf("museWaitsForUserChoice() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The builder reaches the flag through Definition.Coding, so check that path
// sets it, rather than only the field.
func TestDefinitionCodingUserAnswersNativeQuestionsOption(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		a := &Agent{}
		for _, opt := range runtimeAgentOptions(RuntimeConfig{Coding: CodingRuntimeConfig{PersistentMuse: true, UserAnswersNativeQuestions: enabled}}) {
			opt(a)
		}
		if a.userAnswersNativeQuestions != enabled || !a.musePersistentInteractiveSession {
			t.Fatalf("enabled=%v: userAnswersNativeQuestions=%v persistent=%v", enabled, a.userAnswersNativeQuestions, a.musePersistentInteractiveSession)
		}
	}
}
