package picli

import (
	"testing"
	"time"
)

// The regression this exists for, measured live on 2026-08-19: the first
// version of the stall logging reported on total elapsed run time, so a
// perfectly healthy 17-minute step -- one emitting tool events throughout and
// making real progress the whole time -- produced ~34 error-level lines. That
// is the same alert-fatigue failure that made the TOOL_ERROR_SUSPECT channel
// unreadable at 929 lines: a signal nobody can read is not a signal.
func TestPiStallReporterStaysSilentWhileEventsKeepArriving(t *testing.T) {
	r := &piStallReporter{}
	start := time.Now()

	// 40 minutes of polling every 30s on a step that never goes quiet for more
	// than 20 seconds. Long, but healthy: not one line.
	for elapsed := piStallPollInterval; elapsed <= 40*time.Minute; elapsed += piStallPollInterval {
		if r.shouldReport(false, 20*time.Second, start.Add(elapsed)) {
			t.Fatalf("reported a stall at %s on a step that was still emitting events", elapsed)
		}
	}
}

// The deadlock case never resolves on its own, so it must not wait out a
// silence threshold before saying anything.
func TestPiStallReporterReportsTerminalDeadlockImmediately(t *testing.T) {
	r := &piStallReporter{}
	// Terminal seen, stream only just quiet -- still report on the first check.
	if !r.shouldReport(true, time.Second, time.Now()) {
		t.Fatal("did not report a pi that already emitted agent_settled and has not exited")
	}
}

func TestPiStallReporterReportsGenuineSilence(t *testing.T) {
	r := &piStallReporter{}
	now := time.Now()
	if r.shouldReport(false, piStallSilenceThreshold-time.Second, now) {
		t.Fatal("reported just under the silence threshold")
	}
	if !r.shouldReport(false, piStallSilenceThreshold, now) {
		t.Fatal("did not report once the stream had been silent for the full threshold")
	}
}

// Backoff is what keeps a genuinely stuck run readable: a heartbeat, not a
// flood. Pinned by counting real reports over a long stall rather than
// asserting on the interval field, so the property (few lines) is what is
// tested, not the implementation detail.
func TestPiStallReporterBacksOffOnASustainedStall(t *testing.T) {
	r := &piStallReporter{}
	start := time.Now()
	reports := 0
	for elapsed := piStallPollInterval; elapsed <= 2*time.Hour; elapsed += piStallPollInterval {
		if r.shouldReport(true, time.Hour, start.Add(elapsed)) {
			reports++
		}
	}
	// Unbounded 30s reporting would be 240 lines over two hours.
	if reports > 12 {
		t.Fatalf("a two-hour stall produced %d lines; backoff is not limiting the flood", reports)
	}
	// It must still say something periodically, or a stuck run goes unnoticed.
	if reports < 5 {
		t.Fatalf("a two-hour stall produced only %d lines; the heartbeat is too sparse to notice", reports)
	}
}
