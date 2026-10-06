[← goals / evaluation](index.md)

# PLAT-227 — `update_evaluation_plan` can now realign route gating and pre-run evidence in one atomic change

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | P1 — LinkedIn `PUL-E45BE152` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — LinkedIn `PUL-E45BE152` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_plan_tool.go`.
- **Note on the audit's own grouping:** the audit filed this alongside
  Upwork `PUL-E67413EC` and LinkedIn `PUL-90D1E2C9` under one "evaluation
  pipeline" P1 row, "keep a shared umbrella link only." Checked each
  individually before starting: `PUL-E67413EC` and this one are both filed
  with `issue_kind: workflow_issue` — the finding's own text says the
  *content* defect (which route auto-triggers evaluation, which route gates
  a given eval step) is workflow-owned, not platform code. Splitting them
  out was the right call, not an oversight to fold back together:

  - `PUL-E67413EC` (Upwork, "no automatic evaluation trigger for
    weekly-profile"): purely a workflow-config question — whether the
    `weekly-profile` route has evaluation wired to auto-trigger is
    Upwork's own `plan.json`/`evaluation_plan.json` content, not a shared
    harness mechanism. **Reclassified out of the platform-code queue**;
    belongs to Upwork's own Engineering Review pass, not this repository.
  - `PUL-90D1E2C9` (LinkedIn, "evaluation report aggregator dropped a valid
    verdict"): this one's finding record has no structured `issue_kind`
    detail row (only a `run_concerns` text line survives), so it could not
    be classified the same way and was **not investigated this pass** — its
    described mechanism ("evaluation report aggregator") sounds like a
    genuine shared platform component worth checking, but that is a
    separate, unstarted investigation, not something this ticket covers.
  - This ticket covers `PUL-E45BE152` only, which — despite also being
    `issue_kind: workflow_issue` — names a genuine, confirmed *platform tool
    capability gap* as its blocker: the workflow-owned fix cannot be applied
    by any tool available to the workflow.

## Problem

LinkedIn's `eval-strategy-loop` evaluation step needed its producer-route
gate (`applies_to_routes`) and its pre-run evidence requirement
(`pre_validation`) realigned together, atomically — the step had drifted to
require a prevalidation artifact (`linkedin_topic_selection.json`) that only
the *old* route set actually produced, while the route gate itself still
spanned three routes. `update_evaluation_plan`'s editable-field whitelist
had no `pre_validation` entry at all. A bounded attempt changed only the
route/description fields; the tool silently ignored the unmodeled
`pre_validation` property, so the change had to be rolled back rather than
land half-aligned (confirmed in `planning/changelog`; no workflow run was
launched on the misaligned state).

## Fix

Added `pre_validation` to `evaluationPlanEditableFields` and its JSON Schema
property, matching the existing `{"files":[{"file_name":..., "must_exist":
true}]}` shape already used by real plans (confirmed directly against
LinkedIn's own `evaluation/evaluation_plan.json`). No other code change was
needed: `createUpdateEvaluationPlanExecutor` already forwards any field
present in the whitelist generically (`args[field]`), and
`UpdateEvaluationPlanStep` already edits the decoded JSON map rather than a
typed struct specifically so unmodeled/future fields survive a write intact
— `pre_validation` was simply never in the list of fields a caller was
*allowed* to set, not a structural gap.

Also corrected `validation_schema`'s own tool-schema description in the same
edit — it previously said "Pre-validation schema for this step's output",
which is self-contradictory now that a real `pre_validation` field exists
for the *actual* pre-run check; reworded to "Validation schema for this
step's OWN OUTPUT, checked after it runs" so the two fields read as
obviously distinct.

## Verification

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...
go test ./pkg/orchestrator/... ./cmd/server/...
```

New `TestUpdateEvaluationPlanCanRealignRouteGatingAndPreValidationAtomically`
reproduces the exact PUL-E45BE152 shape: one `UpdateEvaluationPlanStep` call
narrows `applies_to_routes` to the single actual producer route and points
`pre_validation` at that producer's real artifact
(`strategies_summary.json`) in the same atomic write, then confirms both
landed together and the plan still round-trips through the existing
struct-shape sanity check. Full existing suite passes unchanged.

## Reverify

No live Engineering Review turn has called `update_evaluation_plan` with
`pre_validation` through the deployed server yet. Reverify by having a real
LinkedIn Engineering Review pass apply this exact realignment and confirming
the next full Default Explore evaluation run measures the correct producer
route's artifact, per the finding's own `next_check`.
