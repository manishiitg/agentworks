package server

import (
	"strings"
	"sync"
)

const maxConcurrentPulseRuns = 2

// pulseLauncherMu serializes the per-tick Pulse launcher passes, so the cap
// check and the start it guards are never interleaved across ticks.
var pulseLauncherMu sync.Mutex

// runningPulseRuns counts Pulse goal checks in progress on any workflow.
func (s *SchedulerService) runningPulseRuns() int {
	s.runtimeStatesMu.RLock()
	defer s.runtimeStatesMu.RUnlock()
	running := 0
	for key, state := range s.runtimeStates {
		if state == nil || state.LastStatus != "running" {
			continue
		}
		if strings.HasSuffix(key, scheduleScopeSeparator+manualWorkflowPulseScheduleID) {
			running++
		}
	}
	return running
}
