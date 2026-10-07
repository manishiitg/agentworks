package goalcheck

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The pilot that motivated PLAT-697: Substack's subscriber goal went
// unmeasured and its growth route stopped completing while publish_review runs
// went on, and nothing flagged it. The fixture is a read-only copy of only the
// relevant rows of the owner's data on 2026-10-07.
func TestSilenceAlarmOnSubstackData(t *testing.T) {
	raw, err := os.ReadFile("testdata/substack_2026-10-07.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Now             time.Time     `json:"now"`
		SchedulesPaused bool          `json:"schedules_paused"`
		Metrics         []Metric      `json:"metrics"`
		Observations    []Observation `json:"observations"`
		Runs            []Run         `json:"runs"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	in := Input{Now: fx.Now, Metrics: fx.Metrics, Observations: fx.Observations, Runs: fx.Runs, SchedulesPaused: fx.SchedulesPaused}
	facts := Evaluate(in)

	if facts.Status != StatusNotMeasured || facts.DaysSinceRunMeasured != 20 {
		t.Fatalf("want not measured for 20 days, got %s / %d: %s", facts.Status, facts.DaysSinceRunMeasured, facts.Summary)
	}
	byKind := map[string]Alarm{}
	for _, a := range facts.Alarms {
		byKind[a.Kind] = a
	}
	for _, want := range []struct{ kind, text string }{
		{AlarmNotMeasured, "not been measured by a workflow run for 20 days (last reading 17 Sep)"},
		{AlarmNotMeasured, "1 reading(s) since came from outside a run"},
		{AlarmGoalWorkNotMeasuring, "ran 6 time(s) since 17 Sep without recording a reading"},
		{AlarmGoalWorkSkipped, "(growth_funnel) has not completed for 11 days (last on 26 Sep)"},
		{AlarmGoalWorkSkipped, "publish_review ×6"},
		{AlarmGoalWorkSkipped, "1 goal run(s) failed"},
		{AlarmNoRun, "No workflow run for 7 days (last on 30 Sep). Its schedules are paused."},
	} {
		if !strings.Contains(byKind[want.kind].Message, want.text) {
			t.Errorf("%s alarm %q lacks %q", want.kind, byKind[want.kind].Message, want.text)
		}
	}

	// A deliberate pause is reported once, then stays quiet until something
	// changes; a new run changes it.
	if facts.PauseAlreadyReported || facts.PauseFingerprint == "" {
		t.Fatalf("first look at the pause must report it: %+v", facts)
	}
	in.ReportedPauseFingerprint = facts.PauseFingerprint
	if again := Evaluate(in); !again.PauseAlreadyReported {
		t.Fatal("the same paused state was reported again")
	}
	in.Runs = append(in.Runs, Run{RunID: "iteration-85-sched", StartedAt: fx.Now.Add(-time.Hour), FinishedAt: fx.Now, Status: "success", Routes: []string{"publish_review"}})
	if changed := Evaluate(in); changed.PauseAlreadyReported {
		t.Fatal("a new run must end the quiet period")
	}
}
