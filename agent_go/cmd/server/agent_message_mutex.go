package server

import (
	"log"
	"runtime"
	"sync"
	"time"
)

// Observe slow holders without logging account IDs, message text or credentials.
// Keep mutation semantics unchanged: releasing I/O transactions early requires
// a storage transaction/CAS, never an unlocked read-modify-write of the JSON file.
type agentMessageMutex struct {
	mu       sync.Mutex
	acquired time.Time
	caller   string
	wait     time.Duration
}

func (m *agentMessageMutex) Lock() {
	start := time.Now()
	m.mu.Lock()
	m.acquired = time.Now()
	m.caller = "unknown"
	if pc, _, _, ok := runtime.Caller(1); ok {
		if f := runtime.FuncForPC(pc); f != nil {
			m.caller = f.Name()
		}
	}
	m.wait = m.acquired.Sub(start)
}

// Presence is optional metadata; it must not queue behind message I/O.
func (m *agentMessageMutex) TryLock() bool {
	if !m.mu.TryLock() {
		return false
	}
	m.acquired, m.wait, m.caller = time.Now(), 0, "presence"
	return true
}

func (m *agentMessageMutex) Unlock() {
	hold, caller, wait := time.Since(m.acquired), m.caller, m.wait
	m.mu.Unlock()
	if wait > time.Second {
		log.Printf("[AGENT-MESSAGE-LOCK] wait caller=%s duration=%s", caller, wait.Round(time.Millisecond))
	}
	if hold > time.Second {
		log.Printf("[AGENT-MESSAGE-LOCK] hold caller=%s duration=%s", caller, hold.Round(time.Millisecond))
	}
}

// Count read callers even when warm-cache operations are below the slow-log
// threshold. Only source function names and counts are recorded.
var agentMailboxReaders struct {
	sync.Mutex
	counts map[string]struct {
		since time.Time
		calls int
	}
}

func agentMailboxReadCaller() string {
	caller := "unknown"
	if pc, _, _, ok := runtime.Caller(2); ok {
		if f := runtime.FuncForPC(pc); f != nil {
			caller = f.Name()
		}
	}
	now := time.Now()
	agentMailboxReaders.Lock()
	if agentMailboxReaders.counts == nil {
		agentMailboxReaders.counts = make(map[string]struct {
			since time.Time
			calls int
		})
	}
	count := agentMailboxReaders.counts[caller]
	if count.since.IsZero() {
		count.since = now
	}
	count.calls++
	if elapsed := now.Sub(count.since); elapsed >= time.Minute {
		log.Printf("[AGENT-MAILBOX-READS] caller=%s calls=%d window_ms=%d", caller, count.calls, elapsed.Milliseconds())
		count.since, count.calls = now, 0
	}
	agentMailboxReaders.counts[caller] = count
	agentMailboxReaders.Unlock()
	return caller
}
