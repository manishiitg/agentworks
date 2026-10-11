package goalcheck

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The retained fixture pins measurement staleness and actual run silence.
// Which recorded work advances the goal is now agent judgment (PLAT-822).
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
	// The historic fixture predates evidence fields in this pure-code input.
	for i := range fx.Observations {
		fx.Observations[i].Evidence = []string{"fixture source"}
	}
	in := Input{Now: fx.Now, Metrics: fx.Metrics, Observations: fx.Observations, Runs: fx.Runs, SchedulesPaused: fx.SchedulesPaused}
	facts := Evaluate(in)

	if facts.Status != StatusAtRisk || facts.DaysSinceRunMeasured != 20 {
		t.Fatalf("want stale DB metric and separate run coverage for 20 days, got %s / %d: %s", facts.Status, facts.DaysSinceRunMeasured, facts.Summary)
	}
	byKind := map[string]Alarm{}
	for _, a := range facts.Alarms {
		byKind[a.Kind] = a
	}
	for _, want := range []struct{ kind, text string }{
		{AlarmMeasurementStale, "subscriber_delta measurement is stale: last reading 17 Sep"},
		{AlarmNoRun, "No workflow run for 7 days (last on 30 Sep). Its schedules are paused."},
	} {
		if !strings.Contains(byKind[want.kind].Message, want.text) {
			t.Errorf("%s alarm %q lacks %q", want.kind, byKind[want.kind].Message, want.text)
		}
	}

	if len(facts.Alarms) != 2 || len(facts.RecentRuns) == 0 {
		t.Fatalf("expected only measurement/run-silence alarms and retained execution evidence: %+v", facts)
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
