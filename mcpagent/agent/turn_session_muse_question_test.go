package mcpagent

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

func TestMuseQuestionUsesSharedCodingAgentContract(t *testing.T) {
	requested := codingAgentQuestionFromMuse(musecli.QuestionEvent{
		Sequence: 17, NativeSessionID: "native-1", PromptID: "prompt-1", Kind: "user_input_prompt_requested",
		Questions: []musecli.Question{{ID: "scope", Header: "Scope", Question: "Choose a scope", Options: []musecli.QuestionOption{{Label: "One"}, {Label: "Two", Description: "Wide"}}}},
	})
	if requested.Provider != "muse-cli" || requested.Kind != "requested" || requested.EventID != "muse:question:native-1:17" || requested.Questions[0].MultiSelect || requested.Questions[0].Options[1].Description != "Wide" {
		t.Fatalf("requested: %+v", requested)
	}
	settled := codingAgentQuestionFromMuse(musecli.QuestionEvent{
		Sequence: 18, NativeSessionID: "native-1", PromptID: "prompt-1", Kind: "user_input_prompt_settled", Outcome: "answered",
		Answers: []musecli.QuestionAnswer{{ID: "scope", SelectedLabel: "Two"}},
	})
	if settled.Kind != "settled" || settled.Outcome != "answered" || len(settled.Answers[0].SelectedLabels) != 1 || settled.Answers[0].SelectedLabels[0] != "Two" {
		t.Fatalf("settled: %+v", settled)
	}
}
