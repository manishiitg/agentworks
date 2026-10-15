package events

import (
	"testing"
	"time"

	mcpevents "github.com/manishiitg/mcpagent/events"
)

func phaseEvents(es *EventStore, sessionID string) []string {
	var out []string
	for _, e := range es.GetAllEventsRaw(sessionID) {
		if e.Type != turnPhaseEventType {
			continue
		}
		generic, _ := e.Data.Data.(*mcpevents.GenericEventData)
		out = append(out, generic.Data["phase"].(string)+": "+generic.Data["text"].(string))
	}
	return out
}

func add(es *EventStore, sessionID, typ string, data mcpevents.EventData) {
	ev := Event{ID: typ + time.Now().Format(time.RFC3339Nano), Type: typ, Timestamp: time.Now(), SessionID: sessionID}
	if data != nil {
		ev.Data = mcpevents.NewAgentEvent(data)
	}
	es.AddEvent(sessionID, ev)
}

// The owner's local Muse case: the launch sat on a screen, the retry notice came twice, and only then did output
// arrive. "Starting" must last until the agent produced something, a terminal-screen chunk must not end it, and the
// turn's end must clear it.
func TestTurnPhaseFollowsTheAgentNotTheClock(t *testing.T) {
	es := NewEventStore(100)
	defer es.Stop()
	const session = "turn-phase-session"
	add(es, session, "user_message", nil)
	add(es, session, "llm_generation_start", &mcpevents.LLMGenerationStartEvent{ModelID: "muse-wonder-1.0"})
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "Review plugin hooks", Source: "terminal"})
	add(es, session, "retry_attempt", mcpevents.NewRetryAttemptEvent(0, 0, 5, "muse-wonder-1.0", "muse-cli", "retry", false, 10*time.Second, "Muse did not start in 1m30s: it was showing a plugin hooks review screen - retrying original model"))
	add(es, session, "llm_generation_start", &mcpevents.LLMGenerationStartEvent{ModelID: "muse-wonder-1.0"})
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "Hello", Source: "transcript"})
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "again", Source: "transcript"})
	add(es, session, "unified_completion", nil)
	add(es, session, "user_message", nil)

	want := []string{
		"starting: Starting Muse",
		"retrying: Retrying: Muse did not start in 1m30s: it was showing a plugin hooks review screen (attempt 1 of 5, next in 10s)",
		"starting: Starting Muse",
		"running: ",
		"starting: Starting Muse",
	}
	got := phaseEvents(es, session)
	if len(got) != len(want) {
		t.Fatalf("phases = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("phase %d = %q, want %q", i, got[i], want[i])
		}
	}
}
