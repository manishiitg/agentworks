package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
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
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "sample-cost.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	cost := newCostObserver(ledger, "chat", "caller", "simple", withCostModel("codex-cli", "parent-model"), withCostAccount("admitted-account"), withCostAttribution("crew", "", "", "admitted-execution"))
	addInput("input-a", "turn-a")
	ctx := f.api.withNextPrompt(context.Background(), "chat", "codex-cli", "turn-a", true, cost)
	scope := llmtypes.NextPromptFor(ctx, "codex-cli", "chat")
	scope.Finish(llmtypes.NextPromptResult{Text: "Check the diff"})
	rows := store.GetAllEventsRaw("chat")
	if len(rows) != 2 || rows[1].Type != "next_prompt" || storeevents.IsDurableChatEvent(rows[1]) {
		t.Fatalf("prediction became conversation history: %+v", rows)
	}
	stale := f.api.withNextPrompt(context.Background(), "chat", "codex-cli", "turn-a", true, cost)
	addInput("input-b", "turn-b")
	llmtypes.NextPromptFor(stale, "codex-cli", "chat").Finish(llmtypes.NextPromptResult{Text: "Old prediction", NativeThread: "native-parent", NativeTurn: "native-turn", Usage: &llmtypes.NextPromptUsage{ResponseID: "sample-response", Model: "native-model", Input: 100, CachedInput: 80, Output: 5}})
	if len(store.GetAllEventsRaw("chat")) != 3 {
		t.Fatal("old prediction survived a newer input")
	}
	var entries []costledger.Entry
	if err := ledger.VisitEntries(context.Background(), time.Now().UTC().Format("2006-01-02"), time.Now().UTC().Format("2006-01-02"), func(e costledger.Entry) error { entries = append(entries, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("stale hint's paid sample missing: %+v", entries)
	}
	e := entries[0]
	if e.UserID != "caller" || e.SessionID != "chat" || e.AccountID != "admitted-account" || e.ExecutionID != "admitted-execution" || e.TurnCount != 0 || e.LLMCallCount != 1 || e.PromptTokens != 100 || e.CacheReadTokens != 80 || e.CompletionTokens != 5 || e.EffectiveModelID != "native-model" || e.Component != "native_internal_task" {
		t.Fatalf("sample lost admitted attribution or became a turn: %+v", e)
	}
}
