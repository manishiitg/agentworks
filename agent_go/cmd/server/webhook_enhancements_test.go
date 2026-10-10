package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookEnvelopeOptions(t *testing.T) {
	s := WorkflowSchedule{GroupNames: []string{"dev"}, Webhook: &WorkflowWebhookConfig{InputMode: "envelope", AllowedVariables: []string{"base_url"}}}
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"group":"dev","variables":{"base_url":"https://example.test"},"payload":{"pr":12}}`, true},
		{`{"group":"prod"}`, false}, {`{"variables":{"TOKEN":"bad"}}`, false}, {`{"variables":{"base_url":false}}`, false},
	} {
		d := &WorkflowWebhookDelivery{Payload: json.RawMessage(tc.body)}
		err := resolveWebhookDeliveryOptions(s, d)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.body, err)
		}
		if tc.ok && (d.Group != "dev" || d.Variables["base_url"] != "https://example.test" || string(d.Payload) != `{"pr":12}`) {
			t.Fatalf("bad envelope: %+v", d)
		}
	}
	s.Webhook.InputMode = "raw"
	d := &WorkflowWebhookDelivery{Payload: json.RawMessage(`{"group":"prod","variables":{"TOKEN":"data"}}`)}
	if err := resolveWebhookDeliveryOptions(s, d); err != nil || d.Group != "" || d.Variables != nil {
		t.Fatal("raw event interpreted as config")
	}
}

func TestWebhookRawPayloadMappingsSelectGroupAndNestedBranch(t *testing.T) {
	s := WorkflowSchedule{
		GroupNames:      []string{"customer-prod", "customer-staging"},
		RouteSelections: map[string]string{"workflow-route": "regression"},
		Webhook: &WorkflowWebhookConfig{InputMode: "raw", PayloadMappings: &WorkflowWebhookPayloadMappings{
			Group: &WorkflowWebhookValueMapping{Source: "$.env", Values: map[string]string{"prod": "customer-prod", "staging": "customer-staging"}},
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
	if delivery.Group != "customer-prod" {
		t.Fatalf("group = %q, want customer-prod", delivery.Group)
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
		Group: &WorkflowWebhookValueMapping{Source: "env", Values: map[string]string{"prod": "customer-prod"}},
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
func TestWebhookRetentionUsesWorkflowRunRetentionCountAndPreservesActiveRuns(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/test"
	if err := os.MkdirAll(filepath.Join(docs, workspace, "runs/iteration-0"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, workspace, "workflow.json"), []byte(`{"schema_version":1,"id":"test","label":"Test","run_retention_count":4}`), 0600); err != nil {
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
		if i < 9 {
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

func TestWebhookRejectsProtectedAndUndeclaredVariables(t *testing.T) {
	manifest := NewWorkflowManifest("test")
	manifest.Capabilities.SelectedSecrets = []string{"DB_URL"}
	raw, _ := json.Marshal(manifest)
	mock := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/test"): string(raw), "Workflow/test/variables/variables.json": `{"variables":[{"name":"base_url"},{"name":"DB_URL"},{"name":"PATH"},{"name":"access_token"}]}`}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	if err := validateWebhookVariableNames(context.Background(), "Workflow/test", []string{"base_url"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"DB_URL", "PATH", "access_token", "unknown"} {
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
