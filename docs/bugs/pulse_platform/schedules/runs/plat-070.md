[← platform / scheduler-runs](index.md)

# PLAT-070 — a failed run-folder listing makes the scheduler blame an old failure on today's run

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | runs |
| Summary | P1 — a healthy production security run was recorded as failed |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — cause isolated from a live occurrence; fix and regression test shipped |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — a healthy production security run was recorded as failed
- **Owner:** scheduler run-outcome reconciliation (`scheduler.go`), workspace state loading (`workspace_state.go`)
- **Found on:** hetznerssh, 2026-08-10, session `schedule-cron--b2234610_1786352453203966000`

## What happened

The 14:30 IST hetznerssh security audit **succeeded** and was recorded as an **error**.

```
today's run:  runs/iteration-0/production-server    status: completed   09:01:33Z → 09:05:21Z
blamed run:   runs/iteration-25/production-server   status: failed      completed 2026-08-09
```

All eight steps ran (`authenticated-known-baseline-audit` through `post-slack-security-summary`). The scheduler then reported:

> workflow run **iteration-25**/production-server failed (its run_metadata.json records status "failed"), even though the orchestrating workshop session completed its turns without an infrastructure error

`iteration-25` is a **day-old** archive (dir mtime `08-09 20:10`). `iteration-0` is the live folder and is what this run used. The message even contains the contradiction — the session completed normally — and resolves it the wrong way, trusting a stale folder over its own clean result.

## Root cause

Two defects compose.

**1. The listing converts errors into empty data.** `loadRunFoldersInternal` swallowed the error entirely:

```go
folders, err := api.getRunFoldersFromWorkspace(ctx, workspacePath)
if err != nil {
    return []RunFolderInfo{}, nil   // error dropped: empty slice, nil error
}
```

Callers cannot distinguish *"this workflow has no run folders"* from *"the listing failed"*. Both existing HTTP callers already had `if err != nil` branches that could never fire.

**2. The reconciler infers newness from absence.** `reconcileWorkshopRunOutcome` reports any folder **absent from the pre-run set** that carries `status: "failed"`:

```go
preRunFolders, _ := s.api.loadRunFoldersInternal(...)   // also discards the error
preRunFolderNames := runFolderNameSet(preRunFolders)     // → empty set on failure
```

With an empty baseline **every** pre-existing folder looks newly created by this invocation, so the first historical `failed` run is attributed to it.

**Why it fired here:** the workspace API was demonstrably unhealthy at snapshot time — the log shows four `[WORKSPACE] Warning: Could not create ... folder (status 409)` errors at `14:30:53`, seconds before the snapshot, following the 12:26 restart. That is the general risk: **the listing is most likely to fail right after a restart, which is exactly when scheduled runs resume.**

`reconcileWorkshopRunOutcome` is not itself wrong — it exists to close BUG-20260729-10 (a run that fully failed while its session looked healthy) and its behaviour is correct *given a real baseline*. The defect is feeding it a guess.

## Fix shipped

1. **`loadRunFoldersInternal` propagates its error** instead of returning empty+nil. This also repairs the two HTTP callers, whose error handling was previously unreachable.
2. **The scheduler skips reconciliation when either snapshot is unavailable**, logging the reason rather than reconciling against a baseline it does not have. Failing open is correct here: the check exists to catch a silently-failed run, and inventing a failure from a missing snapshot is a worse error than missing one. The run's own `run_metadata.json` remains the durable record and the next invocation reconciles normally once the listing recovers.

**Regression test:** `TestReconcileWorkshopRunOutcomeMisattributesWhenBaselineIsLost` pins the hazard rather than a desired behaviour — it asserts that an empty baseline *does* misattribute, which is precisely why the caller must never pass one. It documents its own obsolescence: if the primitive later learns to reject an empty baseline, delete the test and keep the caller guard.

## Deliberately not changed

`workshopRunProducedEvidence` also consumes the pre-run set. A lost baseline makes it read every folder as new and answer "evidence produced" — the same answer its metadata-based fallback gives, so the outcome is unchanged today. Left alone rather than widened on speculation; noted here because a future change to that fallback would expose it.

## Acceptance

- A scheduled run whose workspace listing fails no longer reports a historical failure as its own; the skip is visible in the session log.
- A genuinely new failed run is still detected (existing `TestReconcileWorkshopRunOutcomeDetectsNewFailedRun` unchanged).
- **Runtime reverify pending:** requires a scheduled run during a failing listing. hetznerssh's `2026-08-10` error status was raised against a healthy run and should be read as spurious, not as a security finding.
