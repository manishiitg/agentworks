package server

import (
	"testing"
)

// A workflow already stamped before retirement remains loadable; runtime
// validation does not repeat the stamp prerequisites.
func TestStampedScheduleWithoutRetiredPulseFieldsStillValidates(t *testing.T) {
	m := NewWorkflowManifest("Example")
	m.Version = WorkflowContractCurrentVersion
	m.Schedules = []WorkflowSchedule{{ID: "daily", CronExpression: "0 9 * * *", GroupNames: []string{"prod"}}}
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
}
