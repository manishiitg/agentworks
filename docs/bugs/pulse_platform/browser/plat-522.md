# PLAT-522 — Browser cleanup trusts persisted process IDs before signaling

State: open. Date: 2026-10-05. Priority: P2. Source: owner-supplied browser review.

## Verified review

`killChromePID` in `executor.go` sends SIGKILL to `-chromePID` and falls back
to signaling the PID. The stored `.chrome-pid` path only checks `> 0`, so PID 1
passes and becomes the special process selector `-1`; there is no executable,
start-time or session/profile identity check for a recycled PID.

The surrounding force-cleanup also reads a daemon PID without a positive bound,
enumerates its children and signals it. `resetCDPSessionRuntime` checks `> 0`
but does not verify daemon identity. Stale or corrupt runtime files can therefore
cause unrelated process signaling, even beyond the stored Chrome group case.
Graceful close runs first but does not verify these subsequent force-kill targets.

Files are server-side; this review did not establish an untrusted write path to
them. No dangerous PID or group kill was executed. Findings are source-verified,
not a live exploit reproduction.

## Remaining

Reject special/system/self PIDs before every signaling or child-enumeration
path. Capture process identity while the daemon/Chrome is known to belong to the
session; before cleanup verify executable, session/profile and process start
identity using platform-appropriate facilities. Use identity-safe signaling where
available and fail closed when identity cannot be established. Do not treat a
command-line substring check alone as protection against recycled PIDs.
Verify stale PID files cannot kill an unrelated real subprocess, and confirm
legitimate managed-browser teardown still removes its own runtime/profile locks.

No runtime fix or deployment is claimed by this ticket.
