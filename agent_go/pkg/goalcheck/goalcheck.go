// Package goalcheck holds the Pulse's code-only goal facts and silence
// alarm (PLAT-697 phase 1, docs/design/pulse_goal_owner.md). It is pure: the
// caller loads metric definitions, observations and runs, and Evaluate says
// whether the goal is measured, whether the work that drives it runs, and
// which alarms to raise. No AI and no I/O, so the decision is cheap enough to
// compute on every Pulse view and scheduler tick.
package goalcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultSilenceDays is the design default: no run, or no measurement, for
// this many days raises the alarm.
const DefaultSilenceDays = 3

// recentRunsShown bounds the per-run facts returned to the agent and the UI.
const recentRunsShown = 10

// Metric is one active goal metric definition. Only primary metrics measure
// the goal; Route names the route whose runs drive and measure it, if any.
type Metric struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role"`
	Route string `json:"route,omitempty"`
	Unit  string `json:"unit,omitempty"`
}

// Observation is one pulse_goal_observations row.
type Observation struct {
	Metric     string    `json:"metric"`
	RunID      string    `json:"run_id"`
	Value      *float64  `json:"value,omitempty"`
	Status     string    `json:"status,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Run is one finished workflow run (not a Pulse pass).
type Run struct {
	RunID      string    `json:"run_id"` // run folder, e.g. iteration-41-sched or iteration-41-sched/default
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	Routes     []string  `json:"routes,omitempty"`
	// Measured is set when the run-finish recorder saw a primary reading
	// recorded during the run. Older runs are matched to observations here.
	Measured bool `json:"measured,omitempty"`
}

// Input is everything Evaluate needs.
type Input struct {
	Now          time.Time
	SilenceDays  int
	Metrics      []Metric
	Observations []Observation
	Runs         []Run
	// SchedulesPaused: the workflow has schedules and all are off (a
	// deliberate pause). ReportedPauseFingerprint is the fingerprint the last
	// goal check reported, so a pause is said once.
	SchedulesPaused          bool
	ReportedPauseFingerprint string
}

// Alarm kinds.
const (
	AlarmNotMeasured          = "not_measured"
	AlarmNoRun                = "no_run"
	AlarmGoalWorkSkipped      = "goal_work_skipped"
	AlarmGoalWorkNotMeasuring = "goal_work_not_measuring"
)

// Alarm is one silence alarm, in plain words for the owner.
type Alarm struct {
	Kind    string `json:"kind"`
	Days    int    `json:"days,omitempty"`
	Message string `json:"message"`
}

// RunFact is the per-run goal fact: did the goal-driving work run, and was the
// goal measured.
type RunFact struct {
	RunID        string   `json:"run_id"`
	FinishedAt   string   `json:"finished_at"`
	Status       string   `json:"status"`
	Routes       []string `json:"routes,omitempty"`
	GoalWorkRan  bool     `json:"goal_work_ran"`
	GoalMeasured bool     `json:"goal_measured"`
}

// Code statuses. The agent's goal check turns these into on track / at risk /
// off track / not measured; code only says what the facts show.
const (
	StatusNoGoal      = "no_goal"
	StatusOK          = "ok"
	StatusAtRisk      = "at_risk"
	StatusNotMeasured = "not_measured"
)

// Facts is the result.
type Facts struct {
	Status            string   `json:"status"`
	Summary           string   `json:"summary"`
	HasGoal           bool     `json:"has_goal"`
	PrimaryMetrics    []string `json:"primary_metrics,omitempty"`
	GoalRoutes        []string `json:"goal_routes,omitempty"`
	KeyMetric         string   `json:"key_metric,omitempty"`
	KeyValue          *float64 `json:"key_value,omitempty"`
	LastMeasuredAt    string   `json:"last_measured_at,omitempty"`
	LastRunMeasuredAt string   `json:"last_run_measured_at,omitempty"`
	// Days since the last reading a workflow run recorded; -1 when none.
	DaysSinceRunMeasured int       `json:"days_since_run_measured"`
	ReadingsOutsideRuns  int       `json:"readings_outside_runs_since,omitempty"`
	LastRunAt            string    `json:"last_run_at,omitempty"`
	DaysSinceRun         int       `json:"days_since_run"`
	LastGoalWorkAt       string    `json:"last_goal_work_at,omitempty"`
	DaysSinceGoalWork    int       `json:"days_since_goal_work"`
	RecentRuns           []RunFact `json:"recent_runs,omitempty"`
	Alarms               []Alarm   `json:"alarms"`
	SchedulesPaused      bool      `json:"schedules_paused"`
	PauseFingerprint     string    `json:"pause_fingerprint,omitempty"`
	// PauseAlreadyReported: the pause and these alarms were reported once;
	// stay quiet until something changes.
	PauseAlreadyReported bool `json:"pause_already_reported,omitempty"`
}

var runFolderPattern = regexp.MustCompile(`^iteration-\d+`)

// runSegment is the run folder's top segment ("iteration-41-sched"), or ""
// when the id is not a workflow run (a builder chat or a Pulse review).
func runSegment(id string) string {
	id = strings.Trim(strings.TrimSpace(id), "/")
	top := id
	if i := strings.Index(id, "/"); i >= 0 {
		top = id[:i]
	}
	if !runFolderPattern.MatchString(top) {
		return ""
	}
	return top
}

func completed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "success", "partial":
		return true
	}
	return false
}

func days(now, then time.Time) int {
	if then.IsZero() {
		return -1
	}
	d := int(now.Sub(then).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func dateLabel(t time.Time) string { return t.UTC().Format("2 Jan") }

func finishedAt(r Run) time.Time {
	if !r.FinishedAt.IsZero() {
		return r.FinishedAt
	}
	return r.StartedAt
}

// Evaluate computes the goal facts and silence alarms.
func Evaluate(in Input) Facts {
	if in.SilenceDays <= 0 {
		in.SilenceDays = DefaultSilenceDays
	}
	now := in.Now.UTC()
	facts := Facts{Status: StatusNoGoal, Alarms: []Alarm{}, DaysSinceRunMeasured: -1, DaysSinceRun: -1, DaysSinceGoalWork: -1, SchedulesPaused: in.SchedulesPaused}

	primary := map[string]Metric{}
	routeSet := map[string]bool{}
	for _, m := range in.Metrics {
		if m.Role != "primary" {
			continue
		}
		primary[m.ID] = m
		facts.PrimaryMetrics = append(facts.PrimaryMetrics, m.ID)
		if r := strings.TrimSpace(m.Route); r != "" && !routeSet[r] {
			routeSet[r] = true
			facts.GoalRoutes = append(facts.GoalRoutes, r)
		}
	}
	sort.Strings(facts.PrimaryMetrics)
	sort.Strings(facts.GoalRoutes)
	if len(primary) == 0 {
		facts.Summary = "No goal metric is configured, so the goal cannot be checked."
		return facts
	}
	facts.HasGoal = true

	// Readings: a primary observation with a value.
	readings := []Observation{}
	for _, o := range in.Observations {
		if _, ok := primary[o.Metric]; ok && o.Value != nil && !o.ObservedAt.IsZero() {
			readings = append(readings, o)
		}
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].ObservedAt.Before(readings[j].ObservedAt) })
	var lastRunReading time.Time
	if n := len(readings); n > 0 {
		latest := readings[n-1]
		facts.LastMeasuredAt = latest.ObservedAt.UTC().Format(time.RFC3339)
		facts.KeyMetric = latest.Metric
		v := *latest.Value
		facts.KeyValue = &v
	}
	for _, o := range readings {
		if runSegment(o.RunID) != "" && o.ObservedAt.After(lastRunReading) {
			lastRunReading = o.ObservedAt
		}
	}
	for _, o := range readings {
		if runSegment(o.RunID) == "" && o.ObservedAt.After(lastRunReading) {
			facts.ReadingsOutsideRuns++
		}
	}
	if !lastRunReading.IsZero() {
		facts.LastRunMeasuredAt = lastRunReading.UTC().Format(time.RFC3339)
	}
	facts.DaysSinceRunMeasured = days(now, lastRunReading)

	// Per-run facts, newest first.
	runs := append([]Run(nil), in.Runs...)
	sort.SliceStable(runs, func(i, j int) bool { return finishedAt(runs[i]).After(finishedAt(runs[j])) })
	runFacts := make([]RunFact, 0, len(runs))
	for _, r := range runs {
		fact := RunFact{RunID: r.RunID, FinishedAt: finishedAt(r).UTC().Format(time.RFC3339), Status: r.Status, Routes: r.Routes, GoalMeasured: r.Measured}
		for _, route := range r.Routes {
			if routeSet[route] && completed(r.Status) {
				fact.GoalWorkRan = true
			}
		}
		if !fact.GoalMeasured {
			fact.GoalMeasured = measuredBy(r, readings)
		}
		runFacts = append(runFacts, fact)
	}
	if len(runFacts) > recentRunsShown {
		facts.RecentRuns = runFacts[:recentRunsShown]
	} else {
		facts.RecentRuns = runFacts
	}

	var lastRun, lastGoalWork time.Time
	if len(runs) > 0 {
		lastRun = finishedAt(runs[0])
		facts.LastRunAt = lastRun.UTC().Format(time.RFC3339)
	}
	facts.DaysSinceRun = days(now, lastRun)
	lastGoalWorkIndex := -1
	for i, f := range runFacts {
		if f.GoalWorkRan {
			lastGoalWork = finishedAt(runs[i])
			lastGoalWorkIndex = i
			facts.LastGoalWorkAt = lastGoalWork.UTC().Format(time.RFC3339)
			break
		}
	}
	facts.DaysSinceGoalWork = days(now, lastGoalWork)
	n := in.SilenceDays

	// 1. The goal is not measured by the workflow's own runs.
	if lastRunReading.IsZero() || facts.DaysSinceRunMeasured >= n {
		msg := "The goal has never been measured by a workflow run."
		if !lastRunReading.IsZero() {
			msg = fmt.Sprintf("The goal has not been measured by a workflow run for %d days (last reading %s).", facts.DaysSinceRunMeasured, dateLabel(lastRunReading))
		}
		if facts.ReadingsOutsideRuns > 0 {
			msg += fmt.Sprintf(" %d reading(s) since came from outside a run, the latest on %s; the runs themselves do not record it.", facts.ReadingsOutsideRuns, dateLabel(readings[len(readings)-1].ObservedAt))
		}
		facts.Alarms = append(facts.Alarms, Alarm{Kind: AlarmNotMeasured, Days: facts.DaysSinceRunMeasured, Message: msg})
	}

	// 2. Goal work ran but recorded no reading.
	unmeasured := 0
	for i, f := range runFacts {
		if f.GoalWorkRan && !f.GoalMeasured && finishedAt(runs[i]).After(lastRunReading) {
			unmeasured++
		}
	}
	if unmeasured > 0 {
		since := "ever"
		if !lastRunReading.IsZero() {
			since = "since " + dateLabel(lastRunReading)
		}
		facts.Alarms = append(facts.Alarms, Alarm{Kind: AlarmGoalWorkNotMeasuring, Message: fmt.Sprintf("The goal work (%s) ran %d time(s) %s without recording a reading.", strings.Join(facts.GoalRoutes, ", "), unmeasured, since)})
	}

	// 3. The goal work is skipped while other work runs.
	if len(routeSet) > 0 && (lastGoalWork.IsZero() || facts.DaysSinceGoalWork >= n) {
		others := runFacts
		if lastGoalWorkIndex >= 0 {
			others = runFacts[:lastGoalWorkIndex]
		}
		if len(others) > 0 {
			counts := map[string]int{}
			failedGoal := 0
			for _, f := range others {
				goalRoute := false
				for _, route := range f.Routes {
					if routeSet[route] {
						goalRoute = true
					}
				}
				if goalRoute {
					failedGoal++
					continue
				}
				label := strings.Join(f.Routes, "+")
				if label == "" {
					label = "no route"
				}
				counts[label]++
			}
			labels := make([]string, 0, len(counts))
			for label := range counts {
				labels = append(labels, label)
			}
			sort.Slice(labels, func(i, j int) bool {
				if counts[labels[i]] != counts[labels[j]] {
					return counts[labels[i]] > counts[labels[j]]
				}
				return labels[i] < labels[j]
			})
			parts := make([]string, 0, len(labels))
			for _, label := range labels {
				parts = append(parts, fmt.Sprintf("%s ×%d", label, counts[label]))
			}
			msg := fmt.Sprintf("The goal work (%s) has not completed in any run", strings.Join(facts.GoalRoutes, ", "))
			if !lastGoalWork.IsZero() {
				msg = fmt.Sprintf("The goal work (%s) has not completed for %d days (last on %s)", strings.Join(facts.GoalRoutes, ", "), facts.DaysSinceGoalWork, dateLabel(lastGoalWork))
			}
			msg += fmt.Sprintf("; the %d run(s) since", len(others))
			if len(parts) > 0 {
				msg += " took other routes: " + strings.Join(parts, ", ")
			}
			if failedGoal > 0 {
				if len(parts) > 0 {
					msg += ";"
				}
				msg += fmt.Sprintf(" %d goal run(s) failed", failedGoal)
			}
			facts.Alarms = append(facts.Alarms, Alarm{Kind: AlarmGoalWorkSkipped, Days: facts.DaysSinceGoalWork, Message: msg + "."})
		}
	}

	// 4. No run at all.
	if lastRun.IsZero() || facts.DaysSinceRun >= n {
		msg := "The workflow has never run."
		if !lastRun.IsZero() {
			msg = fmt.Sprintf("No workflow run for %d days (last on %s).", facts.DaysSinceRun, dateLabel(lastRun))
		}
		if in.SchedulesPaused {
			msg += " Its schedules are paused."
		}
		facts.Alarms = append(facts.Alarms, Alarm{Kind: AlarmNoRun, Days: facts.DaysSinceRun, Message: msg})
	}

	switch {
	case hasAlarm(facts.Alarms, AlarmNotMeasured):
		facts.Status = StatusNotMeasured
	case len(facts.Alarms) > 0:
		facts.Status = StatusAtRisk
	default:
		facts.Status = StatusOK
	}
	if in.SchedulesPaused {
		facts.PauseFingerprint = pauseFingerprint(facts)
		facts.PauseAlreadyReported = facts.PauseFingerprint != "" && facts.PauseFingerprint == strings.TrimSpace(in.ReportedPauseFingerprint)
	}
	facts.Summary = summary(facts)
	return facts
}

// measuredBy matches a run to a reading its own folder recorded while it ran
// (an hour of slack each side: readings carry the source's time).
func measuredBy(r Run, readings []Observation) bool {
	seg := runSegment(r.RunID)
	if seg == "" {
		return false
	}
	start, end := r.StartedAt, finishedAt(r)
	if start.IsZero() {
		start = end
	}
	for _, o := range readings {
		if runSegment(o.RunID) != seg {
			continue
		}
		if !o.ObservedAt.Before(start.Add(-time.Hour)) && !o.ObservedAt.After(end.Add(time.Hour)) {
			return true
		}
	}
	return false
}

func hasAlarm(alarms []Alarm, kind string) bool {
	for _, a := range alarms {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// pauseFingerprint changes when anything the owner should hear about changes:
// a new run, a new reading, or a different set of alarms. While it is the
// same, a paused workflow's alarms were already reported once.
func pauseFingerprint(f Facts) string {
	kinds := make([]string, 0, len(f.Alarms))
	for _, a := range f.Alarms {
		kinds = append(kinds, a.Kind)
	}
	sort.Strings(kinds)
	sum := sha256.Sum256([]byte(strings.Join([]string{"paused", f.LastRunAt, f.LastMeasuredAt, strings.Join(kinds, ",")}, "|")))
	return hex.EncodeToString(sum[:8])
}

func summary(f Facts) string {
	if !f.HasGoal {
		return "No goal metric is configured."
	}
	parts := []string{}
	for _, a := range f.Alarms {
		parts = append(parts, a.Message)
	}
	if len(parts) == 0 {
		parts = append(parts, "The goal is measured and its work is running.")
	}
	if f.SchedulesPaused {
		if f.PauseAlreadyReported {
			parts = append(parts, "Schedules are paused on purpose and this was already reported; nothing new to say until something changes.")
		} else {
			parts = append(parts, "Schedules are paused on purpose; report this once.")
		}
	}
	return strings.Join(parts, " ")
}
