package events

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

type failingEventJournal struct{}

func (failingEventJournal) Append(string, Event) (Event, bool, error) {
	return Event{}, false, errors.New("disk unavailable")
}
func (failingEventJournal) LoadTail(string, int) ([]Event, error) { return []Event{}, nil }
func (failingEventJournal) Close() error                          { return nil }

func journalTestEvent(id, content string) Event {
	message := agentevents.NewUserMessageEvent(1, content, "user")
	return Event{
		ID: id, Type: string(agentevents.UserMessage), Timestamp: time.Now(),
		Data: agentevents.NewAgentEvent(message),
	}
}

func streamingTestEvent(id, content string) Event {
	chunk := &agentevents.StreamingChunkEvent{Content: content, Source: "llm"}
	return Event{
		ID: id, Type: string(agentevents.StreamingChunk), Timestamp: time.Now(),
		Data: agentevents.NewAgentEvent(chunk),
	}
}

func classifyInteractiveTestSession(t *testing.T, store *EventStore, sessionID string) {
	t.Helper()
	if err := store.SetSessionPersistenceClass(sessionID, SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
}

func journalEventIDs(events []Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.ID)
	}
	return result
}

func TestSQLiteEventJournalAssignsSequenceAndDeduplicates(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	first, inserted, err := journal.Append("session-1", journalTestEvent("event-1", "one"))
	if err != nil || !inserted || first.Sequence != 1 {
		t.Fatalf("first append = seq %d inserted %v err %v", first.Sequence, inserted, err)
	}
	replayed, inserted, err := journal.Append("session-1", journalTestEvent("event-1", "changed"))
	if err != nil || inserted || replayed.Sequence != 1 {
		t.Fatalf("duplicate append = seq %d inserted %v err %v", replayed.Sequence, inserted, err)
	}
	second, inserted, err := journal.Append("session-1", journalTestEvent("event-2", "two"))
	if err != nil || !inserted || second.Sequence != 2 {
		t.Fatalf("second append = seq %d inserted %v err %v", second.Sequence, inserted, err)
	}
	loaded, err := journal.LoadTail("session-1", 10)
	if err != nil || len(loaded) != 2 || loaded[0].ID != "event-1" || loaded[1].ID != "event-2" {
		t.Fatalf("loaded events = %+v err=%v", loaded, err)
	}
}

func TestMuseBackgroundTaskPersistsAfterForegroundCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(20)
	store.SetDurableJournal(journal)
	const sessionID = "chat-muse-background"
	classifyInteractiveTestSession(t, store, sessionID)
	if err := store.AddEventChecked(sessionID, Event{ID: "foreground", Type: "unified_completion", Timestamp: time.Now(), SessionID: sessionID,
		Data: &agentevents.AgentEvent{Type: agentevents.EventTypeUnifiedCompletion, Timestamp: time.Now(), SessionID: sessionID}}); err != nil {
		t.Fatal(err)
	}
	native := &agentevents.AgentEvent{Type: agentevents.CodingAgentBackgroundTask, Timestamp: time.Now(), SessionID: sessionID,
		Data: &agentevents.CodingAgentBackgroundTaskEvent{BaseEventData: agentevents.BaseEventData{EventID: "muse:native:3"}, Provider: "muse-cli", NativeSessionID: "native", RunID: "run", TaskID: "task", NativeSequence: 3, Kind: "completed"}}
	late := Event{ID: StableAgentEventID("bridge", native), Type: string(native.Type), Timestamp: time.Now(), SessionID: sessionID, Data: native}
	if err := store.AddEventChecked(sessionID, late); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEventChecked(sessionID, late); err != nil {
		t.Fatal(err)
	}
	store.Stop()
	journal.Close()
	reopened, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := reopened.ReadPage(sessionID, DurableEventPageOptions{Limit: 10, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].Type != "unified_completion" || page.Events[1].Type != string(agentevents.CodingAgentBackgroundTask) {
		t.Fatalf("durable events=%v", journalEventIDs(page.Events))
	}
}

