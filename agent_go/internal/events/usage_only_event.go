package events

import pkgevents "github.com/manishiitg/mcpagent/events"

// A model-call event that only reports usage read from a coding CLI's own transcript (a turn typed into the running
// terminal; metadata usage_source=retained) exists for the cost ledger, which reads it straight from the agent. It is not
// a chat event: stored and sent to the browser it drew a raw "LLM Generation End" card in the transcript. The server
// decides, so no client has to know to hide it: the store never records it and never publishes it.
func isUsageOnlyEvent(event Event) bool {
	if event.Type != "llm_generation_end" || event.Data == nil {
		return false
	}
	switch data := event.Data.Data.(type) {
	case *pkgevents.LLMGenerationEndEvent:
		return data != nil && data.Metadata["usage_source"] == "retained"
	case *GenericEventData:
		if data == nil {
			return false
		}
		if data.Fields["usage_source"] == "retained" {
			return true
		}
		metadata, _ := data.Fields["metadata"].(map[string]interface{})
		return metadata["usage_source"] == "retained"
	}
	return false
}
