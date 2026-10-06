[← goals / pulse / lifecycle](index.md)

# PLAT-094 — a stale busy signal aborted Pulse Finalize on a turn that had already finished

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse/lifecycle |
| Summary | P1 — silent loss of Finalize (backup/publish/notify) with no |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — fixed and tested; runtime reverify pending |
| Last synchronized | `2026-08-12` |

- **Priority:** P1 — silent loss of Finalize (backup/publish/notify) with no
  operator-visible signal beyond one diagnostic log line; the scheduler's own
  top-level log still reports the run `✅ completed`.
- **Owner:** Pulse step-boundary busy check (`abortIfTurnStillBusy`,
  `scheduler.go`)
- **Found on:** live, build-in-public, 2026-08-12,
  `schedule-cron--c2e7578f_1786500054325933000`

## Evidence

`review-fix-continuation` finished normally. The scheduler then tried to start
Finalize, and the attempt to start itself failed:

```
08:08:35  [PULSE] abortIfTurnStillBusy diagnostic: step=review-fix-continuation
          outcome=start_failed err=session failed sessionBusy=true durable...
08:08:35  [PULSE] Pulse stopped after review-fix-continuation failed while its
          agent turn was still live; refusing to overlap another message
08:08:35  [SCHEDULER] ✅ c2e7578f-... completed
```

But in the exact same second:

```
08:08:35  [COMPLETION] Updating session schedule-cron--c2e7578f_... status to completed
08:08:35  [ACTIVE_SESSION] Updated session schedule-cron--c2e7578f_... status to: completed
```

The turn had genuinely finished. `abortIfTurnStillBusy` aborted anyway because
`sessionBusy=true` — the runtime snapshot phase — disagreed with reality.
Finalize never ran: no backup, no publish, no notify for that pass, and
nothing above the `[PULSE]` log line shows the loss; the scheduler's own
completion log reports success.

## Root cause

This is the exact race **PLAT-071 already diagnosed and fixed** — but only at
one of its two call sites.

`abortIfTurnStillBusy` (`scheduler.go:2250`, the single closure used at every
Pulse step boundary: Gate, Review+Fix, the review-fix continuation, and
Finalize) checked only `s.api.sessionIsBusy(sessionID)` — the runtime snapshot
phase. PLAT-071 established that this signal can lag a turn's own completion by
minutes, and fixed it for the workshop idle-wait
(`waitForWorkshopIdleWithInactivityTimeout`, `scheduler.go:4123`) by
reconciling against `s.api.isSessionBusy(sessionID)`, the explicit per-turn
flag set when a turn starts and cleared when it ends — which "carries positive
evidence" while the snapshot does not. That reconciliation was never applied to
`abortIfTurnStillBusy`, so the same disagreement at the Pulse step boundary had
no correction and aborted Finalize outright.

## Fix

New pure function `reconcilePulseStepSessionBusy(snapshotBusy, explicitBusy bool) bool`,
called from `abortIfTurnStillBusy` before the busy value reaches
`pulseStepFailureMustStopBeforeNextTurn`. Deliberately asymmetric, mirroring
PLAT-071's own precedent exactly: the explicit flag only ever corrects a
snapshot *claiming* busy; an idle snapshot is trusted as-is and never escalated
to busy from the explicit flag alone. Escalating in that direction would trade
a known race (stale busy) for a new, unproven one (spurious aborts on a
session the snapshot never flagged) — matching neither this ticket's evidence
nor PLAT-071's.

Downstream is unaffected in shape, only in outcome: when the abort no longer
fires, the step's real result (here, `start_failed`) still flows through the
existing `handleStepFailure` → `recoveryNotes` path, producing `pulse finalized
partially … after N failed step(s)` — a diagnosable partial result — instead
of either silently completing or silently aborting.

## Verification

- `go build ./...` clean.
- `TestReconcilePulseStepSessionBusy`: both-agree cases pass through unchanged;
  a stale busy snapshot with an idle explicit flag reconciles to idle (the
  exact race observed); an idle snapshot is trusted even when the explicit
  flag disagrees, pinning the deliberate asymmetry.
- `TestAbortIfTurnStillBusyReclassificationChangesTheOutcome`: proves the fix
  changes the actual gating decision, not just the helper's return value —
  `pulseStepFailureMustStopBeforeNextTurn` flips from must-stop to
  must-continue once reconciliation is applied to the captured live shape.
- Full suite: 24 failures, all accounted for (22 known baseline + 2 from
  another session's in-flight work), zero unexplained.
- **Not yet reverified live** — needs a restart, then a scheduled run whose
  Pulse pass hits this exact disagreement; expect Finalize to complete instead
  of aborting, or a `pulse finalized partially` note if the underlying
  `start_failed` cause (currently unidentified — see below) recurs.

## Not fixed here

**Why the turn's own start attempt returned `err=session failed` at all** is
still unknown. This fix stops that failure from being *misclassified and
silently swallowed* as a stale-busy abort; it does not explain the failure
itself. If `pulse finalized partially` starts appearing after this ships, that
is the next thing to trace — with the real reason now visible instead of
hidden behind the busy check.

## Acceptance

- A Pulse step boundary does not abort on a snapshot-busy signal once the
  explicit per-turn flag confirms the turn has finished.
- A genuine failure to start the next step is still recorded as a partial
  result with its real cause, not silently dropped.
- An idle snapshot is never escalated to busy by this reconciliation.
