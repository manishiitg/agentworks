package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// PLAT-821: exercise persisted pause/resume and the real Builder, external and
// Pulse reads while historical skips remain. No model call or workflow runs.
func TestCurrentSchedulerStateSurvivesHistoricalPausedRuns(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	stub, _ := newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	const ws = "Workflow/scheduler-state"
	manifest := &WorkflowManifest{ID: "scheduler-state", Label: "Schedule state", Schedules: []WorkflowSchedule{
		{ID: "daily", Enabled: true, CronExpression: "0 9 * * *", Timezone: "UTC"},
		{ID: "off", Enabled: false, CronExpression: "0 9 * * *", Timezone: "UTC"},
		{ID: "hook", Enabled: true, ScheduleType: "webhook"},
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	stub.files[ws+"/workflow.json"] = string(raw)
	if err := AppendScheduleRun(ctx, ws, &ScheduleRunEntry{ID: "old-skip", ScheduleID: "daily", Status: "skipped_paused", StartedAt: time.Now().UTC().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	put := func(body string) {
		req := httptest.NewRequest(http.MethodPut, "/api/scheduler/config", strings.NewReader(body))
		req.Header.Set("User-Agent", "private-client-marker")
		req = req.WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "private-user-marker", Username: "owner"}))
		rec := httptest.NewRecorder()
		updateSchedulerConfigHandler(nil)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("save config: %d %s", rec.Code, rec.Body.String())
		}
	}
	check := func(wantState, wantReason string, global, product bool) {
		t.Helper()
		out, err := (&StreamingAPI{}).buildSchedulerCallbacks().ListSchedules(ctx, ws)
		if err != nil {
			t.Fatal(err)
		}
		begin := strings.Index(out, "```json\n")
		if begin < 0 {
			t.Fatalf("Builder missing current state: %s", out)
		}
		payload := strings.SplitN(out[begin+len("```json\n"):], "\n```", 2)[0]
		var builder WorkflowSchedulerState
		if err := json.Unmarshal([]byte(payload), &builder); err != nil {
			t.Fatal(err)
		}
		external := httptest.NewRecorder()
		(&StreamingAPI{}).externalListSchedules(external, httptest.NewRequest(http.MethodGet, "/", nil), DiscoveredWorkflow{WorkspacePath: ws, Manifest: manifest})
		var apiView struct {
			State WorkflowSchedulerState `json:"scheduler_state"`
		}
		if err := json.Unmarshal(external.Body.Bytes(), &apiView); err != nil {
			t.Fatal(err)
		}
		pulse, err := computeGoalStatus(ctx, ws, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		for name, state := range map[string]WorkflowSchedulerState{"Builder": builder, "external": apiView.State, "Pulse": pulse.SchedulerState} {
			if state.ConfigState != "known" || state.GloballyPaused == nil || *state.GloballyPaused != global || state.ProductPaused == nil || *state.ProductPaused != product || state.ProductEffectivelyPaused == nil || *state.ProductEffectivelyPaused != (global || product) || state.Product != "agentworks" || state.ObservedAt.IsZero() || state.UpdatedAt == nil {
				t.Fatalf("%s current flags: %+v", name, state)
			}
			if len(state.Schedules) != 3 || state.Schedules[0].State != wantState || state.Schedules[1].State != "disabled" || state.Schedules[2].State != "eligible" || state.Schedules[2].SchedulerPauseApplies || state.WorkflowAllSchedulesDisabled == nil || *state.WorkflowAllSchedulesDisabled {
				t.Fatalf("%s individual flags/blockers: %+v", name, state)
			}
			daily := state.Schedules[0]
			if wantReason != "" && !strings.Contains(strings.Join(daily.BlockingReasons, ","), wantReason) {
				t.Fatalf("%s missing blocker %s: %+v", name, wantReason, daily)
			}
			if wantState == "eligible" && (daily.NextScheduledAt == nil || !daily.NextScheduledAt.After(state.ObservedAt)) || wantState != "eligible" && daily.NextScheduledAt != nil {
				t.Fatalf("%s misleading next occurrence: %+v", name, daily)
			}
			for _, event := range state.RecentPauseEvents {
				if event.Product != "" && event.Product != "agentworks" {
					t.Fatalf("%s leaked unrelated product event: %+v", name, event)
				}
			}
			encoded, _ := json.Marshal(state)
			for _, marker := range []string{"private-client-marker", "private-user-marker", "user_agent", "username", "paused_by"} {
				if strings.Contains(string(encoded), marker) {
					t.Fatalf("%s leaked identity/client metadata: %s", name, marker)
				}
			}
		}
		if pulse.Facts.SchedulesPaused {
			t.Fatal("global/product pause changed all-individual-disabled goal fact")
		}
		query := pulseLifecycleGoalCheckStep(ctx, ws, "check-state", workflowNotificationContentInstructions{}).query
		if !strings.Contains(query, "Read list_schedules") || strings.Contains(query, `"scheduler_state":`) || !strings.Contains(query, "Past skipped_paused runs") {
			t.Fatal("initial Pulse goal turn must request current state through tools without preloading it")
		}
	}
	put(`{"globally_paused":true,"paused_products":["work"]}`)
	check("paused", "global_pause", true, false)
	put(`{"globally_paused":false}`)
	check("eligible", "", false, false)
	state := readWorkflowSchedulerState(ctx, manifest, time.Now().UTC())
	if state.HistoryState != "known" || len(state.RecentPauseEvents) != 2 || state.RecentPauseEvents[0].Action != "resumed" || state.RecentPauseEvents[0].Scope != "global" {
		t.Fatalf("resume evidence missing: %+v", state)
	}
	put(`{"globally_paused":false,"paused_products":["agentworks"]}`)
	check("paused", "product_pause", false, true)
	put(`{"globally_paused":false,"paused_products":[]}`)
	check("eligible", "", false, false)
	// Neither state reads nor resume erase past skipped runs or enable off.
	runs, total, err := ListScheduleRuns(ctx, ws, "daily", 10, 0)
	if err != nil || total != 1 || runs[0].Status != "skipped_paused" {
		t.Fatalf("history changed: total=%d runs=%+v err=%v", total, runs, err)
	}
	stub.mu.Lock()
	afterManifest := stub.files[ws+"/workflow.json"]
	stub.mu.Unlock()
	if afterManifest != string(raw) {
		t.Fatal("read-only state view changed the workflow")
	}
}

func TestCurrentSchedulerStateUnknownAndEmptyWorkflow(t *testing.T) {
	stub, host := newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	empty := &WorkflowManifest{ID: "empty"}
	state := readWorkflowSchedulerState(ctx, empty, time.Now().UTC())
	if state.ConfigState != "known" || state.GloballyPaused == nil || *state.GloballyPaused || state.WorkflowAllSchedulesDisabled == nil || *state.WorkflowAllSchedulesDisabled || len(state.Schedules) != 0 {
		t.Fatalf("missing config uses scheduler's unpaused default, no schedules is not all disabled: %+v", state)
	}
	stub.files["Workflow/empty/workflow.json"] = `{"id":"empty"}`
	out, err := (&StreamingAPI{}).buildSchedulerCallbacks().ListSchedules(ctx, "Workflow/empty")
	if err != nil || !strings.Contains(out, `"globally_paused": false`) || !strings.Contains(out, "No schedules found") {
		t.Fatalf("empty workflow omitted current global state: %s err=%v", out, err)
	}
	manifest := &WorkflowManifest{Schedules: []WorkflowSchedule{{ID: "daily", Enabled: true}, {ID: "off", Enabled: false}}}
	assertUnknown := func() {
		t.Helper()
		state := readWorkflowSchedulerState(ctx, manifest, time.Now().UTC())
		if state.ConfigState != "unknown" || state.GloballyPaused != nil || state.ProductPaused != nil || state.ProductEffectivelyPaused != nil || state.Schedules[0].State != "unknown" || state.Schedules[0].NextScheduledAt != nil || state.Schedules[1].State != "disabled" {
			t.Fatalf("unreadable config claimed paused/running: %+v", state)
		}
	}
	// A Relay reads its own product flag, and calendar timing uses the same
	// clock/timezone semantics as the scheduler rather than old runtime dates.
	stub.files[schedulerConfigFilePath] = `{"globally_paused":false,"paused_products":["agentworks"]}`
	at := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	relay := &WorkflowManifest{Kind: "relay", Schedules: []WorkflowSchedule{{ID: "calendar", Enabled: true, ScheduleType: "calendar", Timezone: "Asia/Kolkata", CalendarItems: []CalendarScheduleItem{{Date: "2026-10-10", Time: "14:00"}}}}}
	relayState := readWorkflowSchedulerState(ctx, relay, at)
	if relayState.Product != "relays" || relayState.ProductPaused == nil || *relayState.ProductPaused || relayState.Schedules[0].State != "eligible" || relayState.Schedules[0].NextScheduledAt == nil || !relayState.Schedules[0].NextScheduledAt.Equal(at.Add(30*time.Minute)) {
		t.Fatalf("wrong product scope or calendar time: %+v", relayState)
	}
	noOccurrence := readWorkflowSchedulerState(ctx, &WorkflowManifest{Kind: "relay", Schedules: []WorkflowSchedule{{ID: "impossible-date", Enabled: true, CronExpression: "0 9 31 2 *", Timezone: "UTC"}}}, at)
	if noOccurrence.Schedules[0].NextScheduledAt != nil {
		t.Fatalf("an impossible calendar date must not report a zero/past next occurrence: %+v", noOccurrence)
	}
	stub.files[schedulerConfigFilePath] = "{"
	assertUnknown()
	host.Close()
	assertUnknown()
}
