package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

const crewRunnerTriggers = `[
	{"id":"trig-1","name":"Review","enabled":true,"message":"Review it","kind":"internal","caller":{"type":"workflow","id":"wf-1"}},
	{"id":"trig-off","name":"Off","enabled":false,"message":"Off","kind":"internal","caller":{"type":"workflow","id":"wf-1"}}
]`

func testCrewStepRequest() stepworkflow.CrewStepRequest {
	return stepworkflow.CrewStepRequest{
		WorkflowID: "wf-1", WorkflowRunFolder: "iteration-0", ExecutionID: "exec-1", StepID: "crew-1",
		Group: "production", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Instruction: "Review it", Inputs: map[string]interface{}{"pr": map[string]interface{}{"n": float64(87)}},
		TimeoutSeconds: 30,
	}
}

func crewRunnerDeliveryBase(req stepworkflow.CrewStepRequest) string {
	scope := strings.TrimSpace(req.ExecutionID)
	if scope == "" {
		scope = req.WorkflowRunFolder
	}
	// Crew steps call through an internal trigger, which always runs in its
	// own conversation.
	return crewStepDeliveryBase(req.WorkflowID, scope, req.Group, req.StepID, req.TriggerID, runDestinationIsolated)
}

func TestCrewStepDeliveryBaseVariesWithTrigger(t *testing.T) {
	base := crewStepDeliveryBase("wf-1", "exec-1", "g", "crew-1", "trig-1", runDestinationCrewChat)
	if other := crewStepDeliveryBase("wf-1", "exec-1", "g", "crew-1", "trig-2", runDestinationCrewChat); other == base {
		t.Fatal("retargeted trigger reuses the delivery key")
	}
	if other := crewStepDeliveryBase("wf-1", "exec-1", "g", "crew-1", "trig-1", runDestinationIsolated); other == base {
		t.Fatal("flipped destination reuses the delivery key")
	}
	if same := crewStepDeliveryBase("wf-1", "exec-1", "g", "crew-1", "trig-1", runDestinationCrewChat); same != base {
		t.Fatal("identical step attempt changed the delivery key")
	}
}

func TestRunCrewStepAdoptsSuccessfulDuplicate(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	convKey := "owner\x1fproduct-project:crewx:project-a:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	req := testCrewStepRequest()
	runID := webhookDeliveryRunID("project-a", "trig-1", crewRunnerDeliveryBase(req))

	// A prior attempt already completed this delivery; the step must adopt
	// it without invoking the trigger again.
	first, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: crewRunnerDeliveryBase(req), Payload: []byte(`{}`),
	})
	if err != nil || first.RunID != runID {
		t.Fatalf("seed dispatch = %+v err=%v", first, err)
	}
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")
	if err := UpdateScheduleRun(ctx, runsWorkspace, runID, "success", "", nil, "", "sess-9"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateScheduleRunFinalResponse(ctx, runsWorkspace, runID, "the verdict"); err != nil {
		t.Fatal(err)
	}

	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	result, err := runner.RunCrewStep(ctx, req)
	if err != nil {
		t.Fatalf("RunCrewStep: %v", err)
	}
	if result.FinalResponse != "the verdict" || result.Status != "success" || result.SessionID != "sess-9" || result.CrewRunID != runID {
		t.Fatalf("result = %+v", result)
	}
	svc.mu.Lock()
	depth := len(svc.queued[convKey])
	svc.mu.Unlock()
	if depth != 1 {
		t.Fatalf("queued depth = %d, want 1 (no second dispatch)", depth)
	}
}

func TestRunCrewStepIsolatesExecutionsSharingRunFolder(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	convKey := "owner\x1fproduct-project:crewx:project-a:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")

	// Execution A completes the step. Execution B runs the same step in
	// the same run folder: it must dispatch its own delivery (which never
	// finishes here, so the step times out) instead of adopting A's
	// success. With folder-keyed identity this test fails: B adopts A's
	// run and returns verdict-A with no error.
	reqA := testCrewStepRequest()
	reqA.ExecutionID = "exec-A"
	runA := webhookDeliveryRunID("project-a", "trig-1", crewRunnerDeliveryBase(reqA))
	if _, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: crewRunnerDeliveryBase(reqA), Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	duration := int64(100)
	if err := UpdateScheduleRunResult(ctx, runsWorkspace, runA, ScheduleRunCompletion{
		Status: "success", DurationMs: &duration, FinalResponse: "verdict-A",
	}); err != nil {
		t.Fatal(err)
	}
	reqB := testCrewStepRequest()
	reqB.ExecutionID = "exec-B"
	reqB.TimeoutSeconds = 1
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	// The runner takes its deadline from ctx (the executor applies the
	// step's timeout_seconds); bound the wait so the test cannot poll
	// a delivery that never finishes here.
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	result, err := runner.RunCrewStep(deadline, reqB)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("execution B err = %v result = %+v, want a timeout on its own delivery", err, result)
	}
	if result.CrewRunID == runA || result.FinalResponse == "verdict-A" {
		t.Fatalf("execution B adopted execution A's run: %+v", result)
	}
	runs, err := ReadScheduleRuns(ctx, runsWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (one delivery per execution)", len(runs))
	}
}

