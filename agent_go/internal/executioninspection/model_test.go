package executioninspection

import (
	"encoding/json"
	"testing"
	"time"

	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func TestModelUsesRecordedChronologyAndPreservesProviderQualifiedIDs(t *testing.T) {
	event := func(at int, provider, id string) orchevents.BackgroundAgentTranscriptEvent {
		text, _ := json.Marshal(orchevents.ExecutionModel{Provider: provider, ModelID: id})
		return orchevents.BackgroundAgentTranscriptEvent{Timestamp: time.Unix(int64(at), 0), Type: "execution_model", Text: string(text)}
	}
	transcript := &orchevents.BackgroundAgentTranscript{Provider: "codex-cli", ModelID: "old-header", Events: []orchevents.BackgroundAgentTranscriptEvent{
		event(3, "openrouter", "anthropic/claude-opus-5"), event(1, "codex-cli", "codex-cli/gpt-6.1-sol"), event(2, "codex-cli", "gpt-6.1-sol"),
		{Type: "execution_model", Text: "invalid"}, event(4, "", "not-enough-evidence"),
	}}
	model := Model(transcript, nil)
	if model.Label != "OpenRouter · anthropic/claude-opus-5" || len(model.Previous) != 1 || model.Previous[0].Label != "Codex · gpt-6.1-sol" || model.Tooltip != "Previously used: Codex · gpt-6.1-sol" {
		t.Fatalf("wrong actual model chronology: %+v", model)
	}
	transcript.Events = nil
	if Model(transcript, nil).Label != "Codex · old-header" {
		t.Fatal("lost older execution header evidence")
	}
	transcript.ModelID = ""
	unknown := Model(transcript, nil)
	if unknown.Label != "Model not recorded" || unknown.Provider != "" || len(unknown.Previous) != 0 {
		t.Fatalf("guessed missing model: %+v", unknown)
	}
}
