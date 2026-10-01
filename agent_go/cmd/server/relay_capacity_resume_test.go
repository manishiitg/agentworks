package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestRelayCapacityResumeRestoresPublishedInputAndCheckpoint(t *testing.T) {
	draft := "Workflow/quota-relay"
	release := relayReleaseWorkspace(draft, "v1")
	m := NewWorkflowManifest("Quota Relay")
	m.Kind = "relay"
	m.CreatedBy = "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}}
	sched := WorkflowSchedule{ID: "call", Enabled: true, ScheduleType: "webhook", Kind: triggerKindFunction, GroupNames: []string{"original-group"}, Function: &WorkflowFunctionSpec{Name: "process", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}
	m.Schedules = []WorkflowSchedule{sched}
	now := time.Now().UTC()
	run := schedulerstate.Run{RunID: "original", ScopeType: "workflow", ScopeID: release, ScheduleID: sched.ID, RunFolder: "original-folder", TriggerSource: "webhook", State: schedulerstate.StateWaitingForCapacity}
	input := WorkflowWebhookDelivery{RunID: run.RunID, Variables: map[string]string{"INPUT": `{"name":"original API input"}`}, Group: "original-group", Payload: json.RawMessage(`{"relay_caller":"reader","function":"process","relay_version":"v1"}`)}
	wait := stepworkflow.WorkflowCapacityWait{StepNumber: 3, StepID: "third", RetryAt: now.Add(-time.Minute), RecordedAt: now.Add(-2 * time.Minute)}
	raw, _ := json.Marshal(m)
	delivery, _ := json.Marshal(input)
	checkpoint, _ := json.Marshal(wait)
	releaseRaw, _ := json.Marshal(relayRelease{Version: "v1", Hash: relaySnapshotHash(map[string]string{"workflow.json": string(raw)}), FileCount: 1, Files: []string{"workflow.json"}})
	mock := &mockWorkspaceAPI{files: map[string]string{release + "/release.json": string(releaseRaw), manifestPath(draft): string(raw), manifestPath(release): string(raw), webhookInputPath(release, run.RunID): string(delivery), stepworkflow.WorkflowCapacityWaitPath(release, run.RunFolder): string(checkpoint)}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	s := &SchedulerService{}
	resume, err := s.capacityResumeContext(context.Background(), run, now)
	if err != nil {
		t.Fatal(err)
	}
	if resume == nil || resume.WorkspacePath != release || resume.CapacityResumeRunID != run.RunID || resume.CapacityResumeRunFolder != run.RunFolder || resume.CapacityResumeFromStep != 3 || resume.WebhookInput.Group != input.Group || resume.WebhookInput.Variables["INPUT"] != input.Variables["INPUT"] {
		t.Fatalf("changed resume identity/input: %+v", resume)
	}
	m.Schedules[0].Enabled = false
	raw, _ = json.Marshal(m)
	mock.mu.Lock()
	mock.files[manifestPath(draft)] = string(raw)
	mock.mu.Unlock()
	if _, err := s.capacityResumeContext(context.Background(), run, now); err == nil {
		t.Fatal("disabled live function was resumed")
	}
}

func TestRelayCapacityWaitProjectionIsNotInterrupted(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.BeginRun(ctx, schedulerstate.Run{RunID: "waiting", ScheduleID: "test", ScopeType: "workflow", ScopeID: "Workflow/test", LockKey: "wait"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []schedulerstate.State{schedulerstate.StateWorkflowRunning, schedulerstate.StateWorkflowFinished, schedulerstate.StateWaitingForCapacity} {
		if err := store.Transition(ctx, schedulerstate.Transition{RunID: "waiting", To: state}); err != nil {
			t.Fatal(err)
		}
	}
	svc := &SchedulerService{stateStore: store}
	status, _, completed, ok := svc.durableScheduleRunProjection(ctx, "waiting")
	if !ok || status != scheduleRunStatusWaitingForCapacity || completed != nil {
		t.Fatalf("wait projection = %q %v %t", status, completed, ok)
	}
}
