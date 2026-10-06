[← goals / evaluation](index.md)

# PLAT-333 — Eval subsystem removal with producer-owned measurement

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | goals |
| Area | evaluation |
| Summary | records the eval-subsystem removal and the final-review rejection of the mandatory 1.0.43 measurement migration (unneeded imposed topology, uncreatable router, nonexistent tool, unimplemented metrics table). |

| Coordination | Value |
|---|---|
| Ticket state | `implemented; deployment pending` |
| Last synchronized | `2026-09-21` |
| Priority | `P0 direction` |

## Background

The file/run-based workflow evaluation subsystem is being deleted per
[eval_removal_plan.md](../../../../workflow/eval_removal_plan.md). A mandatory
replacement migration (contract 1.0.43: every workflow gains a
`measurement-router` → `measure-outcomes` route plus a `workflow_metrics`
table) was implemented, then rejected in final review before shipping.

## Review findings

- **[P0]** The migration imposed unnecessary topology: many steps already
  calculate and store the outcomes Pulse needs.
- **[P0]** The proposed router cannot be created: one routing step per
  workflow, and routes may not point directly at `end`.
- **[P0]** The migration used the nonexistent `add_regular_step` tool (the
  real `add_scripted_step` does not create the required `main.py`).
- **[P1]** An appended router does not connect every execution path
  (routes, branches, explicit `next_step_id: "end"` edges bypass it).
- **[P1]** `workflow_metrics` was documentation only: no runtime code reads
  or writes it. Pulse and Goal Advisor consume `workflow_goal_metrics` /
  `pulse_goal_observations` through `get_goal_metrics`.
- **[P1]** Table-scoped DB authorization does not exist (steps get
  database-wide read-write), and `RUN_FOLDER` reuses `iteration-0`, so the
  promised permissions and immutable measurement identity were unachievable.
- **[P2]** The migration test only checked prompt strings, so the suite
  stayed green despite the migration being unusable.

## Decision

No mandatory route, step, or table. Any normal step or scheduled collector
may produce a run-scoped, evidence-backed measurement; Pulse does not depend
on which step or route produced it. Per legacy eval: reuse a producer's DB
output, add `record_goal_observations` only for Pulse history, localize to
routes, converge or dedicate a step only when genuinely required, otherwise
change nothing, and flag ambiguity for manual migration. Old evaluation
artifacts stay read-only history.

## Implemented

Commit `f13173f80` is pushed to `main`.

- Deleted the 1.0.43 migration and its prompt; the upgrade chain ends at
  1.0.41, with 1.0.42 retained as the current historical marker. Versions
  1.0.41, 1.0.42, and an already-stamped 1.0.43 are execution-compatible, so
  no workflow is blocked merely to advance through either retired checkpoint.
  Added `TestNoUpgradeMandatesMeasurementTopology`, a full-chain guard against
  the rejected topology or evaluation-retirement turn returning.
- Rewrote `measurement-plan.md`, the Goal Advisor/optimizer prompts,
  eight guidance templates, and the `MetricSource` schema to the flexible
  contract (producer outputs + goal observations).
- Scrubbed live eval path/endpoint references from active docs and schemas
  (e.g. `workflow_monitoring.md` claimed the removed
  `/api/workflow/evaluation-reports` endpoint was current).
- Deleted the orphaned `PulseEvalSummary` bundles as part of the evaluation
  subsystem removal.

## Verification

- Focused workflow-contract, schedule, manual-run, and webhook regression
  tests cover the retired-version compatibility window.
- The full-chain guard rejects any return of the 1.0.43 turn or the rejected
  mandatory measurement topology.
- `go test ./cmd/server -run 'Test.*(Upgrade|Contract|Webhook)' -count=1`
  passes, along with the focused schedule/manual/webhook suite, pre-commit
  lint, the Go build, workspace build, and Electron build.

## Register notes

[PLAT-333](plat-333.md) records the eval-subsystem removal and the final-review rejection of the mandatory 1.0.43 measurement migration (unneeded imposed topology, uncreatable router, nonexistent tool, unimplemented metrics table). No replacement migration ships: 1.0.41–1.0.43 are execution-compatible historical markers, measurement comes from producing steps' own stored outputs plus `record_goal_observations` for Pulse history, and old evaluation artifacts stay read-only. Implemented in `f13173f80` and pushed to `main`; deployment pending. See the [removal plan](../workflow/eval_removal_plan.md).
