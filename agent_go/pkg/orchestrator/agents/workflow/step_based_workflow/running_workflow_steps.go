package step_based_workflow

import (
	"path/filepath"
	"strings"
	"sync"
)

// Steps running right now, across every workshop in this process, keyed by
// workflow and step. A Pulse run that re-runs a step to fix or verify it while
// the workflow's own scheduled run is executing that same step makes the step
// act twice (a second email send, a second post). Pulse asks this registry
// first (WorkshopConfig.StepBusyForPulse) and waits instead.

type runningWorkflowStep struct {
	executionID string
	sessionID   string // the launching workshop's main session
}

var runningWorkflowSteps sync.Map // key -> *runningWorkflowStepSet

type runningWorkflowStepSet struct {
	mu   sync.Mutex
	runs map[string]runningWorkflowStep
}

func runningWorkflowStepKey(workspacePath, stepID string) string {
	return filepath.Clean(strings.TrimSpace(workspacePath)) + "\x00" + strings.TrimSpace(stepID)
}

// RegisterRunningWorkflowStep records a launched step until release is called.
func RegisterRunningWorkflowStep(workspacePath, stepID, executionID, sessionID string) (release func()) {
	key := runningWorkflowStepKey(workspacePath, stepID)
	value, _ := runningWorkflowSteps.LoadOrStore(key, &runningWorkflowStepSet{runs: map[string]runningWorkflowStep{}})
	set := value.(*runningWorkflowStepSet)
	set.mu.Lock()
	set.runs[executionID] = runningWorkflowStep{executionID: executionID, sessionID: strings.TrimSpace(sessionID)}
	set.mu.Unlock()
	return func() {
		set.mu.Lock()
		delete(set.runs, executionID)
		set.mu.Unlock()
	}
}

// RunningWorkflowStepSessions returns the main sessions currently running
// stepID of workspacePath, other than exceptSession.
func RunningWorkflowStepSessions(workspacePath, stepID, exceptSession string) []string {
	value, ok := runningWorkflowSteps.Load(runningWorkflowStepKey(workspacePath, stepID))
	if !ok {
		return nil
	}
	set := value.(*runningWorkflowStepSet)
	set.mu.Lock()
	defer set.mu.Unlock()
	var sessions []string
	for _, run := range set.runs {
		if run.sessionID != strings.TrimSpace(exceptSession) {
			sessions = append(sessions, run.sessionID)
		}
	}
	return sessions
}
