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
	es.EnableTurnPhase()
	const session = "turn-phase-session"
	add(es, session, "user_message", nil)
	add(es, session, "llm_generation_start", &mcpevents.LLMGenerationStartEvent{ModelID: "muse-wonder-1.0"})
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "Review plugin hooks", Source: "terminal"})
	add(es, session, "retry_attempt", mcpevents.NewRetryAttemptEvent(0, 0, 5, "muse-wonder-1.0", "muse-cli", "retry", false, 10*time.Second, "Muse did not start in 1m30s: it was showing a plugin hooks review screen - retrying original model"))
	add(es, session, "llm_generation_start", &mcpevents.LLMGenerationStartEvent{ModelID: "muse-wonder-1.0"})
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "Hello", Source: "transcript"})
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "again", Source: "transcript"})
	add(es, session, "unified_completion", nil)
	// A quiet CLI that took the prompt is running even before its first word.
	add(es, session, "user_message", nil)
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Source: mcpevents.StreamingChunkSourcePromptAccepted})

	// A model family cannot identify the CLI (Cursor can run Claude models). Startup stays neutral.
	want := []string{
		"starting: Starting the agent",
		"retrying: Retrying: Muse did not start in 1m30s: it was showing a plugin hooks review screen (attempt 1 of 5, next in 10s)",
		"starting: Starting the agent",
		"running: ",
		"starting: Starting the agent",
		"running: ",
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

// After a restart the live turn state is gone; the stored events decide whether a turn is still open (PLAT-861-228).
func TestPersistedTurnOpenFollowsTheStoredEvents(t *testing.T) {
	es := NewEventStore(100)
	defer es.Stop()
	const session = "stored-turn-session"
	if es.PersistedTurnOpen(session) {
		t.Fatal("an empty session has no open turn")
	}
	add(es, session, "user_message", nil)
	add(es, session, "streaming_chunk", &mcpevents.StreamingChunkEvent{Content: "partial", Source: "transcript"})
	if !es.PersistedTurnOpen(session) {
		t.Fatal("a message with no end is an open turn")
	}
	add(es, session, "unified_completion", nil)
	if es.PersistedTurnOpen(session) {
		t.Fatal("a completed turn is not open")
	}
	add(es, session, "user_message", nil)
	if !es.PersistedTurnOpen(session) {
		t.Fatal("a new message opens the next turn")
	}
}

func TestStartingUsesOnlyThisTurnsServerAgentLabel(t *testing.T) {
	es := NewEventStore(100)
	defer es.Stop()
	es.EnableTurnPhase()
	const session = "cursor-with-claude-model"
	message := mcpevents.NewUserMessageEvent(0, "Check this", "user")
	message.Metadata = map[string]interface{}{"provider": "cursor-cli", "agent_label": "Cursor"}
	add(es, session, "user_message", message)
	add(es, session, "llm_generation_start", &mcpevents.LLMGenerationStartEvent{ModelID: "claude-sonnet-5-5"})
	add(es, session, "unified_completion", nil)
	add(es, session, "user_message", nil)
	add(es, session, "llm_generation_start", &mcpevents.LLMGenerationStartEvent{ModelID: "claude-sonnet-5-5"})
	got := phaseEvents(es, session)
	if len(got) != 2 || got[0] != "starting: Starting Cursor" || got[1] != "starting: Starting the agent" {
		t.Fatalf("model inference or cached name survived: %q", got)
	}
}
