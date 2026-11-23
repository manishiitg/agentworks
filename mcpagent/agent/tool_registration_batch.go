package mcpagent

import "github.com/manishiitg/mcpagent/agent/codeexec"

// DeferToolRegistryPublication batches setup-time RegisterCustomTool calls.
// Call the returned function before serving tools; defer it too for error exits.
// Agent.mu serializes begin/finish with registrations and publication.
// Existing canonical definitions and authorization closures are unchanged.
func (a *Agent) DeferToolRegistryPublication() func() {
	a.mu.Lock()
	a.toolRegistrationDepth++
	a.mu.Unlock()
	finished := false
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if finished {
			return
		}
		finished = true
		a.toolRegistrationDepth--
		a.publishCustomToolRegistryLocked()
	}
}

// publishCustomToolRegistryLocked requires Agent.mu; canonical snapshots retain
// their existing inner canonicalToolRegistry.mu read lock.
func (a *Agent) publishCustomToolRegistryLocked() {
	if a.toolRegistrationDepth != 0 || !a.toolRegistryDirty {
		return
	}
	a.toolRegistryDirty = false
	if a.clients == nil {
		return
	}
	executors := a.directToolExecutors()
	codeexec.InitRegistry(a.clients, executors, a.realToolToServer(), a.logger)
	if a.sessionID != "" {
		codeexec.InitRegistryForSession(a.sessionID, executors, a.logger)
	}
}
