[← goals / pulse / lifecycle](index.md)

# PLAT-260 — Learning-write yield needs a dependable Pulse lifecycle owner

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | pulse/lifecycle |
| Summary | Pulse post-run governance → Pulse Fixer |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `superseded by PLAT-263` |
| Last synchronized | `2026-08-31` |

- **Priority:** P2
- **Owner:** Pulse post-run governance → Pulse Fixer
- **Related:** PLAT-055 (merged reflection), PLAT-059 (reasoned learnings locks), PLAT-258 (plan-drift review)

## Problem

Reflections can write reusable execution learnings. Pulse needs to decide when
per-step write access remains useful, but the original proposal modeled that
decision through a second lock layered over `learnings_access`.

Runtime never changes it. The user is not expected to discover or operate an
internal configuration flag. The Workshop Builder and Pulse Fixer *can* set it, but
no Pulse module is responsible for deciding when to lock, retain, or reopen
it. Consequently a stable workflow keeps paying for low-yield learning
writes indefinitely, while an existing lock can remain stale after behavior
changes.

The former workflow-wide `lock_knowledgebase` described by the original ticket
was retired by PLAT-265. KB ownership is now deliberately step-based:
`knowledgebase_access` plus `knowledgebase_contribution` decides which step may
write, and a mature/no-op contributor becomes `read` without freezing unrelated
writers.

PLAT-059 deliberately made a learnings lock a considered, reasoned action; it
did not supply the evidence-gathering process that should initiate that action.
PLAT-258 reviews whether locks are appropriate during plan-drift work, but its
current policy is recommendation-only and is not a reflection-lock lifecycle.

## Superseding decision (PLAT-263)

The second lock is removed. Pulse may inspect contribution yield and change a
mature/no-op contributor from `learnings_access="read-write"` to `"read"`.
Material drift may justify restoring `"read-write"` with a concrete updated
`learning_objective`. This keeps one permission model and preserves the useful
governance goal without a lock lifecycle.

The remainder below is retained as historical design context and is no longer
the active acceptance contract.

## Original required outcome

Pulse owns the lock lifecycle. It must evaluate reflection yield from durable
evidence, apply safe freezes through the existing configuration tools, and
reopen freezes when their evidence becomes stale. This is governance over
reflection writes, not a new third `lock_reflections` flag.

### Learnings — per-step lifecycle

For each step with `learnings_access="read-write"` and a learning objective,
Pulse must inspect its current description hash, recent successful runs, and
`.learning_metadata.json`. It may lock only when the current behavior is stable
and recent reflection output is repetitive, empty, or otherwise no longer
improving the shared skill. It records this evidence through
`update_step_config(lock_learnings=true, lock_learnings_reason=...)`.

Pulse must reopen a learnings lock when a material description,
learning-objective, access-mode, or ownership change makes the evidence stale.
It must never lock before the shared skill has bootstrapped, and must prefer
`learnings_access="read"` when a step simply has no reusable HOW to contribute.

### KB — per-step ownership review

Pulse may review whether an individual KB contributor remains useful. When its
writes are demonstrably low-yield, it recommends or applies
`knowledgebase_access="read"` and clears the contribution contract. It must not
disable unrelated contributors workflow-wide.

## Design constraints

- Retain the per-step learning flag; do not introduce `lock_reflections` or a
  workflow-wide KB lock.
- Every action records reviewed runs/metadata/manifest, expected cost benefit,
  and risk of freezing new knowledge.
- An inconclusive review leaves state unchanged and records a decision-required
  finding rather than inventing a rationale.
- A learning lock does not silence that step's independently configured KB
  contribution.
- The lifecycle must work for unattended/scheduled Pulse, not only an
  interactive Builder turn.
- Plan-drift review may supply evidence, but stable unchanged workflows also
  need periodic reflection-yield review.

## Acceptance criteria

1. Manual and scheduled Pulse can evidence-lock eligible learnings per step.
2. Pulse can make one mature/no-op KB contributor read-only without affecting
   other contributors.
3. Material changes make a locked learning or read-only KB ownership decision
   due for reassessment; Pulse can reopen it with an audit record.
4. Tests cover bootstrap protection, reason persistence, inconclusive no-op,
   stale-lock reopening, cross-store independence, and scheduled reachability.
