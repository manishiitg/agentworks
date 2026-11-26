package events

import (
	"fmt"
	agentevents "github.com/manishiitg/mcpagent/events"
	"strings"
	"testing"
	"time"
)

// PLAT-746-838: the reported reconnect lost exactly 45 packets / 657 bytes.
// Replay must start at packet 1 and fence packets also queued on the live subscription.
func TestStreamingPrefixRestoresMissingTokensWithoutRepeatingQueuedPackets(t *testing.T) {
	store := NewEventStore(200)
	defer store.Stop()
	const sid = "chat-reconnect"
	add := func(id, kind string, data agentevents.EventData) {
		t.Helper()
		store.AddEvent(sid, Event{ID: id, Type: kind, Timestamp: time.Now(), SessionID: sid, ExecutionKind: "main", Data: agentevents.NewAgentEvent(data)})
	}
	add("start", "streaming_start", &agentevents.StreamingStartEvent{})
	for i := 1; i <= 44; i++ {
		add(fmt.Sprint("chunk-", i), "streaming_chunk", &agentevents.StreamingChunkEvent{Content: strings.Repeat("a", 14), ChunkIndex: i, Source: "content", IsDelta: true})
	}
	sub := store.Subscribe(sid)
	defer store.Unsubscribe(sid, sub)
	// This packet enters both the replay snapshot and the subscriber queue.
	add("chunk-45", "streaming_chunk", &agentevents.StreamingChunkEvent{Content: strings.Repeat("b", 41), ChunkIndex: 45, Source: "content", IsDelta: true})
	prefix, covered := store.StreamingPrefix(sid)
	if len(prefix) != 46 || prefix[0].Type != "streaming_start" || StreamingPrefixBytes(prefix) != 657 {
		t.Fatalf("prefix events=%d bytes=%d", len(prefix), StreamingPrefixBytes(prefix))
	}
	queued := <-sub.Ch
	if !covered[queued.ID] {
		t.Fatal("queued packet would be appended twice")
	}
	add("chunk-46", "streaming_chunk", &agentevents.StreamingChunkEvent{Content: "remaining text", ChunkIndex: 46, Source: "content", IsDelta: true})
	if next := <-sub.Ch; covered[next.ID] {
		t.Fatal("new packet was suppressed")
	}
	if other, _ := store.StreamingPrefix("another-chat"); len(other) != 0 {
		t.Fatal("another chat received this prefix")
	}
	add("end", "streaming_end", &agentevents.StreamingEndEvent{})
	if ended, ids := store.StreamingPrefix(sid); len(ended) != 0 || len(ids) != 0 {
		t.Fatal("completed stream replayed")
	}
}

// A pruned prefix must not be presented as a complete snapshot, and the
// unsupported snapshot must not suppress the ordinary live queue either.
func TestStreamingPrefixDoesNotClaimPrunedOrChildTokens(t *testing.T) {
	store := NewEventStore(200)
	defer store.Stop()
	const sid = "chat"
	mk := func(id, kind string, index int, owner string) Event {
		var data agentevents.EventData = &agentevents.StreamingStartEvent{}
		if kind == "streaming_chunk" {
			data = &agentevents.StreamingChunkEvent{Content: "part", ChunkIndex: index, Source: "content", IsDelta: true}
		}
		return Event{ID: id, Type: kind, SessionID: sid, TerminalOwnerID: owner, TerminalID: TerminalIDForOwner(sid, owner), Data: agentevents.NewAgentEvent(data)}
	}
	store.events[sid] = []Event{mk("start", "streaming_start", 0, "main:"+sid), mk("missing-first", "streaming_chunk", 2, "main:"+sid)}
	if prefix, covered := store.StreamingPrefix(sid); len(prefix) != 0 || len(covered) != 0 {
		t.Fatal("incomplete stream claimed as replayable")
	}
	store.events[sid] = []Event{mk("child-start", "streaming_start", 0, "child"), mk("child-piece", "streaming_chunk", 1, "child")}
	if prefix, covered := store.StreamingPrefix(sid); len(prefix) != 0 || len(covered) != 0 {
		t.Fatal("child stream leaked into main replay")
	}
}
