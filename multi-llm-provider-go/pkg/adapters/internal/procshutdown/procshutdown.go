// Package procshutdown implements the structured-CLI process shutdown
// contract documented in docs/coding_sdk_structured_contract.md (section
// "Process Shutdown Contract", certification matrix row #22).
//
// The contract is: once an adapter has observed the CLI's terminal event
// (result / task_complete / done), tear down the subprocess with
//
//	SIGTERM  →  10s  →  SIGTERM  →  10s  →  SIGTERM  →  5s  →  SIGKILL
//
// Three SIGTERMs give a CLI multiple opportunities to notice the signal —
// useful for Node.js-based CLIs whose event loop may not service signals
// promptly while blocked in a syscall (e.g. a synchronous HTTP call to
// their MCP bridge). After 25s the kernel-unblockable SIGKILL fires so an
// uncooperative CLI cannot leak as an orphan.
//
// The graces (10/10/5s) exist so the CLI can flush state the *next* call
// needs (session files for --resume, transcript JSONL, etc.). Token usage
// and pricing data must already be captured from the terminal event itself,
// so escalating to SIGKILL after 25s does not lose billing data — it only
// forfeits --resume for that turn.
package procshutdown

import (
	"errors"
	"os/exec"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
)

// Package-level vars rather than const so tests can shrink them. Production
// callers should never override these — the contract is uniform across all
// structured CLI adapters.
var (
	FirstGrace  = 10 * time.Second
	SecondGrace = 10 * time.Second
	ThirdGrace  = 5 * time.Second
)

// Graceful tears down a CLI subprocess according to the structured shutdown
// contract. It is intended to be launched as a goroutine from the adapter's
// stdout-decode loop the moment a terminal event is observed:
//
//	go procshutdown.Graceful(cmd, decodeDone, g.logger)
//
// `terminated` MUST be a channel the caller closes when the subprocess's
// stdout reaches EOF (i.e. the process has exited and the decode goroutine
// has finished draining its output). In practice this is the same
// `decodeDone` channel the adapter's main goroutine selects on.
//
// Graceful returns once the shutdown sequence is complete (process exited
// at some stage of the sequence, or SIGKILL has been sent). It does NOT
// call cmd.Wait — the adapter's existing main goroutine is responsible for
// reaping.
//
// The subprocess must have been started with syscall.SysProcAttr{Setpgid: true}
// so the kill targets the whole process group (-pid), reaching any child
// workers the CLI spawned. All structured-CLI adapters in this repo do this.
//
// Edge cases:
//   - cmd or cmd.Process nil: returns immediately, no-op.
//   - syscall.Kill returning ESRCH (process already gone): logged at info, returns.
//   - terminated already closed when called: SIGTERM still sent (idempotent on a
//     dead process group); function returns on the immediate <-terminated read.
func Graceful(cmd *exec.Cmd, terminated <-chan struct{}, logger interfaces.Logger) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if logger == nil {
		logger = noopLogger{}
	}
	pid := cmd.Process.Pid

	// Attempt #1 — first SIGTERM with the longest grace, since this is when
	// the CLI is most likely to do useful state-flush work.
	if !sigtermAndWait(pid, terminated, FirstGrace, 1, logger) {
		return
	}

	// Attempt #2 — second SIGTERM. If the first signal was lost in the noise
	// (very rare on Unix but possible on Node.js if the event loop was busy),
	// the second one lands on a now-warmer event loop. If the CLI just
	// ignores SIGTERM (e.g. trap '' TERM), this won't help — but it also
	// won't hurt, and gives operators a second log marker.
	if !sigtermAndWait(pid, terminated, SecondGrace, 2, logger) {
		return
	}

	// Attempt #3 — last polite nudge before we escalate to SIGKILL.
	if !sigtermAndWait(pid, terminated, ThirdGrace, 3, logger) {
		return
	}

	// SIGKILL — unblockable by the kernel. After this, the process is dead
	// within microseconds and stdout will close (terminated will fire).
	logger.Infof("[SHUTDOWN] all 3 SIGTERMs exhausted for pid %d — sending SIGKILL", pid)
	if err := killGroup(pid); err != nil {
		logger.Errorf("[SHUTDOWN] SIGKILL to pgrp %d failed: %v", pid, err)
	}
}

