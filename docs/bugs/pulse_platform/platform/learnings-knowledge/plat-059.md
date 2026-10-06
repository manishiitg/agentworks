[← platform / learnings-knowledge](index.md)

# PLAT-059 — a learnings lock could be set with no stated reason

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | platform |
| Area | learnings-knowledge |
| Summary | `update_step_config` lock path |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `superseded by PLAT-263` |
| Last synchronized | `2026-08-09` |

- **Priority:** P2
- **Owner:** `update_step_config` lock path
- **Follows:** PLAT-058, which made the cost of a lock materially higher.

> Historical note: this ticket documents the former learning-lock contract.
> PLAT-263 removed that redundant control. `learnings_access="read"` is now the
> sole way to consume shared guidance without contributing, and legacy lock
> fields are migrated automatically.

## Problem

`lock_learnings=true` was settable with nothing recorded about why. The existing
convention — *"include `review_notes` explaining why learning should stop"* —
was advisory, and was therefore skipped: **LinkedIn reached 6 of 6 steps locked
with no recorded justification for any of them**, and nobody could tell whether
that was deliberate or accumulated drift.

PLAT-058 raised the stakes. Under per-step file ownership a lock froze that
step's own file — contained and easy to reason about. Now that the skill is one
shared topic-organised artifact, **a locked step reads every other step's
contributions and can never give anything back**. That is a free rider, not
isolation, and it is a standing cost someone has to be able to re-judge later.

## Implementation (2026-08-09)

- New `lock_learnings_reason` on `AgentConfigs`, with a `validateLockLearningsChange`
  validator (`controller_learning_helpers.go`) called from the
  `update_step_config` handler.
- **Rejected, not defaulted**: only the caller knows the evidence, and a
  synthesized reason would defeat the purpose.
- **Unlocking never requires a reason.** It restores the default (contribute) and
  costs nothing; gating it would leave steps frozen because nobody could phrase
  a release note.
- **Clearing the lock clears the reason** — a justification must not outlive the
  freeze it justified, or it reads as pre-approval for a future re-lock nobody
  reviewed.
- The rejection points at the cheaper alternative: `learnings_access="read"` is
  the ordinary way to say "consumes but does not contribute", and it needs no
  reason. Reserve the lock for a step that *does* have something to contribute
  and is deliberately frozen anyway.
- The UI lock toggle was removed (`LearningsView.tsx`). Locking is a considered
  decision, not a click; state stays visible, and it is set through chat or
  Pulse — both of which carry the reason.

## Who can lock

Three actors, one field, no auto-lock anywhere: **you** (HTTP/UI path), the
**Workshop/builder agent**, and the **Pulse Fixer** (full workshop tool surface
since PLAT-053). The runtime never sets it. Note the `auto_locked_at` /
`auto_lock_reason` / `auto_lock_iteration` keys in `.learning_metadata.json` are
residue from a retired system — the only code touching them *clears* them.

Unlocking is deliberately harder than locking: passing `lock_learnings:false` is
ignored when the step is already locked, so an agent editing other fields cannot
silently unlock a step as a side effect.

## Regression tests

`lock_learnings_reason_test.go` — locking without a reason is rejected and the
message names the field, the cost, and the `learnings_access="read"` alternative;
whitespace is not a justification; unlocking never requires one; clearing the
lock clears the reason; the reason survives `MergeAgentConfigFields`.

## Related

The 1.0.22 `upgrade-learnings-lock-audit` preflight reports (never clears) any
locked step whose justification is missing, via `record_pulse_finding` with
`recommended_route="decision_required"`. PLAT-060 extends the same
required-reason pattern to the Ops-owned tier/model/mode decisions.