func TestRunCrewStepGroupsDoNotShareDelivery(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	convKey := "owner\x1fproduct-project:crewx:project-a:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")

	// Groups share one execution identity but render their own instruction
	// and inputs. The production group completes first; the staging group
	// in the same execution must dispatch its own delivery (which never
	// finishes here, so the step times out) instead of adopting the
	// production response.
	prod := testCrewStepRequest()
	prod.ExecutionID = "exec-groups"
	prod.Group = "production"
	runProd := webhookDeliveryRunID("project-a", "trig-1", crewRunnerDeliveryBase(prod))
	if _, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: crewRunnerDeliveryBase(prod), Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	duration := int64(100)
	if err := UpdateScheduleRunResult(ctx, runsWorkspace, runProd, ScheduleRunCompletion{
		Status: "success", DurationMs: &duration, FinalResponse: "verdict-production",
	}); err != nil {
		t.Fatal(err)
	}
	staging := testCrewStepRequest()
	staging.ExecutionID = "exec-groups"
	staging.Group = "staging"
	staging.Instruction = "Review it for staging"
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	result, err := runner.RunCrewStep(deadline, staging)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("staging err = %v result = %+v, want a timeout on its own delivery", err, result)
	}
	if result.CrewRunID == runProd || result.FinalResponse == "verdict-production" {
		t.Fatalf("staging adopted the production delivery: %+v", result)
	}
	runs, err := ReadScheduleRuns(ctx, runsWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (one delivery per group)", len(runs))
	}
}

func TestRunCrewStepRetryAdoptsSameExecutionDelivery(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	convKey := "owner\x1fproduct-project:crewx:project-a:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	req := testCrewStepRequest()
	runID := webhookDeliveryRunID("project-a", "trig-1", crewRunnerDeliveryBase(req))
	if _, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: crewRunnerDeliveryBase(req), Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")
	duration := int64(100)
	if err := UpdateScheduleRunResult(ctx, runsWorkspace, runID, ScheduleRunCompletion{
		Status: "success", DurationMs: &duration, FinalResponse: "the verdict",
	}); err != nil {
		t.Fatal(err)
	}
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	for i := 0; i < 2; i++ {
		result, err := runner.RunCrewStep(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if result.CrewRunID != runID || result.FinalResponse != "the verdict" {
			t.Fatalf("retry %d: result = %+v, want adopted run %s", i, result, runID)
		}
	}
	runs, err := ReadScheduleRuns(ctx, runsWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (retries must not start another delivery)", len(runs))
	}
}

func TestRunCrewStepReinvokesPastFailedDuplicate(t *testing.T) {
	svc, files := newInternalDispatchCrew(t, crewRunnerTriggers)
	convKey := "owner\x1fproduct-project:crewx:project-a:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	req := testCrewStepRequest()
	base := crewRunnerDeliveryBase(req)
	runID := webhookDeliveryRunID("project-a", "trig-1", base)

	first, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: base, Payload: []byte(`{}`),
	})
	if err != nil || first.RunID != runID {
		t.Fatalf("seed dispatch = %+v err=%v", first, err)
	}
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")
	if err := UpdateScheduleRun(ctx, runsWorkspace, runID, "error", "crew exploded", nil, "", ""); err != nil {
		t.Fatal(err)
	}

	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	_, err = runner.RunCrewStep(deadline, req)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("re-invoked run err = %v, want timeout", err)
	}
	retryRunID := webhookDeliveryRunID("project-a", "trig-1", base+"#retry-1")
	entry, err := FindScheduleRun(ctx, runsWorkspace, retryRunID)
	if err != nil || entry.Status != "queued" {
		t.Fatalf("retry run = %+v err=%v, want queued", entry, err)
	}
	payloadPath := "_users/owner/Chats/Work/projects/project-a/triggers/deliveries/" + retryRunID + ".json"
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(files[payloadPath]), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["source"] != "workflow" || payload["workflow_step_id"] != "crew-1" || payload["instruction"] != "Review it" {
		t.Fatalf("payload = %v", payload)
	}
	inputs, _ := payload["inputs"].(map[string]interface{})
	pr, _ := inputs["pr"].(map[string]interface{})
	if pr["n"] != float64(87) {
		t.Fatalf("payload inputs = %v", inputs)
	}
}