// GracefulAfterNaturalExit gives a CLI a bounded opportunity to finish its
// normal post-result cleanup before sending SIGTERM. Some CLIs emit their
// terminal event before flushing resumable session state to disk; signaling
// immediately can therefore make a successful turn impossible to resume.
func GracefulAfterNaturalExit(cmd *exec.Cmd, terminated <-chan struct{}, naturalExitGrace time.Duration, logger interfaces.Logger) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if logger == nil {
		logger = noopLogger{}
	}
	if naturalExitGrace > 0 {
		timer := time.NewTimer(naturalExitGrace)
		defer timer.Stop()
		select {
		case <-terminated:
			logger.Infof("[SHUTDOWN] process %d exited naturally after terminal event", cmd.Process.Pid)
			return
		case <-timer.C:
			logger.Infof("[SHUTDOWN] natural-exit grace (%s) expired for pid %d", naturalExitGrace, cmd.Process.Pid)
		}
	}
	Graceful(cmd, terminated, logger)
}

// Reap calls cmd.Wait, escalating through the shutdown sequence if the process
// does not exit within grace.
//
// A bare cmd.Wait() after the stdout stream ends is unbounded, and every
// structured adapter used one. That is safe only while the terminal event is
// guaranteed to arrive: teardown is triggered by observing it, so a turn that
// ends without one leaves a live process nobody will ever signal, and Wait
// blocks for as long as the caller is willing to hold on — which in practice
// meant forever. Reap makes the reap itself the last line of defence, so the
// worst case is a bounded, logged kill rather than a stuck call.
//
// Callers must still only invoke this once all reads from the process's stdout
// pipe have completed, per os/exec's contract for StdoutPipe.
func Reap(cmd *exec.Cmd, grace time.Duration, logger interfaces.Logger) error {
	if cmd == nil {
		return nil
	}
	if logger == nil {
		logger = noopLogger{}
	}
	waitDone := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		err := cmd.Wait()
		waitDone <- err
		close(exited)
	}()

	if grace > 0 {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case err := <-waitDone:
			return err
		case <-timer.C:
			logger.Errorf("[SHUTDOWN] reap grace (%s) expired — process still alive after its output stream ended; escalating", grace)
		}
	}

	Graceful(cmd, exited, logger)
	return <-waitDone
}

// sigtermAndWait sends a SIGTERM to the process group and waits up to
// `grace` for the terminated channel to close. Returns true if the caller
// should proceed to the next escalation step, false if the process has
// already exited (or the kill failed with ESRCH) and we can return early.
func sigtermAndWait(pid int, terminated <-chan struct{}, grace time.Duration, attempt int, logger interfaces.Logger) (proceed bool) {
	logger.Infof("[SHUTDOWN] SIGTERM attempt %d/3 to pgrp %d (grace=%s)", attempt, pid, grace)
	if err := termGroup(pid); err != nil {
		if errors.Is(err, errProcessGone) {
			logger.Infof("[SHUTDOWN] SIGTERM #%d: pgrp %d already gone", attempt, pid)
			return false
		}
		logger.Errorf("[SHUTDOWN] SIGTERM #%d to pgrp %d failed: %v", attempt, pid, err)
		// Try to keep going anyway — the next escalation might land.
	}

	select {
	case <-terminated:
		logger.Infof("[SHUTDOWN] process %d exited after SIGTERM #%d (within %s)", pid, attempt, grace)
		return false
	case <-time.After(grace):
		logger.Infof("[SHUTDOWN] SIGTERM #%d grace (%s) expired for pid %d", attempt, grace, pid)
		return true
	}
}

// noopLogger satisfies interfaces.Logger for callers that pass nil. Adapters
// in this repo guard their own logger calls with nil checks; mirroring that
// here means Graceful is safe to use from any code path without a separate
// fallback at every call site.
type noopLogger struct{}

func (noopLogger) Infof(string, ...any)  {}
func (noopLogger) Errorf(string, ...any) {}
func (noopLogger) Debugf(string, ...any) {}

// KillGroupOnCancel makes a cancelled context kill the CLI's whole process group, not only the process Go
// started. exec.CommandContext's default sends SIGKILL to that one pid; when the command is a launcher (the
// npm `codex` and `cursor-agent` wrappers start the real binary as a child), the launcher dies and the real CLI
// keeps running. A stopped workflow step kept driving the browser for 2.5 minutes that way (Upwork, 2026-10-05).
// Requires SysProcAttr.Setpgid, so the group id is the started process's pid.
func KillGroupOnCancel(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killGroup(cmd.Process.Pid)
	}
}
