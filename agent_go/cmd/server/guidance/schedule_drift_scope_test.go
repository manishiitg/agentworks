package guidance

import (
	"strings"
	"testing"
)

// The schedule's message queue is what the scheduler actually sends, so it is a
// contract with the plan. Nothing in Pulse read it: a workflow here had eight
// messages that only restated plan steps, two that carried work existing in no
// step at all (including one that emails four people), and a plan with no
// ordering -- so the queue silently owned the sequence. None of it was
// reviewable, because the checklist never named the schedule as evidence.
func TestArtifactDriftAuditsTheSchedule(t *testing.T) {
	rendered, err := renderFromRegistry("run-plan-drift", tmplData{}, allKinds)
	if err != nil {
		t.Fatalf("render run-plan-drift: %v", err)
	}

	for _, want := range []string{
		"workflow.json", // the schedule lives here
		"messages",      // the queue itself
		"cron",          // cadence
		"execute_step",  // what a message should be doing
		"validate_plan", // why inline work is a problem
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("schedule evidence missing %q from the artifact-drift checklist", want)
		}
	}

	// The three drift shapes that were invisible before.
	for _, want := range []string{
		"drives no plan step",
		"no schedule message reaches",
		"exists only in the message queue",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("checklist does not flag drift case %q", want)
		}
	}
}

func TestScheduleReferenceAdvertisesCoordinationChoices(t *testing.T) {
	rendered, err := renderFromRegistry("schedules", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatalf("render schedules reference: %v", err)
	}
	for _, want := range []string{
		"after_schedule_ids",
		"waits for **all**",
		"after_terminal_status",
		"dependency_deadline",
		"cannot contain the schedule itself or form a cycle",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("schedule reference is missing %q", want)
		}
	}
	if description := referenceKinds["schedules"].Description; !strings.Contains(description, "multi-schedule fan-in") {
		t.Errorf("schedule reference discovery description does not advertise coordination: %q", description)
	}
	if strings.Contains(rendered, "max_run_duration_minutes") {
		t.Error("schedule reference still advertises the retired generic run-duration limit")
	}
}