func TestSQLiteEventJournalForwardPagesStartAtFirstRow(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for i := 1; i <= 5; i++ {
		if _, _, err := journal.Append("chat", journalTestEvent(fmt.Sprintf("event-%d", i), "text")); err != nil {
			t.Fatal(err)
		}
	}
	first, err := journal.ReadPage("chat", DurableEventPageOptions{Limit: 2, FromStart: true})
	if err != nil || len(first.Events) != 2 || first.Events[0].ID != "event-1" || first.Events[1].ID != "event-2" || !first.HasNewer {
		t.Fatalf("first forward page = %+v err=%v", first, err)
	}
	second, err := journal.ReadPage("chat", DurableEventPageOptions{Limit: 2, AfterSequence: first.LatestSequence})
	if err != nil || len(second.Events) != 2 || second.Events[0].ID != "event-3" || second.Events[1].ID != "event-4" || !second.HasNewer {
		t.Fatalf("second forward page = %+v err=%v", second, err)
	}
}

func TestOnlyInteractiveChatSessionsUseDurableJournal(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)

	if err := store.SetSessionPersistenceClass("execution-1", SessionPersistenceExecution); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEventChecked("execution-1", journalTestEvent("execution-event", "run")); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEventChecked("unknown-1", journalTestEvent("unknown-event", "unknown")); err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{"execution-1", "unknown-1"} {
		persisted, err := journal.LoadTail(sessionID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(persisted) != 0 {
			t.Fatalf("%s persisted %d live-only events", sessionID, len(persisted))
		}
		if got := store.GetAllEventsRaw(sessionID); len(got) != 1 {
			t.Fatalf("%s live events = %d, want 1", sessionID, len(got))
		}
	}
}

func TestLiveInputConfirmationSurvivesChatJournalRestore(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(10)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "chat")
	event := Event{ID: "message-1:confirmed", Type: "live_input_confirmed", Timestamp: time.Now()}
	if err := store.AddEventChecked("chat", event); err != nil {
		t.Fatal(err)
	}
	page, err := store.ReadDurableChatPage("chat", DurableEventPageOptions{Limit: 10})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != event.ID {
		t.Fatalf("confirmation was not durable: %+v err=%v", page, err)
	}
}

