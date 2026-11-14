package server

import (
	"encoding/json"
	"testing"
)

// PLAT-746-876: trimming replay must release payloads, not just shorten the slice.
func TestRelayCacheCapReleasesDroppedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count, size int
	}{
		{"batch_cap", 1000, 1},
		{"byte_cap", 2, 4 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := &crewRelayStreamSet{streams: map[string]*crewRelayStream{}}
			for i := 0; i < tc.count; i++ {
				set.append("owner", "project", "chat", crewRelayBatch{Kind: "data", Seq: i, Body: make(json.RawMessage, tc.size)})
			}
			stream := set.streams["owner/project/chat"]
			// Keep append in the same backing array so this observes the
			// evicted slot, rather than an unrelated array retired by append.
			backing := make([]crewRelayBatch, len(stream.batches), len(stream.batches)+1)
			copy(backing, stream.batches)
			stream.batches = backing
			old := stream.batches
			set.append("owner", "project", "chat", crewRelayBatch{Kind: "data", Seq: tc.count, Body: make(json.RawMessage, tc.size)})
			if old[0].Body != nil {
				t.Fatal("evicted payload retained by backing array")
			}
			if len(stream.batches) != tc.count || stream.bytes != tc.count*tc.size {
				t.Fatal("replay window changed")
			}
			if &stream.batches[0] == &old[1] {
				t.Fatal("kept window still shares evicted backing array")
			}
			if stream.batches[0].Seq != 1 || stream.batches[len(stream.batches)-1].Seq != tc.count {
				t.Fatal("retained replay order changed")
			}
		})
	}
}

// Global eviction must keep live delivery and signal replay gaps.
func TestRelayCacheGlobalCapKeepsLiveSubscriptions(t *testing.T) {
	set := &crewRelayStreamSet{streams: map[string]*crewRelayStream{}}
	for _, chat := range []string{"old", "new"} {
		set.append("owner", "project", chat, crewRelayBatch{Kind: "event", Seq: 2, Body: json.RawMessage(`{"events":[]}`)})
	}
	_, _, sub, closeSub := set.subscribe("owner", "project", "old", 2)
	defer closeSub()
	old := set.streams["owner/project/old"]
	old.running = true
	backing := old.batches
	set.Lock()
	set.trimReplay(len(set.streams["owner/project/new"].batches[0].Body))
	set.Unlock()
	if len(old.batches) != 0 || old.bytes != 0 || backing[0].Body != nil {
		t.Fatal("global eviction retained oldest replay")
	}
	if !old.running || len(old.subs) != 1 {
		t.Fatal("eviction changed live state")
	}
	_, gap, _, done := set.subscribe("owner", "project", "old", 1)
	done()
	if !gap {
		t.Fatal("evicted replay must signal resync")
	}
	set.append("owner", "project", "old", crewRelayBatch{Kind: "event", Seq: 3, Body: json.RawMessage(`{"events":[]}`)})
	select {
	case batch := <-sub:
		if batch.Seq != 3 {
			t.Fatal("wrong live event")
		}
	default:
		t.Fatal("live delivery lost")
	}
}
