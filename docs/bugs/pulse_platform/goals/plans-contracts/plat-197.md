[← goals / plans-contracts](index.md)

# PLAT-197 — plan changes lose their true initiator and can close without reconciling dependent surfaces

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | P1 — a plan mutation can be recorded as reviewed while a |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implementation_in_progress` — origin and dependency closure implemented; revision/effect closure remains |
| Last synchronized | `2026-09-17` |

- **Priority:** P1 — a plan mutation can be recorded as reviewed while a
  downstream step, validation contract, evaluation, report, DB contract, or
  learning still describes the previous behavior.
- **Owner:** managed plan changelog, execution-origin context, Pulse
  plan-change backlog, Artifact Review closure, and Pulse impact linkage.
- **Found on:** Social Media plan-change and Pulse follow-through audit.
- **Related:** [PLAT-033](plat-033.md) and [PLAT-074](plat-074.md) make
  before/after mutation evidence truthful; [PLAT-047](../../schedules/runs/plat-047.md)) owns
  immutable run identity and plan-revision binding; [PLAT-037](../learnings/plat-037.md))
  records the same class of guessed authorship for learnings.

## Defect

The plan changelog records the mutation mechanism, not the true initiator. A
current entry can say:

```json
{
  "actor": "managed_tool:update_todo_task_step",
  "reason": "Require truthful terminal states..."
}
```

That proves which tool wrote the plan and preserves a free-text rationale. It
does not say whether the change originated from user chat, a Pulse Fixer, an
approved human decision, a planner/replan, or a system migration. It also does
not link a Pulse issue, fix attempt, human decision, source message, or later
impact record.

The existing plan-change backlog correctly keeps entries pending until
`artifact_review.done=true`, but that completion stamp is too coarse. It does
not record separate outcomes for:

- downstream step inputs, outputs, routes, and conditions;
- prevalidation and validation schemas;
- evaluation contracts;
- reporting/dashboard queries and projections;
- database reads, writes, schemas, and ownership rules; and
- learnings, knowledge, and operational documentation.

Consequently, one surface can be inspected while another remains stale, and
the entry still looks completely reconciled. Later run output may expose the
damage, but the changelog has no durable chain from mutation to expected effect,
verification runs, and observed outcome.

## Required design

### 1. Trusted change origin

Every successful managed mutation receives a stable `change_id` and structured
origin in addition to the existing `actor`, `reason`, target, changes, and
before/after refs:

```json
{
  "change_id": "change-...",
  "origin": {
    "type": "user_chat|pulse_fixer|human_decision|planner|system_migration|other",
    "session_id": "...",
    "message_id": "...",
    "pulse_run_id": "...",
    "issue_ids": ["PUL-..."],
    "fix_attempt_id": "fix-...",
    "human_input_id": "..."
  },
  "reason": "Why this mutation was required",
  "before_plan_revision": "plan-...",
  "after_plan_revision": "plan-..."
}
```

Only applicable references are populated. Trusted session/Pulse identity comes
from runtime context; an agent argument cannot impersonate another user,
review, decision, or fix attempt. Plan-revision fields consume PLAT-047's
canonical identity rather than inventing another hashing scheme.

### 2. Per-surface dependency obligation

A material plan mutation opens a dependency-review obligation. It remains in
`plan_change_backlog` until Artifact Review records a disposition and evidence
for every applicable surface listed above.

The closed disposition set is:

`updated`, `already_compatible`, `not_applicable`, `blocked`, or `broken`.

This is a review obligation, not an instruction to modify everything. A title
change may mark behavioral surfaces `not_applicable`; a changed output field may
require updates to several consumers. Each result must be explicit and
evidence-backed.

`blocked` or `broken` creates or links the appropriate Pulse/platform lifecycle
item. A bare `artifact_review.done=true` cannot close an entry whose required
surface records are absent.

### 3. Expected and observed effect linkage

When a change has a defensible measurable effect, the changelog links to the
existing Pulse impact intervention and later verification run IDs. It does not
duplicate the impact ledger. The linked state is one of:

`not_measurable`, `awaiting_evidence`, `improved`, `unchanged`, `regressed`,
`inconclusive`, or `confounded`.

Comparable-run assessment uses the immutable run and plan identities owned by
PLAT-047. Reliability and measurement repairs remain labeled as such and do not
claim direct goal impact.

### 4. Pulse intake

`get_pulse_state` exposes pending dependency obligations and their per-surface
status. Gate can prioritize stale or high-risk plan changes without reopening
and interpreting every changelog file. An entry remains visible until its
structured obligation is closed or linked to a blocking lifecycle item.

## Acceptance

1. A user-chat edit and Pulse Fixer edit produce changelog entries with
   distinct, trusted origins and source links.
2. An approved human decision applied by pre-run/fixer retains both the human
   decision ID and the applying fix attempt.
3. Every material plan mutation remains in the dependency-review backlog until
   all applicable surfaces have evidence-backed dispositions.
4. A stale downstream dependency, validation contract, evaluation, dashboard
   query, DB contract, or learning reference creates/links a lifecycle item and
   cannot be hidden by a coarse completion stamp.
5. Cosmetic/no-behavior changes can close without meaningless edits by using
   evidence-backed `not_applicable` dispositions.
6. A measurable change links to impact observations and verification runs that
   used the expected PLAT-047 plan revisions; insufficient evidence stays
   `awaiting_evidence` or `inconclusive`.
7. Existing PLAT-033/074 before/after evidence and changelog readers remain
   backward compatible for legacy entries with no structured origin.

## Non-goals

- Reimplementing run identity or plan-revision storage; PLAT-047 owns it.
- Asking workflow steps to author platform provenance.
- Automatically editing every possible dependency after every mutation.
- Treating a changelog timestamp as proof of the plan used by a run.
- Claiming goal impact from one post-change run or from reliability-only work.

## Implementation status — 2026-08-28

Implemented:

- every successful managed plan/config/evaluation mutation receives a stable
  `change_id` and structured origin derived from trusted execution context;
- user chat, planner, background agent, Pulse Fixer, and Pulse-applied human
  decisions are distinguishable. Pulse changes retain the run, fix attempt,
  issue IDs, and linked human-input ID when present;
- the Artifact Review marker requires explicit evidence-backed dispositions
  for downstream steps, validation, evaluation, reporting, database, and
  learnings/knowledge before it can set `artifact_review.done=true`;
- `blocked` and `broken` surfaces require durable Pulse issue IDs, preventing
  unresolved dependencies from disappearing behind a coarse completion stamp;
- the plan-change backlog exposes change identity, origin, and structured
  review state through the existing Pulse state path; and
- deterministic intake now validates every current-contract (`change_id`)
  change even when a caller already set `artifact_review.done=true`; an
  incomplete six-surface receipt remains pending and the backend requires Gate
  to route it to agentic Technical Review under
  `plan_orchestration_integrity`. Legacy reviewed entries are not reopened;
- measurable effects reuse the existing Pulse impact ledger: guidance requires
  linking interventions back to `change_id` with a typed `review` source.

Backward compatibility is preserved: legacy changelog entries still decode,
and an explicit cursor-backfill path records visible legacy `not_applicable`
surface evidence instead of silently inventing a modern review.

Still open before this ticket can close:

- changelog entries do not yet store PLAT-047 `before_plan_revision` and
  `after_plan_revision` values at mutation time;
- ordinary user-chat origin has a trusted session ID but no durable source
  message ID yet;
- impact linkage is required by reviewer guidance and supported by the typed
  ledger, but the backend does not yet require an intervention (or an explicit
  `not_measurable` disposition) for every closed material change; and
- deployed workflow re-verification is still required for user-chat, generic
  Pulse Fixer, and approved-human-decision paths.

## Follow-up — 2026-09-17: Plan Drift/Technical duplicate-review gap

The six-surface contract was deployed, but the scheduler exposed a second-order
gap: Gate could route an incomplete `plan_change_dependencies` receipt to
`plan_drift_review`, which then cleared only the per-step drift record. The
unchanged PLAT-197 receipt remained open and deterministically forced a later
Technical Review whose only useful action was marking those same entries.

Plan Drift now owns both closure records when Gate selects it in lieu of
Technical Review. Its procedure marks only the exact changelog entries it
actually inspected, with all six surface dispositions and durable issue links
for unresolved defects. `record_pulse_result` also rejects a successful Plan
Drift result if current-contract dependency receipts remain incomplete and
Technical Review is not due in that same run. This preserves revision safety:
newer/uninspected entries stay open, while an already-completed drift review can
no longer manufacture a receipt-only Technical pass on the next schedule.

The scheduler boundary is stricter as of the same follow-up: any due Plan Drift
review makes that Pulse cycle an exclusive prerequisite pass. Technical,
Architecture, and Strategic Review are deferred until the following cycle, so
they never assess runtime or product behavior against a plan already known to
be stale. Pending reviewer-recovery records remain durable and are selected
again after Plan Drift establishes the new baseline. A persisted `fail` check
now keeps `drift_review.needs_review=true`, so an unresolved drift cannot clear
the prerequisite merely because its reviewer turn ended.

The Pulse UI now reads a lightweight live due-item projection from Go and shows
a prominent prerequisite banner with the candidate count, affected step IDs,
and reasons. Technical, Architecture, and Strategy visibly show “Waiting for Plan
Drift,” including before the next scheduled Gate has run; a scan error is shown
as unknown/not-clean rather than silently appearing healthy.

The 2026-09-17 Pulse control follow-through also adds a direct **Run drift
check** action. Downstream manual Technical, Architecture and Strategic actions
are disabled while drift is due or its live status cannot be established.
The due state is rendered once: the prominent prerequisite card owns its
details and manual-run actions, while the normal compact drift-status row is
suppressed until the prerequisite clears.
Plan Drift deliberately has no disable switch: allowing the owner to suppress
the compatibility prerequisite would violate this ticket's current-plan
baseline guarantee.
