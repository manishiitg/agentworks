package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookEnvelopeOptions(t *testing.T) {
	s := WorkflowSchedule{InputPreset: "Development", Webhook: &WorkflowWebhookConfig{InputMode: "envelope", AllowedVariables: []string{"base_url"}}}
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"variables":{"base_url":"https://example.test"},"payload":{"pr":12}}`, true},
		{`{"group":"prod"}`, false}, {`{"variables":{"TOKEN":"bad"}}`, false}, {`{"variables":{"base_url":false}}`, false},
	} {
		d := &WorkflowWebhookDelivery{Payload: json.RawMessage(tc.body)}
		err := resolveWebhookDeliveryOptions(s, d, &VariablesManifest{Variables: []Variable{{Name: "base_url"}}})
		if (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if tc.ok && (d.Group != "" || d.Variables["base_url"] != "https://example.test" || string(d.Payload) != `{"pr":12}`) {
			t.Fatalf("bad envelope: %+v", d)
		}
	}
	s.Webhook.InputMode = "raw"
	d := &WorkflowWebhookDelivery{Payload: json.RawMessage(`{"group":"prod","variables":{"TOKEN":"data"}}`)}
	if err := resolveWebhookDeliveryOptions(s, d); err != nil || d.Group != "" || d.Variables != nil {
		t.Fatal("raw event interpreted as config")
	}
	typed := WorkflowSchedule{Webhook: &WorkflowWebhookConfig{InputMode: "envelope", AllowedVariables: []string{"count", "details"}}}
	definitions := &VariablesManifest{Variables: []Variable{{Name: "count", Type: "integer"}, {Name: "details", Type: "object"}}}
	for _, test := range []struct {
		body string
		ok   bool
	}{{`{"variables":{"count":2,"details":{"ok":true}}}`, true}, {`{"variables":{"count":2.5}}`, false}, {`{"variables":{"details":false}}`, false}} {
		delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(test.body)}
		err := resolveWebhookDeliveryOptions(typed, delivery, definitions)
		if (err == nil) != test.ok {
			t.Fatalf("typed envelope %s: %v", test.body, err)
		}
	}

	// PLAT-746-265 / PR498: decoding into float64 changed submitted integers.
	delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(`{"variables":{"count":9007199254740993,"details":{"id":9007199254740993}}}`)}
	if err := resolveWebhookDeliveryOptions(typed, delivery, definitions); err != nil || delivery.Variables["count"] != "9007199254740993" || delivery.Variables["details"] != `{"id":9007199254740993}` {
		t.Fatalf("webhook rounded the caller's integer: %+v %v", delivery, err)
	}
}

// PLAT-746-450: typed raw inputs retain exact numbers and objects; explicit
// selection and fixed-value rules apply before any script is started.
func TestWebhookRawInputsKeepTypesAndFixedValueRules(t *testing.T) {
	definitions := &VariablesManifest{Variables: []Variable{{Name: "count", Type: "integer"}, {Name: "details", Type: "object"}}}
	schedule := WorkflowSchedule{InputPreset: "Saved", Webhook: &WorkflowWebhookConfig{InputMode: "raw", AllowedVariables: []string{"count", "details"}, PayloadMappings: &WorkflowWebhookPayloadMappings{Inputs: map[string]workflowtrigger.InputMapping{"count": {Source: "event.count"}, "details": {Source: "event.details"}}}}}
	delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(`{"event":{"count":9007199254740993,"details":{"id":9007199254740993}}}`)}
	if err := resolveWebhookDeliveryOptions(schedule, delivery, definitions); err != nil || delivery.Variables["count"] != "9007199254740993" || delivery.Variables["details"] != `{"id":9007199254740993}` {
		t.Fatalf("typed raw mapping: %+v %v", delivery, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*WorkflowSchedule)
		body   string
	}{
		{"type", func(*WorkflowSchedule) {}, `{"event":{"count":1.5,"details":{}}}`},
		{"selection", func(s *WorkflowSchedule) { s.Webhook.AllowedVariables = []string{"details"} }, `{"event":{"count":1,"details":{}}}`},
		{"fixed", func(s *WorkflowSchedule) { s.Inputs = map[string]string{"count": "4"} }, `{"event":{"count":1,"details":{}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copySchedule := schedule
			config := *schedule.Webhook
			copySchedule.Webhook = &config
			tc.mutate(&copySchedule)
			if err := resolveWebhookDeliveryOptions(copySchedule, &WorkflowWebhookDelivery{Payload: json.RawMessage(tc.body)}, definitions); err == nil {
				t.Fatal("invalid mapped input accepted")
			}
		})
	}
	schedule.Webhook.PayloadMappings.Inputs["count"] = workflowtrigger.InputMapping{Source: "kind", Values: map[string]json.RawMessage{"review": json.RawMessage(`8`)}, Default: json.RawMessage(`3`)}
	for _, tc := range []struct{ body, want string }{{`{"kind":"review","event":{"details":{}}}`, "8"}, {`{"kind":"other","event":{"details":{}}}`, "3"}} {
		delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(tc.body)}
		if err := resolveWebhookDeliveryOptions(schedule, delivery, definitions); err != nil || delivery.Variables["count"] != tc.want {
			t.Fatalf("typed choices/default: %+v %v", delivery, err)
		}
	}
}

