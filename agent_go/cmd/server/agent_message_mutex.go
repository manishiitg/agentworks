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
