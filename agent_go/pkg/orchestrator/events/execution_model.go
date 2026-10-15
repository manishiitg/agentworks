package events

import (
	"encoding/json"
	"strings"

	"github.com/manishiitg/mcpagent/events"
)

// ExecutionModel is evidence from the running provider, not a saved preference.
type ExecutionModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
}

// NormalizeExecutionModelEvent keeps model identity even for calls with no text.
// Capture it separately so the conversation's messages remain unchanged.
func NormalizeExecutionModelEvent(event *events.AgentEvent) (BackgroundAgentTranscriptEvent, bool) {
	if event == nil || event.Data == nil {
		return BackgroundAgentTranscriptEvent{}, false
	}
	data, ok := event.Data.(*events.LLMGenerationEndEvent)
	if !ok || data == nil {
		return BackgroundAgentTranscriptEvent{}, false
	}
	provider, _ := data.Metadata["provider"].(string)
	model, _ := data.Metadata["model_id"].(string)
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" || model == "" {
		return BackgroundAgentTranscriptEvent{}, false
	}
	encoded, err := json.Marshal(ExecutionModel{Provider: provider, ModelID: model})
	if err != nil {
		return BackgroundAgentTranscriptEvent{}, false
	}
	return BackgroundAgentTranscriptEvent{Timestamp: eventTimestamp(event, data.Timestamp), Type: "execution_model", SourceType: string(event.Type), Text: string(encoded)}, true
}
