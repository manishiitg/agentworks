package productschedule

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	bad := []Schedule{
		{ID: "", Name: "x", CadenceHours: 1, Messages: []string{"m"}},
		{ID: "a", Name: "x", Messages: []string{"m"}},
		{ID: "a", Name: "x", CronExpression: "not cron", Messages: []string{"m"}},
		{ID: "a", Name: "x", CadenceHours: 1},
		{ID: "a", Name: "x", CronExpression: "0 8 * * *", Timezone: "Mars/Olympus", Messages: []string{"m"}},
	}
	for i, s := range bad {
		if err := Validate(s); err == nil {
			t.Fatalf("case %d should fail: %+v", i, s)
		}
	}
	good := Schedule{ID: "daily", Name: "Daily", CronExpression: "0 8 * * *", Timezone: "Asia/Kolkata", Messages: []string{"hi"}}
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAll([]Schedule{good, good}); err == nil {
		t.Fatal("duplicate ids should fail")
	}
}

func TestDecideCadence(t *testing.T) {
	hour := 9
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	s := Schedule{ID: "a", Name: "a", Enabled: true, CadenceHours: 24, QuietMinutes: 10, MaxDeferralHours: 4, Messages: []string{"m"}}
	cases := []struct {
		name string
		s    Schedule
		in   Inputs
		run  bool
		def  bool
	}{
		{"disabled", Schedule{ID: "a", Name: "a", CadenceHours: 24}, Inputs{Now: now, SinceInteractive: time.Hour}, false, false},
		{"never ran → due", s, Inputs{Now: now, SinceInteractive: time.Hour}, true, false},
		{"ran recently", s, Inputs{Now: now, LastRun: now.Add(-time.Hour), SinceInteractive: time.Hour}, false, false},
		{"cadence elapsed", s, Inputs{Now: now, LastRun: now.Add(-25 * time.Hour), SinceInteractive: time.Hour}, true, false},
		{"user active → defer", s, Inputs{Now: now, LastRun: now.Add(-25 * time.Hour), SinceInteractive: time.Minute}, false, true},
		{"active but way overdue → run", s, Inputs{Now: now, LastRun: now.Add(-30 * time.Hour), SinceInteractive: time.Minute}, true, false},
		{"preferred hour not reached", Schedule{ID: "a", Name: "a", Enabled: true, CadenceHours: 24, PreferredHour: &hour}, Inputs{Now: now.Add(-5 * time.Hour), SinceInteractive: time.Hour}, false, false},
		{"preferred hour reached", Schedule{ID: "a", Name: "a", Enabled: true, CadenceHours: 24, PreferredHour: &hour}, Inputs{Now: now, SinceInteractive: time.Hour}, true, false},
		// A failed run must not re-fire on the next tick: it backs off.
		{"failed 10m ago → wait", s, Inputs{Now: now, LastRun: now.Add(-25 * time.Hour), SinceInteractive: time.Hour, LastAttempt: now.Add(-10 * time.Minute), ConsecutiveFailures: 1}, false, false},
		{"failed 31m ago → retry", s, Inputs{Now: now, LastRun: now.Add(-25 * time.Hour), SinceInteractive: time.Hour, LastAttempt: now.Add(-31 * time.Minute), ConsecutiveFailures: 1}, true, false},
		{"3 failures, 1h ago → still waiting (2h backoff)", s, Inputs{Now: now, LastRun: now.Add(-25 * time.Hour), SinceInteractive: time.Hour, LastAttempt: now.Add(-time.Hour), ConsecutiveFailures: 3}, false, false},
		{"attempt without failure does not back off", s, Inputs{Now: now, LastRun: now.Add(-25 * time.Hour), SinceInteractive: time.Hour, LastAttempt: now.Add(-time.Minute), ConsecutiveFailures: 0}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(c.s, c.in)
			if d.Run != c.run || d.Deferred != c.def {
				t.Fatalf("Decide = %+v, want run=%v deferred=%v", d, c.run, c.def)
			}
		})
	}
}

