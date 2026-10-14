package events

import (
	"testing"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// Usage read from a CLI transcript is for the cost ledger: the store neither keeps nor publishes it, in either payload
// shape, while an ordinary model-call event is kept.
func TestUsageOnlyEventsNeverReachTheTranscript(t *testing.T) {
	store := NewEventStore(100)
	typed := Event{ID: "usage-typed", Type: "llm_generation_end", Data: &pkgevents.AgentEvent{
		Type: pkgevents.LLMGenerationEnd,
		Data: &pkgevents.LLMGenerationEndEvent{BaseEventData: pkgevents.BaseEventData{Metadata: map[string]interface{}{"usage_source": "retained"}}},
	}}
	generic := Event{ID: "usage-generic", Type: "llm_generation_end", Data: &pkgevents.AgentEvent{
		Type: pkgevents.LLMGenerationEnd,
		Data: NewGenericEventData("llm_generation_end", map[string]interface{}{"metadata": map[string]interface{}{"usage_source": "retained"}}),
	}}
	ordinary := Event{ID: "model-call", Type: "llm_generation_end", Data: &pkgevents.AgentEvent{
		Type: pkgevents.LLMGenerationEnd, Data: &pkgevents.LLMGenerationEndEvent{},
	}}
	for _, event := range []Event{typed, generic, ordinary} {
		store.AddEvent("chat-1", event)
	}
	got := store.GetEvents("chat-1", GetEventsOptions{})
	if len(got.Events) != 1 || got.Events[0].ID != "model-call" {
		t.Fatalf("only the ordinary model-call event may be stored, got %+v", got.Events)
	}
}
