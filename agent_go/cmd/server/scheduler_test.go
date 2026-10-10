package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/robfig/cron/v3"
)

// manualWorkflowUpgradeTurns is test-only rendering for the manual Workshop
// migration contract. Production scheduling never constructs upgrade turns.
func manualWorkflowUpgradeTurns(manifest *WorkflowManifest, messages []string, workspacePath string) ([]scheduledWorkshopTurn, error) {
	upgradePlan := workflowVersionUpgradePlan(manifest)
	manifestVersion := workflowContractVersionForUpgrade(manifest)
	if !workflowContractVersionIsExecutionCompatible(manifestVersion) &&
		(len(upgradePlan) == 0 || !workflowContractVersionIsExecutionCompatible(upgradePlan[len(upgradePlan)-1].to)) {
		return nil, fmt.Errorf(
			"workflow has no complete upgrade path from version %q to %q for manual migration: %w",
			manifestVersion,
			WorkflowContractCurrentVersion,
			errWorkflowContractMigrationRequired,
		)
	}

	turns := make([]scheduledWorkshopTurn, 0, len(upgradePlan)+len(messages))
	for _, upgrade := range upgradePlan {
		query := bindWorkflowUpgradeWorkspacePath(upgrade.query, workspacePath)
		if strings.Contains(query, workflowUpgradeWorkspacePathPlaceholder) {
			return nil, fmt.Errorf("manual workflow upgrade %s requires a workspace path", upgrade.label)
		}
		turns = append(turns, scheduledWorkshopTurn{
			label:         upgrade.label,
			query:         query,
			upgradeTarget: upgrade.to,
		})
	}
	for i, message := range messages {
		turns = append(turns, scheduledWorkshopTurn{
			label: fmt.Sprintf("schedule-message-%d", i+1),
			query: message,
		})
	}
	return turns, nil
}

func TestBuildScheduleCronExpressionAlwaysSetsTimezone(t *testing.T) {
	tests := []struct {
		name     string
		cronExpr string
		timezone string
		want     string
	}{
		{
			name:     "utc timezone is explicit",
			cronExpr: "0 9 * * *",
			timezone: "UTC",
			want:     "CRON_TZ=UTC 0 9 * * *",
		},
		{
			name:     "empty timezone defaults to UTC",
			cronExpr: "0 9 * * *",
			timezone: "",
			want:     "CRON_TZ=UTC 0 9 * * *",
		},
		{
			name:     "named timezone is preserved",
			cronExpr: "0 18 * * *",
			timezone: "America/New_York",
			want:     "CRON_TZ=America/New_York 0 18 * * *",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildScheduleCronExpression(tt.cronExpr, tt.timezone); got != tt.want {
				t.Fatalf("buildScheduleCronExpression() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeRuntimeStatePreservesRunningPulseForSameSession(t *testing.T) {
	runtimeStarted := time.Now().Add(-time.Minute).UTC()
	historyStarted := runtimeStarted.Add(time.Millisecond)
	duration := int64(1200)

	got := mergeRuntimeStateWithRuns(ScheduleRuntimeState{
		LastStatus:    "running",
		LastRunAt:     &runtimeStarted,
		LastSessionID: "session-1",
	}, "schedule-1", []ScheduleRunEntry{{
		ID:         "run-1",
		ScheduleID: "schedule-1",
		SessionID:  "session-1",
		Status:     "success",
		StartedAt:  historyStarted,
		DurationMs: &duration,
	}}, true, "")

	if got.LastStatus != "running" {
		t.Fatalf("LastStatus = %q, want running while Pulse owns the session", got.LastStatus)
	}
	if got.LastSessionID != "session-1" {
		t.Fatalf("LastSessionID = %q, want session-1", got.LastSessionID)
	}
}

func TestMergeRuntimeStateAdoptsGenuinelyNewerRun(t *testing.T) {
	runtimeStarted := time.Now().Add(-time.Hour).UTC()
	historyStarted := runtimeStarted.Add(30 * time.Minute)

	got := mergeRuntimeStateWithRuns(ScheduleRuntimeState{
		LastStatus:    "running",
		LastRunAt:     &runtimeStarted,
		LastSessionID: "old-session",
	}, "schedule-1", []ScheduleRunEntry{{
		ID:         "run-2",
		ScheduleID: "schedule-1",
		SessionID:  "new-session",
		Status:     "success",
		StartedAt:  historyStarted,
	}}, false, "")

	if got.LastStatus != "success" || got.LastSessionID != "new-session" {
		t.Fatalf("merged state = %+v, want newer persisted run", got)
	}
}

func TestUpdateRuntimeStateSerializesMutations(t *testing.T) {
	svc := NewSchedulerService(nil)
	const workers = 32
	const increments = 250

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				svc.updateRuntimeState("schedule-1", func(state *ScheduleRuntimeState) {
					state.RunCount++
				})
			}
		}()
	}
	wg.Wait()

	got := svc.GetRuntimeState("schedule-1")
	if got.RunCount != workers*increments {
		t.Fatalf("RunCount = %d, want %d", got.RunCount, workers*increments)
	}
}

func TestRuntimeStateIsScopedAcrossWorkflows(t *testing.T) {
	svc := NewSchedulerService(nil)
	workflowOneKey := workflowScheduleRuntimeKey("Workflow/one", "copied-schedule")
	workflowTwoKey := workflowScheduleRuntimeKey("Workflow/two", "copied-schedule")

	svc.updateRuntimeState(workflowOneKey, func(state *ScheduleRuntimeState) {
		state.LastStatus = "running"
	})
	svc.updateRuntimeState(workflowTwoKey, func(state *ScheduleRuntimeState) {
		state.LastStatus = "stopped"
	})

	if got := svc.getRuntimeStateByKey(workflowOneKey); got.LastStatus != "running" {
		t.Fatalf("workflow one state = %+v", got)
	}
	if got := svc.getRuntimeStateByKey(workflowTwoKey); got.LastStatus != "stopped" {
		t.Fatalf("workflow two state = %+v", got)
	}
}

func TestScheduleStateLockKeyFromRuntimeKey(t *testing.T) {
	workflowKey := workflowScheduleRuntimeKey("/tmp/Workflow/demo", "daily")
	wantWorkflow := strings.Join([]string{"workflow", "/tmp/Workflow/demo"}, scheduleScopeSeparator)
	if got := scheduleStateLockKeyFromRuntimeKey(workflowKey); got != wantWorkflow {
		t.Fatalf("workflow lock key = %q, want %q", got, wantWorkflow)
	}
	pulseKey := workflowScheduleRuntimeKey("/tmp/Workflow/demo", manualWorkflowPulseScheduleID)
	wantPulse := strings.Join([]string{"workflow-pulse", "/tmp/Workflow/demo"}, scheduleScopeSeparator)
	if got := scheduleStateLockKeyFromRuntimeKey(pulseKey); got != wantPulse {
		t.Fatalf("Pulse lock key = %q, want %q", got, wantPulse)
	}
}

// A Pulse fix run holds the Pulse lock, not the workflow's schedule lock: a
// real schedule that comes due while Pulse repairs the workflow still starts
// (salesoutreach/upwork/social-media lost runs to fix runs, 2026-09-26/28).
func TestPulseFixRunDoesNotHoldTheWorkflowScheduleLock(t *testing.T) {
	workflow := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{ID: "daily-send"}}
	fix := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{ID: pulseFixRunScheduleID}}
	pulse := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{ID: manualWorkflowPulseScheduleID}}
	_, _, workflowKey := scheduleStateScope(workflow)
	_, _, fixKey := scheduleStateScope(fix)
	_, _, pulseKey := scheduleStateScope(pulse)
	if fixKey == workflowKey {
		t.Fatalf("a Pulse fix run took the workflow schedule lock %q", workflowKey)
	}
	if fixKey != pulseKey {
		t.Fatalf("fix runs and scheduled Pulse must share the one-Pulse-per-workflow lock: %q vs %q", fixKey, pulseKey)
	}
	if got := scheduleStateLockKeyFromRuntimeKey(workflowScheduleRuntimeKey("Workflow/demo", pulseFixRunScheduleID)); got != pulseKey {
		t.Fatalf("runtime-key mapping for a fix run = %q, want %q", got, pulseKey)
	}
}

func TestScheduleStateScopeUsesIndependentParallelLane(t *testing.T) {
	sequential := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{ID: "growth"}}
	parallel := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{
		ID: "measurement", ConcurrencyMode: "parallel", ParallelRiskAcknowledged: true,
	}}
	_, _, sequentialKey := scheduleStateScope(sequential)
	_, _, parallelKey := scheduleStateScope(parallel)
	if sequentialKey == parallelKey {
		t.Fatalf("parallel schedule reused sequential workflow lane: %q", parallelKey)
	}
	if !strings.Contains(parallelKey, "workflow-parallel") || !strings.Contains(parallelKey, "measurement") {
		t.Fatalf("parallel lock key lacks schedule-local identity: %q", parallelKey)
	}
}

func TestValidateScheduleRuntimePolicyRequiresParallelRiskAcknowledgement(t *testing.T) {
	schedule := WorkflowSchedule{ID: "measurement", ScheduleType: "cron", ConcurrencyMode: "parallel"}
	if err := validateScheduleRuntimePolicy(schedule); err == nil || !strings.Contains(err.Error(), "parallel_risk_acknowledged") {
		t.Fatalf("parallel schedule without acknowledgement error = %v", err)
	}
	schedule.ParallelRiskAcknowledged = true
	if err := validateScheduleRuntimePolicy(schedule); err != nil {
		t.Fatalf("approved parallel cron schedule rejected: %v", err)
	}
	schedule.ScheduleType = "webhook"
	if err := validateScheduleRuntimePolicy(schedule); err == nil {
		t.Fatal("parallel webhook schedule was accepted")
	}
}

func TestParallelScheduleOverlapsOnlyTrustedScheduleExecutions(t *testing.T) {
	api := &StreamingAPI{trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{}}
	svc := &SchedulerService{api: api}
	workspace := "Workflow/demo"
	put := func(sessionID, triggeredBy string) {
		api.trackedWorkflowExecutions = map[string]*TrackedWorkflowExecution{
			"exec": {ExecutionID: "exec", SessionID: sessionID, WorkspacePath: workspace, Kind: "full_workflow", Status: trackedExecutionStatusRunning, TriggeredBy: triggeredBy, StartedAt: time.Now()},
		}
	}

	put("manual-session", "manual")
	if got := svc.findActiveExecutionBlockingSchedule(workspace, true); got == nil {
		t.Fatal("parallel schedule was allowed to overlap manual workflow execution")
	}

	put("schedule-cron--measurement_1", "cron")
	api.scheduleInvocations.Store("schedule-cron--measurement_1", &todo_creation_human.ScheduleInvocation{AllowParallel: false})
	if got := svc.findActiveExecutionBlockingSchedule(workspace, true); got != nil {
		t.Fatalf("parallel schedule was blocked by another trusted schedule: %+v", got)
	}
	if got := svc.findActiveExecutionBlockingSchedule(workspace, false); got == nil {
		t.Fatal("sequential schedule ignored another sequential schedule")
	}

	api.scheduleInvocations.Store("schedule-cron--measurement_1", &todo_creation_human.ScheduleInvocation{AllowParallel: true})
	if got := svc.findActiveExecutionBlockingSchedule(workspace, false); got != nil {
		t.Fatalf("sequential lane was blocked by approved parallel schedule: %+v", got)
	}

	api.scheduleInvocations.Delete("schedule-cron--measurement_1")
	if got := svc.findActiveExecutionBlockingSchedule(workspace, true); got == nil {
		t.Fatal("parallel schedule ignored unbound scheduled/Pulse-like execution")
	}
}

// TestBuildScheduleContextThreadsOwnerUserID is the regression test for a
// real production incident on the Dominion deployment 2026-08-31/09-01:
// scheduled/cron runs always passed an empty user ID to startSessionInternal,
// which falls through to the "default" placeholder user for secret lookup.
// Nobody configures a workflow's API keys while logged in as "default", so
// every scheduled run's own secrets were silently empty even though the
// same secrets worked fine in an interactive chat session as the real
// owning user. Confirms buildScheduleContext (the single place a
// WorkflowManifest becomes a ScheduleContext before either
// startSessionInternal call site) actually carries CreatedBy forward.
func TestBuildScheduleContextThreadsOwnerUserID(t *testing.T) {
	manifest := &WorkflowManifest{ID: "demo", CreatedBy: "ac513919db5b67ed95b2263679e51052"}
	sctx := buildScheduleContext("Workflow/demo", manifest, WorkflowSchedule{ID: "daily"})
	if sctx.OwnerUserID != "ac513919db5b67ed95b2263679e51052" {
		t.Fatalf("OwnerUserID = %q, want the manifest's CreatedBy", sctx.OwnerUserID)
	}

	// A single-user workflow created before ownership fields existed executes
	// as the authenticated local owner. This must be explicit: an empty user ID
	// is rejected by the shared action-tool authorization boundary.
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "local-owner")
	legacy := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo"}, WorkflowSchedule{ID: "daily"})
	if legacy.OwnerUserID != "local-owner" {
		t.Fatalf("OwnerUserID = %q, want the single-user legacy owner", legacy.OwnerUserID)
	}
	requestCtx := internalBotRequestContext(context.Background(), legacy.OwnerUserID)
	resolve := (&StreamingAPI{}).bindToolExecutionContext(requestCtx, "schedule-cron--daily_123", QueryRequest{}, false)
	toolCtx, err := resolve(context.Background(), "execute_shell_command")
	if err != nil {
		t.Fatalf("legacy scheduled action tool rejected: %v", err)
	}
	if claims := GetUserFromContext(toolCtx); claims == nil || claims.UserID != "local-owner" {
		t.Fatalf("legacy scheduled tool claims = %+v, want local-owner", claims)
	}

	// An access owner is a durable authenticated principal even if an older
	// manifest has not been backfilled with CreatedBy.
	owned := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo", Access: &WorkflowAccess{Owners: []string{"access-owner"}}}, WorkflowSchedule{ID: "daily"})
	if owned.OwnerUserID != "access-owner" {
		t.Fatalf("OwnerUserID = %q, want the first recorded access owner", owned.OwnerUserID)
	}

	// There is no safe account to invent for an ownerless multi-user workflow.
	t.Setenv("MULTI_USER_MODE", "true")
	ambiguous := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo"}, WorkflowSchedule{ID: "daily"})
	if ambiguous.OwnerUserID != "" {
		t.Fatalf("OwnerUserID = %q, want empty for an ownerless multi-user workflow", ambiguous.OwnerUserID)
	}
}

