[← goals / evaluation](index.md)

# PLAT-075 — auto-evaluation starts before its target execution is finalized

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | P1 — evaluators are forced to grade an execution whose |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — runtime reverify pending |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — evaluators are forced to grade an execution whose
  authoritative interval is still open
- **Owner:** batch execution / auto-evaluation boundary
- **Finding:** LinkedIn `9495aef3dab65c42`

## Evidence and root cause

The full workflow-local finding says evaluators observed `run_metadata.json`
with `status=running` and no completion time. The retained run later records
`started_at=2026-08-06T17:01:54Z` and
`completed_at=2026-08-06T17:27:52Z`; evaluation output explicitly says it had
to substitute terminal timing because the metadata was unfinished.

This was deterministic ordering, not a scheduler race. In
`controller_batch_execution.go`, a successful group called
`MaybeRunAutoEvaluation` and only called `finalizeRunMetadata` after evaluation
and its token persistence completed. Every auto-evaluator therefore saw the
target execution as running by construction.

## Fix

Finalize the target execution immediately after its execution steps and their
own persistence complete, before starting the separate evaluation run.
Post-evaluation persistence errors remain evaluation diagnostics and no longer
rewrite the already-completed target execution's status.

`TestSuccessfulTargetRunIsFinalizedBeforeAutoEvaluation` pins this ordering.

## Verification

- Focused regression tests: pass.
- `go build ./...`: pass.
- Required broad suite: exactly 22 known baseline failures; no additional
  failures.
- Runtime reverify: pending server restart and one evaluation-producing run.

Ready to close after runtime verification: `9495aef3dab65c42`.

