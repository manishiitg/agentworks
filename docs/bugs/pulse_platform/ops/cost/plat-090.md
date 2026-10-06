[← ops / cost](index.md)

# PLAT-090 — no surface reports Pulse time/cost against workflow time/cost, so "is Pulse worth it?" cannot be answered

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | P1 — Pulse can incur substantial reviewer/fixer spend while the |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — daily Pulse scope cost exists, but no durable current per-Pulse-run/per-stage cost record exists |
| Last synchronized | `2026-08-16` |

- **Priority:** P1 — Pulse can incur substantial reviewer/fixer spend while the
  product still cannot attribute that spend to a specific Pulse pass and show
  it beside the result of that pass.
- **Owner:** Pulse measurement surface (`pulse_agent_metrics` read path,
  `pkg/costledger`, Pulse popup)
- **Requested by:** the operator, 2026-08-11 — *"we should measure pulse time,
  cost, goal progress, workflow time, cost"*
- **Depends on:** [PLAT-088](plat-088.md) (cost attribution — landed
  2026-08-11; without it every number here is wrong)
- **Related:** [PLAT-069](plat-069.md) (trend measurement: cost/time down and
  accuracy up over runs — deferred partly because this data was not trustworthy)

## Why this is open rather than merely unbuilt

The accounting events exist, but the per-Pulse-run measurement does not. This
distinction matters: fixing the missing surface must read the authoritative
ledger rather than introduce a second cost counter.

| Piece | State |
|---|---|
| Per-call cost with scope (`pulse` / `workflow_execution` / `builder` / `evaluation` / `chat`) | **Exists and is populated** — `cost_events` in `workspace-docs/_system/costs.sqlite`, accurate as of PLAT-088 |
| Cost Analysis daily Pulse column | **Exists** — reads `Summary.ByDate[date].ByScope["pulse"]`, including background agents, but it is only a date/workflow aggregate |
| `pulse_agent_metrics` table, `LoadPulseAgentMetrics`, `/api/workflow/pulse-agent-metrics`, `PulseWorkspace.tsx` panel | **Structurally exists but is stale/incomplete** — several workflows retain historical rows, while current runs have no production writer |
| The writer intended to fill that table (`RecordPulseAgentMetric`) | **Has zero production callers** since `aad50dfb0` (2026-08-08) |
| Pulse/workflow stage durations | **Logged only** — never persisted anywhere queryable |

## 2026-08-16 live verification — what “Pulse cost is not captured” means

The central ledger currently contains 494 `scope=pulse` events totalling
$1,947.8569. Recent workflow examples include Upwork (85 events, $590.9842),
Build in Public (118 events, $326.1655), Social Media (95 events, $290.5045),
and Tectonicus USA Day Trading (86 events, $280.1095). This proves that the
LLM calls themselves, including background reviewers/fixers when attributed
correctly, are not disappearing from accounting.

What is still missing is the useful product record:

- no durable row says which exact `pulse_run_id` incurred which total;
- Gate, Review+Fix, Finalize, reviewer, and fixer amounts/durations cannot be
  reconstructed as one stable per-pass hierarchy in the UI;
- the Finalizer receives a backend-measured Review+Fix window as prompt text,
  but that snapshot is not persisted as a typed per-run cost metric;
- historical `pulse_agent_metrics` rows do not solve the gap because the
  current architecture no longer calls their writer.

Therefore a daily Pulse number in Cost Analysis and a per-Pulse-run cost are
not equivalent. The former exists; the latter remains the open defect.

## Why the writer has no callers — and why simply restoring it is wrong

`RecordPulseAgentMetric` is not broken. It is well built: it refuses
agent-self-reported counters, derives usage only from the cost ledger via
`SummarizeExecution(executionID)`, prices any unpriced token slice through the
same immutable pricing contract the execution ledgers use, and upserts by
`execution_id`.

It became unreachable because the architecture beneath it changed. It was
written when Pulse ran **one Go-spawned child execution per reviewer module**,
so every module had its own `execution_id` to summarize. `aad50dfb0`
("stabilize pulse orchestration and scheduled sessions", the PLAT-050 change)
replaced that with **one continuing main-agent conversation** driving Gate →
Review+Fix → Finalize as scheduler-sent turns. The surrounding machinery it
depended on — `isPulseStage`, `reviewExecID`, `pulseReviewerPersistenceContext`
— was removed with it.

