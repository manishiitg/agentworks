[← goals / pulse-governance](index.md)

# PLAT-163 — Pulse technical and strategic reviews need durable focus rotation, one canonical technical identity, and visible coverage history

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | The user observed that routine tool errors could continually select Technical |

> **2026-09-10 update:** [PLAT-306](plat-306.md) supersedes mandatory Markdown
> checkpoint/report maintenance with optional SQLite review notes on the existing
> result tool. Typed lifecycle records remain authoritative; historical design
> discussion below is retained. No reporting-only turn is required.

> **2026-08-28 sequencing update:** PLAT-199 replaces the fresh independent
> Technical Fixer with a later receipt-gated message in the retained technical
> sequence. Focus selection and the terminal review receipt remain reviewer
> facts; repair mutation and outcome remain a distinct post-receipt phase.
> Strategic Review stays separate and read-only.

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — original focus/coverage work shipped; 2026-09-05 impact-aware Gate and evidence-accumulation correction implemented and tested; rebuild/deployment and live Pulse verification remain |
| Last synchronized | `2026-09-09` |

## 2026-09-05 correction — recovered tool errors must not monopolize review selection

### Report and root cause

The user observed that routine tool errors could continually select Technical
Review, leaving Strategic Review without a turn. They explicitly requested
that Gate judge whether an error actually affected a step's job, consider
step-raised concerns, and wait for more relevant workflow runs after a recent
review when there is insufficient new evidence. Skipping all three reviewers
is a valid result.

The code confirmed the selection problem:

- `pulseintake.CheckRuntime` reports an errored/canceled child call in a
  completed run as `runtime_status_disagreement`, and also reports structured
  tool failures and non-completed runs. These facts do not establish whether
  recovery succeeded or the final required outcome was harmed.
- `validateDeterministicIntakeRouting` nevertheless forced Technical Review
  whenever a new runtime signal existed. Recovery/impact judgment happened
  only after Technical Review had already taken the review slot.
- The current worklist permits at most one reviewer per pass, with Plan Drift
  taking priority. A repeated error in each new run could therefore keep
  displacing otherwise eligible Strategic Review.
- Gate updates `last_checked_at` on skips and clears current-pass result
  fields. The last actual review timestamp and audit receipt survive, but
  current-pass state alone does not retain the previous review conclusion.

### Agreed behavior and implementation

1. **Runtime signals are advisory evidence.** Removed the runtime-error
   mandatory-routing branch; kept the collector and its typed evidence in
   `get_pulse_state(view="module")`. Gate may choose Technical, Strategic, or
   no review. No keyword classifier or fixed error-count threshold replaces
   the removed rule.
2. **Impact and recovery are assessed before selection.** Gate reads the
   smallest relevant step summary, validation/output receipt, or trace. A
   verified retry/fallback can justify skipping; `completed` alone cannot.
   Missing/partial/stale output, failed required side effects, uncertain
   recovery, or material retry cost/latency can justify focused Technical
   Review. New critical regressions and security/data-loss risks must not wait
   for sample accumulation; this is an agentic selection obligation, not a
   new Go semantic classifier.
3. **Use fresh step concerns without reviving the old backlog.** Gate reads
   `CONCERNS:` from relevant retained step summaries. `open_concerns` is the
   accepted canonical issue backlog; historical workflow-observation rows
   remain audit-only. Missing concerns do not prove health, especially for
   scripted steps or crashed agents.
4. **Accumulate evidence since the actual review.** Use `last_ran_at`, the
   retained terminal receipt, and relevant focus/route history—not
   `last_checked_at`. Compare distinct completed, comparable workflow runs;
   chat turns, tool calls, retries, unrelated routes, and repeated reads of
   the same run are not new data points. State retention/provenance gaps.
   The agent chooses the necessary sample or outcome boundary; there is no
   universal minimum run count.
5. **Preserve the waiting boundary.** Record the prior outcome, new relevant
   sample, remaining uncertainty, and next boundary in existing worklist
   reason/evidence and scheduling fields. Do not restart the cooldown or move
   the boundary forward merely because Gate checked again. A due boundary
   prompts reassessment, not an automatic expensive review.
6. **Expose the real baseline directly.** Added `last_review_receipts` and
   `last_review_receipts_error` to the module view. Three bounded lookups read
   existing `pulse_module_audit` records, excluding skips; no new table or
   copied history. A failed/blocked/timed-out receipt is not a completed
   assessment. Missing or unreadable history is explicitly not a clean review.
7. **Keep deterministic plan safeguards.** Non-empty plan-drift candidates
   still force Plan Drift. Missing current-contract plan-dependency receipts
   still require Technical Review unless Plan Drift takes the pass. The
   one-review limit, interrupted-review recovery, and review/fix authorization
   boundaries are unchanged. This correction adds no schedule or recurring job.