func TestRetryBackoffDoublesAndCaps(t *testing.T) {
	cases := []struct {
		failures int
		cadence  time.Duration
		want     time.Duration
	}{
		{0, 24 * time.Hour, 0},
		{1, 24 * time.Hour, 30 * time.Minute},
		{2, 24 * time.Hour, time.Hour},
		{4, 24 * time.Hour, 4 * time.Hour},
		{10, 24 * time.Hour, 6 * time.Hour},
		{10, 2 * time.Hour, 2 * time.Hour}, // never longer than the cadence
	}
	for _, c := range cases {
		if got := RetryBackoff(c.failures, c.cadence); got != c.want {
			t.Fatalf("RetryBackoff(%d, %s) = %s, want %s", c.failures, c.cadence, got, c.want)
		}
	}
}

func TestDecideCron(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	s := Schedule{ID: "a", Name: "a", Enabled: true, CronExpression: "0 8 * * *", Timezone: "Asia/Kolkata", Messages: []string{"m"}}
	eightToday := time.Date(2026, 9, 3, 8, 0, 0, 0, loc)

	if d := Decide(s, Inputs{Now: eightToday.Add(time.Hour), SinceInteractive: time.Hour}); d.Run {
		t.Fatalf("a schedule without an activation anchor must wait for its next occurrence: %+v", d)
	}
	activatedYesterday := eightToday.Add(-20 * time.Hour)
	if d := Decide(s, Inputs{Now: eightToday.Add(-time.Hour), ActivatedAt: activatedYesterday, SinceInteractive: time.Hour}); d.Run {
		t.Fatalf("a newly activated schedule must wait until its first occurrence: %+v", d)
	}
	d := Decide(s, Inputs{Now: eightToday.Add(time.Hour), ActivatedAt: activatedYesterday, SinceInteractive: time.Hour})
	if !d.Run || !d.ScheduledFor.Equal(eightToday) {
		t.Fatalf("first 08:00 occurrence after activation should be due: %+v", d)
	}
	d = Decide(s, Inputs{Now: eightToday.Add(time.Hour), LastRun: eightToday.Add(-20 * time.Hour), SinceInteractive: time.Hour})
	if !d.Run || !d.ScheduledFor.Equal(eightToday) {
		t.Fatalf("08:00 occurrence should be due: %+v", d)
	}
	if d := Decide(s, Inputs{Now: eightToday.Add(-time.Hour), LastRun: eightToday.Add(-20 * time.Hour), SinceInteractive: time.Hour}); d.Run {
		t.Fatalf("before 08:00 nothing is due: %+v", d)
	}
}

type fakeRun struct {
	turns    []Turn
	failAt   int
	sent     []string
	finished error
	done     bool
}

func (f *fakeRun) Turns() []Turn { return f.turns }
func (f *fakeRun) Send(_ context.Context, i int, t Turn) (string, error) {
	f.sent = append(f.sent, t.Label)
	if i == f.failAt {
		return "", errors.New("boom")
	}
	return "reply " + t.Label, nil
}
func (f *fakeRun) Finish(_ context.Context, err error) { f.finished, f.done = err, true }

