package server

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// memoryLogInterval is how often the server logs its memory use. There was
// no record of what grew before the 2026-09-25 OOM kill on server A.
const memoryLogInterval = time.Minute

// startMemoryLog logs heap, goroutines and the event store's in-memory size
// every minute, so a memory spike leaves evidence in agent.log.
func startMemoryLog(store *events.EventStore) {
	go func() {
		ticker := time.NewTicker(memoryLogInterval)
		defer ticker.Stop()
		for range ticker.C {
			log.Print(memoryLogLine(store))
		}
	}()
}

func memoryLogLine(store *events.EventStore) string {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	mb := func(bytes uint64) uint64 { return bytes / (1 << 20) }
	line := fmt.Sprintf("[MEM] heap_inuse=%dMB heap_alloc=%dMB sys=%dMB goroutines=%d gc=%d",
		mb(mem.HeapInuse), mb(mem.HeapAlloc), mb(mem.Sys), runtime.NumGoroutine(), mem.NumGC)
	if store == nil {
		return line
	}
	stats := store.Stats(5)
	largest := make([]string, 0, len(stats.Largest))
	for _, session := range stats.Largest {
		largest = append(largest, fmt.Sprintf("%s:%d+%dt", session.SessionID, session.Events, session.TerminalEvents))
	}
	return fmt.Sprintf("%s event_sessions=%d events=%d terminal_events=%d largest=[%s]",
		line, stats.Sessions, stats.Events, stats.TerminalEvents, strings.Join(largest, " "))
}