### Verification and delivery status

**Implemented and tested; not yet rebuilt/restarted or verified in a live
scheduled Pulse pass as part of this correction.** Earlier
shipped work elsewhere in this ticket retains its historical status.

Passing targeted checks:

- `TestPulseWorklistRuntimeSignalsDoNotReserveReviewSlot`: nine combinations
  of errored child calls, success-labeled structured failures, and known
  failed runs with observe/Strategic/Technical selection. Signals remain
  present and Gate's chosen reviewer is not overridden.
- `TestPulseSkippedChecksPreserveActualReviewBaseline`: repeated skips
  preserve the actual Technical/Strategic review timestamp, retained
  conclusion, and explicitly supplied next-check boundary.
- `TestPulseModuleViewExposesActualReviewAfterSkip`: module view returns the
  actual prior review rather than a later skip, isolates workflows, does not
  create a DB just to read absent history, and exposes malformed receipt
  evidence as a read error instead of silently claiming clean history.
- Existing worklist/plan-drift/plan-dependency routing tests and
  `pkg/pulseintake` tests pass.
- Rendered guidance tests pin impact assessment, fresh concerns, comparable
  runs, no sliding evidence window, all-review skips, critical-failure
  exceptions, and the current one-review limit. `git diff --check` passes.

Remaining runtime acceptance after rebuild/restart:

- A recovered error with verified required output may produce an evidenced
  Technical skip; eligible Strategic work is not rejected by the backend.
- Repeated insufficient samples retain the original review baseline and
  meaningful future boundary rather than repeatedly launching a reviewer.
- A new materially harmful failure receives focused diagnosis; no concern or
  an outer success label alone is treated as recovery proof.
- Plan Drift still wins when candidates exist. With no mandatory plan check
  and no useful new technical/strategic evidence, `observe` skips all three.

Implementation: `agent_go/cmd/server/pulse_worklist.go`,
`pulse_worklist_test.go`, `guidance/templates/system/pulse-gate.md`,
`guidance/pulse_gate_evidence_test.go`, and `guidance/render_all_test.go`.

- **Priority:** P1 — technical and strategic reviews are expensive, yet the
  platform cannot currently explain which deep theme a pass selected, why it
  took priority, what evidence it inspected, which themes it deferred, or when
  each theme was last reviewed deeply.
- **Owners:** Pulse review dispatch/guidance, typed Pulse persistence, review
  agenda/query tools, and the Pulse review-history UI.
- **Related:** [PLAT-138](plat-138.md), [PLAT-155](plat-155.md),
  [PLAT-090](../../platform/cost-telemetry/plat-090.md)), [PLAT-114](../../platform/coding-agent-bridge/plat-114.md)),
  [PLAT-156](../../brain/pulse-governance/plat-156.md), [PLAT-158](plat-158.md), and
  [PLAT-137](plat-137.md).

## Problem

Technical Review and Strategic Review cannot inspect every meaningful
concern deeply on every pass without becoming slow, expensive, repetitive, and
compaction-prone. PLAT-138 correctly bounded a pass to one agent-chosen
coherent objective, but the platform still has no durable coverage model behind
that choice.

Today a reviewer can rediscover the same familiar issue while another area is
never examined. A later reviewer cannot answer basic questions such as:

- Which engineering or operations theme was reviewed deeply last time?
- Why was it selected ahead of the other themes?
- Was the pass verifying an earlier repair, applying an answered decision, or
  doing new discovery?
- Which evidence selectors and canonical issue IDs were considered?
- What was deliberately deferred, and when should it become due?
- Has a changed plan/runtime surface invalidated an older review?

The existing typed state is useful but only module-level:
`pulse_module_state` stores the latest module result and next-check condition,
while `pulse_review_log` stores a terminal module receipt. Neither records
coverage within a module. Reusing either row as an overloaded focus ledger
would destroy its current meaning.

## 2026-08-21 architecture decision

Engineering Review and Operations Review were separate durable module names
even though PLAT-138 already ran them as one retained sequence. That split
created duplicate Gate decisions, receipts, focus histories, and slash-command
behavior for one technical lifecycle. The canonical model is now:

- `technical_review`: one module, one retained reviewer sequence, one focus,
  and one terminal receipt. Engineering correctness, Stores Health, operations,
  tool/runtime reliability, orchestration, models, cost, schedules, and
  efficiency are lenses inside it.
- `strategic_review`: a separate module because goal/measurement validity,
  feedback loops, alternative headroom, and experiments have a different
  evidence and decision lifecycle.

