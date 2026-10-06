[← goals / pulse-governance](index.md)

# PLAT-303 — Exception-driven technical reviews and protected strategic work

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | pulse-governance |
| Summary | September 1–9 module audit rows across six local workflows contain 70 technical |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented protected asymmetric per-module scheduling; live acceptance pending` |
| Last synchronized | `2026-09-17` |

## Problem and evidence

September 1–9 module audit rows across six local workflows contain 70 technical
executions and 6 strategic executions. Technical executions include checks and
repairs, not only defects. Several revisit shared validation, permissions and
provenance failures. Strategic reports have useful goal diagnoses, but approval
or plan edits alone do not establish business impact.

Examples: Sales Outreach identified incompatible email and LinkedIn warmup
queues; Upwork proposed a past-client channel and a marginal bid rule; Substack
identified publishing/review handoff as the bottleneck; Social Media had an
approved strategic direction that remained unapplied. These require execution
and outcome follow-through, not more generic infrastructure reviews.

## Implemented in this change

- Gate guidance makes technical review exception-driven: concrete unresolved
  impact, an available repair, or new material evidence. Healthy runs, elapsed
  time, a harmless edit, and unchanged platform handoffs do not justify a sweep
  through all technical lenses. Mandatory drift/dependency safeguards remain.
- The scheduler previously applied the technical repair-drain completion check
  to every `review-fix` step, including strategic-only runs. It now reads the
  durable due worklist and enforces that check only when technical review was
  due. A strategic review can complete with unrelated technical backlog open.
- Regression tests seed a real actionable issue: strategic-only completion
  passes, technical completion fails until drained, and missing worklists fail
  explicitly. Runtime advisory severity is covered by [PLAT-163](plat-163.md).

## Remaining acceptance — not implemented by this patch

1. Persist independent strategic due state/cadence so repeated technical/drift
   selection cannot move the strategic boundary forward indefinitely. Preserve
   explicit user scheduling configuration; do not invent a universal cadence.
2. Sequence technical mutations and strategic reads safely when both are due,
   with module-specific permissions, receipts and recovery. Merely removing the
   current one-review-per-pass invariant risks concurrent mutations and is not
   sufficient. Critical failures may defer strategy with a durable reason.
3. An approved strategic decision must track application and a named measurable
   outcome boundary; distinguish adopted, executed and outcome-assessed states.
   Missing evidence must not be recorded as zero impact or success.
4. Routine technical health remains observable without creating review work.
   Shared platform failures link to one platform ticket; newly reproduced
   material failures remain visible. Healthy checks do not require zero logging.
5. Verify behavior on local workflow evidence and a live server Pulse pass.

## Related work

PLAT-047/089 cover immutable evidence, PLAT-229 the empty-array validator,
PLAT-304 retention permissions, PLAT-257 generated instruction consistency,
PLAT-163 impact-aware selection, and PLAT-155/199 lifecycle/retained execution.
Do not close those separate acceptance gaps from this scheduling correction.

## Local validation record

New regression tests pass. The initial full affected-package run exposed two
pre-existing guidance-test failures: `TestWorkflowToolsReferenceDistinguishesLogicalFromNativeBridgeTools`
expects removed `foreground curl` wording, and
`TestPulseEvalGuidanceSeparatesCorrectnessRepairsFromSemanticApproval` reads
`improve/goal-advisor.md`, which is absent in HEAD. These are not claimed fixed.
All remaining tests in `pkg/pulseintake`, `step_based_workflow`, and `cmd/server`
pass with only those two named tests excluded. `git diff --check` also passes.

## 2026-09-10 implementation follow-through

PLAT-305 adds independent Architecture/Strategy due decisions with protected
`next_check_at` boundaries, explicit dated deferrals, sequential module stages,
and module-scoped receipt/recovery checks. It removes the single-review-per-pass
restriction. It uses existing Pulse triggers rather than a new cron per reviewer.
Decision approval/application events now update linked improvement records;
Architecture adoption requires an outcome assessment. The earlier "remaining
acceptance" list above is historical planning context; the current implementation
and limits are in [PLAT-305](plat-305.md). Production workflow acceptance remains.

The stale guidance tests noted above were corrected against their current
canonical references/normalized text during this change; no named test exclusion
is needed for the final affected-package run.

## 2026-09-17 cadence follow-through

Independent boundaries are now intentionally asymmetric. Architecture uses the
longest review horizon and normally waits for several comparable producing runs
unless a material design change, repeated structural signal, or reached proposal
checkpoint makes it useful sooner. Strategic Review uses the shorter research
horizon and is reconsidered at the next meaningful goal/outcome, feedback,
experiment, decision, or measurement checkpoint. It is still skipped when no
strategic evidence changed; "shorter" does not mean every Pulse tick.

When both need a new boundary and no domain-specific date is stronger, Gate is
instructed to place Architecture later than Strategy and never copy the same
date/cooldown mechanically onto both. Existing protected dates remain durable
across Gate skips. Plan Drift remains an exclusive prerequisite cycle and does
not erase either later research boundary.

The guidance regression suite passes locally. Deployment and multi-schedule RTS
evidence are still required before claiming the observed cadence distribution.
