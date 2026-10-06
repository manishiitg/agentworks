[← goals / pulse / findings](index.md)

# PLAT-084 — scheduled runs using `execute_step` directly had no Pulse evidence signal, so Gate/Review+Fix/Fixer/dashboard/publish were silently skipped every time

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | goals |
| Area | pulse/findings |
| Summary | P0 — Pulse's entire review/fix/publish pipeline was silently |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — main fix + the SetMetadata follow-up both fixed and tested; runtime reverify pending |
| Last synchronized | `2026-08-11` |

- **Priority:** P0 — Pulse's entire review/fix/publish pipeline was silently
  disabled for a whole class of scheduled runs, not a cosmetic or edge-case
  defect. The runs completed real work; the operator would have had no way to
  know Pulse never reviewed any of it.
- **Owner:** scheduler Pulse evidence detection (`SchedulerService.executeWorkshopJob`,
  `scheduledWorkflowStepProducedEvidence`) and the `execute_step` tool
  registration (`registerInteractiveWorkshopTools`)
- **Found on:** live overnight log audit, 2026-08-11 — every scheduled run
  that invoked `execute_step` directly (rather than `run_full_workflow`) hit
  this path that same night: rtslatency's "Daily Ops" schedule (11 real
  workshop turns over ~2 hours, `session=schedule-cron--42eca39a_...`),
  build-in-public's "Daily X draft" (`session=schedule-cron--c2e7578f_...`),
  and build-in-public's "Publish approved packages". All three genuinely ran
  real steps; all three were logged as `[PULSE] workflow did not start in
  this invocation; skipping Gate, reviewers, Fixer, dashboard and publish`
  immediately after their workshop turns completed successfully.

## Root cause

`sctx.ProducedRunEvidence` gates whether Pulse gets the full flow
(`reviewEvidenceAvailable := sctx.ProducedRunEvidence || sctx.PulseOnly`,
`scheduler.go:2280`). The primary evidence check, `workshopRunProducedEvidence`
(a run-folder-name set-difference + timestamp check), correctly returns false
when `execute_step` reuses an existing `iteration-0` folder rather than
creating a new one — expected for a schedule that calls `execute_step`
against specific steps rather than `run_full_workflow`. That's fine *if*
there's a fallback for this exact shape — and there wasn't one:

1. **`scheduledWorkflowStepProducedEvidence` did not exist at all.** No
   fallback checked whether the schedule's own session had launched any
   `execute_step` executions — the run-folder check was the only signal, and
   it structurally cannot see `execute_step`-driven work.
2. **`execute_step`'s own background-agent registration never declared what
   kind of execution it was.** `registerWorkshopExecutionBeforeLaunch`
   (`interactive_workshop_manager.go:~3170`) registered the execution with no
   `Kind` and no `Metadata["execution_type"]` — even a Pulse-side fallback
   keyed on execution kind would have had nothing to match against.

Both gaps had to close together — a detection function with nothing to
detect, and a declaration with nothing consuming it, are each individually
useless.

## Fix

- `interactive_workshop_manager.go` — `execute_step`'s background-agent
  registration now sets `Kind: string(orchestrator_events.ExecutionKindWorkflowStep)`
  ("workflow_step") and `Metadata: {"execution_type": "workflow-step"}`,
  with a comment stating the reason plainly: *"This is a real workflow step,
  even when a schedule invokes it directly instead of through
  run_full_workflow. The scheduler uses the declared kind as invocation
  evidence for Pulse."*
- `scheduler.go` — new `scheduledWorkflowStepProducedEvidence(sessionID,
  since)` checks the schedule's own `bgAgentRegistry` entries for any
  execution created after the invocation started with `Kind == "workflow_step"`
  or `Metadata["execution_type"] == "workflow-step"`. Wired in as a fallback
  at both evidence-check sites: the normal end-of-invocation check
  (`sctx.ProducedRunEvidence = workshopRunProducedEvidence(...)`, now falls
  through to this when false) and the idle-wait-timeout path (mirroring
  PLAT-071's existing `workshopRunStartedDuringInvocation` fallback).

Verified end-to-end by tracing the actual call chain (not just reading the
fix in isolation): `execute_step`'s handler → `registerWorkshopExecutionBeforeLaunch`
→ `OnExecutionStart` (`delegation.go`) → `bgAgentRegistry.Register`, using the
exact same `sessionID` the scheduler later queries with (`s.newScheduleSessionID`
is minted once, before the turn loop, and threaded through unchanged to
`startSessionInternal`, `installWorkflowPhaseTools`, and the evidence check).
No sessionID mismatch, no premature reaping of registry entries (confirmed:
`BackgroundAgentRegistry.Cleanup` is only ever called from the explicit
`/stop-session` handler, not mid-run).

## A related, independent defect — found and fixed (2026-08-11)

While tracing this, found that `OnExecutionComplete` (`delegation.go:~907`)
calls `agent.SetMetadata(meta)`, and `BackgroundAgent.SetMetadata`
(`background_agents.go:~326`) **replaced** `Metadata` wholesale rather than
merging. The completion-time `meta` (`{"iteration", "group_name", "lock_code",
"workshop_mode"}`) does not include `execution_type`, so every background
execution's registration-time metadata (`execution_type`, `workflow_path`,
`preset_query_id`, `execution_source`, `suppress_auto_notification`) was
silently wiped the moment it completed. This did not cause the bug above —
`scheduledWorkflowStepProducedEvidence` also checks `Kind`, which is set once
at `Register` and never mutated by `SetMetadata` — but it's a real defect on
its own with a genuine behavioral consequence: `OnExecutionComplete` itself
reads `agent.GetSnapshot().Metadata["suppress_auto_notification"]` (line 924)
*after* the overwrite, so a caller that suppressed auto-notification at
registration time would have that suppression silently undone by its own
completion, firing an unwanted notification.

**Fixed**: `SetMetadata` now merges the given keys into the existing map
instead of replacing it. A key explicitly present in the completion-time
`meta` still overwrites (this is a merge, not a "first write wins" union) —
only keys absent from `meta` are preserved. Tests in
`plat084_background_agent_metadata_merge_test.go`: registration-time keys
survive a completion-time call that doesn't mention them; an explicitly
passed key still overwrites; a nil initial `Metadata` map is handled.

## Verification

- `go build ./...` clean.
- New test `TestScheduledWorkflowStepProducedEvidenceUsesLinkedStepExecutions`
  (`scheduler_test.go`) — a linked workflow-step execution counts as
  evidence; unrelated background work doesn't erase that evidence; generic
  background work alone does not manufacture evidence.
- Full baseline (`go test ./cmd/server/... ./pkg/orchestrator/...`) still
  shows exactly 22 pre-existing failures — no new failures.
- **Not yet reverified live** — requires a restart and a scheduled run that
  uses `execute_step` directly (rtslatency's Daily Ops or build-in-public's
  X-draft/Publish schedules are the known reproducers) to confirm Pulse now
  runs its full Gate/Review+Fix/Fixer/dashboard/publish flow instead of
  taking the no-run Finalizer path.

## Acceptance

- A scheduled run that invokes `execute_step` (not `run_full_workflow`)
  against real steps is recognized as having produced Pulse-reviewable
  evidence.
- Generic/unrelated background agent activity in the same session does not
  by itself satisfy this check.
- `BackgroundAgent.SetMetadata`'s replace-not-merge behavior remains open as
  a separate, smaller defect.
