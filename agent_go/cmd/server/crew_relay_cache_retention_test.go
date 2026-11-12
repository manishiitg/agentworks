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
