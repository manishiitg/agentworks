package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// PLAT-821: exercise persisted pause/resume and the real Builder, external and
// Pulse reads while historical skips remain. No model call or workflow runs.

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
