package server

import "sync"

// External Ask calls reuse a caller's conversation. Keep one call active in a
// session at a time so a pending question has only one call that can own it.
var externalAskSessionGates = struct {
	sync.Mutex
	m map[string]*externalAskSessionGate
}{m: make(map[string]*externalAskSessionGate)}

type externalAskSessionGate struct {
	mu   sync.Mutex
	refs int
}

func lockExternalAskSession(sessionID string) func() {
	externalAskSessionGates.Lock()
	gate := externalAskSessionGates.m[sessionID]
	if gate == nil {
		gate = &externalAskSessionGate{}
		externalAskSessionGates.m[sessionID] = gate
	}
	gate.refs++
	externalAskSessionGates.Unlock()

	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		externalAskSessionGates.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(externalAskSessionGates.m, sessionID)
		}
		externalAskSessionGates.Unlock()
	}
}