func TestWorkflowTriggersUseOwnerExecutionScope(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"workflow-owner","username":"owner","can_create":true}]}`)
	manifest := &WorkflowManifest{
		ID:        "triggered",
		CreatedBy: "workflow-owner",
		Access:    &WorkflowAccess{Owners: []string{"workflow-owner", "co-owner"}},
	}
	sctx := buildScheduleContext("Workflow/triggered", manifest, WorkflowSchedule{
		ID:           "incoming",
		ScheduleType: "webhook",
	})
	sctx.TriggerSource = "webhook"
	sctx.WebhookInput = &WorkflowWebhookDelivery{DeliveryID: "delivery-1"}
	if sctx.OwnerUserID != "workflow-owner" {
		t.Fatalf("trigger OwnerUserID = %q, want workflow-owner", sctx.OwnerUserID)
	}

	requestCtx := internalBotRequestContext(context.Background(), sctx.OwnerUserID)
	resolve := (&StreamingAPI{}).bindToolExecutionContext(requestCtx, "schedule-webhook--incoming_123", QueryRequest{}, false)
	toolCtx, err := resolve(context.Background(), "execute_shell_command")
	if err != nil {
		t.Fatalf("owner-scoped trigger action tool rejected: %v", err)
	}
	if claims := GetUserFromContext(toolCtx); claims == nil || claims.UserID != "workflow-owner" {
		t.Fatalf("trigger tool claims = %+v, want workflow-owner", claims)
	}
}

// TestHandleCreateWorkflowManifestStampsCreatedBy proves the other half of
// the same fix: a newly created workflow actually records who created it,
// using the authenticated request's own user ID -- not a hardcoded or
// inferred value -- so buildScheduleContext has something real to thread
// through later.
func TestHandleCreateWorkflowManifestStampsCreatedBy(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	wsServer := httptest.NewServer(workspace)
	defer wsServer.Close()
	t.Setenv("WORKSPACE_API_URL", wsServer.URL)

	const workspacePath = "Workflow/created-by-test"
	body := `{"label":"CreatedBy Test","workspace_path":"` + workspacePath + `"}`
	req := httptest.NewRequest("POST", "/api/workflows/manifest", strings.NewReader(body))
	claims := &UserClaims{UserID: "user-abc-123", Username: "tester"}
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
	w := httptest.NewRecorder()

	api := &StreamingAPI{}
	api.handleCreateWorkflowManifest(w, req)

	if w.Code != 201 {
		t.Fatalf("handleCreateWorkflowManifest status = %d, body: %s", w.Code, w.Body.String())
	}

	saved, exists, err := ReadWorkflowManifest(context.Background(), workspacePath)
	if err != nil || !exists {
		t.Fatalf("ReadWorkflowManifest: exists=%v err=%v", exists, err)
	}
	if saved.CreatedBy != "user-abc-123" {
		t.Fatalf("saved manifest CreatedBy = %q, want the authenticated request's user ID", saved.CreatedBy)
	}
}

func TestPulseAndWorkflowScheduleUseSeparateDurableLanes(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewSchedulerService(nil)
	svc.stateStore = store
	manifest := &WorkflowManifest{ID: "demo"}
	pulse := buildScheduleContext("Workflow/demo", manifest, WorkflowSchedule{ID: manualWorkflowPulseScheduleID})
	daily := buildScheduleContext("Workflow/demo", manifest, WorkflowSchedule{ID: "daily"})
	now := time.Now().UTC()

	if err := svc.claimScheduleRun(context.Background(), pulse, "pulse-run", now); err != nil {
		t.Fatalf("claim Pulse lane: %v", err)
	}
	if err := svc.claimScheduleRun(context.Background(), daily, "daily-run", now); err != nil {
		t.Fatalf("workflow schedule should coexist with Pulse/chat lane: %v", err)
	}
	if err := svc.claimScheduleRun(context.Background(), daily, "second-daily-run", now); err == nil {
		t.Fatal("second workflow schedule unexpectedly acquired the workflow lane")
	}
}

// TestListFireDecisionsSurfacesSkippedOccurrences is the regression test for
// the customer-login live finding: four Technical Review passes theorized a
// missing scheduler misfire-recovery mechanism because get_schedule_runs
// only shows actual runs, and a global-pause skip never creates one. This
// proves the fix — ListFireDecisions must return the skip decision with its
// real reason, not just "started" occurrences.
func TestListFireDecisionsSurfacesSkippedOccurrences(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewSchedulerService(nil)
	svc.stateStore = store
	manifest := &WorkflowManifest{ID: "demo"}
	sctx := buildScheduleContext("Workflow/demo", manifest, WorkflowSchedule{ID: "daily"})
	scheduledFor := time.Date(2026, 8, 19, 3, 30, 0, 0, time.UTC)
	sctx.ScheduledFor = scheduledFor
	firedAt := scheduledFor.Add(59 * time.Second)

	if err := svc.recordScheduleFireDecision(context.Background(), sctx, "skipped_paused", "global scheduler pause is active", "", firedAt); err != nil {
		t.Fatalf("record skip decision: %v", err)
	}

	decisions, err := svc.ListFireDecisions(context.Background(), "Workflow/demo", "daily", 10)
	if err != nil {
		t.Fatalf("ListFireDecisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("got %d decisions, want 1: %+v", len(decisions), decisions)
	}
	got := decisions[0]
	if got.Decision != "skipped_paused" {
		t.Errorf("decision = %q, want %q", got.Decision, "skipped_paused")
	}
	if got.Reason != "global scheduler pause is active" {
		t.Errorf("reason = %q, want the real skip reason", got.Reason)
	}
	if !got.ScheduledFor.Equal(scheduledFor) {
		t.Errorf("scheduled_for = %v, want %v", got.ScheduledFor, scheduledFor)
	}
}

func TestPulseScheduleTimingSummaryOmitsSectionWhenPulseIsDisabled(t *testing.T) {
	ctx := context.Background()
	if got := pulseScheduleTimingSummary(ctx, "Workflow/demo", nil); got != "" {
		t.Fatalf("nil manifest: want empty summary (section omitted), got %q", got)
	}
	disabled := &WorkflowManifest{ID: "demo", Schedules: []WorkflowSchedule{
		{ID: "daily", Enabled: true, CronExpression: "0 9 * * *", Timezone: "UTC"},
	}}
	if got := pulseScheduleTimingSummary(ctx, "Workflow/demo", disabled); got != "" {
		t.Fatalf("Pulse off: want empty summary (section omitted), got %q", got)
	}
	// A retired pulse_review_only entry alone no longer describes a schedule;
	// it only migrates into pulse.enabled.
	enabled := &WorkflowManifest{ID: "demo", Pulse: &WorkflowPulseConfig{Enabled: true}}
	if got := pulseScheduleTimingSummary(ctx, "Workflow/pulse-timing-no-state", enabled); got == "" {
		t.Fatal("Pulse on: want a non-empty timing summary even before a next time is chosen")
	}
}

func TestDurableScheduleRunProjectionRecoversCompletedRun(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	run := schedulerstate.Run{
		RunID: "run-completed", ScopeType: "workflow", ScopeID: "Workflow/demo",
		LockKey: "workflow:Workflow/demo", ScheduleID: "daily", StartedAt: now,
	}
	if err := store.BeginRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := store.ForceTerminal(context.Background(), schedulerstate.Transition{
		RunID: run.RunID, To: schedulerstate.StateCompleted, Reason: "terminal receipt persisted", At: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewSchedulerService(nil)
	svc.stateStore = store
	status, errMsg, completedAt, ok := svc.durableScheduleRunProjection(context.Background(), run.RunID)
	if !ok || status != "success" || errMsg != "" || completedAt == nil {
		t.Fatalf("projection = status=%q error=%q completed=%v ok=%t", status, errMsg, completedAt, ok)
	}
}

func TestRecordOrQueueBlockedScheduleRetainsLatestOccurrence(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewSchedulerService(nil)
	svc.stateStore = store
	sctx := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo"}, WorkflowSchedule{
		ID: "daily", CollisionPolicy: "queue_latest", MaxStartDelayMinutes: 30,
	})
	first := time.Now().UTC()
	sctx.ScheduledFor = first
	if !svc.recordOrQueueBlockedSchedule(context.Background(), sctx, "busy", first) {
		t.Fatal("first occurrence was not queued")
	}
	second := first.Add(5 * time.Minute)
	sctx.ScheduledFor = second
	if !svc.recordOrQueueBlockedSchedule(context.Background(), sctx, "still busy", second) {
		t.Fatal("second occurrence was not queued")
	}
	rows, err := store.ListPendingOccurrences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].ScheduledFor.Equal(second) {
		t.Fatalf("queue_latest rows = %+v, want one occurrence at %s", rows, second)
	}
}

func TestScheduleDependencyDispositionUsesMatchingOccurrenceAndPolicy(t *testing.T) {
	newFixture := func(t *testing.T, terminal schedulerstate.State) (*SchedulerService, *ScheduleContext, time.Time, time.Time) {
		t.Helper()
		store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		svc := NewSchedulerService(nil)
		svc.stateStore = store

		location, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		parentOccurrence := time.Date(2026, 8, 19, 15, 55, 0, 0, location).UTC()
		dependentOccurrence := time.Date(2026, 8, 19, 16, 10, 0, 0, location).UTC()
		parent := schedulerstate.Run{
			RunID: "close-run", ScopeType: "workflow", ScopeID: "Workflow/trading",
			LockKey: "workflow:Workflow/trading", ScheduleID: "market-close", TriggerSource: "cron",
			ScheduledFor: parentOccurrence, StartedAt: parentOccurrence,
		}
		if err := store.BeginRun(context.Background(), parent); err != nil {
			t.Fatal(err)
		}
		for _, state := range []schedulerstate.State{schedulerstate.StateWorkflowRunning, schedulerstate.StateWorkflowFinished, terminal} {
			if err := store.Transition(context.Background(), schedulerstate.Transition{
				RunID: parent.RunID, To: state, At: parentOccurrence.Add(20 * time.Minute),
			}); err != nil {
				t.Fatalf("transition to %s: %v", state, err)
			}
		}
		if err := store.RecordFireDecision(context.Background(), schedulerstate.FireDecision{
			DecisionID: "close-fire", ScopeType: "workflow", ScopeID: "Workflow/trading",
			ScheduleID: "market-close", TriggerSource: "cron", Decision: "started", RunID: parent.RunID,
			ScheduledFor: parentOccurrence, FiredAt: parentOccurrence,
		}); err != nil {
			t.Fatal(err)
		}
		manifest := &WorkflowManifest{ID: "trading"}
		sctx := buildScheduleContext("Workflow/trading", manifest, WorkflowSchedule{
			ID: "daily-pulse", Timezone: "America/New_York", AfterScheduleID: "market-close",
		})
		sctx.ScheduledFor = dependentOccurrence
		return svc, sctx, parentOccurrence.Add(20 * time.Minute), dependentOccurrence
	}

	t.Run("completed parent releases the dependent occurrence", func(t *testing.T) {
		svc, sctx, completedAt, _ := newFixture(t, schedulerstate.StateCompleted)
		got, reason := svc.scheduleDependencyDisposition(context.Background(), sctx, completedAt.Add(time.Second))
		if got != scheduleDependencyReady {
			t.Fatalf("disposition=%v reason=%q, want ready", got, reason)
		}
	})

	t.Run("failed parent is visible and blocked by completed-only policy", func(t *testing.T) {
		svc, sctx, completedAt, _ := newFixture(t, schedulerstate.StateFailed)
		got, reason := svc.scheduleDependencyDisposition(context.Background(), sctx, completedAt.Add(time.Second))
		if got != scheduleDependencyBlocked || !strings.Contains(reason, "completed is required") {
			t.Fatalf("disposition=%v reason=%q, want explicit completed-only block", got, reason)
		}
	})

	t.Run("any-terminal policy releases after a failed parent", func(t *testing.T) {
		svc, sctx, completedAt, _ := newFixture(t, schedulerstate.StateFailed)
		sctx.Schedule.AfterTerminalStatus = "any_terminal"
		got, reason := svc.scheduleDependencyDisposition(context.Background(), sctx, completedAt.Add(time.Second))
		if got != scheduleDependencyReady {
			t.Fatalf("disposition=%v reason=%q, want ready", got, reason)
		}
	})

	t.Run("configured delay waits from the durable terminal timestamp", func(t *testing.T) {
		svc, sctx, completedAt, _ := newFixture(t, schedulerstate.StateCompleted)
		sctx.Schedule.AfterDelayMinutes = 10
		got, reason := svc.scheduleDependencyDisposition(context.Background(), sctx, completedAt.Add(5*time.Minute))
		if got != scheduleDependencyWaiting || !strings.Contains(reason, "configured delay") {
			t.Fatalf("disposition=%v reason=%q, want delay wait", got, reason)
		}
	})

	t.Run("all listed parents must release the dependent occurrence", func(t *testing.T) {
		svc, sctx, completedAt, dependentOccurrence := newFixture(t, schedulerstate.StateCompleted)
		sctx.Schedule.AfterScheduleID = ""
		sctx.Schedule.AfterScheduleIDs = []string{"market-close", "risk-snapshot"}
		got, reason := svc.scheduleDependencyDisposition(context.Background(), sctx, completedAt.Add(time.Second))
		if got != scheduleDependencyWaiting || !strings.Contains(reason, "risk-snapshot") {
			t.Fatalf("disposition=%v reason=%q, want wait for second prerequisite", got, reason)
		}

		riskOccurrence := dependentOccurrence.Add(-10 * time.Minute)
		risk := schedulerstate.Run{
			RunID: "risk-run", ScopeType: "workflow", ScopeID: "Workflow/trading",
			LockKey: "workflow:Workflow/trading", ScheduleID: "risk-snapshot", TriggerSource: "cron",
			ScheduledFor: riskOccurrence, StartedAt: riskOccurrence,
		}
		if err := svc.stateStore.BeginRun(context.Background(), risk); err != nil {
			t.Fatal(err)
		}
		for _, state := range []schedulerstate.State{schedulerstate.StateWorkflowRunning, schedulerstate.StateWorkflowFinished, schedulerstate.StateCompleted} {
			if err := svc.stateStore.Transition(context.Background(), schedulerstate.Transition{
				RunID: risk.RunID, To: state, At: completedAt,
			}); err != nil {
				t.Fatalf("transition risk prerequisite to %s: %v", state, err)
			}
		}
		if err := svc.stateStore.RecordFireDecision(context.Background(), schedulerstate.FireDecision{
			DecisionID: "risk-fire", ScopeType: "workflow", ScopeID: "Workflow/trading",
			ScheduleID: "risk-snapshot", TriggerSource: "cron", Decision: "started", RunID: risk.RunID,
			ScheduledFor: riskOccurrence, FiredAt: riskOccurrence,
		}); err != nil {
			t.Fatal(err)
		}
		got, reason = svc.scheduleDependencyDisposition(context.Background(), sctx, completedAt.Add(time.Second))
		if got != scheduleDependencyReady {
			t.Fatalf("disposition=%v reason=%q, want ready after every prerequisite", got, reason)
		}
	})

	t.Run("deadline expires a dependency that never released", func(t *testing.T) {
		svc, sctx, _, dependentOccurrence := newFixture(t, schedulerstate.StateCompleted)
		sctx.Schedule.DependencyDeadline = "16:00"
		got, reason := svc.scheduleDependencyDisposition(context.Background(), sctx, dependentOccurrence)
		if got != scheduleDependencyBlocked || !strings.Contains(reason, "deadline") {
			t.Fatalf("disposition=%v reason=%q, want deadline block", got, reason)
		}
	})
}

func TestStopRunningJobCancelsBeforeSessionStarts(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewSchedulerService(nil)
	svc.stateStore = store
	sctx := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo"}, WorkflowSchedule{ID: "daily"})
	runID := "run-before-session"
	if err := svc.claimScheduleRun(context.Background(), sctx, runID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	runCtx := svc.registerScheduleRunContext(runID)
	runtimeKey := scheduleRuntimeKey(sctx)
	svc.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.ActiveRunID = runID
		state.LastStatus = "running"
	})

	svc.stopRunningJob(runtimeKey, sctx.Schedule.ID)
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("run context was not canceled")
	}
	run, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != schedulerstate.StateStopped {
		t.Fatalf("durable state = %s, want stopped", run.State)
	}
	state := svc.getRuntimeStateByKey(runtimeKey)
	if state.LastStatus != "stopped" || state.ActiveRunID != "" {
		t.Fatalf("runtime state after stop = %+v", state)
	}
}

func TestScheduleRunPublicationRegistersCancelBeforeUnlock(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewSchedulerService(nil)
	svc.stateStore = store
	sctx := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo"}, WorkflowSchedule{ID: "daily"})
	runID := "atomic-publication"
	startedAt := time.Now().UTC()
	if err := svc.claimScheduleRun(context.Background(), sctx, runID, startedAt); err != nil {
		t.Fatal(err)
	}

	runtimeKey := scheduleRuntimeKey(sctx)
	svc.runtimeStatesMu.Lock()
	state := svc.getRuntimeStateLocked(runtimeKey)
	runCtx := svc.activateScheduleRunLocked(state, runID, startedAt)
	stopStarted := make(chan struct{})
	stopDone := make(chan struct{})
	go func() {
		close(stopStarted)
		svc.stopRunningJob(runtimeKey, sctx.Schedule.ID)
		close(stopDone)
	}()
	<-stopStarted
	select {
	case <-stopDone:
		t.Fatal("stop completed before active run publication released its lock")
	case <-time.After(20 * time.Millisecond):
	}
	svc.runtimeStatesMu.Unlock()

	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the atomically published run context")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("stop did not complete")
	}
}

func TestStopBetweenReservationAndDurableClaimPreventsExecution(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := NewSchedulerService(nil)
	svc.stateStore = store
	sctx := buildScheduleContext("Workflow/demo", &WorkflowManifest{ID: "demo"}, WorkflowSchedule{ID: "daily"})
	runtimeKey := scheduleRuntimeKey(sctx)
	runID := "stop-during-claim"
	startedAt := time.Now().UTC()

	svc.runtimeStatesMu.Lock()
	state := svc.getRuntimeStateLocked(runtimeKey)
	runCtx := svc.activateScheduleRunLocked(state, runID, startedAt)
	svc.runtimeStatesMu.Unlock()

	// This is the historical race window: Stop sees the reservation before the
	// SQLite run row exists.
	svc.stopRunningJob(runtimeKey, sctx.Schedule.ID)
	if err := svc.claimScheduleRun(context.Background(), sctx, runID, startedAt); err != nil {
		t.Fatalf("durable claim after stop: %v", err)
	}
	if !svc.abortCanceledScheduleRunBeforeStart(runCtx, sctx, runtimeKey, runID) {
		t.Fatal("canceled reservation was allowed to start")
	}

	run, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != schedulerstate.StateStopped {
		t.Fatalf("durable state = %s, want stopped", run.State)
	}
}

func TestScheduleConfigFingerprintChangesWithCapabilities(t *testing.T) {
	sctx := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{ID: "daily", Enabled: true, CronExpression: "0 9 * * *", Timezone: "UTC"}, Capabilities: WorkflowCapabilities{}}
	first := scheduleConfigFingerprint(sctx)
	second := scheduleConfigFingerprint(sctx)
	if first == "" || first != second {
		t.Fatalf("fingerprint should be stable and non-empty: %q %q", first, second)
	}
	sctx.Capabilities.SelectedSkills = []string{"research"}
	if changed := scheduleConfigFingerprint(sctx); changed == first {
		t.Fatal("capability change did not change schedule fingerprint")
	}
}

func TestScheduleWithReloadedCalendarItemUsesLatestOverrides(t *testing.T) {
	requested := &CalendarScheduleItem{ID: "item-1", Date: "2030-01-01", Time: "09:00", Messages: []string{"old"}}
	sched := WorkflowSchedule{
		ID: "calendar", ScheduleType: "calendar", Timezone: "UTC",
		Messages:      []string{"base"},
		CalendarItems: []CalendarScheduleItem{{ID: "item-1", Date: requested.Date, Time: requested.Time, Messages: []string{"new"}}},
	}
	resolved, item, ok := scheduleWithReloadedCalendarItem(sched, requested)
	if !ok || item == nil {
		t.Fatal("expected calendar item to resolve")
	}
	if got := strings.Join(resolved.Messages, ","); got != "new" {
		t.Fatalf("resolved messages = %q, want latest override", got)
	}
}

func TestLoadScheduleReplacesCalendarRegistrations(t *testing.T) {
	svc := NewSchedulerService(nil)
	when := time.Now().UTC().Add(24 * time.Hour)
	item := CalendarScheduleItem{ID: "item-1", Date: when.Format("2006-01-02"), Time: when.Format("15:04"), Messages: []string{"old"}}
	sched := WorkflowSchedule{ID: "calendar", Name: "Calendar", Enabled: true, ScheduleType: "calendar", Timezone: "UTC", CalendarItems: []CalendarScheduleItem{item}}
	if err := svc.LoadSchedule(&ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: sched}); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	sched.CalendarItems[0].Messages = []string{"new"}
	if err := svc.LoadSchedule(&ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: sched}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	keyPrefix := workflowScheduleRuntimeKey("Workflow/demo", sched.ID) + "__cal__"
	matching := 0
	for key, job := range svc.jobs {
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}
		matching++
		if job.sctx.CalendarItem == nil || strings.Join(job.sctx.Schedule.Messages, ",") != "new" {
			t.Fatalf("calendar registration did not retain latest item override: %+v", job.sctx)
		}
	}
	if matching != 1 {
		t.Fatalf("calendar registrations = %d, want 1", matching)
	}
}

func TestLoadScheduleDoesNotRememberInvalidCronFingerprint(t *testing.T) {
	svc := NewSchedulerService(nil)
	sctx := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{
		ID: "daily", Enabled: true, CronExpression: "not-a-cron", Timezone: "UTC",
	}}
	runtimeKey := scheduleRuntimeKey(sctx)
	if err := svc.LoadSchedule(sctx); err == nil {
		t.Fatal("invalid cron unexpectedly loaded")
	}
	if _, ok := svc.scheduleFingerprints[runtimeKey]; ok {
		t.Fatal("invalid cron fingerprint should not suppress the next rescan")
	}

	sctx.Schedule.CronExpression = "0 9 * * *"
	if err := svc.LoadSchedule(sctx); err != nil {
		t.Fatalf("corrected cron was not retried: %v", err)
	}
	if _, ok := svc.scheduleFingerprints[runtimeKey]; !ok {
		t.Fatal("valid schedule fingerprint was not recorded")
	}
}

func TestLoadScheduleDoesNotRegisterLegacyPulseCron(t *testing.T) {
	svc := NewSchedulerService(nil)
	sctx := &ScheduleContext{
		WorkspacePath: "Workflow/test",
		Schedule: WorkflowSchedule{
			ID: "legacy-pulse", Enabled: true, PulseReviewOnly: true,
			CronExpression: "0 9 * * *", Timezone: "UTC",
		},
	}
	if err := svc.LoadSchedule(sctx); err != nil {
		t.Fatalf("LoadSchedule() error = %v", err)
	}
	if len(svc.jobs) != 0 {
		t.Fatalf("legacy Pulse cron registered %d jobs, want 0", len(svc.jobs))
	}
}

func TestRemoveJobDropsInactiveRuntimeState(t *testing.T) {
	svc := NewSchedulerService(nil)
	runtimeKey := workflowScheduleRuntimeKey("Workflow/demo", "daily")
	svc.updateRuntimeState(runtimeKey, func(state *ScheduleRuntimeState) {
		state.LastStatus = "success"
	})
	if err := svc.removeJobByKey(runtimeKey); err != nil {
		t.Fatal(err)
	}
	svc.runtimeStatesMu.RLock()
	_, exists := svc.runtimeStates[runtimeKey]
	svc.runtimeStatesMu.RUnlock()
	if exists {
		t.Fatal("removed schedule retained inactive runtime state")
	}
}

func TestValidateScheduleTimezone(t *testing.T) {
	valid := []string{"UTC", "Asia/Kolkata", "America/New_York"}
	for _, timezone := range valid {
		t.Run("valid "+timezone, func(t *testing.T) {
			if err := ValidateScheduleTimezone(timezone); err != nil {
				t.Fatalf("ValidateScheduleTimezone(%q) returned error: %v", timezone, err)
			}
		})
	}

	invalid := []string{"", "IST", "EST", "Not/AZone"}
	for _, timezone := range invalid {
		t.Run("invalid "+timezone, func(t *testing.T) {
			if err := ValidateScheduleTimezone(timezone); err == nil {
				t.Fatalf("ValidateScheduleTimezone(%q) returned nil error", timezone)
			}
		})
	}
}

func TestWorkflowScheduleShouldResumePreviousIsOptIn(t *testing.T) {
	trueValue := true
	falseValue := false

	tests := []struct {
		name           string
		resumePrevious *bool
		want           bool
	}{
		{
			name: "omitted starts fresh",
			want: false,
		},
		{
			name:           "explicit false starts fresh",
			resumePrevious: &falseValue,
			want:           false,
		},
		{
			name:           "explicit true resumes",
			resumePrevious: &trueValue,
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched := WorkflowSchedule{ResumePrevious: tt.resumePrevious}
			if got := sched.ShouldResumePrevious(); got != tt.want {
				t.Fatalf("ShouldResumePrevious() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkflowScheduleListExposesWorkshopMode(t *testing.T) {
	workspacePath := "Workflow/social-media"
	manifest := &WorkflowManifest{
		SchemaVersion: WorkflowManifestSchemaVersion,
		ID:            "social-media",
		Label:         "Social Media",
		Schedules: []WorkflowSchedule{
			{
				ID:             "run-schedule",
				Name:           "Daily publish",
				CronExpression: "0 9 * * *",
				Timezone:       "Asia/Kolkata",
				Enabled:        true,
				GroupNames:     []string{"group-1"},
				Mode:           "workshop",
				WorkshopMode:   "run",
			},
			{
				ID:             "optimizer-schedule",
				Name:           "Goal Advisor",
				CronExpression: "0 23 * * 1,4",
				Timezone:       "Asia/Kolkata",
				Enabled:        true,
				GroupNames:     []string{"group-1"},
				Mode:           "workshop",
				WorkshopMode:   "optimizer",
			},
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	callbacks := (&StreamingAPI{}).buildSchedulerCallbacks()
	out, err := callbacks.ListSchedules(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	for _, want := range []string{
		"## Schedules (2 found)",
		"### Daily publish",
		"- **Mode**: `workshop`",
		"- **Workshop Mode**: `run`",
		"### Goal Advisor",
		"- **Workshop Mode**: `optimizer`",
		"- **Type**: cron",
		"- **Cron**: `0 23 * * 1,4`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("schedule list missing %q:\n%s", want, out)
		}
	}
}

func TestWorkflowScheduleListExposesEffectiveWebhookConcurrency(t *testing.T) {
	workspacePath := "Workflow/webhook-concurrency"
	manifest := &WorkflowManifest{
		SchemaVersion: WorkflowManifestSchemaVersion,
		ID:            "webhook-concurrency",
		Label:         "Webhook concurrency",
		Schedules: []WorkflowSchedule{{
			ID:              "review-hook",
			Name:            "Review hook",
			ScheduleType:    "webhook",
			Timezone:        "UTC",
			Enabled:         true,
			Mode:            "workshop",
			WorkshopMode:    "run",
			CollisionPolicy: "skip",
			ConcurrencyMode: "sequential",
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	out, err := (&StreamingAPI{}).buildSchedulerCallbacks().ListSchedules(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	for _, want := range []string{
		"- **Type**: webhook",
		"- **Delivery concurrency**: parallel, up to 4 active deliveries (server-enforced)",
		"- **Overflow policy**: retryable busy response (HTTP 503 with Retry-After)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("webhook schedule list missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- **Concurrency mode**: sequential") || strings.Contains(out, "- **Collision policy**: skip") {
		t.Fatalf("webhook schedule list exposed inapplicable schedule policy:\n%s", out)
	}
	response := buildJobResponse(workspacePath, manifest, manifest.Schedules[0], ScheduleRuntimeState{}, WorkflowScheduleMissedStatus{})
	if response.MaxConcurrency != maxWebhookConcurrency || response.ConcurrencyMode != "" || response.CollisionPolicy != "" {
		t.Fatalf("webhook API policy = max %d, concurrency %q, collision %q", response.MaxConcurrency, response.ConcurrencyMode, response.CollisionPolicy)
	}
}

// TestUpdateScheduleClearsMessagesOnlyWhenExplicitlySet pins PLAT-097:
// update_schedule(messages=[]) or update_schedule(messages=null) must clear a
// schedule's messages back to the route-based default, and omitting the field
// entirely must leave existing messages untouched. Before the fix, all three
// cases collapsed to the same "messages == nil" value downstream, so an
// explicit clear silently no-oped — the schedule reported success but
// workflow.json kept the full prior array, with no way to migrate a
// message-based schedule to the canonical route-backed model without
// deleting and recreating it (losing get_schedule_runs history).
func TestUpdateScheduleClearsMessagesOnlyWhenExplicitlySet(t *testing.T) {
	workspacePath := "Workflow/social-media"
	newManifest := func() *WorkflowManifest {
		return &WorkflowManifest{
			SchemaVersion: WorkflowManifestSchemaVersion,
			ID:            "social-media",
			Label:         "Social Media",
			Schedules: []WorkflowSchedule{
				{
					ID:        "run-schedule",
					PulseMode: "basic", PulseModeReason: "Routine scheduled processing",
					Name:           "Daily publish",
					CronExpression: "0 9 * * *",
					Timezone:       "Asia/Kolkata",
					Enabled:        true,
					GroupNames:     []string{"group-1"},
					Mode:           "workshop",
					WorkshopMode:   "run",
					Messages:       []string{"Run the full workflow using run_full_workflow(group_name=\"group-1\")"},
				},
			},
		}
	}

	setup := func(t *testing.T) (*todo_creation_human.SchedulerCallbacks, func() []string) {
		t.Helper()
		manifest := newManifest()
		manifestJSON, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		variablesJSON, err := json.Marshal(VariablesManifest{
			Groups: []VariableGroup{{Name: "group-1", Enabled: true, Values: map[string]string{}}},
		})
		if err != nil {
			t.Fatalf("marshal variables manifest: %v", err)
		}
		mock := &mockWorkspaceAPI{files: map[string]string{
			workspacePath + "/workflow.json":            string(manifestJSON),
			workspacePath + "/variables/variables.json": string(variablesJSON),
		}}
		workspace := httptest.NewServer(mock)
		t.Cleanup(workspace.Close)
		t.Setenv("WORKSPACE_API_URL", workspace.URL)

		callbacks := (&StreamingAPI{}).buildSchedulerCallbacks()
		currentMessages := func() []string {
			mock.mu.Lock()
			content := mock.files[workspacePath+"/workflow.json"]
			mock.mu.Unlock()
			var current WorkflowManifest
			if err := json.Unmarshal([]byte(content), &current); err != nil {
				t.Fatalf("unmarshal persisted manifest: %v", err)
			}
			return current.Schedules[0].Messages
		}
		return callbacks, currentMessages
	}

	t.Run("omitting messages leaves existing messages untouched", func(t *testing.T) {
		callbacks, currentMessages := setup(t)
		if _, err := callbacks.UpdateSchedule(context.Background(), "run-schedule", "", "", "", nil, false, nil, false, nil, "", nil, false, nil, "", nil, nil, nil); err != nil {
			t.Fatalf("UpdateSchedule() error = %v", err)
		}
		if got := currentMessages(); len(got) != 1 {
			t.Fatalf("messages = %v, want unchanged 1-item array", got)
		}
	})

	// Both interactive_workshop_manager.go's append-loop parser and
	// workflow_schedule_tools.go's stringSlice() helper turn a JSON []
	// (empty array) into a nil Go slice, same as an explicit JSON null — so
	// this is the shape that actually reaches UpdateSchedule for messages=[]
	// through the primary (workshop) tool path. setMessages=true is what
	// must carry the "clear" intent, since messages itself is
	// indistinguishable from "omitted".
	t.Run("explicit empty array clears messages (nil-slice parsing shape)", func(t *testing.T) {
		callbacks, currentMessages := setup(t)
		if _, err := callbacks.UpdateSchedule(context.Background(), "run-schedule", "", "", "", nil, false, nil, false, nil, "", nil, true, nil, "", nil, nil, nil); err != nil {
			t.Fatalf("UpdateSchedule() error = %v", err)
		}
		if got := currentMessages(); len(got) != 0 {
			t.Fatalf("messages = %v, want cleared to empty", got)
		}
	})

	// workflow_schedule_tools.go's stringSlice() helper produces a non-nil
	// empty slice for messages=[] specifically (unlike the workshop path
	// above) — pin that setMessages, not messages' nil-ness, is what
	// UpdateSchedule must trust either way.
	t.Run("explicit empty array clears messages (non-nil empty-slice parsing shape)", func(t *testing.T) {
		callbacks, currentMessages := setup(t)
		if _, err := callbacks.UpdateSchedule(context.Background(), "run-schedule", "", "", "", nil, false, nil, false, nil, "", []string{}, true, nil, "", nil, nil, nil); err != nil {
			t.Fatalf("UpdateSchedule() error = %v", err)
		}
		if got := currentMessages(); len(got) != 0 {
			t.Fatalf("messages = %v, want cleared to empty", got)
		}
	})

	t.Run("explicit null clears messages", func(t *testing.T) {
		callbacks, currentMessages := setup(t)
		if _, err := callbacks.UpdateSchedule(context.Background(), "run-schedule", "", "", "", nil, false, nil, false, nil, "", nil, true, nil, "", nil, nil, nil); err != nil {
			t.Fatalf("UpdateSchedule() error = %v", err)
		}
		if got := currentMessages(); len(got) != 0 {
			t.Fatalf("messages = %v, want cleared to empty", got)
		}
	})
}

// TestCreateAndUpdatePulseReviewOnlyScheduleSkipsGroupNamesRequirement pins
// PLAT-115 at the real CreateSchedule/UpdateSchedule implementations (not
// just the tool-layer guard): a pulse_review_only schedule must not be
// rejected for missing group_names at create time, and a later update that
// only changes its cron must not be rejected either — CreateSchedule's own
// explicit skip and UpdateSchedule's final unconditional
// validateScheduleGroupNamesForWorkspace revalidation (which runs
// independent of whether this call touched group_names at all) both had to
// be guarded, not just the one the caller happens to exercise first.
func TestCreateAndUpdatePulseReviewOnlyScheduleSkipsGroupNamesRequirement(t *testing.T) {
	workspacePath := "Workflow/social-media"
	manifest := &WorkflowManifest{
		SchemaVersion: WorkflowManifestSchemaVersion,
		ID:            "social-media",
		Label:         "Social Media",
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
		// Deliberately no variables/variables.json — a PulseReviewOnly schedule
		// must never need it, unlike an ordinary workflow-execution schedule.
	}}
	workspace := httptest.NewServer(mock)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	callbacks := (&StreamingAPI{}).buildSchedulerCallbacks()
	readManifest := func() WorkflowManifest {
		mock.mu.Lock()
		content := mock.files[workspacePath+"/workflow.json"]
		mock.mu.Unlock()
		var current WorkflowManifest
		if err := json.Unmarshal([]byte(content), &current); err != nil {
			t.Fatalf("unmarshal persisted manifest: %v", err)
		}
		return current
	}

	if _, err := callbacks.CreateSchedule(context.Background(), workspacePath, "Periodic Pulse Review", "0 3 * * *", "Asia/Kolkata",
		nil, nil, "workshop", nil, "", "run", nil, true, todo_creation_human.ScheduleRuntimePolicy{PulseMode: "basic", PulseModeReason: "Legacy compatibility review fixture"}); err != nil {
		t.Fatalf("CreateSchedule(pulseReviewOnly=true) error = %v, want success without group_names", err)
	}
	current := readManifest()
	if len(current.Schedules) != 1 {
		t.Fatalf("schedules = %d, want 1", len(current.Schedules))
	}
	if !current.Schedules[0].PulseReviewOnly {
		t.Fatal("persisted schedule must have pulse_review_only=true")
	}
	if len(current.Schedules[0].GroupNames) != 0 {
		t.Fatalf("group_names = %v, want empty for a pulse_review_only schedule", current.Schedules[0].GroupNames)
	}
	jobID := current.Schedules[0].ID

	newCron := "0 4 * * *"
	if _, err := callbacks.UpdateSchedule(context.Background(), jobID, "", newCron, "", nil, false, nil, false, nil, "", nil, false, nil, "", nil, nil, nil); err != nil {
		t.Fatalf("UpdateSchedule() on an existing pulse_review_only schedule, changing only cron, error = %v", err)
	}
	current = readManifest()
	if current.Schedules[0].CronExpression != newCron {
		t.Fatalf("cron_expression = %q, want %q", current.Schedules[0].CronExpression, newCron)
	}
	if !current.Schedules[0].PulseReviewOnly {
		t.Fatal("pulse_review_only must survive an unrelated update")
	}
}

func TestPostRunMonitorUsesDynamicModulesAndSingleFinalizer(t *testing.T) {
	steps := pulseLifecycleSteps()
	// Goal Work (strategic-review) runs first; platform upkeep follows. Workflow
	// Review is not a Pulse stage: it runs before runs (PLAT-697 phase 0).
	want := []string{"gate", "strategic-review", "architecture-review", "technical-review", "finalize"}
	if len(steps) != len(want) {
		t.Fatalf("stages=%d, want %d", len(steps), len(want))
	}
	for i, label := range want {
		if steps[i].label != label {
			t.Fatalf("stage %d=%q, want %q", i, steps[i].label, label)
		}
	}
	for _, stage := range steps[1:4] {
		for _, contract := range []string{"do the review yourself", "your own subagents", "Do not render a dashboard"} {
			if !strings.Contains(stage.query, contract) {
				t.Fatalf("%s missing %s", stage.label, contract)
			}
		}
	}
	if !strings.Contains(steps[4].query, "PULSE FINALIZER") {
		t.Fatal("missing finalization")
	}
}

func TestGoalAdvisorGuidanceBalancesExplorationEvidenceAndHumanDecisions(t *testing.T) {
	repoRoot := findRepoRoot(t)
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(raw)
	}

	advisorGuidance := read("agent_go/cmd/server/guidance/templates/system/strategy-auditor.md")
	for _, want := range []string{
		"expected value", "hypotheses", "guardrails", "human_input_id",
	} {
		if !strings.Contains(advisorGuidance, want) {
			t.Fatalf("goal-advisor guidance missing %q", want)
		}
	}
}

func TestDesignPlanGuidanceSupportsReadOnlyPulseChecklist(t *testing.T) {
	repoRoot := findRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(repoRoot, "agent_go/cmd/server/guidance/templates/builder/design-plan.md"))
	if err != nil {
		t.Fatalf("read design-plan guidance: %v", err)
	}
	guidance := string(raw)
	for _, want := range []string{
		"parent Pulse prompt",
		"read-only checklist",
		"overrides persistence",
		"do not edit any workspace file",
		"parent Pulse Fixer remains the only writer",
		"technical_review",
		"is this checklist's automated owner",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("design-plan guidance missing Pulse read-only contract %q:\n%s", want, guidance)
		}
	}
}

func TestGateCanRunOpsWithoutEngineering(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/ops-only"
	pulseRunID := "pulse-ops-only"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "Runtime cost regressed."},
	})); err != nil {
		t.Fatalf("record ops-only worklist: %v", err)
	}
	due, err := pulseWorklistHasDueModule(ctx, workspacePath, pulseRunID)
	if err != nil {
		t.Fatalf("inspect ops-only worklist: %v", err)
	}
	if !due {
		t.Fatal("ops-only worklist should schedule the agent-owned Review+Fix turn")
	}
}

func TestGateCanSkipEveryReviewAndFixer(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/all-reviews-skipped"
	pulseRunID := "pulse-all-reviews-skipped"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(nil)); err != nil {
		t.Fatalf("record all-skipped worklist: %v", err)
	}

	due, err := pulseWorklistHasDueModule(ctx, workspacePath, pulseRunID)
	if err != nil {
		t.Fatalf("inspect all-skipped worklist: %v", err)
	}
	if due {
		t.Fatal("all-skipped worklist must omit the Review+Fix turn")
	}
}

func TestPostRunMonitorPrependsWorkflowVersionUpgradeForOldManifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.0"}, "1.0.0")
}

func TestScheduledWorkshopTurnsCurrentWorkflowStartsWithScheduleMessage(t *testing.T) {
	turns, err := manualWorkflowUpgradeTurns(&WorkflowManifest{Version: WorkflowContractCurrentVersion}, []string{"first", "second"}, "Workflow/test")
	if err != nil {
		t.Fatalf("scheduledWorkshopTurns: %v", err)
	}
	if len(turns) != 2 || turns[0].label != "schedule-message-1" || turns[1].label != "schedule-message-2" {
		t.Fatalf("current workflow turns = %+v", turns)
	}
}

func TestScheduledWorkshopTurnsRejectsUnknownVersionBeforeScheduleMessage(t *testing.T) {
	turns, err := manualWorkflowUpgradeTurns(&WorkflowManifest{Version: "9.9.9"}, []string{"must not run"}, "Workflow/test")
	if err == nil || !strings.Contains(err.Error(), "no complete upgrade path") {
		t.Fatalf("error = %v, want no complete upgrade path", err)
	}
	if turns != nil {
		t.Fatalf("turns = %+v, want nil", turns)
	}
}

func TestPostRunMonitorPrependsWorkflowVersionUpgradeForMissingVersion(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{}, "1.0.0")
}

func TestPostRunMonitorPrependsPublishGateUpgradeForVersion101Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.1"}, "1.0.1")
}

func TestPostRunMonitorPrependsPulseReadabilityUpgradeForVersion103Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.3"}, "1.0.3")
}

func TestPostRunMonitorPrependsPulseFilterUpgradeForVersion104Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.4"}, "1.0.4")
}

func TestPostRunMonitorPrependsRichPulseWidgetUpgradeForVersion105Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.5"}, "1.0.5")
}

func TestPostRunMonitorPrependsLegacyOptimizerCleanupUpgradeForVersion106Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.6"}, "1.0.6")
}

func TestPostRunMonitorPrependsPulseDatePickerCleanupForVersion107Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.7"}, "1.0.7")
}

func TestPostRunMonitorPrependsStableSoulAndPulseHierarchyUpgradeForVersion108Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.8"}, "1.0.8")
}

func TestPostRunMonitorPrependsMessageSequenceCodeMigrationForVersion109Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.9"}, "1.0.9")
}

func TestPostRunMonitorPrependsPulseHistoryContractUpgradeForVersion110Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.10"}, "1.0.10")
}

// assertDirectContractUpgrade checks the upgrade turns a workflow at `from`
// receives. It reads the blocking pre-run preflight, which is the only path
// that delivers contract migrations: Pulse used to deliver a second copy of
// them (b4e4fc14), the preflight replaced that (f58ac5b5), and the older path
// was finally removed once it started bundling four unverified rungs into one
// Review+Fix turn.
func assertDirectContractUpgrade(t *testing.T, manifest *WorkflowManifest, from string) {
	t.Helper()
	turns, err := manualWorkflowUpgradeTurns(manifest, nil, "Workflow/test")
	if err != nil {
		t.Fatalf("manualWorkflowUpgradeTurns(%s): %v", from, err)
	}
	upgrades := workflowVersionUpgradePlan(&WorkflowManifest{Version: from})
	if got, want := len(turns), len(upgrades); got != want {
		t.Fatalf("upgrade turns for %s = %d, want %d", from, got, want)
	}
	for index, upgrade := range upgrades {
		if got := turns[index].label; got != upgrade.label {
			t.Fatalf("upgrade %d label = %q, want %q", index, got, upgrade.label)
		}
		if got := turns[index].upgradeTarget; got != upgrade.to {
			t.Fatalf("upgrade %q target = %q, want %q", upgrade.label, got, upgrade.to)
		}
		// The version pair reached only the retired Pulse path for a while, so
		// the preflight never told the agent what it was migrating between.
		for _, want := range []string{
			"WORKFLOW CONTRACT UPGRADE",
			`Current workflow.json version: "` + from + `"`,
			`Target workflow contract version: "` + upgrade.to + `"`,
		} {
			if !strings.Contains(turns[index].query, want) {
				t.Fatalf("upgrade %q missing %q:\n%s", upgrade.label, want, turns[index].query)
			}
		}
	}
}

func TestNoUpgradeTurnsForAWorkflowAlreadyAtTheCurrentContract(t *testing.T) {
	turns, err := manualWorkflowUpgradeTurns(&WorkflowManifest{Version: WorkflowContractCurrentVersion}, []string{"run the workflow"}, "Workflow/test")
	if err != nil {
		t.Fatalf("scheduledWorkshopTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].upgradeTarget != "" {
		t.Fatalf("a current-contract workflow should start straight at its schedule message: %+v", turns)
	}
}

func TestPostRunMonitorPrependsLightweightPulseReportUpgradeForVersion118Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.18"}, "1.0.18")
}

func TestPostRunMonitorPrependsExecutivePulseJournalUpgradeForVersion119Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.19"}, "1.0.19")
}

func TestPostRunMonitorPrependsArtifactPurityUpgradeForVersion120Manifest(t *testing.T) {
	assertDirectContractUpgrade(t, &WorkflowManifest{Version: "1.0.20"}, "1.0.20")
}

func TestWorkflowHasPendingPlanChangelogArtifactReview(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		want      bool
		wantError bool
	}{
		{
			name:  "missing changelog folder",
			files: map[string]string{},
			want:  false,
		},
		{
			name: "unreviewed changelog entry",
			files: map[string]string{
				"Workflow/demo/planning/changelog/changelog-2026-07-02-06-12-46.json": `{"entries":[{"timestamp":"2026-07-02T06:12:46Z","tool":"update_regular_step","reason":"test","step_ids":["step-a"]}]}`,
			},
			want: true,
		},
		{
			name: "reviewed changelog entries",
			files: map[string]string{
				"Workflow/demo/planning/changelog/changelog-2026-07-02-06-12-46.json": `{"entries":[{"timestamp":"2026-07-02T06:12:46Z","tool":"update_regular_step","reason":"test","step_ids":["step-a"],"artifact_review":{"done":true,"reviewed_at":"2026-07-02T06:20:00Z","reviewed_by":"pulse_fixer","result":"clean"}}]}`,
			},
			want: false,
		},
		{
			name: "one unreviewed entry keeps review pending",
			files: map[string]string{
				"Workflow/demo/planning/changelog/changelog-2026-07-02-06-12-46.json": `{"entries":[{"timestamp":"2026-07-02T06:12:46Z","tool":"update_regular_step","reason":"old","artifact_review":{"done":true}},{"timestamp":"2026-07-02T06:13:46Z","tool":"update_step_config","reason":"new"}]}`,
			},
			want: true,
		},
		{
			name: "malformed changelog is pending",
			files: map[string]string{
				"Workflow/demo/planning/changelog/changelog-2026-07-02-06-12-46.json": `{`,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := httptest.NewServer(&mockWorkspaceAPI{files: tt.files})
			defer workspace.Close()
			t.Setenv("WORKSPACE_API_URL", workspace.URL)

			got, err := workflowHasPendingPlanChangelogArtifactReview(context.Background(), "Workflow/demo")
			if tt.wantError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("workflowHasPendingPlanChangelogArtifactReview() = %v, want %v", got, tt.want)
			}
		})
	}
}

// pulseWorklistModulesDue lets a caller ask about a specific module subset —
// needed once plan_drift_review became its own preceding lifecycle step, so
// each step only checks the modules it is actually responsible for.
// forcePulseModuleDueForLateRepairDebt is the strategic-only edge-case fix:
// Gate's own due decision for technical_review is a static per-pass row with
// no live recomputation, so a plan_drift_review finding filed after Gate
// runs must amend that row directly, not merely check it, or the
// already-scheduled (strategic_review-only) review-fix turn would read
// technical_review.due=false forever and never pick up the new repair work.
func TestForcePulseModuleDueForLateRepairDebtFlipsDueAndClearsResult(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/force-module-due"
	pulseRunID := "pulse-force-module-due"

	// Gate's original decision: only strategic_review is due.
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleStrategicReview: {Module: pulseModuleStrategicReview, Due: true, Reason: "Strategic evidence matured."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview); err != nil {
		t.Fatalf("inspect technical_review due-ness: %v", err)
	} else if due {
		t.Fatal("technical_review should start not due")
	}

	if err := forcePulseModuleDueForLateRepairDebt(ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview, "plan_drift_review left 2 actionable issue(s) Gate did not anticipate"); err != nil {
		t.Fatalf("forcePulseModuleDueForLateRepairDebt: %v", err)
	}

	if due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview); err != nil {
		t.Fatalf("inspect technical_review due-ness after force: %v", err)
	} else if !due {
		t.Fatal("technical_review should be due after forcePulseModuleDueForLateRepairDebt")
	}
	// strategic_review's own due decision must be untouched by forcing a
	// different module due — this is a narrow single-row amendment, not a
	// second full worklist write.
	if due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModuleStrategicReview); err != nil {
		t.Fatalf("inspect strategic_review due-ness: %v", err)
	} else if !due {
		t.Fatal("strategic_review's original due decision must survive forcing technical_review due")
	}
	if err := validatePulseDueModuleResultsFor(ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview); err == nil {
		t.Fatal("technical_review is due with no terminal result recorded for this run yet; validatePulseDueModuleResultsFor should require one")
	}
}

func TestPulseWorklistModulesDueChecksOnlyGivenModules(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/modules-due-scope"
	pulseRunID := "pulse-modules-due-scope"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "Operational evidence."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}

	if due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModulePlanDriftReview); err != nil {
		t.Fatalf("inspect plan_drift_review due-ness: %v", err)
	} else if due {
		t.Fatal("plan_drift_review was not marked due, but pulseWorklistModulesDue reported it due")
	}
	if due, err := pulseWorklistModulesDue(ctx, workspacePath, pulseRunID, pulseModuleTechnicalReview, pulseModuleStrategicReview); err != nil {
		t.Fatalf("inspect technical/strategic due-ness: %v", err)
	} else if !due {
		t.Fatal("technical_review was marked due, but pulseWorklistModulesDue(technical, strategic) reported neither due")
	}
}

// validatePulseDueModuleResultsFor must only flag the modules it was asked
// about — checking the full module set right after plan_drift_review's own
// step (which runs before technical_review/strategic_review even get their
// turn) would wrongly treat "hasn't run yet" as "receipt missing."
func TestValidatePulseDueModuleResultsForScopesToGivenModules(t *testing.T) {
	ctx := context.Background()
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/scoped-results"
	pulseRunID := "pulse-scoped-results"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModulePlanDriftReview: {Module: pulseModulePlanDriftReview, Due: true, Reason: "A step's drift_review is null."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}

	// The selected plan-drift module has no terminal result yet, so scoped
	// validation must name it.
	err := validatePulseDueModuleResultsFor(ctx, workspacePath, pulseRunID, pulseModulePlanDriftReview)
	if err == nil || !strings.Contains(err.Error(), pulseModulePlanDriftReview) {
		t.Fatalf("expected an error naming plan_drift_review, got: %v", err)
	}

	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModulePlanDriftReview, pulseRunID, "done", "Ground truth established for all pending steps.", nil); err != nil {
		t.Fatalf("mark plan_drift_review: %v", err)
	}
	if err := validatePulseDueModuleResultsFor(ctx, workspacePath, pulseRunID, pulseModulePlanDriftReview); err != nil {
		t.Fatalf("plan_drift_review has a terminal result, scoped validation should pass: %v", err)
	}
}

func TestValidatePulseDueModuleResultsRequiresTerminalModuleResults(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/demo"
	pulseRunID := "pulse-run-results"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "Operational evidence."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if err := validatePulseDueModuleResults(ctx, workspacePath, pulseRunID); err == nil || !strings.Contains(err.Error(), "technical_review") {
		t.Fatalf("missing-result validation error = %v", err)
	}
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModuleTechnicalReview, pulseRunID, "done", "Clean review.", []string{"pulse_review_log:run:technical_review"}); err != nil {
		t.Fatalf("mark bug review: %v", err)
	}
	if err := validatePulseDueModuleResults(ctx, workspacePath, pulseRunID); err != nil {
		t.Fatalf("terminal validation: %v", err)
	}
	worklist, _, err := getPulseWorklistForRun(ctx, workspacePath, pulseRunID)
	if err != nil {
		t.Fatalf("read worklist: %v", err)
	}
	if got := worklist[pulseModuleTechnicalReview].LastResult; got != "done" {
		t.Fatalf("existing completed module was overwritten: %q", got)
	}
}

func TestValidatePulseDueModuleResultsIgnoresLegacyRunningReviewReceipt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspacePath := "Workflow/demo"
	pulseRunID := "pulse-running-review"
	if _, err := recordPulseWorklist(ctx, workspacePath, pulseRunID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
		pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "Operational evidence."},
	})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if _, err := markPulseModuleResultFromAgent(ctx, workspacePath, pulseModuleTechnicalReview, pulseRunID, "done", "Review turn ended.", []string{"pulse_review_log:run:technical_review"}); err != nil {
		t.Fatalf("mark review: %v", err)
	}
	seedPulseReviewLogRow(ctx, t, workspacePath, pulseModuleTechnicalReview, pulseRunID, "running", "")
	if err := validatePulseDueModuleResults(ctx, workspacePath, pulseRunID); err != nil {
		t.Fatalf("terminal module result should not depend on legacy review receipt: %v", err)
	}
}

func TestPulseFinalBackupRunsOnlyInParentTurn(t *testing.T) {
	finalizer := pulseStepQueryByLabel(t, pulseLifecycleFinalSteps("pulse-run-1"), "finalize")
	if !strings.Contains(finalizer, `read_skill(skills=[{"name":"builder-reference","path":"references/pulse-finalizer.md"}])`) {
		t.Fatalf("finalizer does not load its focused contract: %s", finalizer)
	}
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), "agent_go/cmd/server/guidance/templates/system/pulse-finalizer.md"))
	if err != nil {
		t.Fatalf("read finalizer contract: %v", err)
	}
	for _, required := range []string{"directly in this parent", "never through a reviewer/sub-agent", "zero-config local-git default", "backup/database/db.sqlite", "Publish is independent of Backup"} {
		if !strings.Contains(string(raw), required) {
			t.Fatalf("finalizer contract missing parent-only backup guard %q", required)
		}
	}
	if strings.Contains(string(raw), "publish unbacked changes after backup failure") {
		t.Fatalf("finalizer still couples publish eligibility to backup success")
	}
	for _, forbidden := range []string{"REVIEWER/FIXER COST", "Reviewers + Fixer"} {
		if strings.Contains(finalizer, forbidden) || strings.Contains(string(raw), forbidden) {
			t.Fatalf("Pulse notification still requires reviewer/fixer cost field %q", forbidden)
		}
	}
}

func TestWorkflowBackupRequiresDatabaseSnapshotOnlyForDueTriggerAndCoverage(t *testing.T) {
	config := &WorkflowBackupConfig{
		Enabled:  true,
		Triggers: WorkflowBackupTriggers{AfterScheduledRun: true},
		Destinations: []WorkflowBackupDestination{{
			ID: "config-repo", Covers: []string{"workflow", "db-sqlite"},
		}},
	}
	if !workflowBackupRequiresDatabaseSnapshot(config, "cron") {
		t.Fatal("scheduled db-sqlite backup did not require a managed snapshot")
	}
	if workflowBackupRequiresDatabaseSnapshot(config, "manual") {
		t.Fatal("manual trigger incorrectly used after_scheduled_run")
	}
	config.Triggers.AfterManualRun = true
	if !workflowBackupRequiresDatabaseSnapshot(config, "manual") {
		t.Fatal("manual db-sqlite backup did not require a managed snapshot")
	}
	config.Destinations[0].Covers = []string{"workflow"}
	if workflowBackupRequiresDatabaseSnapshot(config, "cron") {
		t.Fatal("backup without db-sqlite coverage requested a database snapshot")
	}
	config.Enabled = false
	config.Destinations[0].Covers = []string{"db-sqlite"}
	if workflowBackupRequiresDatabaseSnapshot(config, "cron") {
		t.Fatal("disabled backup requested a database snapshot")
	}
}

func TestPostRunMonitorFinalStepsIncludesOwnerNotificationInstructions(t *testing.T) {
	runInstructions := "Include delivered outputs and the primary metric."
	pulseInstructions := "Put decisions and material fixes first."
	finalizer := pulseStepQueryByLabel(t, pulseLifecycleFinalSteps("pulse-run-1", workflowNotificationContentInstructions{
		runSummary: runInstructions, pulseSummary: pulseInstructions,
	}), "finalize")
	for _, required := range []string{"WORKFLOW RUN SUMMARY INSTRUCTIONS", runInstructions, "PULSE REVIEW SUMMARY INSTRUCTIONS", pulseInstructions, "recipients, channels, secrets, permissions"} {
		if !strings.Contains(finalizer, required) {
			t.Fatalf("finalizer missing notification instruction guard %q: %s", required, finalizer)
		}
	}
	withoutInstructions := pulseStepQueryByLabel(t, pulseLifecycleFinalSteps("pulse-run-1"), "finalize")
	if strings.Contains(withoutInstructions, "SUMMARY INSTRUCTIONS") {
		t.Fatalf("finalizer included owner instructions when none were configured: %s", withoutInstructions)
	}
}

func TestPostRunMonitorFinalStepsIncludesSplitNotificationRouting(t *testing.T) {
	finalizer := pulseStepQueryByLabel(t, pulseLifecycleFinalSteps("pulse-run-1", workflowNotificationContentInstructions{
		runSummaryChannels:   []string{"slack"},
		pulseSummaryChannels: []string{"gmail"},
	}), "finalize")
	for _, required := range []string{"SPLIT NOTIFICATION ROUTING", `notification_kind="run_summary"`, `notification_kind="pulse_summary"`, "configured channels: slack", "configured channels: gmail"} {
		if !strings.Contains(finalizer, required) {
			t.Fatalf("finalizer missing split-route instruction %q: %s", required, finalizer)
		}
	}
}

func TestEveryRunFinalizerRequiresRichGmailWhenConfigured(t *testing.T) {
	finalizers := map[string]string{
		"full Pulse": pulseStepQueryByLabel(t, pulseLifecycleFinalSteps("pulse-run-1"), "finalize"),
		"no run":     pulseLifecycleNoRunSteps("pulse-run-1", "preflight failed")[0].query,
	}
	for name, prompt := range finalizers {
		t.Run(name, func(t *testing.T) {
			for _, required := range []string{"RICH GMAIL OUTPUT", "email_subject", "email_html", "message_for_user", "inline-styled"} {
				if !strings.Contains(prompt, required) {
					t.Fatalf("finalizer prompt missing rich Gmail requirement %q:\n%s", required, prompt)
				}
			}
		})
	}
}

// A Pulse lifecycle turn is tagged and elevated to Workshop while retaining the
// workflow's Builder llm_config. pulse_llm is applied by the workshop to the
// background review agents the tagged turn launches.
func TestPulseLifecycleTurnKeepsBuilderLLMAndTagsTheTurn(t *testing.T) {
	builder := &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5", Options: map[string]interface{}{"reasoning_effort": "high"}}
	sctx := &ScheduleContext{
		WorkflowID:    "wf_test",
		WorkspacePath: "Workflow/test",
		Schedule:      WorkflowSchedule{ID: "daily", Name: "Daily", WorkshopMode: "run"},
		Capabilities: WorkflowCapabilities{
			LLMConfig: &workflowtypes.PresetLLMConfig{
				SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
				Mode:          workflowtypes.LLMConfigModeExplicit,
				BuilderLLM:    builder,
				TieredConfig:  &workflowtypes.TieredLLMConfig{Tier1: builder, Tier2: builder, Tier3: builder},
				PulseLLM: &workflowtypes.AgentLLMConfig{
					Provider: "codex-cli",
					ModelID:  "gpt-5.5",
					Options:  map[string]interface{}{"reasoning_effort": "high"},
				},
			},
		},
	}

	svc := &SchedulerService{}
	reqMap := svc.buildWorkshopRequest(context.Background(), sctx)
	markPulseLifecycleTurn(reqMap)

	if got := reqMap["pulse_lifecycle_turn"]; got != true {
		t.Fatalf("pulse_lifecycle_turn = %#v, want true", got)
	}
	execOpts, ok := reqMap["execution_options"].(map[string]interface{})
	if !ok || execOpts["workshop_mode"] != "workshop" {
		t.Fatalf("Pulse execution_options = %#v, want workshop mode", reqMap["execution_options"])
	}
	if _, set := reqMap["llm_config_source"]; set {
		t.Fatalf("a Pulse turn must not override the manifest LLM, got llm_config_source=%#v", reqMap["llm_config_source"])
	}
	llmConfig, ok := reqMap["llm_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("llm_config missing or wrong type: %#v", reqMap["llm_config"])
	}
	primary, ok := llmConfig["primary"].(map[string]interface{})
	if !ok {
		t.Fatalf("llm_config.primary missing or wrong type: %#v", llmConfig["primary"])
	}
	if primary["provider"] != "claude-code" || primary["model_id"] != "claude-sonnet-5-5" {
		t.Fatalf("Pulse turn llm_config = %#v, want the workflow Builder model, never pulse_llm", primary)
	}
	markPulseLifecycleTurn(nil) // nil-safe
}

func TestBuildWorkshopRequestDisablesLiveInputDeliveryForSchedulerTurns(t *testing.T) {
	svc := &SchedulerService{}
	sctx := &ScheduleContext{
		WorkflowID:    "wf_test",
		WorkspacePath: "Workflow/test",
		Schedule:      WorkflowSchedule{ID: "daily", Name: "Daily"},
		Capabilities:  WorkflowCapabilities{},
	}

	reqMap := svc.buildWorkshopRequest(context.Background(), sctx)
	if got := reqMap["disable_live_input_delivery"]; got != true {
		t.Fatalf("disable_live_input_delivery = %#v, want true", got)
	}
	if got := reqMap["triggered_by"]; got != "cron" {
		t.Fatalf("triggered_by = %#v, want cron", got)
	}
	if got := reqMap["session_title"]; got != "Daily" {
		t.Fatalf("session_title = %#v, want Daily", got)
	}
	execOpts, ok := reqMap["execution_options"].(map[string]interface{})
	if !ok {
		t.Fatalf("execution_options missing or wrong type: %#v", reqMap["execution_options"])
	}
	if got := execOpts["workshop_mode"]; got != "workshop" {
		t.Fatalf("workshop_mode = %#v, want workshop for writable scheduled work", got)
	}
}

func TestScheduledTurnsUseLeastPrivilegedWorkshopMode(t *testing.T) {
	cases := []struct {
		name string
		turn scheduledWorkshopTurn
		want string
	}{
		{name: "normal schedule message", turn: scheduledWorkshopTurn{label: "schedule-message-1"}, want: "workshop"},
		{name: "contract upgrade", turn: scheduledWorkshopTurn{upgradeTarget: "1.2.3"}, want: "workshop"},
		{name: "decision drain", turn: scheduledWorkshopTurn{decisionDrain: true}, want: "workshop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.turn.workshopMode(); got != tc.want {
				t.Fatalf("workshopMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestWithWorkshopModeDoesNotLeakElevationAcrossTurns(t *testing.T) {
	base := map[string]interface{}{
		"execution_options": map[string]interface{}{
			"workshop_mode":      "workshop",
			"execution_strategy": "start_from_beginning_no_human",
		},
	}

	maintenance := requestWithWorkshopMode(base, "workshop")
	normal := requestWithWorkshopMode(base, "workshop")

	if got := maintenance["execution_options"].(map[string]interface{})["workshop_mode"]; got != "workshop" {
		t.Fatalf("maintenance mode = %#v, want workshop", got)
	}
	if got := normal["execution_options"].(map[string]interface{})["workshop_mode"]; got != "workshop" {
		t.Fatalf("normal mode = %#v, want workshop", got)
	}
	if got := base["execution_options"].(map[string]interface{})["workshop_mode"]; got != "workshop" {
		t.Fatalf("base mode was mutated to %#v", got)
	}

	markPulseLifecycleTurn(normal)
	if got := normal["execution_options"].(map[string]interface{})["workshop_mode"]; got != "workshop" {
		t.Fatalf("Pulse mode = %#v, want workshop", got)
	}
	if got := base["execution_options"].(map[string]interface{})["workshop_mode"]; got != "workshop" {
		t.Fatalf("Pulse elevation leaked into base request: %#v", got)
	}
}

func TestRefreshSessionTmuxSnapshotsForIdleCheckCapturesFreshPane(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-scheduler-refresh"
	tmuxSession := "tmux-scheduler-refresh"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "old pane", 1))

	oldRunOutput := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()
	var calls [][]string
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		return "fresh pane\n❯", nil
	}

	svc := &SchedulerService{api: &StreamingAPI{terminalStore: store}}
	if err := svc.refreshSessionTmuxSnapshotsForIdleCheck(context.Background(), sessionID); err != nil {
		t.Fatalf("refreshSessionTmuxSnapshotsForIdleCheck returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("tmux capture calls = %d, want 1", len(calls))
	}
	snapshots := store.ListMetadata(sessionID)
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snapshots))
	}
	if got := snapshots[0].Content; !strings.Contains(got, "fresh pane") {
		t.Fatalf("snapshot content = %q, want fresh capture", got)
	}
	if got := snapshots[0].ContentSource; got != "tmux_capture" {
		t.Fatalf("content source = %q, want tmux_capture", got)
	}
}

func TestRefreshSessionTmuxSnapshotsForIdleCheckMarksMissingPaneStale(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-scheduler-missing"
	tmuxSession := "tmux-scheduler-missing"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "old pane", 1))

	oldRunOutput := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		return "", errors.New("can't find session: tmux-scheduler-missing")
	}

	svc := &SchedulerService{api: &StreamingAPI{terminalStore: store}}
	if err := svc.refreshSessionTmuxSnapshotsForIdleCheck(context.Background(), sessionID); err != nil {
		t.Fatalf("refreshSessionTmuxSnapshotsForIdleCheck returned error: %v", err)
	}
	snapshots := store.ListMetadata(sessionID)
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snapshots))
	}
	if snapshots[0].Active {
		t.Fatalf("missing tmux snapshot should be inactive")
	}
	if got := snapshots[0].State; got != "stale" {
		t.Fatalf("state = %q, want stale", got)
	}
	if got := snapshots[0].TmuxSession; got != "" {
		t.Fatalf("tmux session = %q, want cleared", got)
	}
}

func TestRunJobDoesNotJoinAnotherActiveRun(t *testing.T) {
	startedAt := time.Now().Add(-time.Minute)
	sctx := &ScheduleContext{
		WorkspacePath: "/tmp/workflow",
		Schedule:      WorkflowSchedule{ID: "schedule-1", Name: "Active schedule"},
	}
	runtimeKey := scheduleRuntimeKey(sctx)
	svc := &SchedulerService{runtimeStates: map[string]*ScheduleRuntimeState{
		runtimeKey: {
			ActiveRunID: "active-run",
			LastStatus:  "running",
			LastRunAt:   &startedAt,
		},
	}, runCancels: map[string]context.CancelFunc{
		"stale-run": func() {},
	}}

	_, err := svc.runJob(context.Background(), sctx, "stale-run")
	if !errors.Is(err, errWorkshopSequenceInterrupted) {
		t.Fatalf("runJob error = %v, want errWorkshopSequenceInterrupted", err)
	}
	if got := svc.runtimeStates[runtimeKey].ActiveRunID; got != "active-run" {
		t.Fatalf("active run ownership changed to %q", got)
	}
}

func TestPostRunMonitorStepsUseOneTurnInactivityBoundary(t *testing.T) {
	oldNormal := schedulerWorkshopMaxInactivity
	schedulerWorkshopMaxInactivity = 10 * time.Minute
	defer func() {
		schedulerWorkshopMaxInactivity = oldNormal
	}()

	for _, label := range []string{"gate", "review", "fix", "review-fix-continuation", "dashboard", "finalize"} {
		if got := (pulseLifecycleStep{label: label}).idleMaxInactivity(); got != 10*time.Minute {
			t.Fatalf("%s max inactivity = %s, want 10m", label, got)
		}
	}
	// A module review runs inside its own turn, so it may stay quiet longer.
	for _, label := range []string{"plan-drift-review", "technical-review", "architecture-review", "strategic-review"} {
		if got := (pulseLifecycleStep{label: label}).idleMaxInactivity(); got != schedulerPulseReviewMaxInactivity {
			t.Fatalf("%s max inactivity = %s, want %s", label, got, schedulerPulseReviewMaxInactivity)
		}
	}
}

// Goal Work's permission levels reach its step as text and as the levels the
// tools enforce (PLAT-697). Workflow Review no longer holds them.
func TestGoalWorkAutonomyTextMatchesTheEnforcedLevels(t *testing.T) {
	perms, text := goalWorkAutonomy(context.Background(), "Workflow/does-not-exist")
	if !perms.Run || perms.Outward || perms.Change {
		t.Fatalf("defaults are run auto, outward and change ask, got %+v", perms)
	}
	for _, want := range []string{"The tools hold them", "level 1 of 5, Measure", "✓ 1 Measure", "checked by the Workflow Review first"} {
		if !strings.Contains(text, want) {
			t.Errorf("autonomy text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Plan Drift is due") {
		t.Errorf("Workflow Review must not hold Goal Work's levels:\n%s", text)
	}
}

func TestReviewFixContinuationIsParentReceiptReconciliationOnly(t *testing.T) {
	step := pulseLifecycleReviewFixContinuationStep("pulse-test", errors.New("workflow_review receipt missing"))
	for _, want := range []string{
		"PULSE REVIEW + FIX RECEIPT CHECK",
		"Do not reconstruct findings or fixes",
		"Validate the receipts already persisted by each sequence",
		"Resolve only a genuine cross-module ownership conflict",
		"do not infer or invent its findings, restart it automatically, or mutate workflow artifacts",
	} {
		if !strings.Contains(step.query, want) {
			t.Fatalf("continuation prompt missing %q:\n%s", want, step.query)
		}
	}
}

// TestPulseReviewBacklogSummaryExcludesNonTerminalIterationZero pins PLAT-115:
// iteration-0 is the live/reused slot, never a stable identity across time,
// so Gate must never be handed it while it might still be mid-run. Rotated
// folders (iteration-1, iteration-2, ...) are always included regardless of
// status — a failed run is real evidence too, the same principle
// ProducedRunEvidence already applies elsewhere in this file.
func TestPulseReviewBacklogSummaryExcludesNonTerminalIterationZero(t *testing.T) {
	completedAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	startedAt := completedAt.Add(-10 * time.Minute)

	t.Run("iteration-0 still running is excluded", func(t *testing.T) {
		folders := []RunFolderInfo{
			{Name: "iteration-0", Metadata: &RunMetadata{Status: "running", StartedAt: startedAt}},
			{Name: "iteration-5", Metadata: &RunMetadata{Status: "completed", StartedAt: startedAt, CompletedAt: &completedAt}},
		}
		summary := pulseReviewBacklogSummary(folders)
		if strings.Contains(summary, "iteration-0") {
			t.Fatalf("summary must exclude a still-running iteration-0:\n%s", summary)
		}
		if !strings.Contains(summary, "iteration-5") {
			t.Fatalf("summary must include the terminal rotated folder:\n%s", summary)
		}
	})

	t.Run("iteration-0 terminal is included", func(t *testing.T) {
		folders := []RunFolderInfo{
			{Name: "iteration-0", Metadata: &RunMetadata{Status: "completed", StartedAt: startedAt, CompletedAt: &completedAt}},
		}
		summary := pulseReviewBacklogSummary(folders)
		if !strings.Contains(summary, "iteration-0") {
			t.Fatalf("summary must include a terminal iteration-0:\n%s", summary)
		}
	})

	t.Run("a failed rotated run is still real evidence", func(t *testing.T) {
		folders := []RunFolderInfo{
			{Name: "iteration-3", Metadata: &RunMetadata{Status: "failed", StartedAt: startedAt, CompletedAt: &completedAt}},
		}
		summary := pulseReviewBacklogSummary(folders)
		if !strings.Contains(summary, "iteration-3") || !strings.Contains(summary, "status=failed") {
			t.Fatalf("a failed rotated folder must still be listed:\n%s", summary)
		}
	})

	t.Run("no folders at all", func(t *testing.T) {
		if summary := pulseReviewBacklogSummary(nil); !strings.Contains(summary, "no run folders") {
			t.Fatalf("empty backlog summary = %q, want an explicit \"no run folders\" statement", summary)
		}
	})
}

// TestBacklogGateStepDefersWhatsNewReasoningToGate pins the design decision:
// Go hands Gate the raw listing and explicitly tells it to compare against
// get_pulse_state's last_checked_at itself — Go must never pre-filter "what's
// new" or silently assume every listed folder is unreviewed.
func TestBacklogGateStepDefersWhatsNewReasoningToGate(t *testing.T) {
	step := pulseLifecycleBacklogGateStep("pulse-test", "- iteration-5 (status=completed, started_at=..., completed_at=...)")
	for _, want := range []string{
		"periodic Pulse review pass",
		"last_checked_at",
		"do not assume every listed folder is new",
		"run_retention_count",
		"iteration-5",
	} {
		if !strings.Contains(step.query, want) {
			t.Fatalf("backlog Gate prompt missing %q:\n%s", want, step.query)
		}
	}
}

func TestPostRunMonitorFinalStepClassification(t *testing.T) {
	tests := []struct {
		label     string
		finalStep bool
	}{
		{label: "gate"},
		{label: "review-fix"},
		{label: "dashboard", finalStep: false},
		{label: "finalize", finalStep: true},
	}
	for _, test := range tests {
		if got := isPulseLifecycleFinalStep(test.label); got != test.finalStep {
			t.Fatalf("final-step classification for %q = %v, want %v", test.label, got, test.finalStep)
		}
	}
}

func TestRunningScheduleInSetLockedFindsOtherRunningSchedule(t *testing.T) {
	states := map[string]*ScheduleRuntimeState{
		"daily":     {LastStatus: "running", LastSessionID: "session-daily"},
		"optimizer": {LastStatus: "success", LastSessionID: "session-optimizer"},
	}

	id, sessionID := runningScheduleInSetLocked(states, []string{"current", "daily", "optimizer"}, "current")
	if id != "daily" {
		t.Fatalf("running schedule id = %q, want daily", id)
	}
	if sessionID != "session-daily" {
		t.Fatalf("running schedule session = %q, want session-daily", sessionID)
	}
}

func TestRunningScheduleInSetLockedIgnoresCurrentSchedule(t *testing.T) {
	states := map[string]*ScheduleRuntimeState{
		"current": {LastStatus: "running", LastSessionID: "session-current"},
	}

	id, sessionID := runningScheduleInSetLocked(states, []string{"current"}, "current")
	if id != "" || sessionID != "" {
		t.Fatalf("running schedule = (%q, %q), want empty", id, sessionID)
	}
}

func TestMaybeResumeLatestWorkflowThreadUsesPreviousScheduledSessionOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspacePath := "Workflow/latency-report"
	scheduleID := "schedule-1"
	writeWorkflowChatRuntime(t, root, workspacePath, "normal-user-chat", "claude-code", "run", true)
	writeWorkflowChatRuntime(t, root, workspacePath, "previous-schedule-chat", "claude-code", "workshop", true)
	writeScheduleRunsForTest(t, root, workspacePath, []ScheduleRunEntry{
		{
			ID:         "current-run",
			ScheduleID: scheduleID,
			SessionID:  "current-schedule-chat",
			Status:     "running",
			StartedAt:  time.Now().UTC(),
		},
		{
			ID:         "previous-run",
			ScheduleID: scheduleID,
			SessionID:  "previous-schedule-chat",
			Status:     "success",
			StartedAt:  time.Now().Add(-time.Hour).UTC(),
		},
	})

	reqMap := map[string]interface{}{}
	resumed := (&SchedulerService{}).maybeResumeLatestWorkflowThread(context.Background(), resumeTestScheduleContext(workspacePath, scheduleID), reqMap, "current-schedule-chat")
	if resumed != "previous-schedule-chat" {
		t.Fatalf("resumed session = %q, want previous scheduled session", resumed)
	}
	if got := reqMap["restored_conversation_session_id"]; got != "previous-schedule-chat" {
		t.Fatalf("restored_conversation_session_id = %#v, want previous scheduled session", got)
	}
}

func TestMaybeResumeLatestWorkflowThreadIgnoresNormalUserChat(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspacePath := "Workflow/latency-report"
	scheduleID := "schedule-1"
	writeWorkflowChatRuntime(t, root, workspacePath, "normal-user-chat", "claude-code", "run", true)
	writeScheduleRunsForTest(t, root, workspacePath, []ScheduleRunEntry{
		{
			ID:         "current-run",
			ScheduleID: scheduleID,
			SessionID:  "current-schedule-chat",
			Status:     "running",
			StartedAt:  time.Now().UTC(),
		},
	})

	reqMap := map[string]interface{}{}
	resumed := (&SchedulerService{}).maybeResumeLatestWorkflowThread(context.Background(), resumeTestScheduleContext(workspacePath, scheduleID), reqMap, "current-schedule-chat")
	if resumed != "" {
		t.Fatalf("resumed session = %q, want empty because normal user chats are not schedule runs", resumed)
	}
	if _, ok := reqMap["restored_conversation_session_id"]; ok {
		t.Fatalf("restored_conversation_session_id was set for a normal user chat: %#v", reqMap)
	}
}

func TestMaybeResumeLatestWorkflowThreadDoesNotCrossModeBoundary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspacePath := "Workflow/latency-report"
	scheduleID := "schedule-1"
	writeWorkflowChatRuntime(t, root, workspacePath, "previous-run-chat", "claude-code", "run", true)
	writeScheduleRunsForTest(t, root, workspacePath, []ScheduleRunEntry{{
		ID:         "previous-run",
		ScheduleID: scheduleID,
		SessionID:  "previous-run-chat",
		Status:     "success",
		StartedAt:  time.Now().Add(-time.Hour).UTC(),
	}})

	reqMap := map[string]interface{}{}
	resumed := (&SchedulerService{}).maybeResumeLatestWorkflowThread(context.Background(), resumeTestScheduleContext(workspacePath, scheduleID), reqMap, "current-schedule-chat")
	if resumed != "" {
		t.Fatalf("resumed session = %q, want fresh Workshop session for legacy Run history", resumed)
	}
	if _, ok := reqMap["restored_conversation_session_id"]; ok {
		t.Fatalf("restored_conversation_session_id crossed Workshop/Run boundary: %#v", reqMap)
	}
}

func resumeTestScheduleContext(workspacePath, scheduleID string) *ScheduleContext {
	resumePrevious := true
	builder := &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "claude-opus-4-6"}
	return &ScheduleContext{
		WorkspacePath: workspacePath,
		Schedule: WorkflowSchedule{
			ID:             scheduleID,
			ResumePrevious: &resumePrevious,
		},
		Capabilities: WorkflowCapabilities{
			LLMConfig: &workflowtypes.PresetLLMConfig{
				SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
				Mode:          workflowtypes.LLMConfigModeExplicit,
				BuilderLLM:    builder,
				PulseLLM:      builder,
				TieredConfig:  &workflowtypes.TieredLLMConfig{Tier1: builder, Tier2: builder, Tier3: builder},
			},
		},
	}
}

func writeScheduleRunsForTest(t *testing.T, root, workspacePath string, runs []ScheduleRunEntry) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(workspacePath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schedule-runs.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeWorkflowChatRuntime(t *testing.T, root, workspacePath, sessionID, provider, workshopMode string, resumeSupported bool) {
	t.Helper()
	convDir := filepath.Join(root, filepath.FromSlash(workspacePath), "builder", "conversation", "2026-05-20")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(map[string]interface{}{
		"session_id":    sessionID,
		"agent_mode":    "workflow_phase",
		"workshop_mode": workshopMode,
		"runtime": map[string]interface{}{
			"kind":                 "coding_agent",
			"provider":             provider,
			"model_id":             "claude-opus-4-6",
			"external_session_id":  "external-" + sessionID,
			"resume_supported":     resumeSupported,
			"resume_flag":          "--resume",
			"workspace_path":       workspacePath,
			"workshop_mode":        workshopMode,
			"agent_session_handle": map[string]interface{}{},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "session-"+sessionID+"-conversation.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileWorkshopRunOutcomeDetectsNewFailedRun reproduces BUG-20260729-10
// (social-media, 2026-07-29): the scheduler recorded "success" for a scheduled
// run whose triggered workflow run fully failed at its first posting step,
// because the orchestrating workshop chat session itself completed its turns
// without an infrastructure-level error. reconcileWorkshopRunOutcome closes
// that gap by checking the real run_metadata.json of any NEW run folder.
func TestReconcileWorkshopRunOutcomeDetectsNewFailedRun(t *testing.T) {
	before := map[string]bool{"iteration-231": true}
	after := []RunFolderInfo{
		{Name: "iteration-231", Metadata: &RunMetadata{Status: "completed"}},
		{Name: "iteration-232", Metadata: &RunMetadata{Status: "failed"}},
	}
	failedFolder, found := reconcileWorkshopRunOutcome(before, after, time.Now())
	if !found {
		t.Fatal("expected the new failed run to be found")
	}
	if failedFolder != "iteration-232" {
		t.Fatalf("failedFolder = %q, want iteration-232", failedFolder)
	}
}

func TestWorkshopRunRotationDoesNotAttributeHistoricalFailure(t *testing.T) {
	since := time.Date(2026, 9, 4, 3, 0, 26, 0, time.UTC)
	before := map[string]bool{"iteration-0/default": true}
	old := RunFolderInfo{Name: "iteration-315/default", Metadata: &RunMetadata{
		Status: "failed", StartedAt: since.Add(-11 * time.Hour), CreatedAt: since.Add(time.Minute),
	}}
	for _, baseline := range []map[string]bool{before, {}} {
		if folder, found := reconcileWorkshopRunOutcome(baseline, []RunFolderInfo{old}, since); found {
			t.Fatalf("rotated historical failure blamed on current run: %s", folder)
		}
		if workshopRunProducedEvidence(baseline, []RunFolderInfo{old}, since) {
			t.Fatal("rotation alone must not count as execution evidence")
		}
	}
	current := RunFolderInfo{Name: "iteration-0/default", Metadata: &RunMetadata{
		Status: "completed", StartedAt: since.Add(time.Minute),
	}}
	after := []RunFolderInfo{old, current}
	if _, found := reconcileWorkshopRunOutcome(before, after, since); found {
		t.Fatal("old failed run must not override the new successful run")
	}
	if !workshopRunProducedEvidence(before, after, since) {
		t.Fatal("current reused-folder execution should count as evidence")
	}
	current.Metadata.Status = "failed"
	if folder, found := reconcileWorkshopRunOutcome(before, after, since); !found || folder != current.Name {
		t.Fatalf("current failure not detected: %q, %v", folder, found)
	}
	old.Metadata.StartedAt = time.Time{}
	old.Metadata.CreatedAt = since.Add(-time.Hour)
	if workshopRunProducedEvidence(before, []RunFolderInfo{old}, since) {
		t.Fatal("old CreatedAt must also prevent name-only attribution")
	}
}

// TestReconcileWorkshopRunOutcomeDetectsFailureInAReusedFolderName reproduces
// the customer-login schedule_run_status:aggregation harness finding: a
// workflow whose runs always land in the same folder name (iteration-0/<group>)
// never looks "new" by name after its first cycle, so a name-only check
// would silently skip every subsequent cycle's own metadata regardless of
// what it recorded — the exact reused-folder-name gap
// workshopRunProducedEvidence (the sibling function, called moments earlier
// in the same code path) already guards against via its own since-based
// fallback, which this function previously lacked.
func TestReconcileWorkshopRunOutcomeDetectsFailureInAReusedFolderName(t *testing.T) {
	invocationStart := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	before := map[string]bool{"iteration-0/customer-staging": true} // same folder, prior cycle
	after := []RunFolderInfo{
		{
			Name: "iteration-0/customer-staging",
			Metadata: &RunMetadata{
				Status:    "failed",
				StartedAt: invocationStart.Add(1 * time.Minute), // (re)started during THIS invocation
			},
		},
	}
	failedFolder, found := reconcileWorkshopRunOutcome(before, after, invocationStart)
	if !found {
		t.Fatal("expected this invocation's own failure in the reused folder to be found")
	}
	if failedFolder != "iteration-0/customer-staging" {
		t.Fatalf("failedFolder = %q, want iteration-0/customer-staging", failedFolder)
	}
}

// TestReconcileWorkshopRunOutcomeIgnoresPreexistingFailure proves an older
// run's failure — already present before this invocation started — must never
// flip THIS invocation's own result. Only a run created during this call
// counts.
func TestReconcileWorkshopRunOutcomeIgnoresPreexistingFailure(t *testing.T) {
	before := map[string]bool{"iteration-231": true}
	after := []RunFolderInfo{
		{Name: "iteration-231", Metadata: &RunMetadata{Status: "failed"}},
	}
	if _, found := reconcileWorkshopRunOutcome(before, after, time.Now()); found {
		t.Fatal("a pre-existing run's failure must not be attributed to this invocation")
	}
}

// TestReconcileWorkshopRunOutcomeMisattributesWhenBaselineIsLost pins the hazard
// that makes the caller's guard necessary, rather than asserting a behavior we
// want. The function decides "new" purely by absence from the before-set, so an
// EMPTY before-set means every folder looks new and any old failure is reported
// as this invocation's.
//
// That is correct for a primitive that is handed a real baseline, and it is why
// the baseline must never be a guess. loadRunFoldersInternal used to return an
// empty slice with a nil error whenever the workspace listing failed, so the
// scheduler could not tell "no run folders" from "could not look" and passed
// exactly this empty set. On 2026-08-10 a hetznerssh production security audit
// completed cleanly in iteration-0 and was recorded as an error, blamed on
// iteration-25, which had failed the previous day — moments after workspace
// folder calls were returning errors following a restart.
//
// The fix is at the call site: propagate the listing error and skip
// reconciliation when either snapshot is unavailable. If this test ever starts
// failing because the function itself learned to ignore an empty baseline, that
// is fine — delete it and keep the caller guard.
func TestReconcileWorkshopRunOutcomeMisattributesWhenBaselineIsLost(t *testing.T) {
	lostBaseline := map[string]bool{} // what a failed listing produced
	after := []RunFolderInfo{
		{Name: "iteration-0", Metadata: &RunMetadata{Status: "completed"}},
		{Name: "iteration-25", Metadata: &RunMetadata{Status: "failed"}}, // yesterday's
	}
	failedFolder, found := reconcileWorkshopRunOutcome(lostBaseline, after, time.Now())
	if !found || failedFolder != "iteration-25" {
		t.Fatalf("expected the documented misattribution (iteration-25), got found=%v folder=%q; "+
			"if the primitive now tolerates an empty baseline this test is obsolete", found, failedFolder)
	}
}

// TestReconcileWorkshopRunOutcomeIgnoresAmbiguousStates proves that anything
// other than an explicit "failed" — no metadata, "running", "completed" — is
// left alone rather than treated as a failure. A transient listing hiccup or
// an in-flight run must fail open toward "cannot verify", never toward a
// false failure that would mislabel a genuinely successful run.
func TestReconcileWorkshopRunOutcomeIgnoresAmbiguousStates(t *testing.T) {
	before := map[string]bool{}
	after := []RunFolderInfo{
		{Name: "iteration-1", Metadata: nil},
		{Name: "iteration-2", Metadata: &RunMetadata{Status: "running"}},
		{Name: "iteration-3", Metadata: &RunMetadata{Status: "completed"}},
	}
	if _, found := reconcileWorkshopRunOutcome(before, after, time.Now()); found {
		t.Fatal("no folder here is explicitly \"failed\"; none should be flagged")
	}
}

// TestReconcileWorkshopRunOutcomeHandlesGroupNestedFolders proves a group-run
// folder name like "iteration-232/default" is diffed correctly, matching the
// naming convention extractIterationFoldersFromTypedChildren produces for
// group-nested runs.
func TestReconcileWorkshopRunOutcomeHandlesGroupNestedFolders(t *testing.T) {
	before := map[string]bool{"iteration-232/production": true}
	after := []RunFolderInfo{
		{Name: "iteration-232/production", Metadata: &RunMetadata{Status: "failed"}},
		{Name: "iteration-232/staging", Metadata: &RunMetadata{Status: "failed"}},
	}
	failedFolder, found := reconcileWorkshopRunOutcome(before, after, time.Now())
	if !found {
		t.Fatal("expected the new group folder's failure to be found")
	}
	if failedFolder != "iteration-232/staging" {
		t.Fatalf("failedFolder = %q, want iteration-232/staging (not the pre-existing production group)", failedFolder)
	}
}

func TestRunFolderNameSetSkipsEmptyNames(t *testing.T) {
	set := runFolderNameSet([]RunFolderInfo{
		{Name: "iteration-1"},
		{Name: ""},
		{Name: "iteration-2"},
	})
	if len(set) != 2 || !set["iteration-1"] || !set["iteration-2"] {
		t.Fatalf("unexpected set: %#v", set)
	}
}

func TestPostRunMonitorModuleStepsReserveHTMLForDashboard(t *testing.T) {
	steps := pulseLifecycleSteps()
	checked := 0
	for _, step := range steps {
		if step.label != "technical-review" && step.label != "architecture-review" && step.label != "strategic-review" {
			continue
		}
		checked++
		for _, want := range []string{"render a dashboard", "durable Gate worklist"} {
			if !strings.Contains(step.query, want) {
				t.Fatalf("module step %q missing single-renderer guard %q:\n%s", step.label, want, step.query)
			}
		}
		if strings.Contains(step.query, `read_skill(skills=[{"name":"builder-reference","path":"references/review-improve-log.md"}])`) {
			t.Fatalf("module step %q still loads the presentation contract:\n%s", step.label, step.query)
		}
	}
	if checked != 3 {
		t.Fatalf("checked %d independent review steps, want 3", checked)
	}
}

// pulseStepQueryByLabel selects a stage by label so these assertions survive
// new stages being added to the final sequence.
func pulseStepQueryByLabel(t *testing.T, steps []pulseLifecycleStep, label string) string {
	t.Helper()
	for _, st := range steps {
		if st.label == label {
			return st.query
		}
	}
	t.Fatalf("no %q stage in final steps", label)
	return ""
}

// The dashboard stage used to say "mark command=dashboard done" without naming
// a tool, unlike the finalize stage right below it, which spells out
// record_pulse_result(command=...) explicitly. On 2026-08-04 latency-report's
// dashboard stage rendered builder/improve.html correctly, then reached for
// mutate_workflow_db to write pulse_final_command_state directly — a
// reasonable guess for "mark this row," and the wrong one: that table is
// framework-owned bookkeeping, mutate_workflow_db correctly denied a session
// without db_access=read-write, and the stage ended without ever calling the
// sanctioned command API. reconcilePulseDashboardCommand then marked the
// whole stage failed even though the render was correct.
//
// This test is deliberately independent of
// TestPostRunMonitorUsesDynamicModulesAndSingleFinalizer, whose detailed
// per-stage assertions are currently unreachable dead code after a premature
// return partway through that function (a concurrent, unrelated in-progress
// edit — see the go vet "unreachable code" flag at this file's line 787).
func TestPulseHasNoDashboardStage(t *testing.T) {
	for _, step := range pulseLifecycleSteps() {
		if step.label == "dashboard" || strings.Contains(step.query, "PULSE DASHBOARD") {
			t.Fatalf("retired HTML dashboard leaked into Pulse stage: %+v", step)
		}
	}
}

func TestWorkshopRunProducedEvidence(t *testing.T) {
	start := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	before := map[string]bool{"iteration-0": true}

	tests := []struct {
		name  string
		after []RunFolderInfo
		want  bool
	}{
		{
			name: "pre-existing folder untouched",
			after: []RunFolderInfo{{Name: "iteration-0", Metadata: &RunMetadata{
				StartedAt: start.Add(-2 * time.Hour), CreatedAt: start.Add(-3 * time.Hour), Status: "completed",
			}}},
			want: false,
		},
		{
			name: "new folder created",
			after: []RunFolderInfo{
				{Name: "iteration-0", Metadata: &RunMetadata{StartedAt: start.Add(-2 * time.Hour)}},
				{Name: "iteration-1", Metadata: &RunMetadata{StartedAt: start.Add(time.Minute)}},
			},
			want: true,
		},
		{
			name: "existing folder restarted",
			after: []RunFolderInfo{{Name: "iteration-0", Metadata: &RunMetadata{
				StartedAt: start.Add(30 * time.Second), CreatedAt: start.Add(-3 * time.Hour),
			}}},
			want: true,
		},
		{
			name:  "failed new run still counts",
			after: []RunFolderInfo{{Name: "iteration-2", Metadata: &RunMetadata{StartedAt: start.Add(time.Minute), Status: "failed"}}},
			want:  true,
		},
		{
			name:  "known folder without metadata proves nothing",
			after: []RunFolderInfo{{Name: "iteration-0"}},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workshopRunProducedEvidence(before, tt.after, start); got != tt.want {
				t.Fatalf("workshopRunProducedEvidence() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestScheduledWorkflowExecutionProducedEvidenceUsesLinkedReceipts(t *testing.T) {
	since := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	registry := NewBackgroundAgentRegistry()
	registry.Register("schedule-session", &BackgroundAgent{
		ID:        "workflow-full-engage-1",
		SessionID: "schedule-session",
		Kind:      "full_run",
		Status:    BGAgentCompleted,
		CreatedAt: since.Add(30 * time.Second),
		Metadata:  map[string]string{"execution_type": "full-workflow", "group_name": "engage"},
	})
	service := &SchedulerService{api: &StreamingAPI{bgAgentRegistry: registry}}
	if !service.scheduledWorkflowExecutionProducedEvidence("schedule-session", since) {
		t.Fatal("run_full_workflow's session-linked full-run receipt must be Pulse evidence even when a capped folder listing omits its reused run folder")
	}

	registry.Register("schedule-session", &BackgroundAgent{
		ID:        "exec-daily-report-1",
		SessionID: "schedule-session",
		Kind:      "workflow_step",
		Status:    BGAgentCompleted,
		CreatedAt: since.Add(time.Minute),
		Metadata:  map[string]string{"execution_type": "workflow-step"},
	})
	if !service.scheduledWorkflowExecutionProducedEvidence("schedule-session", since) {
		t.Fatal("a workflow step launched by this schedule must be Pulse evidence")
	}

	registry.Register("schedule-session", &BackgroundAgent{
		ID:        "bg-unrelated-1",
		SessionID: "schedule-session",
		Kind:      "workshop_background",
		Status:    BGAgentCompleted,
		CreatedAt: since.Add(2 * time.Minute),
	})
	if !service.scheduledWorkflowExecutionProducedEvidence("schedule-session", since) {
		t.Fatal("unrelated background work must not erase linked workflow-step evidence")
	}

	otherRegistry := NewBackgroundAgentRegistry()
	otherRegistry.Register("schedule-session", &BackgroundAgent{
		ID:        "bg-only-1",
		SessionID: "schedule-session",
		Kind:      "workshop_background",
		Status:    BGAgentCompleted,
		CreatedAt: since.Add(time.Minute),
	})
	otherService := &SchedulerService{api: &StreamingAPI{bgAgentRegistry: otherRegistry}}
	if otherService.scheduledWorkflowExecutionProducedEvidence("schedule-session", since) {
		t.Fatal("generic background work must not manufacture workflow evidence")
	}

	staleRegistry := NewBackgroundAgentRegistry()
	staleRegistry.Register("schedule-session", &BackgroundAgent{
		ID:        "workflow-full-old",
		SessionID: "schedule-session",
		Kind:      "full_run",
		Status:    BGAgentCompleted,
		CreatedAt: since.Add(-time.Minute),
	})
	staleService := &SchedulerService{api: &StreamingAPI{bgAgentRegistry: staleRegistry}}
	if staleService.scheduledWorkflowExecutionProducedEvidence("schedule-session", since) {
		t.Fatal("a full-run receipt from before this invocation must not manufacture current evidence")
	}
}

func TestNoRunFinalizerSkipsEvidenceStagesAndReportsReason(t *testing.T) {
	reason := `workflow upgrade preflight upgrade-1.0.18 did not stamp required version "1.0.18"; normal schedule message was not started`
	steps := pulseLifecycleNoRunSteps("pulse-run-1", reason, workflowNotificationContentInstructions{
		runSummaryChannels:   []string{"electron", "slack"},
		runSummaryRecipients: []string{"owner@example.com"},
	})
	if len(steps) != 1 || steps[0].label != "finalize" {
		t.Fatalf("no-run finalizer = %#v, want one finalize step", steps)
	}
	for _, want := range []string{
		"WORKFLOW DID NOT RUN",
		"Gate, reviewers, Fixer, dashboard, and publish were intentionally skipped",
		`notification_kind="run_summary"`,
		"dashboard has no record_pulse_result command and needs no receipt",
		"mark publish skipped",
		"source-hash-gated backup",
		"electron, slack",
		"owner@example.com",
		// The label survives so the operator's run summary can name which
		// migration stalled.
		"workflow upgrade preflight upgrade-1.0.18 did not complete",
	} {
		if !strings.Contains(steps[0].query, want) {
			t.Fatalf("no-run finalizer missing %q:\n%s", want, steps[0].query)
		}
	}
	// The finalizer shares the scheduler's session. Restating the stamp
	// instruction here is what invited the customer-login 2026-08-12 out-of-turn
	// stamp, so the target version and the verb must not reach this turn.
	for _, forbidden := range []string{"did not stamp", `"1.0.18"`} {
		if strings.Contains(steps[0].query, forbidden) {
			t.Fatalf("no-run finalizer restates the upgrade instruction (%q):\n%s", forbidden, steps[0].query)
		}
	}
	if strings.Contains(steps[0].query, "write builder/improve.html once") {
		t.Fatal("no-run finalizer must not contain the normal dashboard mutation contract")
	}
	// PLAT-083 regression: the numbered record_pulse_result instructions must
	// never ask the agent to record "dashboard" — it was never a valid final
	// command (only backup/publish/notify are), so the tool rejected every
	// attempt and the agent had to recover from an error the prompt itself
	// caused.
	if strings.Contains(steps[0].query, "mark dashboard skipped") {
		t.Fatal("no-run finalizer must not instruct the agent to record_pulse_result an invalid \"dashboard\" command")
	}

	fallback := pulseLifecycleNoRunSteps("pulse-run-2", "   ")[0].query
	if !strings.Contains(fallback, "no workflow run was started or resumed") {
		t.Fatal("empty scheduler error must still produce a truthful explanation")
	}
}

func TestDueCronOccurrencesRetainsEveryRecentOccurrenceAndLatestCatchup(t *testing.T) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 7, 10, 17, 0, 0, time.UTC)
	got := dueCronOccurrences(schedule, after, now)
	want := []time.Time{
		time.Date(2026, 8, 7, 10, 5, 0, 0, time.UTC),
		time.Date(2026, 8, 7, 10, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 7, 10, 15, 0, 0, time.UTC),
	}
	if len(got) != len(want) {
		t.Fatalf("occurrences=%v, want %v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatalf("occurrence[%d]=%s, want %s", i, got[i], want[i])
		}
	}
}

func TestLatestCronOccurrenceReconcilesInterruptedAttempt(t *testing.T) {
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "schedule-state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scheduledFor := time.Date(2026, 8, 7, 10, 30, 0, 0, time.UTC)
	if err := store.RecordFireDecision(context.Background(), schedulerstate.FireDecision{
		DecisionID: "attempt", ScopeType: "workflow", ScopeID: "Workflow/demo", ScheduleID: "daily",
		TriggerSource: "cron", Decision: "attempted", ScheduledFor: scheduledFor, FiredAt: scheduledFor,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewSchedulerService(nil)
	service.stateStore = store
	sctx := &ScheduleContext{WorkspacePath: "Workflow/demo", Schedule: WorkflowSchedule{ID: "daily"}}
	got, ok := service.latestCronOccurrence(sctx)
	if !ok || !got.Equal(scheduledFor) {
		t.Fatalf("latest occurrence=(%s,%t), want (%s,true)", got, ok, scheduledFor)
	}
	latest, err := store.LatestFireDecision(context.Background(), "workflow", "Workflow/demo", "daily", "cron")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Decision != "launch_outcome_unknown" || !strings.Contains(latest.Reason, "may or may not have started") {
		t.Fatalf("interrupted attempt was not reconciled: %+v", latest)
	}
}

func TestDueCronOccurrencesDoesNotDropMinuteOccurrencesWithinRecoveryWindow(t *testing.T) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	after := now.Add(-12 * time.Hour)
	got := dueCronOccurrences(schedule, after, now)
	if len(got) != 12*60 {
		t.Fatalf("retained occurrences=%d, want %d", len(got), 12*60)
	}
	if !got[0].Equal(after.Add(time.Minute)) || !got[len(got)-1].Equal(now) {
		t.Fatalf("unexpected occurrence bounds: first=%s last=%s", got[0], got[len(got)-1])
	}
}

// TestWorkshopRunStartedDuringInvocationIgnoresBaseline pins the reason this
// helper exists separately from workshopRunProducedEvidence: it must answer
// correctly with no before-set at all.
//
// social-media 2026-08-10: the run completed at 12:15:18Z, the workshop idle
// wait expired 34 minutes later, and because that path returns before the
// evidence check, ProducedRunEvidence kept its initialized false. Pulse was told
// "the workflow did not run" and skipped publish — against a run that had landed
// 19 actions, 18 of them independently verified. The durable record said
// "completed" the whole time; nothing consulted it.
func TestWorkshopRunStartedDuringInvocationIgnoresBaseline(t *testing.T) {
	since := time.Date(2026, 8, 10, 9, 30, 53, 0, time.UTC)
	folders := []RunFolderInfo{
		// Pre-existing folder reused by this invocation — the shape that made the
		// baseline-dependent helper unreliable here. iteration-0 always exists.
		{Name: "iteration-0", Metadata: &RunMetadata{
			Status:    "completed",
			StartedAt: since.Add(4 * time.Minute),
		}},
	}
	if !workshopRunStartedDuringInvocation(folders, since) {
		t.Fatal("a run whose own metadata says it started during this invocation is evidence, baseline or not")
	}
}

// TestWorkshopRunStartedDuringInvocationRejectsOlderAndUnstamped proves the
// helper cannot manufacture evidence: a run from a previous invocation, and a
// record with no usable timestamp, both count as nothing. The caller treats
// false as "no evidence recorded", which is the pre-existing behavior.
func TestWorkshopRunStartedDuringInvocationRejectsOlderAndUnstamped(t *testing.T) {
	since := time.Date(2026, 8, 10, 9, 30, 53, 0, time.UTC)
	folders := []RunFolderInfo{
		{Name: "iteration-254", Metadata: &RunMetadata{Status: "completed", StartedAt: since.Add(-2 * time.Hour)}},
		{Name: "iteration-253", Metadata: &RunMetadata{Status: "failed"}}, // no timestamps
		{Name: "iteration-252"}, // no metadata
	}
	if workshopRunStartedDuringInvocation(folders, since) {
		t.Fatal("older or unstamped runs must not be reported as evidence for this invocation")
	}
}

func TestApplyLLMAndSecretsToReqMapUsesTheWorkflowModelForEverySchedule(t *testing.T) {
	// Every schedule now runs on the workflow's Builder model. The optimizer
	// workshop mode used to swap in a separate maintenance model and stamp
	// llm_config_source; that mode was retired on 2026-08-17, so a schedule
	// still carrying "optimizer" in workflow.json must behave like any other.
	builder := &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "claude-opus-4-6"}
	high := &workflowtypes.AgentLLMConfig{Provider: "vertex", ModelID: "gemini-2.5-pro"}
	baseConfig := &workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeExplicit,
		BuilderLLM:    builder,
		PulseLLM:      builder,
		TieredConfig:  &workflowtypes.TieredLLMConfig{Tier1: high, Tier2: builder, Tier3: builder},
	}

	for _, workshopMode := range []string{"run", "workshop", "optimizer", " OPTIMIZER ", ""} {
		t.Run("workshop_mode="+workshopMode, func(t *testing.T) {
			reqMap := map[string]interface{}{}
			(&SchedulerService{}).applyLLMAndSecretsToReqMap(context.Background(), reqMap, &ScheduleContext{
				Schedule:     WorkflowSchedule{WorkshopMode: workshopMode},
				Capabilities: WorkflowCapabilities{LLMConfig: baseConfig},
			})

			llmConfig, ok := reqMap["llm_config"].(map[string]interface{})
			if !ok {
				t.Fatalf("llm_config missing or wrong type: %#v", reqMap["llm_config"])
			}
			primary, ok := llmConfig["primary"].(map[string]interface{})
			if !ok {
				t.Fatalf("llm_config.primary missing or wrong type: %#v", llmConfig["primary"])
			}
			if got := primary["provider"]; got != "claude-code" {
				t.Fatalf("provider = %#v, want the Builder model regardless of workshop mode", got)
			}
			if got := primary["model_id"]; got != "claude-opus-4-6" {
				t.Fatalf("model_id = %#v, want the Builder model regardless of workshop mode", got)
			}
			if _, ok := reqMap["llm_config_source"]; ok {
				t.Fatalf("llm_config_source was set: %#v — no schedule overrides the model any more", reqMap["llm_config_source"])
			}
		})
	}
}

func TestPulseRepairDrainOnlyAppliesToDueTechnicalReview(t *testing.T) {
	for _, module := range []string{pulseModuleStrategicReview, pulseModuleTechnicalReview} {
		t.Run(module, func(t *testing.T) {
			ctx := context.Background()
			t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
			workspacePath, runID := "Workflow/completion-contract", "pulse-contract"
			if _, err := recordPulseWorklist(ctx, workspacePath, runID, completePulseWorklistDecisions(map[string]PulseWorklistDecision{
				module: {Due: true, Reason: "Review has new evidence."},
			})); err != nil {
				t.Fatal(err)
			}
			if err := validatePulseTechnicalRepairDrain(ctx, workspacePath, runID); err != nil {
				t.Fatalf("empty backlog should pass: %v", err)
			}
			if _, err := todo_creation_human.RecordPulseReviewFinding(ctx, workspacePath, runID, "review-1", todo_creation_human.PulseReviewFindingInput{
				Concern: "Workflow validation rejects valid data", Module: pulseModuleTechnicalReview,
				PulseFindingDetails: todo_creation_human.PulseFindingDetails{
					IssueKind: todo_creation_human.IssueKindWorkflow, Classification: "correctness_bug", Severity: "high",
					Summary: "Validation rejects valid data.", Impact: "Valid runs cannot complete.",
					Evidence: []string{"runs/iteration-1/result.json"},
				},
			}); err != nil {
				t.Fatal(err)
			}
			if count, err := todo_creation_human.CountPulseActionableWorkflowIssues(ctx, workspacePath); err != nil || count != 1 {
				t.Fatalf("expected one actionable issue, count=%d err=%v", count, err)
			}
			err := validatePulseTechnicalRepairDrain(ctx, workspacePath, runID)
			if module == pulseModuleTechnicalReview {
				if err == nil || !strings.Contains(err.Error(), "1 actionable") {
					t.Fatalf("technical repair debt must fail completion: %v", err)
				}
			} else if err != nil {
				t.Fatalf("strategic completion must not depend on technical debt: %v", err)
			}
		})
	}
}

func TestPulseRepairDrainRequiresDurableWorklist(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	if err := validatePulseTechnicalRepairDrain(context.Background(), "Workflow/missing", "missing"); err == nil {
		t.Fatal("missing worklist must not silently bypass the completion contract")
	}
}
