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

func TestExplicitSchedulePulseMigration(t *testing.T) {
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.40"})
	if len(plan) != 9 || plan[0].to != workflowContractExplicitSchedulePulseVersion || plan[0].label != "upgrade-explicit-schedule-pulse" || plan[1].to != workflowContractNestedAgentArtifactsVersion || plan[2].to != workflowContractManagedDBScriptsVersion || plan[3].to != workflowContractStepDescriptionLayoutVersion {
		t.Fatalf("unexpected migration: %+v", plan)
	}
	for _, want := range []string{"after_run", "disabled schedules", "calendar schedules", "retained", "Do not change cron", "Do not execute the workflow", "do not stamp", "token cost"} {
		if !strings.Contains(plan[0].query, want) {
			t.Errorf("missing migration requirement %q", want)
		}
	}
	if len(workflowVersionUpgradePlan(&WorkflowManifest{Version: WorkflowContractCurrentVersion})) != 0 {
		t.Fatal("migration repeats")
	}
}

func TestRetiredMarkersStillRequireCurrentNestedArtifactMigration(t *testing.T) {
	for _, version := range []string{
		workflowContractExplicitSchedulePulseVersion,
		workflowContractRunScopedRoutesVersion,
		workflowContractEvalRetirementVersion,
	} {
		plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: version})
		if len(plan) != 8 || plan[0].label != "upgrade-nested-agent-artifacts" || plan[0].to != workflowContractNestedAgentArtifactsVersion || plan[1].label != "upgrade-managed-db-scripts" || plan[2].label != "upgrade-step-description-layout" {
			t.Fatalf("marker %s upgrade plan = %+v, want nested artifacts, managed DB scripts, then step description layout", version, plan)
		}
		if workflowContractVersionIsExecutionCompatible(version) {
			t.Errorf("older contract marker %s unexpectedly remained execution-compatible", version)
		}
		turns, err := manualWorkflowUpgradeTurns(&WorkflowManifest{Version: version}, []string{"run normal work"}, "Workflow/demo")
		if err != nil || len(turns) != 9 || turns[0].label != "upgrade-nested-agent-artifacts" || turns[1].label != "upgrade-managed-db-scripts" || turns[2].label != "upgrade-step-description-layout" || turns[3].label != "upgrade-agent-step" || turns[4].label != "upgrade-description-first" || turns[5].label != "upgrade-resource-intent" || turns[6].label != "upgrade-goal-agent" || turns[7].label != "upgrade-workflow-skills" || turns[8].label != "schedule-message-1" {
			t.Fatalf("older marker %s did not inject the required migration turn: turns=%+v err=%v", version, turns, err)
		}
	}
}

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
