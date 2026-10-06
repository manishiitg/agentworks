[← ops / cost](index.md)

# PLAT-069 — nothing measures whether a workflow is getting cheaper, faster, or more accurate over time

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | P1 — this is the measurement that tells you whether Pulse is working at all |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — design below; data sources verified present |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — this is the measurement that tells you whether Pulse is working at all
- **Owner:** Pulse trend measurement + Pulse popup
- **Requested:** 2026-08-10, directly by the operator

## The gap

Every accounting surface built to date answers *"what did this run cost?"*. Nothing answers **"is this workflow improving?"** — the question that decides whether the whole self-improvement loop is earning its keep.

Concretely, none of these can be answered today without hand-querying files:

- Is cost per run trending down?
- Is wall-clock per run trending down?
- Is the evaluation score trending up?
- Did a specific Pulse fix actually move any of them?

Without this, Pulse can run indefinitely, close findings, report healthy — and nobody can tell improvement from churn. It also makes every efficiency finding unfalsifiable: a tier downgrade "for cost" has no before/after it is ever checked against.

## The data already exists — this is a join, not new collection

Verified present, per workflow, going back to 2026-04:

| source | provides |
|---|---|
| `costs/execution/<group>/<date>.json` | per-execution, per-step, per-model USD + full token breakdown |
| `costs/evaluation/<group>/<date>.json` | evaluation-pass cost |
| `costs/phase/daily/<date>.json` | per-day, per-phase, per-model USD |
| `scores/evaluation/<group>/<date>.json` | per-evaluation `step_scores[]` with `score` / `max_score` |
| `runs/<run>/logs/<step>/execution/*-timing.json` | per-step wall/LLM/tool duration (now incl. `reflection-timing.json`) |
| `pulse_goal_observations` | recorded goal-criterion observations |
| `pulse_interventions`, `pulse_impact_assessments` | what Pulse changed and when |

Nothing new needs collecting. What is missing is a per-run series joining **date → cost → duration → eval score %**, and a place to see it.

## Design

**1. A durable per-run trend row.** On run completion, write one record keyed by run: date, group, run id, total USD, total wall duration, eval score achieved/possible, and the run's terminal status. Derived entirely from the sources above, so it is reproducible and backfillable rather than a new source of truth. Backfill from April so the series is useful on day one instead of in three weeks.

**2. Normalisation is the hard part — get it right or the trend lies.** Runs are not comparable by default:
   - a `propose_new` route run and an `execution` route run do different work; compare like-for-like by route, never pool them;
   - a run that legitimately did more (bid on 3 jobs vs 0) costs more without being worse — normalise per unit of work where a unit exists, and where it does not, say so rather than implying a clean comparison;
   - eval score is only comparable while the rubric is unchanged; `evaluation_plan.json` changes must break the series visibly, not silently shift the baseline. Record the rubric version alongside the score.

**3. Surface it in the Pulse popup** as a trend, not a number: cost/run, duration/run, and score% over time, with Pulse interventions marked on the timeline so a change and its effect are visually adjacent. The operator's stated ask is exactly this — *"if cost/time is going down over time and accuracy towards goal is going up"*.

**4. Make it Gate-visible.** Once the series exists, a sustained adverse trend is itself evidence a module is due — which is what turns this from a dashboard into part of the loop.

## Deliberate non-goals

- **Do not auto-act on the trend initially.** Report it, let it be judged. A metric that immediately drives automated change cannot be validated against reality first — and the existing `project_metrics_and_scoring_removed` decision is precedent for being wary of scoring that acquires authority before it has earned it.
- **No new cost/score collection.** If the trend disagrees with the ledgers, the ledgers win; this is a view over them.

## Acceptance

- A per-run series exists per workflow, backfilled from existing files, reproducible from them.
- The Pulse popup shows cost/run, duration/run and score% over time with interventions marked.
- Route and rubric-version boundaries are explicit in the series; pooled or cross-rubric comparisons are never presented as clean.
- A stated improvement claim from a Pulse finding can be checked against the series.