func TestWebhookRawPayloadMappingsKeepPresetAndSelectNestedBranch(t *testing.T) {
	s := WorkflowSchedule{
		InputPreset:     "Production",
		RouteSelections: map[string]string{"workflow-route": "regression"},
		Webhook: &WorkflowWebhookConfig{InputMode: "raw", PayloadMappings: &WorkflowWebhookPayloadMappings{
			Routes: map[string]WorkflowWebhookValueMapping{
				"regression-component-branch": {Source: "component", Values: map[string]string{"service/review": "review"}},
			},
		}},
	}
	payload := `{"component":"service/review","env":"prod","commit_sha":"test-commit-abc123"}`
	delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(payload)}
	if err := resolveWebhookDeliveryOptions(s, delivery); err != nil {
		t.Fatal(err)
	}
	if delivery.Group != "" || s.InputPreset != "Production" {
		t.Fatalf("delivery unexpectedly selected a preset: %q", delivery.Group)
	}
	if got := delivery.RouteSelections["regression-component-branch"]; got != "review" {
		t.Fatalf("mapped branch = %q, want review", got)
	}
	stepID, routes := resolvedWebhookExecutionTarget(s, delivery)
	if stepID != "" || routes["workflow-route"] != "regression" || routes["regression-component-branch"] != "review" {
		t.Fatalf("resolved target = step %q routes %#v", stepID, routes)
	}
	if string(delivery.Payload) != payload {
		t.Fatalf("raw payload was changed: %s", delivery.Payload)
	}
}

func TestWebhookRawPayloadMappingsCanSelectSingleStep(t *testing.T) {
	s := WorkflowSchedule{Webhook: &WorkflowWebhookConfig{InputMode: "raw", PayloadMappings: &WorkflowWebhookPayloadMappings{
		Step: &WorkflowWebhookValueMapping{Source: "component", Values: map[string]string{"service/review": "run-review-tests"}},
	}}}
	delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(`{"component":"service/review"}`)}
	if err := resolveWebhookDeliveryOptions(s, delivery); err != nil {
		t.Fatal(err)
	}
	stepID, routes := resolvedWebhookExecutionTarget(s, delivery)
	if stepID != "run-review-tests" || len(routes) != 0 {
		t.Fatalf("resolved target = step %q routes %#v", stepID, routes)
	}
}