func TestRunCrewStepRevokedAccess(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	svc.conversations = map[string]bool{"owner\x1fproduct-project:crewx:project-a:trig-1": true}
	ctx := context.Background()
	req := testCrewStepRequest()
	req.TriggerID = "trig-off"
	runner := newCrewStepRunner(svc, "owner")
	_, err := runner.RunCrewStep(ctx, req)
	if !errors.Is(err, ErrInternalTriggerDisabled) {
		t.Fatalf("disabled trigger err = %v, want ErrInternalTriggerDisabled", err)
	}
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")
	if _, err := FindScheduleRun(ctx, runsWorkspace, webhookDeliveryRunID("project-a", "trig-off", crewRunnerDeliveryBase(req))); err == nil {
		t.Fatal("revoked access must not dispatch")
	}
}

func TestRunCrewStepCanceled(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	svc.conversations = map[string]bool{"owner\x1fproduct-project:crewx:project-a:trig-1": true}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	_, err := runner.RunCrewStep(ctx, testCrewStepRequest())
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("canceled run err = %v", err)
	}
}

func TestPollCrewStepReturnsPartialOnFailure(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	svc.conversations = map[string]bool{"owner\x1fproduct-project:crewx:project-a:trig-1": true}
	ctx := context.Background()
	req := testCrewStepRequest()
	runID := webhookDeliveryRunID("project-a", "trig-1", "poll-failure")
	if _, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: "poll-failure", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")
	if err := UpdateScheduleRun(ctx, runsWorkspace, runID, "error", "crew exploded", nil, "", "sess-9"); err != nil {
		t.Fatal(err)
	}
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	result, err := runner.pollCrewStep(ctx, req, triggerCaller{Type: "workflow", ID: "wf-1"}, runID)
	if err == nil || !strings.Contains(err.Error(), "ended error") {
		t.Fatalf("failed poll err = %v", err)
	}
	if result.CrewRunID != runID || result.Status != "error" || result.Error != "crew exploded" || result.SessionID != "sess-9" {
		t.Fatalf("partial result = %+v", result)
	}
	if result.StartedAt.IsZero() {
		t.Fatal("partial result lost timestamps")
	}
}

func TestPollCrewStepTimeoutKeepsRunID(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	svc.conversations = map[string]bool{"owner\x1fproduct-project:crewx:project-a:trig-1": true}
	ctx := context.Background()
	req := testCrewStepRequest()
	runID := webhookDeliveryRunID("project-a", "trig-1", "poll-timeout")
	if _, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller:     triggerCaller{Type: "workflow", ID: "wf-1"},
		DeliveryID: "poll-timeout", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	result, err := runner.pollCrewStep(deadline, req, triggerCaller{Type: "workflow", ID: "wf-1"}, runID)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout err = %v", err)
	}
	if result.CrewRunID != runID || result.Status != "queued" {
		t.Fatalf("timeout partial = %+v", result)
	}
}

func TestCheckInternalCrewAccess(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	ctx := context.Background()
	caller := triggerCaller{Type: "workflow", ID: "wf-1"}
	if err := svc.checkInternalCrewAccess(ctx, "owner", "crewx", "project-a", "trig-1", caller); err != nil {
		t.Fatalf("bound access rejected: %v", err)
	}
	if err := svc.checkInternalCrewAccess(ctx, "owner", "crewx", "project-a", "trig-1", triggerCaller{Type: "workflow", ID: "intruder"}); !errors.Is(err, ErrInternalCallerMismatch) {
		t.Fatalf("wrong caller err = %v", err)
	}
	if err := svc.checkInternalCrewAccess(ctx, "owner", "crewx", "project-a", "trig-off", caller); !errors.Is(err, ErrInternalTriggerDisabled) {
		t.Fatalf("disabled trigger err = %v", err)
	}
}

const testPreflightManifestAttached = `{"id":"wf-1","label":"WF1","crew_attachments":[{"id":"att-1","alias":"project-a","crew_profile_id":"crewx","crew_project_id":"project-a","crew_workspace_path":"_users/owner/Chats/Work/projects/project-a"}]}`