func TestSessionPersistenceClassificationIsImmutableAndPrecedesEvents(t *testing.T) {
	store := NewEventStore(100)
	defer store.Stop()
	if err := store.SetSessionPersistenceClass("chat-1", SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	// An automated (cron/webhook) turn into an interactive chat keeps the
	// chat's class instead of failing the request.
	if err := store.SetSessionPersistenceClass("chat-1", SessionPersistenceExecution); err != nil {
		t.Fatalf("automated turn into interactive chat was refused: %v", err)
	}
	if !store.IsDurableChatSession("chat-1") {
		t.Fatal("interactive chat was downgraded by a later execution request")
	}
	if err := store.SetSessionPersistenceClass("run-1", SessionPersistenceExecution); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionPersistenceClass("run-1", SessionPersistenceInteractiveChat); err == nil {
		t.Fatal("execution session was promoted to interactive chat")
	}
	store.AddEvent("unknown-1", journalTestEvent("event-1", "live"))
	if err := store.SetSessionPersistenceClass("unknown-1", SessionPersistenceInteractiveChat); err == nil {
		t.Fatal("late persistence classification unexpectedly succeeded")
	}
	store.AddEvent("execution-1", journalTestEvent("event-2", "live execution"))
	if err := store.SetSessionPersistenceClass("execution-1", SessionPersistenceExecution); err != nil {
		t.Fatalf("late live-only classification failed: %v", err)
	}
}

func TestStreamingChunksStayLiveOnlyAndBoundaryPreservesSequenceGap(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "session-1")
	for _, id := range []string{"chunk-1", "chunk-2", "chunk-3"} {
		if err := store.AddEventChecked("session-1", streamingTestEvent(id, id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AddEventChecked("session-1", journalTestEvent("message-1", "boundary")); err != nil {
		t.Fatal(err)
	}
	if got := store.GetAllEventsRaw("session-1"); len(got) != 4 || got[3].Sequence != 4 {
		t.Fatalf("live sequence = %+v", got)
	}
	persisted, err := journal.LoadTail("session-1", 10)
	if err != nil || len(persisted) != 1 || persisted[0].ID != "message-1" || persisted[0].Sequence != 4 {
		t.Fatalf("durable boundaries = %+v err=%v", persisted, err)
	}
}

func TestSQLiteEventJournalReportsSizeAndCount(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if _, _, err := journal.Append("session-1", journalTestEvent("event-1", "one")); err != nil {
		t.Fatal(err)
	}
	stats, err := journal.Stats()
	if err != nil || stats.Events != 1 || stats.SizeBytes <= 0 || stats.Path == "" {
		t.Fatalf("stats = %+v err=%v", stats, err)
	}
}

func TestSQLiteEventJournalPersistsOwnerAndDeletesAllSessionState(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.RegisterOwner("chat-1", "alice"); err != nil {
		t.Fatal(err)
	}
	if owner, err := journal.Owner("chat-1"); err != nil || owner != "alice" {
		t.Fatalf("owner = %q err=%v", owner, err)
	}
	if err := journal.RegisterOwner("chat-1", "bob"); err == nil {
		t.Fatal("owner overwrite was accepted")
	}
	if _, _, err := journal.Append("chat-1", journalTestEvent("event-1", "one")); err != nil {
		t.Fatal(err)
	}
	if err := journal.MarkMigrationComplete("chat-1"); err != nil {
		t.Fatal(err)
	}
	if err := journal.DeleteSession("chat-1"); err != nil {
		t.Fatal(err)
	}
	if owner, _ := journal.Owner("chat-1"); owner != "" {
		t.Fatalf("owner survived deletion: %q", owner)
	}
	if complete, _ := journal.MigrationComplete("chat-1"); complete {
		t.Fatal("migration marker survived deletion")
	}
}

func TestEventStoreSuppliesStableIdentityForLegacyEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "session-1")
	event := journalTestEvent("", "legacy")
	if err := store.AddEventChecked("session-1", event); err != nil {
		t.Fatalf("legacy event without an ID was rejected: %v", err)
	}
	if err := store.AddEventChecked("session-1", event); err != nil {
		t.Fatalf("legacy replay failed: %v", err)
	}
	stored := store.GetAllEventsRaw("session-1")
	if len(stored) != 1 || stored[0].ID == "" {
		t.Fatalf("legacy replay was not identified exactly once: %+v", stored)
	}
}

func TestEventStoreRestoresDurableEventsAndContinuesSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	firstStore := NewEventStore(100)
	firstStore.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, firstStore, "session-1")
	firstStore.AddEvent("session-1", journalTestEvent("event-1", "one"))
	firstStore.AddEvent("session-1", journalTestEvent("event-2", "two"))
	firstStore.Stop()

	reopened, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	secondStore := NewEventStore(100)
	defer secondStore.Stop()
	secondStore.SetDurableJournal(reopened)
	classifyInteractiveTestSession(t, secondStore, "session-1")
	restored := secondStore.GetEvents("session-1", GetEventsOptions{SinceIndex: -1, IncludeStreaming: true}).Events
	if len(restored) != 2 || restored[0].Sequence != 1 || restored[1].Sequence != 2 {
		t.Fatalf("restored = %+v", restored)
	}
	secondStore.AddEvent("session-1", journalTestEvent("event-3", "three"))
	all := secondStore.GetAllEventsRaw("session-1")
	if len(all) != 3 || all[2].Sequence != 3 {
		t.Fatalf("continued sequence = %+v", all)
	}
	secondStore.AddEvent("session-1", journalTestEvent("event-2", "duplicate"))
	if got := len(secondStore.GetAllEventsRaw("session-1")); got != 3 {
		t.Fatalf("duplicate durable event was published again: %d", got)
	}
}

