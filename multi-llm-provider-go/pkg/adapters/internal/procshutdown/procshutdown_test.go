//go:build !windows

package procshutdown

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// testLogger captures log lines so tests can assert on the shutdown trace.
type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) record(format string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Record the format string — tests look for substrings of the format,
	// which is stable across runs without fmt formatting nuances.
	l.lines = append(l.lines, format)
}
func (l *testLogger) Infof(f string, v ...any)             { l.record(f, v...) }
func (l *testLogger) Errorf(f string, v ...any)            { l.record(f, v...) }
func (l *testLogger) Debugf(f string, args ...interface{}) { l.record(f, args...) }
func (l *testLogger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, sub) {
			return true
		}
	}
	return false
}
func (l *testLogger) countContains(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, sub) {
			n++
		}
	}
	return n
}

// shrinkGraces installs short test graces and restores the production
// values on cleanup. All tests use this to keep wall-clock fast.
func shrinkGraces(t *testing.T, first, second, third time.Duration) {
	t.Helper()
	origFirst, origSecond, origThird := FirstGrace, SecondGrace, ThirdGrace
	FirstGrace, SecondGrace, ThirdGrace = first, second, third
	t.Cleanup(func() {
		FirstGrace, SecondGrace, ThirdGrace = origFirst, origSecond, origThird
	})
}

// startSleepingChild starts a `sleep` in its own process group so the helper
// can target -pid. Caller must reap.
func startSleepingChild(t *testing.T, seconds string) *exec.Cmd {
	t.Helper()
	// Detached from test context — we want shutdown semantics driven by the
	// helper, not by ctx cancellation.
	cmd := exec.CommandContext(context.Background(), "sleep", seconds)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	return cmd
}