func testPreflightWorkspace(t *testing.T, plan string, manifestJSON string) *ProductScheduleService {
	t.Helper()
	svc, files := newInternalTriggerTestCrew(t)
	svc.api = &StreamingAPI{}
	crewPath := "_users/owner/Chats/Work/projects/project-a/product.json"
	crewProduct := `{"schema_version":1,"product":"crewx","id":"project-a","title":"project-a","session_id":"sess-1","triggers":` + crewRunnerTriggers + `}`
	files[crewPath] = crewProduct
	mockFiles := map[string]string{crewPath: crewProduct}
	if plan != "" {
		mockFiles["Workflow/wf1/planning/plan.json"] = plan
	}
	if manifestJSON != "" {
		mockFiles["Workflow/wf1/workflow.json"] = manifestJSON
	}
	mock := &mockWorkspaceAPI{files: mockFiles}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	return svc
}

func TestPreflightCrewSteps(t *testing.T) {
	ctx := context.Background()
	bound := `{"objective":"t","steps":[{"type":"crew","id":"crew-1","title":"Review","crew_profile_id":"work","crew_project_id":"project-a","trigger_id":"trig-1","instruction":"x"}]}`
	// NOTE: the crew profile in the fixture registry is "crewx", so the
	// bound plan uses it; a wrong profile fails the lookup.
	bound = strings.Replace(bound, `"work"`, `"crewx"`, 1)
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, bound, testPreflightManifestAttached), "owner", "Workflow/wf1"); err != nil {
		t.Fatalf("bound plan rejected: %v", err)
	}
	broken := strings.Replace(bound, "trig-1", "trig-off", 1)
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, broken, testPreflightManifestAttached), "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), "crew-1") {
		t.Fatalf("broken plan err = %v, want crew step failure", err)
	}
	unattached := testPreflightWorkspace(t, bound, `{"id":"wf-1","label":"WF1"}`)
	if err := preflightCrewSteps(ctx, unattached, "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("unattached plan err = %v, want attachment failure", err)
	}
	plain := `{"objective":"t","steps":[{"type":"regular","id":"a","title":"A"}]}`
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, plain, ""), "owner", "Workflow/wf1"); err != nil {
		t.Fatalf("crew-free plan rejected: %v", err)
	}
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, "", ""), "owner", "Workflow/wf1"); err != nil {
		t.Fatalf("missing plan rejected: %v", err)
	}
	if err := preflightCrewSteps(ctx, nil, "owner", "Workflow/wf1"); err != nil {
		t.Fatalf("nil service rejected: %v", err)
	}
}

func TestPreflightCrewAttachmentsWithoutCrewSteps(t *testing.T) {
	ctx := context.Background()
	plain := `{"objective":"t","steps":[{"type":"regular","id":"a","title":"A"}]}`
	// A healthy attachment on a crew-free plan passes: downstream steps
	// may read through the alias.
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, plain, testPreflightManifestAttached), "owner", "Workflow/wf1"); err != nil {
		t.Fatalf("healthy attachment rejected: %v", err)
	}
	// A revoked crew fails the run before any step executes, even with
	// no crew step in the plan: ordinary steps could read the alias.
	revoked := testPreflightWorkspaceWithoutCrew(t, plain, testPreflightManifestAttached)
	if err := preflightCrewSteps(ctx, revoked, "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), `"project-a"`) {
		t.Fatalf("revoked attachment err = %v, want failure naming the alias", err)
	}
	// A retargeted binding fails even though the crew itself exists.
	retargeted := `{"id":"wf-1","label":"WF1","crew_attachments":[{"id":"att-1","alias":"project-a","crew_profile_id":"crewx","crew_project_id":"project-a","crew_workspace_path":"_users/owner/secrets"}]}`
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, plain, retargeted), "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), `"project-a"`) {
		t.Fatalf("retargeted attachment err = %v, want failure naming the alias", err)
	}
	// Same project-name suffix under another owner: shape-valid, and the
	// named crew exists for this user, but the stored root is not the
	// freshly authorized binding. Authorizing the project without comparing
	// roots would grant a workspace the check never authorized.
	wrongOwner := `{"id":"wf-1","label":"WF1","crew_attachments":[{"id":"att-1","alias":"project-a","crew_profile_id":"crewx","crew_project_id":"project-a","crew_workspace_path":"_users/other/Chats/Work/projects/project-a"}]}`
	if err := preflightCrewSteps(ctx, testPreflightWorkspace(t, plain, wrongOwner), "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), `"project-a"`) {
		t.Fatalf("wrong-owner attachment err = %v, want failure naming the alias", err)
	}
}

