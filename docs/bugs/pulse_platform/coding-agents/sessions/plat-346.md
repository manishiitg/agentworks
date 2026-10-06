# PLAT-346 — Active retained chat was killed by the provider idle timer and reported as a tmux crash

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | records the Crew session whose active turn was canceled at 11:45 UTC and reported as a tmux pane disappearance. |

**Category:** Coding agent bridge  
**Severity:** P0  
**Status:** Fixed; pending RTS deployment  
**Observed:** 2026-09-21 on RTS

## Incident

The Crew session `work:project:de2da0f6-0b2a-52f9-9ce9-d54d7659601e`
started its Claude tmux process at 08:45:26 UTC. At 11:45 UTC an active turn
was executing a shell tool. The tool request was canceled and the retained tmux
session was removed. At 11:46:42 the server displayed:

> The response could not be completed — tmux pane disappeared unexpectedly

The host had healthy memory and no OOM evidence. This was not caused by the
structured chat event journal.

## Root cause

Persistent Claude tmux sessions have a three-hour idle timer. Ordinary provider
turns re-arm it when they release the provider session, but retained chat turns
are delivered directly with `SendClaudeCodeInput`. That path did not refresh
the timer. A session could therefore remain visibly active while its timer still
counted from the original launch/last ordinary provider turn and expired.

A second race obscured the primary error. Expected adapter cleanup removed tmux
while the logical turn was still unwinding. The 30-second watchdog treated the
first missing-pane observation as a new crash and emitted a second completion,
replacing the useful provider/tool failure with `tmux pane disappeared
unexpectedly`.

## Fix

1. Retained Claude input now re-arms the persistent-session idle deadline.
2. Idle callbacks carry a generation. A callback queued before newer activity
   cannot expire the refreshed session.
3. A session already being expired refuses new direct input so the caller can
   resume it cleanly instead of typing into a closing pane.
4. The watchdog confirms a missing pane across two polls. This gives normal
   provider cleanup time to settle its real completion/error.
5. Terminal-owner reconciliation treats an existing runtime terminal boundary
   as authoritative and cannot overwrite it with a secondary tmux observation.

Dead panes remain immediate failures and still preserve their captured tail;
only a completely missing pane receives the cleanup-race confirmation window.

## Verification

- Unit coverage proves retained input invalidates the prior idle callback.
- Watchdog coverage proves the first missing observation is non-destructive and
  a persistently missing pane still fails after confirmation.
- Lifecycle coverage proves a settled provider/tool failure survives later tmux
  cleanup reconciliation.

## Lifecycle simplification (completed)

Persistent-session activity/expiry now uses one shared, zero-value-safe lease
primitive for Claude, Codex, Cursor, and Pi. It owns exactly four operations:

- `Arm`: accepted activity replaces the deadline;
- `Pause`: an ordinary provider turn temporarily owns the session;
- `Stop`: teardown permanently rejects later activity;
- expiry: one generation-checked callback invokes provider cleanup.

Every accepted owner input and provider-turn release now touches this same
lease. The four adapters no longer maintain private timer/generation/last-used
state, and Pi's previously unsynchronized timer mutation is gone. Tmux remains
the durable interactive process host and diagnostic surface; the lease only
owns retention and never rewrites a completed turn outcome.

Typed cleanup reasons (`idle_expiry`, `explicit_close`, `provider_exit`,
`session_loss`) remain a useful observability follow-up, but they are no longer
required to make session retention correct.

## Register notes

[PLAT-346](plat-346.md) records the Crew
session whose active turn was canceled at 11:45 UTC and reported as a tmux
pane disappearance. Root cause: the 3-hour idle timer was never re-armed on
the retained-chat `SendClaudeCodeInput` path. Fixed by moving retention to a
shared lease (adapters keep no private timer state); pending RTS deployment.
Typed cleanup reasons remain an observability follow-up.