Legacy `workflow_review` and `llm_ops_review` data is migrated into
`technical_review` across module state/audit, review receipts, focus
state/history, findings/attempts, and metrics. New prompts and tools must not
dual-write the old names. Historical engineering/ops human-input sources remain
readable but new technical decisions use `source=technical_review` and a
`technical-decision-...` identity.

Gate may skip technical review, strategic review, or both when no useful
evidence has matured, but every skip records the next evidence/time/run
boundary. Every selected reviewer performs a lightweight urgent-evidence scan
and agentically chooses the smallest sufficient route-aware deep-focus set.
Review completion belongs to the reviewer; the
independent Fixer owns mutations and repair outcome and never rewrites the
review receipt.

## Desired review model

Each technical or strategic pass has two deliberately different depths:

1. **Lightweight safety scan.** Inspect only the compact lifecycle agenda for
   new critical regressions, matured verification work, and answered but
   unapplied human decisions. This prevents rotation from hiding urgent work.
2. **Agentic deep-focus set.** The reviewer maps relevant evidence to stable
   route/group/sub-workflow scopes, then chooses the smallest set of coherent
   themes worth investigating. A small route often needs one focus; a large or
   multi-route workflow may justify several when each has distinct evidence,
   risk, decision, or repair value. It records why each theme won and which
   other themes were deferred. There is no fixed numerical quota.

The technical catalog is: `execution_health`,
`plan_orchestration_integrity`, `store_integrity`, `report_quality_truth`,
`evaluation_quality_truth`, and `model_cost_fitness`. The strategic catalog is:
`goal_measurement_validity`, `strategy_effectiveness`,
`feedback_loops_bias`, `concentration_saturation`, `alternative_headroom`, and
`experiment_impact`. Go stores these stable coverage identities but does not
choose their semantic priority; the agent does.

The current operator decision deliberately removes Safety & Permissions as a
standalone rotating focus. Safety invariants remain enforced by the platform
and reviewer contracts, but the popup does not offer a separate safety review.

### 2026-08-23 — Catalog consolidation and one-off focused review

- Combined execution correctness, efficiency, tool/runtime reliability, and
  schedule recovery into `execution_health`.
- Combined plan contracts and orchestration fitness into
  `plan_orchestration_integrity`, explicitly including scripted-step fitness.
- Split report and evaluation truth so each can enforce its own best practices.
- Combined model/tier fitness and cost attribution into `model_cost_fitness`.
- Added a focused-review action to the Pulse popup. It reuses or opens the
  normal interactive automation chat, queues behind an active foreground turn,
  and sends a curated `/pulse-review` or `/strategy-auditor` request containing
  the exact canonical focus key. It does not create or modify a schedule.
- Historical merged focus rows are retained under their canonical successor.
  The old combined report/evaluation history is kept as legacy history rather
  than copied into both new focuses and falsely counted twice.

## Priority and rotation semantics

The platform supplies a compact typed agenda. The agent reasons within the
highest applicable lifecycle class:

1. new critical regression, data loss, security issue, or widespread runtime
   failure;
2. an earlier fix whose verification condition has matured;
3. an answered decision that is still unapplied;
4. a never-reviewed or materially changed focus;
5. an overdue focus;
6. the oldest remaining focus.

This is not rigid round-robin and is not a numeric relevance score. Go may
enforce lifecycle invariants and require at least one focus for a completed due
review, but it must not choose the semantic subjects or their count. The
reviewer owns that judgment, must persist each reason and route scope, and must
stop when another focus would repeat evidence or could not change a decision,
repair, or next check.

## Durable model

Add focus-level state without changing the authority of existing stores. Exact
names may change during implementation, but the responsibilities must remain
separate:

### `pulse_review_focus_state`

One current row per `(workspace_path, module, focus_key)` containing:

- last deep-review time, review run ID, and `pulse_run_id`;
- last verdict and targeted evidence selectors;
- last reviewed route scope plus route-specific coverage counts in the agenda;
- related canonical issue IDs and verification IDs;
- next-check time/run/evidence condition and reason;
- a source-change fingerprint or equivalent invalidation marker;
- update timestamps.

### `pulse_review_focus_history`

Append-only entries containing:

- module, focus key, review run ID, and `pulse_run_id`;
- stable route/group/sub-workflow scope (empty only for workflow-wide evidence);
- lifecycle class and agent-authored selection reason;
- compact scope and evidence references, not copied evidence bodies;
- verdict, canonical finding IDs, and verification IDs;
- deferred focus keys and their reason;
- terminal status and recorded time.

SQLite remains lifecycle authority. A run-scoped Markdown checkpoint may remain
the reviewer's compaction-safe working memory, as established by PLAT-138, but
it must not become a second backlog or history database.

## Review flow

1. Build a compact agenda from focus state, canonical issues, matured
   verification conditions, answered decisions, and relevant source changes.
