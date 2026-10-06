[← goals / human-decisions](index.md)

# PLAT-092 — answered operator decisions are never applied or consumed, so the decision loop dead-ends at `answered`

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | human-decisions |
| Summary | P1 — the operator answers a question and nothing happens. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — structured application contract and conservative legacy handling shipped; runtime reverify pending |
| Last synchronized | `2026-08-25` |

- **Priority:** P1 — the operator answers a question and nothing happens. This
  is the platform silently discarding human decisions, which is worse than not
  asking: it spends the operator's attention and returns nothing.
- **Owner:** Pulse Review+Fix decision drain (`pulse-review-fixer.md`), and the
  routing of answered decisions to a suppressed module
- **Found by:** `strategy_auditor` on its first pass after being re-enabled
  (rtslatency, 2026-08-12) — *"rtslatency's decision loop has stopped at
  'answered' — five operator answers, three of them this module's own, sit
  unconsumed for 5-7 days while no plan step reads them"*. social-media's Gate
  reported the same shape independently the day before.

## Scale

**26 answered decisions across 6 workflows have never been consumed.** The
oldest was answered **31 days ago**.

| workflow | stranded |
|---|---:|
| tectonicusadaytrading | 8 |
| instagram | 5 |
| rtslatency | 5 |
| social-media | 4 |
| hetznerssh | 3 |
| linkedin | 1 |

By the module that asked:

| source | stranded |
|---|---:|
| `pulse` | 12 |
| `goal_advisor` | 8 |
| `strategy_auditor` | 6 |

## Two overlapping causes

**1. No stage is instructed to apply or consume.** `pulse-review-fixer.md`
tells Review+Fix to *"First inspect current-run results, the complete active
retained backlog, **answered decisions**, awaiting-verification work…"* —
inspect, and nothing further. `mark_human_input_consumed` appears in **zero**
Pulse operational guidance. Its only mentions anywhere are
`system/workflow-tools.md` (a tool catalogue, where the instruction is merely
permissive: *"A later Pulse run **may** apply an approved proposal … call
`mark_human_input_consumed`"*) and `system/chief-task-report.md`. So every
Pulse pass re-reads the same answered decisions, reports them as outstanding,
and moves on — which is exactly the 5-to-31-day loop observed.

**2. Answered decisions are routed to a module that cannot be selected.**
`pulse-review-fixer.md` states *"Goal Advisor is selected only for its own
blank-sheet opportunity, **answered decision**, healthy-headroom, or
experiment-checkpoint trigger"* — an answered decision is a Goal Advisor
trigger. But `goal_advisor` has been suppressed at the Gate for the whole
core-system verification phase, so the designated handler can never run.
`strategy_auditor` was suppressed alongside it until 2026-08-11.

That combination is self-inflicting: **14 of the 26 stranded decisions were
asked by the two modules that were then disabled**, orphaning their own
answered questions. The Gate's suppression note already warns that disabling a
lens reduces new finding volume; nobody accounted for it also stranding
decisions the operator had already answered.

## Fix shipped

`pulse-review-fixer.md` now carries an explicit drain contract: every answered
decision must reach a terminal state in the pass that sees it — applied and
consumed, or explicitly re-parked with a stated reason and a next-check
boundary. Silence is no longer an option, and the drain does not depend on
`goal_advisor` being selectable, since the module that asked a question may be
suppressed while its answer is still valid.

The consumption call requires a real `outcome_summary`, so a consumed decision
always records what was actually done with it.

## 2026-08-25 routing correction — human approval is not an implementation spec

The first drain contract still gave an agent a decision's operator-facing prose
and asked it to infer the repair. That is not safe for prompt, plan, route,
validation, database, tool, or cross-artifact changes. It also made it
impossible for the scheduler to know whether a failed pre-run repair should
block the upcoming run or leave the verified old plan in place.

`report_human_inputs` now persists a reviewer-supplied `apply_contract`:

- `no_change` — record the operator's rejection/defer outcome;
- `direct_apply` — apply one already-defined, bounded setting change;
- `targeted_fixer` — start a dedicated, scope-bounded pre-run Fixer with the
  linked issue, approved scope, required checks, post-run proof boundary and
  explicit failure policy; or
- `external_wait` — retain the answer until an external prerequisite exists.

Unknown legacy prose has **no automatic mutation path**. A rejected
`targeted_fixer` contract is recorded as no change, never dispatched as a
repair. The existing stable namespace for prompt-contract consolidation
decisions is migrated once to `targeted_fixer`; this includes the outstanding
Social Media prompt-bloat approval without pretending arbitrary old decisions
have a machine-readable scope.

## Not fixed here

- **The 26 existing stranded decisions.** They need a pass per workflow to
  apply or re-park each one; the guidance change only stops the backlog from
  growing. Several are months-old proposals whose premise may no longer hold,
  so they need judgement rather than a bulk sweep.
- **The 2 corrupted social-media rows** (`pulse-opportunities-schema-narrowing-2026-07`,
  `pulse-db-shape-decisions-2026-07-21`) that carry `status='answered'` *and* a
  populated `consumed_at`. That impossible state is what PLAT-077 fixed
  going forward; these two predate the fix and were never repaired.
- **Whether `goal_advisor` should return.** Its suppression is deliberate
  (core-system verification) but it is the designated handler for answered
  decisions, so leaving it off while decisions accumulate has a cost that is
  now measured rather than assumed.

## Acceptance

- A decision the operator answers is applied and consumed, or re-parked with a
  reason, in the next Pulse pass that sees it.
- No answered decision sits unconsumed across more than one Pulse pass without
  a recorded reason.
- Suppressing a module does not strand decisions it already asked.
- An approval whose consequence is a nontrivial workflow repair has an explicit
  application contract; it is never guessed from the prose shown to the
  operator.
