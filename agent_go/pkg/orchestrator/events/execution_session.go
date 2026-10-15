package events

import (
	"strings"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Native handles become available after the provider returns. Keep the exact
// canonical turn ID so prompt snapshots can be joined without time guessing.
func NormalizeExecutionSessionEvent(event *events.AgentEvent) (BackgroundAgentTranscriptEvent, bool) {
	if event == nil || event.TurnID == "" {
		return BackgroundAgentTranscriptEvent{}, false
	}
	data, ok := event.Data.(*events.LLMGenerationEndEvent)
	if !ok || data == nil {
		return BackgroundAgentTranscriptEvent{}, false
	}
	// Providers already attach the typed native handle to Additional for event
	// consumers. Prefer it across all CLIs; retain older provider key support.
	if handle, ok := llmtypes.ExtractCodingProviderSessionHandle(&llmtypes.GenerationInfo{Additional: data.Metadata}); ok && strings.TrimSpace(handle.NativeSessionID) != "" && strings.TrimSpace(handle.Provider) != "" {
		return BackgroundAgentTranscriptEvent{Timestamp: eventTimestamp(event, data.Timestamp), Type: "runtime_session", TurnID: event.TurnID, RuntimeSessionID: handle.Provider + ":" + handle.NativeSessionID, SourceType: string(event.Type)}, true
	}
	provider, _ := data.Metadata["provider"].(string)
	keys := map[string][]string{
		"codex-cli":   {"codex_thread_id", "codex_session_id"},
		"claude-code": {"claude_code_session_id"},
		"cursor-cli":  {"cursor_session_id"}, "pi-cli": {"pi_session_id"},
		"muse-cli": {"muse_session_id"}, "agy-cli": {"agy_session_id", "agy_conversation_id"},
	}
	for _, key := range keys[provider] {
		id, _ := data.Metadata[key].(string)
		if id = strings.TrimSpace(id); id != "" {
			return BackgroundAgentTranscriptEvent{Timestamp: eventTimestamp(event, data.Timestamp), Type: "runtime_session", TurnID: event.TurnID, RuntimeSessionID: provider + ":" + id, SourceType: string(event.Type)}, true
		}
	}
	return BackgroundAgentTranscriptEvent{}, false
}