2. Let the reviewer perform the lightweight safety scan and select the smallest
   sufficient route-aware deep-focus set.
3. Load only targeted authoritative evidence through existing files/tools.
4. Persist one focus-history entry per investigated focus and update focus state.
5. Write the existing `pulse_review_log` terminal receipt for the module.
6. Hand canonical repair work to the independent Fixer defined by PLAT-155;
   the reviewer does not repair its own findings.

## Compatibility audit and adverse-impact guardrails

### PLAT-138 — bounded, agent-chosen review objectives

This ticket complements PLAT-138; it does not reopen the unbounded backlog.
One pass still receives a bounded coherent review objective, but its agent may
cover multiple route-scoped focuses when each adds distinct decision value.
Rotation gives the agent a compact durable agenda and records its choices; it
must never instruct a
single review to process every due focus.

### PLAT-155 — observations are not the canonical repair queue

Focus coverage must operate on review themes and canonical lifecycle state. It
must not flatten raw observations back into an apparent issue count, duplicate
canonical issues, or let Review absorb Fixer's responsibility.

### PLAT-090 — cost authority

Focus history may reference `pulse_run_id` for cost/timing correlation, but it
must not create another cost ledger or revive per-module synthetic execution
IDs. Cost remains owned by the authoritative phase/execution ledger.

### PLAT-114 — background-agent audit

Focus history records why a review focus was chosen and its verdict. It must
not copy raw agent output or duplicate `background_agent_log`. A query surface
may correlate the records later.

### PLAT-156 — selective agentic evidence reading

Do not inject the full plan, full review history, or Go-authored plan
interpretation into the prompt. The compact agenda contains selectors and
reasons; the reviewer reads authoritative files and tools selectively.

### PLAT-158 — dedicated Pulse scheduling

Rotation runs only inside explicit manual review commands or the dedicated
Pulse review schedule. It must not add a hidden recurring job, revive
`post_run_monitor`, or cause ordinary workflow runs to launch Pulse.

### PLAT-137 — strategic review

Strategic Review remains separate, but now uses the same focus-state/history
contract. This does not undo PLAT-137's merged Strategy+Goal sequence or fold
strategic work into technical themes.

## Implementation slices

1. **Shipped:** focus-state/history schema, migrations, and focused repository tests.
2. **Shipped:** compact agenda read/write tools with strict workspace/module scoping.
3. **Shipped:** Technical and Strategic review guidance to run the safety scan,
   choose one focus agentically, read targeted evidence, and persist the
   selection/defer reasons.
4. **Shipped:** the terminal-write invariant: a completed review cannot report success
   without its focus-history entry and existing review receipt.
5. **Shipped:** work-area cards show all focuses selected in the latest review,
   their route scope, why selected,
   last-reviewed time, review count, and next candidates. Before the first
   recorded review they show the next eligible focus candidates rather than an
   empty state. A richer
   related-issues/next-due/recent-focus timeline
   remains presentation work; it does not block the rotation lifecycle.

## Acceptance

- Two consecutive unchanged review passes cannot silently select the same deep
  focus while an eligible never-reviewed or overdue focus exists; doing so
  requires a persisted urgent/verification reason.
- A critical regression, matured verification, or answered unapplied decision
  can preempt normal rotation and the preemption is visible in history.
- Each completed Technical/Strategic review has one or more route-aware
  deep-focus history entries, chosen agentically, plus exactly one existing
  `pulse_review_log` receipt.
- Review prompts receive a compact agenda and selectors, not full plan/history
  payloads.
- Raw observations remain distinct from canonical issues, and review remains
  distinct from Fixer.
- Ordinary workflow runs do not trigger the rotation mechanism.
- The UI can explain, without reading logs, what was reviewed, why, what was
  found, what was deferred, and what becomes due next.


## 2026-09-09 local Pulse audit follow-up

The September 1–9 local audit found 70 technical module executions versus
6 strategic executions across six workflows. Counts include successful checks,
repairs and failures; they are not 70 distinct platform bugs.

Correction to diagnosis: the current worklist already treats runtime signals as
advisory (the September 5 fix); there is no remaining unconditional runtime
routing rule to remove. The detector still described any errored/canceled child
inside a completed run as a high-severity `runtime_status_disagreement`.

Implemented detector v2: emit factual `completed_run_child_errors` at medium
severity, with required-output/recovery/material retry-cost guidance. Explicit
non-completion and structured failures inside success-labelled tool results
remain high severity. Outer completion does not prove recovery. Gate guidance
now uses the new kind and explicitly rejects routine technical checklist sweeps
or repeatedly diagnosing an unchanged platform handoff without an available fix.

Protected strategic completion and the remaining independent schedule work are
tracked in [PLAT-303](plat-303.md). Deployment/runtime verification is pending.
