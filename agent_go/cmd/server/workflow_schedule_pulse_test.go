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
	if len(plan) != 3 || plan[0].to != workflowContractExplicitSchedulePulseVersion || plan[0].label != "upgrade-explicit-schedule-pulse" || plan[1].to != workflowContractNestedAgentArtifactsVersion || plan[2].to != workflowContractManagedDBScriptsVersion {
		t.Fatalf("unexpected migration: %+v", plan)
	}
	for _, want := range []string{"pulse_mode_reason", "disabled schedules", "calendar schedules", "retained", "Do not change cron", "Do not execute the workflow", "do not stamp", "token cost", "multiple runs"} {
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
		if len(plan) != 2 || plan[0].label != "upgrade-nested-agent-artifacts" || plan[0].to != workflowContractNestedAgentArtifactsVersion || plan[1].label != "upgrade-managed-db-scripts" {
			t.Fatalf("marker %s upgrade plan = %+v, want nested artifacts then managed DB scripts", version, plan)
		}
		if workflowContractVersionIsExecutionCompatible(version) {
			t.Errorf("older contract marker %s unexpectedly remained execution-compatible", version)
		}
		turns, err := manualWorkflowUpgradeTurns(&WorkflowManifest{Version: version}, []string{"run normal work"}, "Workflow/demo")
		if err != nil || len(turns) != 3 || turns[0].label != "upgrade-nested-agent-artifacts" || turns[1].label != "upgrade-managed-db-scripts" || turns[2].label != "schedule-message-1" {
			t.Fatalf("older marker %s did not inject the required migration turn: turns=%+v err=%v", version, turns, err)
		}
	}
}

func TestExplicitSchedulePulseManifestBoundary(t *testing.T) {
	m := NewWorkflowManifest("Pulse policy")
	m.Version = "1.0.40"
	m.Schedules = []WorkflowSchedule{{ID: "daily", CronExpression: "0 9 * * *", GroupNames: []string{"prod"}}}
	if err := ValidateManifest(m); err != nil {
		t.Fatalf("legacy must remain migratable: %v", err)
	}
	m.Version = WorkflowContractCurrentVersion
	if err := ValidateManifest(m); err == nil {
		t.Fatal("current contract accepted inherited policy")
	}
	m.Schedules[0].PulseMode = "basic"
	if err := ValidateManifest(m); err == nil {
		t.Fatal("current contract accepted missing reason")
	}
	m.Schedules[0].PulseModeReason = "Frequent operations need backup and summary; reviews are unnecessary on every occurrence"
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
}

func TestPulsePolicyUsesMigratedScheduleForCurrentRun(t *testing.T) {
	m := NewWorkflowManifest("Pulse policy")
	m.Schedules = []WorkflowSchedule{{ID: "daily", PulseMode: "basic", PulseModeReason: "Routine"}}
	ctx := &ScheduleContext{Schedule: WorkflowSchedule{ID: "daily", PulseMode: "full"}}
	if got := effectiveSchedulePulseMode(ctx, m); got != "basic" {
		t.Fatalf("stale preflight policy: %s", got)
	}
	m.Schedules[0].PulseMode = "off"
	if shouldRunPulseLifecycle(ctx, m) {
		t.Fatal("new off policy ignored")
	}
	ctx.ForcePulseReview = true
	if got := effectiveSchedulePulseMode(ctx, m); got != "full" {
		t.Fatal("manual review lost explicit override")
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
	if _, err := create(workflow.ScheduleRuntimePolicy{}); err == nil {
		t.Fatal("legacy workflow allowed new inherited schedule")
	}
	policy := workflow.ScheduleRuntimePolicy{PulseMode: "basic", PulseModeReason: "Frequent routine processing; backup and summary only"}
	if _, err := create(policy); err != nil {
		t.Fatal(err)
	}
	if _, err := callbacks.CreateCalendarSchedule(ctx, path, "Calendar", "UTC", []string{"prod"}, `[{"date":"2099-01-01","time":"09:00"}]`, "workshop", nil, "", "run", workflow.ScheduleRuntimePolicy{}); err == nil {
		t.Fatal("calendar accepted missing policy")
	}
	if _, err := callbacks.CreateCalendarSchedule(ctx, path, "Calendar", "UTC", []string{"prod"}, `[{"date":"2099-01-01","time":"09:00"}]`, "workshop", nil, "", "run", policy); err != nil {
		t.Fatal(err)
	}
	saved, _, err := ReadWorkflowManifest(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Schedules) != 3 || saved.Schedules[1].PulseModeReason != policy.PulseModeReason || saved.Schedules[2].PulseMode != "basic" {
		t.Fatalf("policy not persisted: %+v", saved.Schedules)
	}
	update := func(id string, p *workflow.ScheduleRuntimePolicy) (string, error) {
		return callbacks.UpdateSchedule(ctx, id, "", "", "", nil, false, nil, false, nil, "", nil, false, nil, "", nil, nil, p)
	}
	if _, err := update(saved.Schedules[1].ID, &workflow.ScheduleRuntimePolicy{SetPulseMode: true, PulseMode: "full"}); err == nil {
		t.Fatal("mode changed without a new reason")
	}
	// Migrate the old disabled schedule while other schedules remain available.
	policy.SetPulseMode = true
	policy.SetPulseModeReason = true
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
	if err != nil || !strings.Contains(listing, policy.PulseModeReason) {
		t.Fatalf("list hides rationale: %v %s", err, listing)
	}
}

// Found in the live check on 2026-09-23: after retiring pulse_mode=full, every
// save of a workflow that still stored a legacy full schedule failed
// validation (social-media, salesoutreach, substack, linkedin, ...). A stored
// legacy value must keep validating; it runs as basic.
func TestCurrentManifestWithStoredLegacyFullScheduleStillValidates(t *testing.T) {
	m := NewWorkflowManifest("Legacy full")
	m.Version = WorkflowContractCurrentVersion
	m.Schedules = []WorkflowSchedule{{
		ID: "weekly", CronExpression: "0 8 * * 1", GroupNames: []string{"prod"}, Enabled: true,
		PulseMode: "full", PulseModeReason: "Weekly review of accumulated route evidence",
	}}
	if err := ValidateManifest(m); err != nil {
		t.Fatalf("a stored legacy full schedule must not block saving the workflow: %v", err)
	}
	if got := m.EffectivePulseMode(m.Schedules[0]); got != schedulePulseModeBasic {
		t.Fatalf("legacy full runs as %q, want basic", got)
	}
}
