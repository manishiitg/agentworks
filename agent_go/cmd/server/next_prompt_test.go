package server

import (
	"context"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-992: predictions are not messages. A late native prediction cannot
// be offered after a newer typed/incoming/queued input has reached this chat.
func TestNextPromptPublishesOnlyForItsExactLatestInput(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	store := storeevents.NewEventStore(50)
	t.Cleanup(store.Stop)
	f.api.eventStore = store
	addInput := func(id, turn string) {
		event := agentevents.NewAgentEvent(agentevents.NewUserMessageEvent(0, "A person's input", "user"))
		event.TurnID = turn
		store.AddEvent("chat", storeevents.Event{ID: id, Type: "user_message", SessionID: "chat", Timestamp: time.Now(), Data: event})
	}
	addInput("input-a", "turn-a")
	ctx := f.api.withNextPrompt(context.Background(), "chat", "codex-cli", "turn-a", true)
	scope := llmtypes.NextPromptFor(ctx, "codex-cli", "chat")
	scope.Finish(llmtypes.NextPromptResult{Text: "Check the diff"})
	rows := store.GetAllEventsRaw("chat")
	if len(rows) != 2 || rows[1].Type != "next_prompt" || storeevents.IsDurableChatEvent(rows[1]) {
		t.Fatalf("prediction became conversation history: %+v", rows)
	}
	stale := f.api.withNextPrompt(context.Background(), "chat", "codex-cli", "turn-a", true)
	addInput("input-b", "turn-b")
	llmtypes.NextPromptFor(stale, "codex-cli", "chat").Finish(llmtypes.NextPromptResult{Text: "Old prediction"})
	if len(store.GetAllEventsRaw("chat")) != 3 {
		t.Fatal("old prediction survived a newer input")
	}
}
