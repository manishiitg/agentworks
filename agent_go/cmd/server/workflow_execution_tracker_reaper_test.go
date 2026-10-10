package server

import (
	"testing"
	"time"
)

// A running entry only leaves the registry when a completion path fires. If one
// is ever missed, the entry used to be immortal: it kept the global monitor
// spinning and made every new workflow-builder chat fail with 409 workflow_busy
// until the server restarted.
func TestPruneReapsRunningExecutionThatNeverCompleted(t *testing.T) {
	now := time.Now().UTC()
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"stuck": {
				ExecutionID:   "stuck",
				WorkspacePath: "Workflow/customer-login",
				SessionID:     "s1",
				Status:        trackedExecutionStatusRunning,
				StartedAt:     now.Add(-(trackedExecutionRunningMaxAge + time.Hour)),
			},
		},
	}
	api.pruneTrackedExecutionsLocked(now)
	if _, still := api.trackedWorkflowExecutions["stuck"]; still {
		t.Fatal("a running execution far past the max age must be reaped")
	}
}

// The cap must sit far above any legitimate run — observed full runs are ~1.5h
// and the scheduler's own inactivity timeouts are minutes — so live work is never
// reaped out from under the user.
func TestPruneKeepsRunningExecutionWithinMaxAge(t *testing.T) {
	now := time.Now().UTC()
	for name, age := range map[string]time.Duration{
		"just started":   time.Minute,
		"long legit run": 3 * time.Hour,
		"just under cap": trackedExecutionRunningMaxAge - time.Minute,
	} {
		t.Run(name, func(t *testing.T) {
			api := &StreamingAPI{
				trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
					"live": {ExecutionID: "live", Status: trackedExecutionStatusRunning, StartedAt: now.Add(-age)},
				},
			}
			api.pruneTrackedExecutionsLocked(now)
			if _, still := api.trackedWorkflowExecutions["live"]; !still {
				t.Fatalf("a running execution aged %s must be kept", age)
			}
		})
	}
}

// A zero StartedAt carries no evidence of age; reaping on it would be a guess.
func TestPruneKeepsRunningExecutionWithoutStartTime(t *testing.T) {
	api := &StreamingAPI{
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"nostart": {ExecutionID: "nostart", Status: trackedExecutionStatusRunning},
		},
	}
	api.pruneTrackedExecutionsLocked(time.Now().UTC())
	if _, still := api.trackedWorkflowExecutions["nostart"]; !still {
		t.Fatal("an entry with no start time must not be reaped on a guess")
	}
}