func TestSQLiteEventJournalTailIsChronologicalAndBounded(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for _, id := range []string{"one", "two", "three"} {
		if _, _, err := journal.Append("session-1", journalTestEvent(id, id)); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := journal.LoadTail("session-1", 2)
	if err != nil || len(loaded) != 2 || loaded[0].ID != "two" || loaded[1].ID != "three" {
		t.Fatalf("bounded tail = %+v err=%v", loaded, err)
	}
}

func TestSQLiteEventJournalReadsStableSequencePages(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for index, id := range []string{"one", "two", "three", "four", "five"} {
		event := journalTestEvent(id, id)
		event.Sequence = int64(index + 1)
		if _, _, err := journal.Append("session-1", event); err != nil {
			t.Fatal(err)
		}
	}

	tail, err := journal.ReadPage("session-1", DurableEventPageOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := journalEventIDs(tail.Events); !reflect.DeepEqual(got, []string{"four", "five"}) || !tail.HasOlder || tail.HasNewer || tail.OldestSequence != 4 || tail.LatestSequence != 5 {
		t.Fatalf("tail page = %+v ids=%v", tail, got)
	}
	older, err := journal.ReadPage("session-1", DurableEventPageOptions{Limit: 2, BeforeSequence: tail.OldestSequence})
	if err != nil {
		t.Fatal(err)
	}
	if got := journalEventIDs(older.Events); !reflect.DeepEqual(got, []string{"two", "three"}) || !older.HasOlder || !older.HasNewer {
		t.Fatalf("older page = %+v ids=%v", older, got)
	}
	newer, err := journal.ReadPage("session-1", DurableEventPageOptions{Limit: 2, AfterSequence: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := journalEventIDs(newer.Events); !reflect.DeepEqual(got, []string{"three", "four"}) || !newer.HasNewer {
		t.Fatalf("newer page = %+v ids=%v", newer, got)
	}
	beyondTip, err := journal.ReadPage("session-1", DurableEventPageOptions{Limit: 2, AfterSequence: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(beyondTip.Events) != 0 || beyondTip.JournalLatestSequence != 5 {
		t.Fatalf("beyond-tip page = %+v", beyondTip)
	}
}

func TestSQLiteEventJournalDeletesConversationRows(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if _, _, err := journal.Append("session-1", journalTestEvent("one", "one")); err != nil {
		t.Fatal(err)
	}
	if err := journal.DeleteSession("session-1"); err != nil {
		t.Fatal(err)
	}
	page, err := journal.ReadPage("session-1", DurableEventPageOptions{Limit: 10})
	if err != nil || page.Exists || len(page.Events) != 0 {
		t.Fatalf("deleted page = %+v err=%v", page, err)
	}
	reinserted, inserted, err := journal.Append("session-1", journalTestEvent("two", "two"))
	if err != nil || !inserted || reinserted.Sequence != 1 {
		t.Fatalf("reinsert after delete = %+v inserted=%v err=%v", reinserted, inserted, err)
	}
}

func TestEventStoreDoesNotPublishBeforeDurableAppend(t *testing.T) {
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(failingEventJournal{})
	classifyInteractiveTestSession(t, store, "session-1")
	subscriber := store.Subscribe("session-1")
	defer store.Unsubscribe("session-1", subscriber)
	if err := store.AddEventChecked("session-1", journalTestEvent("event-1", "one")); err == nil {
		t.Fatal("durable append failure was hidden")
	}
	if got := len(store.GetAllEventsRaw("session-1")); got != 0 {
		t.Fatalf("non-durable event entered memory: %d", got)
	}
	select {
	case event := <-subscriber.Ch:
		t.Fatalf("non-durable event was published over SSE: %+v", event)
	default:
	}
}

func TestSessionStatusDoesNotHydrateAndRemovalDoesNotResurrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	first := NewEventStore(100)
	first.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, first, "session-1")
	first.AddEvent("session-1", journalTestEvent("event-1", "one"))
	first.Stop()

	reopened, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(100)
	defer store.Stop()
	store.SetDurableJournal(reopened)
	classifyInteractiveTestSession(t, store, "session-1")
	if count, exists := store.GetSessionStatus("session-1"); exists || count != 0 {
		t.Fatalf("status unexpectedly hydrated durable history: count=%d exists=%v", count, exists)
	}
	if got := store.GetEvents("session-1", GetEventsOptions{SinceIndex: -1}).Events; len(got) != 1 {
		t.Fatalf("explicit event restore = %+v", got)
	}
	store.RemoveSession("session-1")
	result := store.GetEvents("session-1", GetEventsOptions{SinceIndex: -1})
	if result.Exists || len(result.Events) != 0 {
		t.Fatalf("removed session resurrected: %+v", result)
	}
	store.AddEvent("session-1", journalTestEvent("event-2", "two"))
	if got := store.GetAllEventsRaw("session-1"); len(got) != 1 || got[0].ID != "event-2" {
		t.Fatalf("new activity resurrected old tail: %+v", got)
	}
}

// PLAT-746-158 A4: failure policy must survive the SQLite journal unchanged,
// instead of being reclassified differently by each live/history UI.
func TestFailureDisplayMatchesLiveAndReopenedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite")
	journal, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	store := NewEventStore(20)
	defer store.Stop()
	store.SetDurableJournal(journal)
	classifyInteractiveTestSession(t, store, "failures")
	var live []Event
	store.SetEventAddedCallback(func(_ string, e Event) { live = append(live, e) })
	raw := "selected LLM failed: cursor-cli/model [billing_error]: Model unavailable on Plus."
	for i, data := range []agentevents.EventData{
		&agentevents.UnifiedCompletionEvent{Error: raw, Status: "error"},
		&agentevents.ConversationEndEvent{Error: raw, Status: "error"},
		&agentevents.ConversationErrorEvent{Error: raw},
		&agentevents.AgentErrorEvent{Error: raw},
		NewGenericEventData("conversation_error", map[string]interface{}{"error": raw}),
		&agentevents.ConversationErrorEvent{Error: "connection reset", BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
			"error_code": "delivery_uncertain", "provider_message": "Delivery is being checked.", "next_step": "Check the conversation.", "retryable": false,
		}}},
		&agentevents.ConversationErrorEvent{Error: raw, BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
			"error_code": "billing_error", "provider_message": "Provider's precise plan reason.", "retryable": false,
		}}},
	} {
		native := agentevents.NewAgentEvent(data)
		e := Event{ID: fmt.Sprintf("failure-%d", i), Type: string(native.Type), Timestamp: time.Now(), SessionID: "failures", Data: native}
		original := eventPayloadMap(&e)
		if err := store.AddEventChecked("failures", e); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(original, eventPayloadMap(&e)) {
			t.Fatal("mutated producer")
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteEventJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := reopened.ReadPage("failures", DurableEventPageOptions{Limit: 20, FromStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != len(live) || len(live) != 7 {
		t.Fatalf("live=%d history=%d", len(live), len(page.Events))
	}
	for i, e := range page.Events {
		stored := eventPayloadMap(&e)["metadata"]
		shown := eventPayloadMap(&live[i])["metadata"]
		if !reflect.DeepEqual(stored, shown) {
			t.Fatalf("live/history differ: %v / %v", shown, stored)
		}
		meta, _ := stored.(map[string]interface{})
		if i == 5 {
			if meta["error_code"] != "delivery_uncertain" || meta["retryable"] != false || meta["provider_message"] != "Delivery is being checked." {
				t.Fatalf("overwrote precise producer: %v", meta)
			}
			continue
		}
		if i == 6 && meta["provider_message"] != "Provider's precise plan reason." {
			t.Fatalf("overwrote producer reason: %v", meta)
		}
		if meta["error_code"] != "billing_error" || meta["retryable"] != false || meta["provider_message"] == "" || meta["next_step"] == "" {
			t.Fatalf("failure metadata: %v", meta)
		}
	}
}
