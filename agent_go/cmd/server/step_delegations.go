package server

import (
	"sync"
)

// stepDelegationRegistry maps a workshop step's ForceCorrelationID ("workshop-step-*") to the
// delegation IDs spawned within that step. This lets query_step include tool calls from API-based
// delegation sub-agents that use their own correlation ID ("delegation-<index>-<ts>") instead of
// the parent step's correlation. CLI sub-agents share the parent's MCP session ID and are already
// covered by the toolcalllog prefix scan, so they don't need this registry.
var (
	stepDelegationMu  sync.RWMutex
	stepDelegationMap = make(map[string][]string) // workshopStepCorrelationID → []delegationID
)

func getStepDelegations(workshopStepCorrelationID string) []string {
	stepDelegationMu.RLock()
	defer stepDelegationMu.RUnlock()
	ids := stepDelegationMap[workshopStepCorrelationID]
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

func cleanupStepDelegation(workshopStepCorrelationID string) {
	stepDelegationMu.Lock()
	defer stepDelegationMu.Unlock()
	delete(stepDelegationMap, workshopStepCorrelationID)
}
