package step_based_workflow

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

func quotaErr(retryAt time.Time, window string) error {
	return &llmerrors.Error{
		Kind:     llmerrors.KindQuotaExhausted,
		Provider: "claudecode",
		Model:    "sonnet",
		Window:   window,
		RetryAt:  retryAt,
		Err:      errors.New("claude code usage limit reached"),
	}
}

// TestCapacityWaitOnlyClassifiesQuotaExhaustion is the guard on the whole
// mechanism. Suspending on an ordinary step failure would stall a workflow
// indefinitely on a defect that will never fix itself, and — because an
// outstanding wait suppresses the schedule — take the cron down with it.
func TestCapacityWaitOnlyClassifiesQuotaExhaustion(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	for _, err := range []error{
		nil,
		errors.New("step blew up"),
		&llmerrors.Error{Kind: llmerrors.KindRateLimit, Err: errors.New("slow down")},
	} {
		if got := newWorkflowCapacityWait(err, "Workflow/project-a", "iteration-0", 4, 7, "s4", "step-4", "collect", now); got != nil {
			t.Errorf("err %v classified as a capacity wait: %+v", err, got)
		}
	}

	got := newWorkflowCapacityWait(quotaErr(now.Add(2*time.Hour), "five_hour"),
		"Workflow/project-a", "iteration-0", 4, 7, "s4", "step-4", "collect", now)
	if got == nil {
		t.Fatal("quota exhaustion was not classified as a capacity wait")
	}
	if got.StepNumber != 4 || got.TotalSteps != 7 {
		t.Errorf("step position = %d of %d, want 4 of 7", got.StepNumber, got.TotalSteps)
	}
	if got.Window != "five_hour" || got.Provider != "claudecode" {
		t.Errorf("provider detail lost: %+v", got)
	}
	if !got.RetryAt.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("RetryAt = %v, want %v", got.RetryAt, now.Add(2*time.Hour))
	}
}

// TestCapacityWaitWithNoStatedResetIsNeverAutomaticallyDue: a resume armed from
// a guessed instant wakes straight back into the same wall and burns the run's
// remaining steps a second time. An unknown reset waits for a person.
func TestCapacityWaitWithNoStatedResetIsNeverAutomaticallyDue(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	wait := newWorkflowCapacityWait(quotaErr(time.Time{}, ""), "Workflow/project-a", "iteration-0", 4, 7, "s4", "step-4", "collect", now)
	if wait == nil {
		t.Fatal("expected a capacity wait")
	}
	if !wait.RetryAt.IsZero() {
		t.Fatalf("RetryAt = %v, want the zero time rather than a fabricated instant", wait.RetryAt)
	}
	if wait.ResumeDue(now.Add(100 * time.Hour)) {
		t.Error("a wait with no stated reset became due on its own")
	}
	if !strings.Contains(wait.Describe(), "unknown") {
		t.Errorf("description hides that the reset time is unknown: %q", wait.Describe())
	}
}

// TestCapacityWaitBecomesDueAtItsStatedReset pins the normal path, including
// the boundary instant itself.
func TestCapacityWaitBecomesDueAtItsStatedReset(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	wait := newWorkflowCapacityWait(quotaErr(reset, "five_hour"), "Workflow/project-a", "iteration-0", 4, 7, "s4", "step-4", "collect", now)

	if wait.ResumeDue(reset.Add(-time.Second)) {
		t.Error("resumed a second before the window reopened")
	}
	if !wait.ResumeDue(reset) {
		t.Error("not due at the stated reset instant")
	}
	if !wait.ResumeDue(reset.Add(time.Hour)) {
		t.Error("not due after the stated reset")
	}
}

// TestCapacityWaitDescribesWhatAReaderNeeds. The description is what lands in
// the run-history row, replacing a raw provider error that said nothing about
// how much of the run had completed or when it would continue.
func TestCapacityWaitDescribesWhatAReaderNeeds(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	wait := newWorkflowCapacityWait(quotaErr(now.Add(2*time.Hour), "five_hour"), "Workflow/project-a", "iteration-0", 4, 7, "s4", "step-4", "collect", now)

	desc := wait.Describe()
	for _, want := range []string{"five_hour", "step 4 of 7", "2026-08-18T14:00:00Z"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description %q is missing %q", desc, want)
		}
	}
}

// TestCapacityWaitRoundTripsThroughItsFile: the record is read back by a
// different package after a restart, so the on-disk shape has to survive.
func TestCapacityWaitRoundTripsThroughItsFile(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	original := newWorkflowCapacityWait(quotaErr(now.Add(2*time.Hour), "five_hour"), "Workflow/project-a", "iteration-0", 4, 7, "s4", "step-4", "collect", now)

	encoded, err := jsonMarshalIndentForTest(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	restored, ok := ParseWorkflowCapacityWait(encoded)
	if !ok {
		t.Fatal("record did not parse back")
	}
	if restored.StepNumber != 4 || !restored.RetryAt.Equal(original.RetryAt) || restored.Window != "five_hour" {
		t.Errorf("round trip lost detail: %+v", restored)
	}

	if _, ok := ParseWorkflowCapacityWait("  "); ok {
		t.Error("an empty file parsed as a wait")
	}
	if _, ok := ParseWorkflowCapacityWait("{not json"); ok {
		t.Error("a corrupt file parsed as a wait")
	}
}

// TestCapacityWaitPathIsRunScoped keeps the record beside the run it describes,
// so two runs of the same workflow cannot overwrite each other's resume point.
func TestCapacityWaitPathIsRunScoped(t *testing.T) {
	got := WorkflowCapacityWaitPath("Workflow/project-a", "iteration-3")
	want := "Workflow/project-a/runs/iteration-3/" + WorkflowCapacityWaitFilename
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func jsonMarshalIndentForTest(v interface{}) (string, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	return string(body), err
}
