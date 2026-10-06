[← ops / cost](index.md)

# PLAT-008 — phase costs omit input and can use the wrong rate card

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | cost observer/phase ledger pricing |

| Coordination | Value |
|---|---|
| Assigned agent | `Unassigned` |
| Ticket state | `runtime_verified` |
| Last synchronized | `2026-08-05` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** cost observer/phase ledger pricing
- **Source finding:** `HARNESS-PHASE-COST-PRICING-2026-08-03`
- **Source database:** `Workflow/build-in-public/db/db.sqlite`
- **Problem:** phase rows omitted input cost and one Opus row used Sonnet output
  and cached-input rates.
- **Impact:** historical and daily spend is materially understated and changes
  without a workload change.
- **Implementation (2026-08-03):** Claude transcript usage now reports total
  prompt input (fresh + cache create + cache read), while retaining the raw
  cache components. Phase persistence carries cache read/write separately,
  recalculates every component from the effective model, and stamps
  `pricing_model_id` plus `pricing_version`. Claude Opus 5 and Sonnet 5 golden
  cases prove distinct input/output/cache-read/cache-write rates and totals.
- **Acceptance:** one immutable model identity selects one versioned rate card;
  input, output, reasoning, cache-read, and cache-write components reconcile to
  the total. Golden tests cover both Opus and Sonnet on adjacent dates.

## Tectonicus follow-up — 2026-08-05

Tectonicus Pulse found a separate reconciliation symptom after the rate-card
repair: `costs/phase/daily` and `costs/execution` disagree by **3.1×** under a
single opaque workflow-builder bucket. The workflow has no producer, plan step,
or configuration surface that controls that grouping, so it is platform-owned.

This does not refute the verified rate-card repair. It shows that correct
per-call pricing alone is insufficient when summaries cannot reconcile the same
execution across phase/daily and execution views. Coordinate with PLAT-009 and
PLAT-031: add a real per-execution reconciliation test that proves each view's
scope, includes every charge once, and labels intentionally cumulative views as
non-comparable rather than presenting them as a run total.

## Scope-presentation repair — 2026-08-05

**Implemented; needs a fresh workflow/Pulse run for runtime re-verification.**
`get_cost_summary(run_folder=...)` now states that its execution totals cover
only that run and renders Builder/Pulse usage as a cumulative reference which
must not be added to it. The dashboard likewise presents **Selected run cost**
separately from Builder/Pulse spend across recorded days, rather than silently
forming a misleading mixed-scope “total cost.” This prevents the observed 3.1×
comparison from being presented as a run-cost mismatch while PLAT-009/031
continue to improve ledger reconciliation itself.

Focused verification: the workflow-review sequence tests and the frontend
production build pass.
