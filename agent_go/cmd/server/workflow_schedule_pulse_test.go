package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulepolicy"
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

func TestScheduleCallbacksRequireAndPersistPulsePolicy(t *testing.T) {
	const path = "Workflow/pulse-policy-test"
	m := NewWorkflowManifest("Pulse policy")
	m.Version = "1.0.40"
	m.Schedules = []WorkflowSchedule{{ID: "old-disabled", CronExpression: "0 8 * * *", Timezone: "UTC", GroupNames: []string{"prod"}}}
	data, _ := json.Marshal(m)
	mock := &mockWorkspaceAPI{files: map[string]string{path + "/workflow.json": string(data), path + "/variables/variables.json": `{"groups":[{"group_name":"prod","name":"prod"}]}`}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	api := &StreamingAPI{}
	callbacks := api.buildSchedulerCallbacks()
	ctx := context.Background()
	create := func(policy workflow.ScheduleRuntimePolicy) (string, error) {
		return callbacks.CreateSchedule(ctx, path, "Frequent queue", "0 */4 * * *", "UTC", []string{"prod"}, nil, "workshop", nil, "", "run", nil, false, policy)
	}
	// PLAT-697 phase 0: a schedule names after_run; without it a new
	// schedule gets backup, publish and notify, and pulse_mode follows.
	policy := workflow.ScheduleRuntimePolicy{}
	if _, err := create(policy); err != nil {
		t.Fatal(err)
	}
	notifyOnly := workflow.ScheduleRuntimePolicy{SetAfterRun: true, AfterRun: workflow.ScheduleAfterRunOptions{Notify: true}}
	if _, err := callbacks.CreateCalendarSchedule(ctx, path, "Calendar", "UTC", []string{"prod"}, `[{"date":"2099-01-01","time":"09:00"}]`, "workshop", nil, "", "run", notifyOnly); err != nil {
		t.Fatal(err)
	}
	saved, _, err := ReadWorkflowManifest(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 3 || saved.Schedules[1].PulseMode != "" || saved.Schedules[2].PulseMode != "" {
		t.Fatalf("policy not persisted: %+v", saved.Schedules)
	}
	if a := saved.Schedules[1].AfterRun; a == nil || !a.Backup || !a.Publish || !a.Notify {
		t.Fatalf("basic schedule after_run = %+v, want all three", a)
	}
	if a := saved.Schedules[2].AfterRun; a == nil || a.Backup || a.Publish || !a.Notify {
		t.Fatalf("calendar after_run = %+v, want notify only", a)
	}
	update := func(id string, p *workflow.ScheduleRuntimePolicy) (string, error) {
		return callbacks.UpdateSchedule(ctx, id, "", "", "", nil, false, nil, false, nil, "", nil, false, nil, "", nil, nil, p)
	}
	policy = workflow.ScheduleRuntimePolicy{SetAfterRun: true, AfterRun: workflow.ScheduleAfterRunOptions{Notify: true}}
	if _, err := update("old-disabled", &policy); err != nil {
		t.Fatal(err)
	}
	saved, _, _ = ReadWorkflowManifest(ctx, path)
	data, _ = json.Marshal(saved)
	if err := schedulepolicy.ValidatePulseStamp(data, WorkflowContractCurrentVersion); err != nil {
		t.Fatal(err)
	}
	if saved.Schedules[0].Enabled || saved.Schedules[0].CronExpression != "0 8 * * *" {
		t.Fatal("policy migration altered timing/enabled state")
	}
	listing, err := callbacks.ListSchedules(ctx, path)
	if err != nil || !strings.Contains(listing, "backup=false, publish=false, notify=true") {
		t.Fatalf("list hides the after-run options: %v %s", err, listing)
	}
}
