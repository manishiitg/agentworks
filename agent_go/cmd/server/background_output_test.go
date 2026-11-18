package server

import (
	"path/filepath"
	"strings"
	"testing"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3131: a background command's live output lands on its card as one current
// tail (older live chunks are replaced, not piled up), offers Stop while it
// runs, and only its last tail is stored when its card has no other result.
func TestBackgroundOutputShowsBoundedTailAndStoresOnlyTheEnd(t *testing.T) {
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	store.SetSessionOwner("chat", "person")
	if err = store.SetSessionPersistenceClass("chat", storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{eventStore: store}
	out := func(text string, done bool) {
		api.recordBackgroundOutput(llmtypes.BackgroundOutput{Provider: "claude-code", Owner: "chat", ToolCallID: "toolu_1", TaskID: "bash_1", Text: text, CanStop: !done, Done: done})
	}
	out("ready on port 3000\n", false)
	out(strings.Repeat("x", backgroundOutputTailBytes), false)
	out("GET / 200\n", false)
	// Another chat's or an unknown chat's output never lands here.
	api.recordBackgroundOutput(llmtypes.BackgroundOutput{Provider: "claude-code", Owner: "other", ToolCallID: "toolu_1", Text: "not ours"})

	var live []map[string]interface{}
	for _, event := range store.GetAllEventsRaw("chat") {
		if generic, ok := event.Data.Data.(*storeevents.GenericEventData); ok && event.Type == storeevents.ToolCallOutputEventType {
			live = append(live, generic.Fields)
		}
	}
	if len(live) != 1 {
		t.Fatalf("live output events kept = %d, want the newest only", len(live))
	}
	shown := live[0]["output"].(string)
	if len(shown) > backgroundOutputTailBytes || !strings.HasSuffix(shown, "GET / 200\n") || live[0]["dropped"] != true || live[0]["stop_id"] != "claude-task:bash_1" {
		t.Fatalf("live output = %d bytes dropped=%v stop=%v", len(shown), live[0]["dropped"], live[0]["stop_id"])
	}
	page, err := store.ReadDurableChatPage("chat", storeevents.DurableEventPageOptions{Limit: 50, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("live output was stored: %d rows", len(page.Events))
	}

	out("bye\n", true)
	page, err = store.ReadDurableChatPage("chat", storeevents.DurableEventPageOptions{Limit: 50, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Type != storeevents.ToolCallOutputEventType || !strings.Contains(mustJSON(t, page.Events[0]), `bye\n`) {
		t.Fatalf("stored rows %+v", page.Events)
	}
	if backgroundOutputs.tails["chat\x00toolu_1"] != nil {
		t.Fatal("ended command's tail kept in memory")
	}
}