func newRunner(t *testing.T, begin func(ctx context.Context, src Source) (Run, error)) *Runner {
	t.Helper()
	r, err := NewRunner(Host{Name: "test", Schedule: func() Schedule { return Schedule{Enabled: true, CadenceHours: 1} }, Begin: begin})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunNowSendsTurnsInOrderAndTracksStatus(t *testing.T) {
	run := &fakeRun{turns: []Turn{{Label: "a"}, {Label: "b"}, {Label: "c"}}, failAt: -1}
	r := newRunner(t, func(context.Context, Source) (Run, error) { return run, nil })
	if err := r.RunNow(context.Background(), SourceManual); err != nil {
		t.Fatal(err)
	}
	if len(run.sent) != 3 || run.sent[0] != "a" || run.sent[2] != "c" || !run.done || run.finished != nil {
		t.Fatalf("run = %+v", run)
	}
	st := r.Status()
	if st.Running || st.Last == nil || st.Last.Source != SourceManual || len(st.Last.Steps) != 3 || st.Last.Steps[1].Status != StepDone || st.Last.Steps[1].Reply != "reply b" {
		t.Fatalf("status = %+v", st)
	}
}

func TestRunNowStopsAtFirstFailureAndSkipsTheRest(t *testing.T) {
	run := &fakeRun{turns: []Turn{{Label: "a"}, {Label: "b"}, {Label: "c"}}, failAt: 1}
	r := newRunner(t, func(context.Context, Source) (Run, error) { return run, nil })
	err := r.RunNow(context.Background(), SourceScheduled)
	if err == nil || run.finished == nil || len(run.sent) != 2 {
		t.Fatalf("err=%v finished=%v sent=%v", err, run.finished, run.sent)
	}
	steps := r.Status().Last.Steps
	if steps[0].Status != StepDone || steps[1].Status != StepFailed || steps[2].Status != StepSkipped {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestRunNowIsSingleFlightAndSkipIsQuiet(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	blocking := &blockingRun{release: release, started: started}
	r := newRunner(t, func(context.Context, Source) (Run, error) { return blocking, nil })
	go func() { _ = r.RunNow(context.Background(), SourceManual) }()
	<-started
	if err := r.RunNow(context.Background(), SourceManual); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second run err = %v, want ErrAlreadyRunning", err)
	}
	if !r.Status().Running {
		t.Fatal("status should report running")
	}
	close(release)

	skip := newRunner(t, func(context.Context, Source) (Run, error) { return nil, ErrSkip })
	if err := skip.RunNow(context.Background(), SourceScheduled); !errors.Is(err, ErrSkip) {
		t.Fatalf("err = %v, want ErrSkip", err)
	}
	if st := skip.Status(); st.Last == nil || !st.Last.Skipped {
		t.Fatalf("skipped run should be recorded: %+v", st)
	}
}

type blockingRun struct {
	release <-chan struct{}
	started chan struct{}
}

func (b *blockingRun) Turns() []Turn { return []Turn{{Label: "wait"}} }
func (b *blockingRun) Send(context.Context, int, Turn) (string, error) {
	close(b.started)
	<-b.release
	return "ok", nil
}
func (b *blockingRun) Finish(context.Context, error) {}

// A one-time schedule ("check the deploy in 3 hours") runs once at its moment and never again: not before
// it, not after a success, and not after OneTimeMaxFailures failed attempts.
func TestOneTimeScheduleRunsOnceAndOnlyOnce(t *testing.T) {
	at := time.Date(2026, 10, 5, 21, 30, 0, 0, time.UTC)
	s := Schedule{ID: "once", Name: "Check deploy", Enabled: true, RunAt: at.Format(time.RFC3339), Messages: []string{"check"}}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
	if d := Decide(s, Inputs{Now: at.Add(-time.Minute)}); d.Run {
		t.Fatalf("ran before its time: %+v", d)
	}
	if d := Decide(s, Inputs{Now: at.Add(time.Minute)}); !d.Run || !d.ScheduledFor.Equal(at) {
		t.Fatalf("not due after its time: %+v", d)
	}
	if d := Decide(s, Inputs{Now: at.Add(time.Hour), LastRun: at.Add(time.Minute)}); d.Run {
		t.Fatalf("ran again after a success: %+v", d)
	}
	if d := Decide(s, Inputs{Now: at.Add(24 * time.Hour), LastAttempt: at, ConsecutiveFailures: OneTimeMaxFailures}); d.Run {
		t.Fatalf("kept retrying after %d failures: %+v", OneTimeMaxFailures, d)
	}
	// Exactly one timing form; a bad instant is refused.
	both := s
	both.CronExpression = "0 8 * * *"
	badAt := s
	badAt.RunAt = "tomorrow"
	for _, bad := range []Schedule{both, badAt} {
		if err := Validate(bad); err == nil {
			t.Fatalf("should be refused: %+v", bad)
		}
	}
}
