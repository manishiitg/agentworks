package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-874: Stop must persist platform-received partial prose before clearing
// the transient stream; it must not label that prose as a final answer.
func TestStopSettlementPersistsPartialProseWithoutFinalAnswer(t *testing.T) {
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	store := storeevents.NewEventStore(200)
	defer store.Stop()
	store.SetDurableJournal(journal)
	const sid = "stopped-chat"
	if err := store.SetSessionPersistenceClass(sid, storeevents.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	add := func(id, kind string, data agentevents.EventData) {
		store.AddEvent(sid, storeevents.Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: sid, ExecutionKind: "main", Data: agentevents.NewAgentEvent(data)})
	}
	add("start", "streaming_start", &agentevents.StreamingStartEvent{})
	add("one", "streaming_chunk", &agentevents.StreamingChunkEvent{Content: "Received ", ChunkIndex: 1, Source: "content", IsDelta: true})
	add("two", "streaming_chunk", &agentevents.StreamingChunkEvent{Content: "partial text", ChunkIndex: 2, Source: "content", IsDelta: true})
	api := &StreamingAPI{eventStore: store}
	api.emitInterruptedTurnEnd(sid, "turn_context_canceled")
	if prefix, _ := store.StreamingPrefix(sid); len(prefix) != 0 {
		t.Fatal("terminal outcome left transient stream active")
	}
	page, err := journal.ReadCompactPage(sid, storeevents.CompactPageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var completions int
	for _, event := range page.Events {
		if event.Type != "unified_completion" {
			continue
		}
		completions++
		raw, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"partial_response":"Received partial text"`, `"stopped":true`, `"final_response_missing":true`, `"final_result":""`} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("durable Stop missing %s: %s", want, raw)
			}
		}
	}
	if completions != 1 {
		t.Fatalf("durable stop completions=%d", completions)
	}
}
