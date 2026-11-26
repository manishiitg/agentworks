package events

import (
	"context"
	"testing"

	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-993: synthetic prompts keep their agent input, but their durable UI
// row is explicitly marked; ordinary/person/steered messages are never guessed.
func TestBackgroundWakeObserverMarksOnlyExactSyntheticInput(t *testing.T) {
	store := NewEventStore(20)
	defer store.Stop()
	observer := NewEventObserver(store, "wake-chat")
	const prompt = "internal continuation prompt"
	ctx := WithBackgroundWakePresentation(context.Background(), prompt, BackgroundWakePresentation{Kind: "background_command_finished", Failed: true, Output: "failed output"})
	for i, tc := range []struct {
		ctx    context.Context
		text   string
		client bool
		marked bool
	}{
		{ctx, prompt, false, true}, {context.Background(), prompt, false, false}, {ctx, "a new person message", false, false}, {ctx, prompt, true, false},
	} {
		message := agentevents.NewUserMessageEvent(i, tc.text, "user")
		if tc.client {
			message.Metadata = map[string]interface{}{"client_message_id": "person-input"}
		}
		original := agentevents.NewAgentEvent(message)
		if err := observer.HandleEvent(tc.ctx, original); err != nil {
			t.Fatal(err)
		}
		rows := store.GetAllEventsRaw("wake-chat")
		got := rows[len(rows)-1].Data.Data.(*agentevents.UserMessageEvent)
		marked := got.Metadata["system_notice_kind"] == "background_command_finished"
		if marked != tc.marked || got.Content != tc.text {
			t.Fatalf("case %d: content=%q metadata=%v", i, got.Content, got.Metadata)
		}
		if tc.marked && (got.Metadata["background_failed"] != true || got.Metadata["background_output"] != "failed output") {
			t.Fatalf("missing result: %v", got.Metadata)
		}
		if message.Metadata["system_notice_kind"] != nil {
			t.Fatal("mutated agent history event")
		}
	}
}
