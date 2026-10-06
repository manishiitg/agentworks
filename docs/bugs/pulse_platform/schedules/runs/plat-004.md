[← platform / scheduler-runs](index.md)

# PLAT-004 — scheduler success can precede actual workflow completion

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | schedules |
| Area | runs |
| Summary | scheduler/background execution completion barrier |

| Coordination | Value |
|---|---|
| Assigned agent | `Unassigned` |
| Ticket state | `runtime_verified` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P0
- **Owner:** scheduler/background execution completion barrier
- **Legacy source:** RTS Latency `run_concerns`, `bug_review`,
  `external_action_required`, seen twice
- **Linked Social Media findings:** `HARNESS-PULSE-RUN-STATUS-MISMATCH` and
  `HARNESS-SCHEDULER-CHILD-STATUS`. These are two observations of the same
  scheduler/workflow terminal-state reconciliation boundary, not two repair
  projects. They were emitted by the pre-rebuild runtime.
- **Problem:** `schedule-runs.json` recorded a dev run as success after 84.6
  seconds even though the pipeline ended at `step-daily-latency-report` and
  later security, cost, digest, and checkpoint work never completed.
- **Impact:** Pulse reviews incomplete evidence, finalization may start early,
  and missed producing steps are reported as a healthy run.
- **Resolution:** fixed by commit `f69de7b6c` ("Stop the reconciler calling an
  in-flight run successful"). The reconciler no longer treats a temporarily
  `completed` workshop turn as completion of the multi-turn schedule. Only the
  scheduler's own completed turn loop can record success; an abandoned run is
  eventually recorded as interrupted/error. The normal scheduler path also
  waits on the consolidated runtime state, which includes foreground work,
  tracked child executions, background agents, and tmux activity.
- **Verification:** scheduler reconciliation and workshop-idle regression tests
  pass on current main. RTS Latency needs one uninterrupted post-fix scheduled
  run to close its historical finding.
- **Acceptance:** the scheduler cannot emit terminal success until every
  required child has an authoritative terminal completion; a lost/truncated
  child yields failed or timed-out status with its identity and last evidence.