// testPreflightWorkspaceWithoutCrew mirrors testPreflightWorkspace but seeds
// no crew project: the attachment's crew is gone (deleted or unshared).
func testPreflightWorkspaceWithoutCrew(t *testing.T, plan string, manifestJSON string) *ProductScheduleService {
	t.Helper()
	svc, _ := newInternalTriggerTestCrew(t)
	svc.api = &StreamingAPI{}
	mockFiles := map[string]string{}
	if plan != "" {
		mockFiles["Workflow/wf1/planning/plan.json"] = plan
	}
	if manifestJSON != "" {
		mockFiles["Workflow/wf1/workflow.json"] = manifestJSON
	}
	mock := &mockWorkspaceAPI{files: mockFiles}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	return svc
}

func TestCrewRunnerForRun(t *testing.T) {
	schedules := &ProductScheduleService{}
	withUser := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	runner := crewRunnerForRun(withUser, schedules)
	if runner == nil {
		t.Fatal("authenticated run has no crew runner")
	}
	if got := runner.(*crewStepRunner).userID; got != "owner" {
		t.Fatalf("runner scoped to %q, want owner", got)
	}
	// Single-user mode carries no claims: the run still binds, scoped to
	// the same default identity every other per-user lookup uses.
	fallback := crewRunnerForRun(context.Background(), schedules)
	if fallback == nil {
		t.Fatal("single-user run has no crew runner")
	}
	if got := fallback.(*crewStepRunner).userID; got != GetDefaultUserID() {
		t.Fatalf("runner scoped to %q, want default %q", got, GetDefaultUserID())
	}
	if runner := crewRunnerForRun(withUser, nil); runner != nil {
		t.Fatal("run without crew service bound a crew runner")
	}
	var nilCtx context.Context
	if runner := crewRunnerForRun(nilCtx, schedules); runner != nil {
		t.Fatal("nil context bound a crew runner")
	}
}

func TestAppendCrewPollTransition(t *testing.T) {
	var timeline []stepworkflow.CrewPollTransition
	timeline = appendCrewPollTransition(timeline, "queued")
	timeline = appendCrewPollTransition(timeline, "queued")
	timeline = appendCrewPollTransition(timeline, "running")
	timeline = appendCrewPollTransition(timeline, "  ")
	timeline = appendCrewPollTransition(timeline, "success")
	if len(timeline) != 3 || timeline[0].Status != "queued" || timeline[1].Status != "running" || timeline[2].Status != "success" {
		t.Fatalf("timeline = %+v", timeline)
	}
	for _, transition := range timeline {
		if transition.At.IsZero() {
			t.Fatalf("transition missing timestamp: %+v", transition)
		}
	}
}

// A step run with no execution identity (Builder/Workshop) is a new request
// every time: it must not adopt the previous run's stored answer just
// because both share the run folder.
func TestRunCrewStepWithoutExecutionIDNeverAdoptsPreviousRun(t *testing.T) {
	svc, _ := newInternalDispatchCrew(t, crewRunnerTriggers)
	convKey := "owner\x1fproduct-project:crewx:project-a:trig-1"
	svc.conversations = map[string]bool{convKey: true}
	ctx := context.Background()
	runsWorkspace := agentProfileRuntimeWorkspace("owner", "_users/owner/Chats/Work/projects/project-a")

	first := testCrewStepRequest()
	first.ExecutionID = ""
	folderKey := crewStepDeliveryBase(first.WorkflowID, first.WorkflowRunFolder, first.Group, first.StepID, first.TriggerID, runDestinationIsolated)
	staleRun := webhookDeliveryRunID("project-a", "trig-1", folderKey)
	if _, err := svc.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
		UserID: "owner", ProfileID: "crewx", ProjectID: "project-a", TriggerID: "trig-1",
		Caller: triggerCaller{Type: "workflow", ID: "wf-1"}, DeliveryID: folderKey, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	duration := int64(100)
	if err := UpdateScheduleRunResult(ctx, runsWorkspace, staleRun, ScheduleRunCompletion{Status: "success", DurationMs: &duration, FinalResponse: "stale answer"}); err != nil {
		t.Fatal(err)
	}
	runner := newCrewStepRunner(svc, "owner")
	runner.pollInterval = 5 * time.Millisecond
	deadline, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	result, err := runner.RunCrewStep(deadline, first)
	if err == nil || result.FinalResponse == "stale answer" || result.CrewRunID == staleRun {
		t.Fatalf("follow-up adopted the previous run: result=%+v err=%v", result, err)
	}
}