// startTrapScript starts a process that ignores SIGTERM, simulating
// a CLI ignoring SIGTERM while blocked in a synchronous HTTP call. It
// exits only on SIGKILL. The script prints "READY" once the signal handler
// is installed so the test can avoid the startup race.
func startTrapScript(t *testing.T) (*exec.Cmd, *bufio.Reader) {
	t.Helper()
	script := `
		$SIG{TERM} = 'IGNORE';
		$SIG{INT} = 'IGNORE';
		$| = 1;
		print "READY\n";
		sleep 30;
	`
	cmd := exec.CommandContext(context.Background(), "perl", "-e", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start trap script: %v", err)
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || line != "READY\n" {
		t.Fatalf("expected READY line, got %q (err=%v)", line, err)
	}
	return cmd, reader
}

func TestGracefulNilCmdIsNoOp(t *testing.T) {
	log := &testLogger{}
	Graceful(nil, nil, log)
	if len(log.lines) != 0 {
		t.Fatalf("expected no log lines for nil cmd, got %d: %v", len(log.lines), log.lines)
	}
}

func TestGracefulCmdWithNilProcessIsNoOp(t *testing.T) {
	log := &testLogger{}
	cmd := &exec.Cmd{}
	Graceful(cmd, nil, log)
	if len(log.lines) != 0 {
		t.Fatalf("expected no log lines for unstarted cmd, got %d: %v", len(log.lines), log.lines)
	}
}

func TestGracefulAfterNaturalExitDoesNotSignalCompletedProcess(t *testing.T) {
	cmd := startSleepingChild(t, "0.05")
	terminated := make(chan struct{})
	log := &testLogger{}
	go func() {
		_ = cmd.Wait()
		close(terminated)
	}()

	GracefulAfterNaturalExit(cmd, terminated, time.Second, log)

	if log.contains("SIGTERM attempt") {
		t.Fatalf("natural exit must not receive SIGTERM; lines=%v", log.lines)
	}
	if !log.contains("exited naturally") {
		t.Fatalf("expected natural-exit log; lines=%v", log.lines)
	}
}

func TestGracefulAfterNaturalExitFallsBackForHungProcess(t *testing.T) {
	shrinkGraces(t, 500*time.Millisecond, 500*time.Millisecond, 500*time.Millisecond)
	cmd := startSleepingChild(t, "30")
	terminated := make(chan struct{})
	log := &testLogger{}
	go func() {
		_ = cmd.Wait()
		close(terminated)
	}()

	GracefulAfterNaturalExit(cmd, terminated, 20*time.Millisecond, log)

	if !log.contains("natural-exit grace") {
		t.Fatalf("expected natural-exit timeout; lines=%v", log.lines)
	}
	if log.countContains("SIGTERM attempt") != 1 {
		t.Fatalf("expected one SIGTERM fallback; lines=%v", log.lines)
	}
}

// TestGracefulExitsAfterFirstSIGTERM simulates a well-behaved CLI: SIGTERM
// lands and the process exits within the first grace. Helper must NOT send
// a second/third SIGTERM and must NOT send SIGKILL.
func TestGracefulExitsAfterFirstSIGTERM(t *testing.T) {
	shrinkGraces(t, 500*time.Millisecond, 500*time.Millisecond, 500*time.Millisecond)
	cmd := startSleepingChild(t, "30")
	terminated := make(chan struct{})
	log := &testLogger{}

	var waitOnce sync.Once
	go func() {
		waitOnce.Do(func() {
			_ = cmd.Wait()
			close(terminated)
		})
	}()

	start := time.Now()
	Graceful(cmd, terminated, log)
	elapsed := time.Since(start)

	if elapsed > 400*time.Millisecond {
		t.Fatalf("helper took too long for a SIGTERM-honoring process: %v", elapsed)
	}
	if log.countContains("SIGTERM attempt") != 1 {
		t.Fatalf("expected exactly 1 SIGTERM attempt, got %d. lines=%v", log.countContains("SIGTERM attempt"), log.lines)
	}
	if !log.contains("exited after SIGTERM") {
		t.Fatalf("expected 'exited after SIGTERM' log; got %v", log.lines)
	}
	if log.contains("SIGKILL") {
		t.Fatalf("did NOT expect SIGKILL for a well-behaved process; got %v", log.lines)
	}
}

// TestGracefulSendsAllThreeSIGTERMsThenSIGKILL simulates a CLI
// orphan bug: the process ignores SIGTERM completely. The helper must send
// all 3 SIGTERMs, then SIGKILL, and the process must be dead afterwards.
func TestGracefulSendsAllThreeSIGTERMsThenSIGKILL(t *testing.T) {
	// Tight graces to keep the test under a second wall-clock.
	shrinkGraces(t, 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)

	cmd, _ := startTrapScript(t)
	terminated := make(chan struct{})
	log := &testLogger{}

	var waitOnce sync.Once
	go func() {
		waitOnce.Do(func() {
			_ = cmd.Wait()
			close(terminated)
		})
	}()

	totalGrace := FirstGrace + SecondGrace + ThirdGrace
	start := time.Now()
	Graceful(cmd, terminated, log)
	elapsed := time.Since(start)

	// Helper itself should return shortly after sending SIGKILL.
	if elapsed < totalGrace {
		t.Fatalf("helper returned before all graces expired (%v < %v)", elapsed, totalGrace)
	}
	if elapsed > totalGrace+2*time.Second {
		t.Fatalf("helper took too long (%v) for total grace=%v", elapsed, totalGrace)
	}

	// Verify all 3 SIGTERMs were attempted.
	if got := log.countContains("SIGTERM attempt"); got != 3 {
		t.Fatalf("expected 3 SIGTERM attempts, got %d. lines=%v", got, log.lines)
	}
	// Verify SIGKILL escalation fired.
	if !log.contains("sending SIGKILL") {
		t.Fatalf("expected SIGKILL escalation in logs; got %v", log.lines)
	}

	// Verify process actually died.
	select {
	case <-terminated:
		// good
	case <-time.After(2 * time.Second):
		t.Fatalf("process did not exit after SIGKILL escalation")
	}
}

// TestGracefulHandlesAlreadyDeadProcess simulates a race where the process
// died before the helper fires. The helper should detect ESRCH on the
// first SIGTERM and return — no further SIGTERMs, no SIGKILL.
func TestGracefulHandlesAlreadyDeadProcess(t *testing.T) {
	shrinkGraces(t, 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)

	cmd := startSleepingChild(t, "1")
	_ = cmd.Wait()

	// Give the kernel a beat to clear the PID — otherwise SIGTERM may
	// succeed against a not-yet-reaped pid (which is fine, the next wait
	// would just time out, but we want the ESRCH path specifically).
	time.Sleep(50 * time.Millisecond)

	terminated := make(chan struct{})
	close(terminated) // process already gone

	log := &testLogger{}
	var ran atomic.Bool
	done := make(chan struct{})
	go func() {
		Graceful(cmd, terminated, log)
		ran.Store(true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Graceful did not return on already-dead process")
	}

	if !ran.Load() {
		t.Fatalf("Graceful did not complete")
	}
	if log.contains("sending SIGKILL") {
		t.Fatalf("should not escalate to SIGKILL for already-dead process; got %v", log.lines)
	}
}

// TestReapReturnsPromptlyForExitedProcess is the ordinary path: the process is
// already on its way out, so Reap must behave exactly like a bare cmd.Wait and
// never reach the escalation ladder.
func TestReapReturnsPromptlyForExitedProcess(t *testing.T) {
	log := &testLogger{}
	cmd := startSleepingChild(t, "0.1")

	start := time.Now()
	if err := Reap(cmd, 5*time.Second, log); err != nil {
		t.Fatalf("Reap returned error for clean exit: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Reap waited %s for a process that exits in 100ms", elapsed)
	}
	if log.contains("reap grace") {
		t.Fatalf("should not have escalated for a cleanly exiting process; got %v", log.lines)
	}
}

// TestReapKillsProcessThatOutlivesItsOutputStream is the regression for the
// 2026-08-01 Pulse fixer hang. Codex wrote its complete result and a native
// task_complete to its rollout, then stayed alive at 0% CPU for 65 minutes.
// Teardown was reachable only from the stdout `turn.completed` branch, so with
// no terminal event on that stream nothing ever signalled the process and the
// adapter's bare cmd.Wait() blocked for as long as its caller would hold on.
// Reap must bound that: escalate, and return.
func TestReapKillsProcessThatOutlivesItsOutputStream(t *testing.T) {
	shrinkGraces(t, 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond)
	log := &testLogger{}
	// Ignores SIGTERM, so only the SIGKILL at the end of the ladder frees it —
	// the strongest form of "nobody is coming to stop this".
	cmd, _ := startTrapScript(t)

	done := make(chan error, 1)
	go func() { done <- Reap(cmd, 100*time.Millisecond, log) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("Reap never returned for a process that outlived its output stream")
	}

	if !log.contains("reap grace") {
		t.Fatalf("expected the reap grace to be reported; got %v", log.lines)
	}
	if !log.contains("sending SIGKILL") {
		t.Fatalf("expected escalation to SIGKILL for a SIGTERM-ignoring process; got %v", log.lines)
	}
}

// TestReapNilCmdIsNoOp mirrors the nil guards on Graceful so callers can invoke
// it unconditionally.
func TestReapNilCmdIsNoOp(t *testing.T) {
	if err := Reap(nil, time.Second, &testLogger{}); err != nil {
		t.Fatalf("Reap(nil) returned %v", err)
	}
}
