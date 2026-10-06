[← schedules / history](index.md)

# PLAT-194 — `reconcileWorkshopRunOutcome` never inspected a reused run-folder's metadata, so a schedule could report success while the run it triggered actually failed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | schedules |
| Area | history |
| Summary | P2 — a status field lying about what actually happened, on |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-28` |

- **Priority:** P2 — a status field lying about what actually happened, on
  the surface (`get_schedule_runs`, schedule history) an operator or
  reviewer would check first to gauge whether a workflow is healthy. Same
  class of bug as PLAT-191/192/193 this session, higher blast radius: this
  one can mask a genuine failure from the dashboard itself, not just from
  one tool's return value.
- **Owner:** `agent_go/cmd/server/scheduler.go`
  (`reconcileWorkshopRunOutcome`, `executeWorkshopJob`).
- **Related:** `harness:schedule_run_status:aggregation` (medium), the
  confida-login finding this addresses — *"A schedule run is reported
  [success] when its primary workflow turn failed... The 2026-08-18 full-qa
  run is recorded as success at 69m33s while the QA cycle it existed to run
  actually died 17 minutes in at step 3 of 14."* Also builds directly on
  PLAT-182 (confirmed live: `iteration-0/<group>` run folders are reused
  across cycles, not freshly created each time).

## Investigation — ruling out two plausible causes before finding the real one

1. **First hypothesis: a run genuinely stuck at `status: "running"` forever**
   (e.g. a crash bypassing normal error-return cleanup). Investigated
   `reconcileWorkshopRunOutcome`'s own doc comment, which explicitly and
   deliberately treats `"running"` as ambiguous — *"a transient listing
   hiccup fails open toward 'cannot verify' rather than toward a false
   failure."* Flipping that unilaterally risked reintroducing exactly the
   false-positive problem the original author had already reasoned through
   and avoided. Did not change this.
2. **Second hypothesis: the async `run_full_workflow` dispatch's completion
   wait could itself silently time out.** Traced `startSessionInternal` →
   `waitForConversationTurnTree`: confirmed this is well-engineered — a
   genuine stuck/hung background child does propagate as a real error
   (`diagnoseAndCleanupStalledConversationTurn`), with an explicit grace
   window for live running children (`schedulerWorkshopLiveChildCeiling`).
   Not the gap.
3. **The real cause**, found while tracing the surrounding code: the sibling
   function `workshopRunProducedEvidence`, called moments earlier in the
   same code path for a different purpose, already has a since-based
   fallback for exactly this reason — a folder's name being absent from the
   pre-invocation snapshot is not the only way to detect "this invocation
   touched it"; its `StartedAt`/`CreatedAt` timestamp landing after the
   invocation began works too. `reconcileWorkshopRunOutcome`, right below
   it, lacked that same fallback: it skipped a folder's metadata check
   entirely whenever the folder *name* already existed in the
   pre-invocation snapshot — which, for a workflow whose runs always land in
   the same reused folder name (`iteration-0/confida-staging`, confirmed
   live in PLAT-182), is **every single cycle after the first**. The
   function was structurally incapable of ever catching a failure for this
   class of workflow, independent of what its metadata said.

## Fix

`reconcileWorkshopRunOutcome` gained a `since time.Time` parameter (the
invocation's start time, already captured as `invocationStartedAt`). A
folder now counts as "touched by this invocation" if either its name is
absent from the pre-invocation snapshot (unchanged, original signal), **or**
its `StartedAt`/`CreatedAt` timestamp is not before `since` (new — mirrors
`workshopRunProducedEvidence`'s existing pattern exactly). Only then is its
`Status` checked for `"failed"`. The "ambiguous states never count as
failure" behavior from the original design (investigation step 1, above) is
completely unchanged — this fix is purely about *which* folders get
inspected, not what counts as failure once one is.

## Explicitly not done

- Did not touch the deliberate `"running"`-is-ambiguous design — investigated
  and ruled out as the cause; changing it remains a live option if a future
  incident proves it's also needed, but wasn't required here.
- Did not audit `workshopRunProducedEvidence` itself for a symmetric gap in
  the other direction (its job is "did this invocation produce evidence,"
  not "did this invocation fail" — the two functions' correctness
  requirements are not identical, only the reused-folder-name detection
  logic needed to match).

## Verification

- `go build ./...` clean.
- Full existing `TestReconcileWorkshopRunOutcome*` suite (5 tests, all
  updated for the new signature) still passes unchanged in behavior —
  verified each one's semantics hold regardless of the new `since` parameter
  before changing the call sites, since none of them exercise the
  reused-folder-with-fresh-timestamp path.
- New test `TestReconcileWorkshopRunOutcomeDetectsFailureInAReusedFolderName`
  reproduces the exact confida-login shape (a reused folder name, a fresh
  `StartedAt` within the invocation window, `status: "failed"`) and proves
  it's now caught.
- Full `cmd/server` package suite run; failure count (17) matches the
  pre-existing baseline exactly, confirmed via prior baseline runs this
  session — no regressions.
- Not yet live-verified: no real scheduled run has hit this corrected path
  in production since it shipped.
