package events

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
)

type nativeSettleCountingJournal struct {
	*SQLiteEventJournal
	tailReads int
}

func (j *nativeSettleCountingJournal) LoadTail(session string, limit int) ([]Event, error) {
	j.tailReads++
	return j.SQLiteEventJournal.LoadTail(session, limit)
}

// PLAT-746-876: losing a native runtime must settle its unresolved command,
// not hydrate an unopened chat's large history. The ordinary durable append,
// sequence, ownership, callbacks and subscriber notification must survive.
func TestNativeRuntimeSettlementDoesNotHydrateHistory(t *testing.T) {
	journal, err := OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	counted := &nativeSettleCountingJournal{SQLiteEventJournal: journal}
	store := NewEventStore(100)
	store.SetDurableJournal(counted)
	defer store.Stop()
	const session = "native-cold-chat"
	appendSaved := func(chat string, event Event) {
		t.Helper()
		if event.ID == "" {
			event.ID = StableAgentEventID("seed:"+chat, event.Data)
		}
		event.ensureExecutionOwnership(chat, nil)
		if _, inserted, err := journal.Append(chat, event); err != nil || !inserted {
			t.Fatalf("seed: %t %v", inserted, err)
		}
	}
	appendSaved(session, journalTestEvent("large-message", strings.Repeat("saved context ", 1<<16)))
	appendSaved(session, startEvent(session, "exec-open", "exec_command"))
	appendSaved(session, startEvent(session, "exec-done", "exec_command"))
	appendSaved(session, Event{ID: "done", Type: "tool_call_end", Timestamp: time.Now(), Data: &agentevents.AgentEvent{Type: agentevents.ToolCallEnd, Data: &agentevents.ToolCallEndEvent{ToolName: "exec_command", ToolCallID: "exec-done", Result: "done"}}})
	appendSaved(session, startEvent(session, "exec-error", "exec_command"))
	appendSaved(session, Event{ID: "failed", Type: "tool_call_error", Timestamp: time.Now(), Data: &agentevents.AgentEvent{Type: agentevents.ToolCallError, Data: &agentevents.ToolCallErrorEvent{ToolName: "exec_command", ToolCallID: "exec-error", Error: "failed"}}})
	appendSaved(session, startEvent(session, "ordinary", "other_tool"))
	appendSaved("another-chat", startEvent("another-chat", "exec-foreign", "exec_command"))
	var prepared, added, active atomic.Int64
	store.SetEventPreparer(func(_ string, _ *Event) { prepared.Add(1) })
	store.SetEventAddedCallback(func(_ string, _ Event) { added.Add(1) })
	store.SetActivityCallback(func(_ string) { active.Add(1) })
	sub := store.Subscribe(session)
	defer store.Unsubscribe(session, sub)
	store.EndNativeCommandsWithoutResult(session)
	store.EndNativeCommandsWithoutResult(session)
	if counted.tailReads != 0 || store.hydratedSessions[session] || len(store.events[session]) != 1 {
		t.Fatalf("settlement loaded history: reads=%d hydrated=%t events=%d", counted.tailReads, store.hydratedSessions[session], len(store.events[session]))
	}
	if prepared.Load() != 1 || added.Load() != 1 || active.Load() != 1 {
		t.Fatalf("callbacks: prepare=%d added=%d active=%d", prepared.Load(), added.Load(), active.Load())
	}
	select {
	case end := <-sub.Ch:
		if end.ID != "native-command-unavailable:exec-open" || end.Sequence != 8 || end.ExecutionID != "main:"+session || end.ExecutionKind != "main_agent" {
			t.Fatalf("wrong native end identity: %+v", end)
		}
		data, ok := end.Data.Data.(*agentevents.ToolCallEndEvent)
		if !ok || !data.SyntheticSettle || data.Result != "No result was captured before the command's runtime ended." {
			t.Fatal("missing neutral command result")
		}
	default:
		t.Fatal("native end was not delivered to subscriber")
	}
	// Opening the chat later still reads its complete saved replay exactly once.
	result := store.GetEvents(session, GetEventsOptions{SinceIndex: -1})
	if len(result.Events) != 8 || counted.tailReads != 1 {
		t.Fatalf("later history replay lost events: events=%d loads=%d", len(result.Events), counted.tailReads)
	}
	ends := 0
	for _, event := range result.Events {
		if event.ID == "native-command-unavailable:exec-open" {
			ends++
		}
	}
	if ends != 1 {
		t.Fatalf("duplicate neutral ends: %d", ends)
	}
	// The cold no-replay end and an ordinary producer share one append order.
	// Ordinary AddEvent must still hydrate, with neither insertion overwritten.
	const racing = "native-racing-chat"
	appendSaved(racing, journalTestEvent("race-context", "saved context"))
	appendSaved(racing, startEvent(racing, "exec-race", "exec_command"))
	start := make(chan struct{})
	errs := make(chan error, 1)
	var writers sync.WaitGroup
	writers.Add(2)
	go func() { defer writers.Done(); <-start; store.EndNativeCommandsWithoutResult(racing) }()
	go func() {
		defer writers.Done()
		<-start
		errs <- store.AddEventChecked(racing, journalTestEvent("concurrent-message", "new message"))
	}()
	close(start)
	writers.Wait()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	for _, replay := range [][]Event{store.GetEvents(racing, GetEventsOptions{SinceIndex: -1}).Events, func() []Event {
		saved, err := journal.LoadTail(racing, 100)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}()} {
		if len(replay) != 4 {
			t.Fatalf("racing append lost replay: %d", len(replay))
		}
		previous := int64(0)
		ids := map[string]bool{}
		for _, event := range replay {
			if event.Sequence <= previous {
				t.Fatalf("non-increasing sequence: %d after %d", event.Sequence, previous)
			}
			previous = event.Sequence
			ids[event.ID] = true
		}
		if !ids["concurrent-message"] || !ids["native-command-unavailable:exec-race"] {
			t.Fatal("missing concurrent producer or neutral end")
		}
	}

}
