package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

func TestMemoryLogLineReportsHeapAndEventStore(t *testing.T) {
	store := events.NewEventStore(100)
	store.AddEvent("sess-a", events.Event{})
	store.AddEvent("sess-a", events.Event{})
	store.AddEvent("sess-b", events.Event{})
	line := memoryLogLine(store)
	for _, want := range []string{"[MEM] heap_inuse=", "goroutines=", "event_sessions=2", "events=3", "event_bytes=", "largest=[sess-a:", " sess-b:"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}
