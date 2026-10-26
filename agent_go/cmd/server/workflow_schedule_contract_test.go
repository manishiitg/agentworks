package server

import (
	"strings"
	"testing"
)

func TestScheduledWorkshopMessagesPassesDeclaredRouteSelections(t *testing.T) {
	got := scheduledWorkshopMessages(&ScheduleContext{Schedule: WorkflowSchedule{
		GroupNames:      []string{"draft"},
		RouteSelections: map[string]string{"step-router": "draft-only"},
	}})
	if len(got) != 1 {
		t.Fatalf("messages = %d, want 1", len(got))
	}
	for _, want := range []string{"[\"draft\"]", `"step-router":"draft-only"`, "run_full_workflow"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("generated trigger missing %q: %s", want, got[0])
		}
	}
}

func TestScheduledWorkshopMessagesRunsRouteBeforeCustomFollowUp(t *testing.T) {
	got := scheduledWorkshopMessages(&ScheduleContext{Schedule: WorkflowSchedule{
		GroupNames:      []string{"default"},
		RouteSelections: map[string]string{"step-router": "execution"},
		Messages:        []string{"After the selected work completes, report the result."},
	}})
	if len(got) != 2 {
		t.Fatalf("messages = %d, want route then follow-up: %v", len(got), got)
	}
	for _, want := range []string{"run_full_workflow", `"step-router":"execution"`} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("route trigger missing %q: %s", want, got[0])
		}
	}
	if got[1] != "After the selected work completes, report the result." {
		t.Fatalf("follow-up = %q", got[1])
	}
}

func TestManifestRefusesExtraFullRunMessageForSavedRoute(t *testing.T) {
	for _, tc := range []struct {
		name     string
		routes   map[string]string
		messages []string
		wantErr  bool
	}{
		{"duplicate tool call", map[string]string{"router": "standard"}, []string{`Call run_full_workflow(group_name="default", route_selections={"router":"standard"})`}, true},
		{"tool name without arguments", map[string]string{"router": "standard"}, []string{"Use run_full_workflow for the daily work."}, true},
		{"genuine follow-up", map[string]string{"router": "standard"}, []string{"After the selected work completes, report the result."}, false},
		{"route only", map[string]string{"router": "standard"}, nil, false},
		{"direct message only", nil, []string{`Call run_full_workflow(group_name="default")`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := NewWorkflowManifest("Scheduled work")
			manifest.Schedules = []WorkflowSchedule{{
				ID: "daily", Name: "Daily work", CronExpression: "0 9 * * *", Timezone: "UTC",
				GroupNames: []string{"default"}, RouteSelections: tc.routes, Messages: tc.messages,
			}}
			err := ValidateManifest(manifest)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), `Schedule "Daily work" already runs the workflow; remove its extra 'run the full workflow' message`) {
					t.Fatalf("validation = %v, want duplicate-run refusal", err)
				}
			} else if err != nil {
				t.Fatalf("valid schedule rejected: %v", err)
			}
		})
	}
}

// PLAT-871-15: a person's own scheduled messages need no extra rationale.
func TestGoalScheduleAcceptsOwnMessagesWithoutExtraReason(t *testing.T) {
	if err := validateScheduleMessages([]string{"Collect evidence.", "SELECT * FROM queue", "Report it."}, ""); err != nil {
		t.Fatalf("scheduled chat request refused: %v", err)
	}
}