func TestWebhookRawPayloadMappingsRejectUnknownOrMissingValues(t *testing.T) {
	s := WorkflowSchedule{Webhook: &WorkflowWebhookConfig{InputMode: "raw", PayloadMappings: &WorkflowWebhookPayloadMappings{
		Step: &WorkflowWebhookValueMapping{Source: "env", Values: map[string]string{"prod": "analyze"}},
	}}}
	for _, payload := range []string{`{"env":"qa"}`, `{"component":"service/review"}`} {
		delivery := &WorkflowWebhookDelivery{Payload: json.RawMessage(payload)}
		if err := resolveWebhookDeliveryOptions(s, delivery); err == nil {
			t.Fatalf("payload %s unexpectedly accepted", payload)
		}
	}
}
func TestWebhookLeasesIndependentFromSchedules(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &SchedulerService{stateStore: store}
	ctx := context.Background()
	normal := &ScheduleContext{WorkspacePath: "Workflow/test", Schedule: WorkflowSchedule{ID: "cron", ScheduleType: "cron"}}
	hook := &ScheduleContext{WorkspacePath: normal.WorkspacePath, Schedule: WorkflowSchedule{ID: "hook", ScheduleType: "webhook"}, WebhookInput: &WorkflowWebhookDelivery{}, TriggerSource: "webhook"}
	for _, c := range []struct {
		s  *ScheduleContext
		id string
	}{{normal, "normal-run"}, {hook, "hook-run"}} {
		if err := svc.claimScheduleRun(ctx, c.s, c.id, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.claimScheduleRun(ctx, hook, "second-hook", time.Now()); err == nil {
		t.Fatal("same trigger overlap allowed")
	}
	parallelSchedule := WorkflowSchedule{ID: "parallel-hook", ScheduleType: "webhook"}
	parallelOne := &ScheduleContext{
		WorkspacePath: normal.WorkspacePath, Schedule: parallelSchedule,
		WebhookInput: &WorkflowWebhookDelivery{RunID: "parallel-one"}, TriggerSource: "webhook",
	}
	parallelTwo := &ScheduleContext{
		WorkspacePath: normal.WorkspacePath, Schedule: parallelSchedule,
		WebhookInput: &WorkflowWebhookDelivery{RunID: "parallel-two"}, TriggerSource: "webhook",
	}
	if scheduleRuntimeKey(parallelOne) == scheduleRuntimeKey(parallelTwo) {
		t.Fatal("parallel webhook deliveries shared runtime state")
	}
	if err := svc.claimScheduleRun(ctx, parallelOne, "parallel-one", time.Now()); err != nil {
		t.Fatalf("first parallel webhook claim: %v", err)
	}
	if err := svc.claimScheduleRun(ctx, parallelTwo, "parallel-two", time.Now()); err != nil {
		t.Fatalf("second parallel webhook claim: %v", err)
	}
	hook.Schedule.ID = "other-hook"
	if err := svc.claimScheduleRun(ctx, hook, "other-hook-run", time.Now()); err != nil {
		t.Fatal(err)
	}
	normal.Schedule.ID = "other-cron"
	if err := svc.claimScheduleRun(ctx, normal, "other-normal-run", time.Now()); err == nil {
		t.Fatal("schedule lock lost")
	}
}

func TestActiveWebhookDeliveryCountIsScopedToTrigger(t *testing.T) {
	states := map[string]*ScheduleRuntimeState{}
	for i := 0; i < maxWebhookConcurrency; i++ {
		sctx := &ScheduleContext{
			WorkspacePath: "Workflow/test",
			Schedule:      WorkflowSchedule{ID: "review-hook", ScheduleType: "webhook"},
			WebhookInput:  &WorkflowWebhookDelivery{RunID: fmt.Sprintf("run-%d", i)},
		}
		states[scheduleRuntimeKey(sctx)] = &ScheduleRuntimeState{LastStatus: "running"}
	}
	states[workflowScheduleRuntimeKey("Workflow/test", "other-hook")] = &ScheduleRuntimeState{LastStatus: "running"}
	states[workflowScheduleRuntimeKey("Workflow/test", "review-hook")] = &ScheduleRuntimeState{LastStatus: "completed"}
	states[workflowScheduleRuntimeKey("Workflow/other", "review-hook")] = &ScheduleRuntimeState{LastStatus: "running"}
	if got := activeWebhookDeliveryCount(states, "Workflow/test", "review-hook"); got != maxWebhookConcurrency {
		t.Fatalf("active review deliveries = %d, want %d", got, maxWebhookConcurrency)
	}
}
func TestWebhookRetentionKeepsTenCompletedRunsAndPreservesActiveRuns(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/test"
	if err := os.MkdirAll(filepath.Join(docs, workspace, "runs/iteration-0"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, workspace, "workflow.json"), []byte(`{"schema_version":1,"id":"test","label":"Test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &SchedulerService{stateStore: store}
	ctx := context.Background()
	folders := []string{}
	for i := 0; i < 14; i++ {
		id := fmt.Sprintf("run-%d", i)
		folder, err := allocateWebhookRunFolder(workspace, id)
		if err != nil {
			t.Fatal(err)
		}
		folders = append(folders, folder)
		if err := store.BeginRun(ctx, schedulerstate.Run{RunID: id, ScopeType: "workflow", ScopeID: workspace, LockKey: id, ScheduleID: id, TriggerSource: "webhook"}); err != nil {
			t.Fatal(err)
		}
		if err := store.AssignRunFolder(ctx, id, folder); err != nil {
			t.Fatal(err)
		}
		if i < 13 {
			if err := store.Transition(ctx, schedulerstate.Transition{RunID: id, To: schedulerstate.StateFailed, At: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if err := svc.pruneWebhookRunsChecked(workspace); err != nil {
			t.Fatal(err)
		}
	}
	for i, folder := range folders {
		_, err := os.Stat(filepath.Join(docs, workspace, "runs", folder))
		if i < 3 {
			if !os.IsNotExist(err) || !webhookArtifactsExpired(workspace, fmt.Sprintf("run-%d", i)) {
				t.Fatalf("old run retained: %s %v", folder, err)
			}
		} else if err != nil {
			t.Fatalf("retained/active removed: %s", folder)
		}
	}
	if _, err := os.Stat(filepath.Join(docs, workspace, "runs/iteration-0")); err != nil {
		t.Fatal("normal removed")
	}
	if _, err := store.GetRun(ctx, "run-0"); err != nil {
		t.Fatal("history removed")
	}
}
func TestWebhookConcurrentOutcomeIsolation(t *testing.T) {
	all := []RunFolderInfo{{Name: "iteration-0/dev"}, {Name: "iteration-1-hook/dev"}, {Name: "iteration-2-hook/prod"}}
	if got := webhookInvocationFolders(all, "iteration-1-hook", true); len(got) != 1 || got[0].Name != "iteration-1-hook/dev" {
		t.Fatalf("mixed hook result: %+v", got)
	}
	if got := webhookInvocationFolders(all, "iteration-0", false); len(got) != 1 || got[0].Name != "iteration-0/dev" {
		t.Fatalf("mixed schedule result: %+v", got)
	}
}

func TestWebhookInputsAllowIdentifiersAndRefuseSelectedVaultClashes(t *testing.T) {
	manifest := NewWorkflowManifest("test")
	manifest.Capabilities.SelectedGlobalSecretNames = &[]string{"VLT_A00001__DB_URL"}
	raw, _ := json.Marshal(manifest)
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/test"): string(raw), "Workflow/test/variables/variables.json": `{"variables":[{"name":"base_url"},{"name":"DB_URL"},{"name":"PATH"},{"name":"access_token"},{"name":"SECRET_DB_URL"},{"name":"RDS_SECRET_ID_DEV"}]}`}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	if err := validateWebhookVariableNames(context.Background(), "Workflow/test", []string{"base_url", "PATH", "access_token", "RDS_SECRET_ID_DEV"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"DB_URL", "SECRET_DB_URL", "unknown"} {
		if err := validateWebhookVariableNames(context.Background(), "Workflow/test", []string{name}); err == nil {
			t.Fatalf("unsafe variable %s accepted", name)
		}
	}
}

func TestWebhookFolderNumbersSurviveCleanup(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/test"
	if err := os.MkdirAll(filepath.Join(docs, workspace), 0700); err != nil {
		t.Fatal(err)
	}
	first, err := allocateWebhookRunFolder(workspace, "one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(docs, workspace, "runs", first)); err != nil {
		t.Fatal(err)
	}
	next, err := allocateWebhookRunFolder(workspace, "two")
	if err != nil || next == first {
		t.Fatalf("folder number reused: %s %v", next, err)
	}
}

func TestStoppingIdleWebhookDoesNotStopConcurrentSchedule(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewSchedulerService(&StreamingAPI{})
	svc.stateStore = store
	ctx := context.Background()
	normal := &ScheduleContext{WorkspacePath: "Workflow/test", Schedule: WorkflowSchedule{ID: "cron", ScheduleType: "cron"}}
	if err := svc.claimScheduleRun(ctx, normal, "normal-run", time.Now()); err != nil {
		t.Fatal(err)
	}
	svc.StopRunningJobForWorkflow("Workflow/test", "idle-hook")
	run, err := store.GetRun(ctx, "normal-run")
	if err != nil || run.CompletedAt != nil {
		t.Fatalf("unrelated schedule stopped: %+v %v", run, err)
	}
}

// PLAT-746-506 / PR1721: the same schedule key can acquire another run
// between chat Stop's lookup and its controller's write lock.
func TestChatStopCannotCancelAReplacementScheduleRun(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewSchedulerService(&StreamingAPI{})
	svc.stateStore = store
	ctx := context.Background()
	schedule := &ScheduleContext{WorkspacePath: "Workflow/test", Schedule: WorkflowSchedule{ID: "timer", ScheduleType: "cron"}}
	if err := svc.claimScheduleRun(ctx, schedule, "replacement", time.Now()); err != nil {
		t.Fatal(err)
	}
	key := scheduleRuntimeKey(schedule)
	svc.runtimeStates[key] = &ScheduleRuntimeState{ActiveRunID: "replacement", LastStatus: "running"}
	canceled := false
	svc.runCancels["replacement"] = func() { canceled = true }
	svc.stopRunningJob(key, "timer", "previous")
	run, err := store.GetRun(ctx, "replacement")
	if canceled || svc.runtimeStates[key].ActiveRunID != "replacement" || err != nil || schedulerstate.IsTerminal(run.State) {
		t.Fatalf("stale chat Stop changed replacement: canceled=%v state=%+v durable=%+v err=%v", canceled, svc.runtimeStates[key], run, err)
	}
	// The corresponding allowed case must reach the real cancellation handle.
	svc.stopRunningJob(key, "timer", "replacement")
	run, err = store.GetRun(ctx, "replacement")
	if !canceled || svc.runtimeStates[key].ActiveRunID != "" || err != nil || run.State != schedulerstate.StateStopped {
		t.Fatalf("matching chat Stop did not stop its run: canceled=%v state=%+v durable=%+v err=%v", canceled, svc.runtimeStates[key], run, err)
	}
}