So the per-module `execution_id` the function is keyed on no longer exists for
the main Pulse stages. Reviving the old call site would mean reviving the old
architecture. **The table's key is the thing that went stale, not its columns.**

Note the asymmetry the current data shows: Pulse's *delegated background
children* still do get real execution ids (`bg-pulse-review+fix:-engineering…`)
and are correctly ledger-attributed. It is the main-session stages that have
no per-stage execution identity.

## Design

**1. Derive a typed Pulse-pass summary from the ledger.** At Pulse start and
each stage boundary, persist the stable `pulse_run_id`, stage name, and exact
UTC window. Aggregate `cost_events` for that workflow, `scope=pulse`, and
window, retaining the background execution breakdown. Persist or expose that
derived summary idempotently by `pulse_run_id`; do not ask an agent to copy a
dollar amount out of prompt text.

`LoadPulseAgentMetrics` can be replaced or repointed to this read model. It
must not treat the old execution-keyed rows as current merely because some
historical rows remain in workflow databases.

**2. Persist stage durations.** The scheduler already knows each Pulse stage's
start and end (it logs `step "gate" done`, `step "review-fix" done`,
`step "finalize" done`). Record start/end per stage against `pulse_run_id` so
time is queryable, not just greppable. Workflow-side timing already exists in
`run_metadata.json` and per-step `*-timing.json`.

**3. Report the comparison per run and as a trend.** Show one Pulse pass with
Gate, Review+Fix, background reviewers/fixer, and Finalize; then compare its
time and cost with the workflow run it reviewed and the verified outcomes it
produced. This is PLAT-069's ask on a foundation that can support it.

## Deliberately not in scope

- **Back-filling historical rows.** Pre-PLAT-088 data attributes scheduled
  Pulse and workflow turns to `chat`, and the 15,235 pre-2026-07-12
  `unknown`-scope rows predate attribution entirely. Any trend must either
  start at 2026-08-11 or state its own floor. Silently charting across that
  boundary would manufacture a fake improvement exactly where the accounting
  changed.
- **Reviving the per-module `execution_id` model.** See above.

## Acceptance

- The Pulse popup shows, for a given run: Pulse cost and duration beside
  workflow cost and duration, rather than an empty panel.
- The per-run Pulse total includes the main Pulse turns and every background
  reviewer/fixer cost exactly once, with an expandable stage/execution
  breakdown.
- The figures reconcile with `cost_events` for the same run and window.
- A Finalize interruption cannot erase the already-measured Pulse cost, and a
  Finalize retry cannot double-count it.
- Any trend view states the date from which its data is trustworthy instead of
  charting across the PLAT-088 attribution change as if it were a real trend.

## 2026-08-15 incremental implementation

The scheduler now snapshots the authoritative central ledger immediately after
Review+Fix and before Finalize, restricted to the current workflow,
`scope=pulse`, and the exact Review+Fix stage window. Finalize receives the
measured reviewer/fixer cost and LLM-call count and must include the compact
amount in the notification's Operations section. Subscription-backed coding
CLI amounts retain their truthful "estimated token-equivalent cost" label.

The snapshot deliberately measures the expensive work the operator asked to
track: the Review+Fix parent turn, its background Engineering/Ops reviewers and
Fixer work, and any receipt continuation. Gate, Finalize, prior Pulse passes,
workflow execution, builder activity, and other workflows are excluded by the
backend query. A pass that skips Review+Fix reports `$0.00`; a stage that ran
without matching ledger events reports a measurement gap rather than a false
zero. Regression coverage proves the time-window and scope isolation.

This does not close PLAT-090. The Review+Fix value is supplied only as
Finalizer context for one notification; it is not the durable typed
per-`pulse_run_id` record required by the Cost Analysis/Pulse UI. The per-pass
comparison and durable Gate / Review+Fix / Finalize timing remain to be
implemented.
