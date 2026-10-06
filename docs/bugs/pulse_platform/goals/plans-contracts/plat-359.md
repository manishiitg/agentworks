[← goals / plans-contracts](index.md)

# PLAT-359 — Plan Drift fired on cosmetic-only plan edits

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | plans-contracts |
| Summary | fixes the trigger the user reported as "even small change trigger a plan drift". |

| Coordination | Value |
|---|---|
| State | Pushed to main; local and RTS deployment pending |
| Date | 2026-09-26 |
| Owner | plans-contracts |
| Related subsystem | pulse-governance (plan_drift_review scheduling) |

## Problem

The user reported that even small plan changes trigger Plan Drift constantly. The trigger was by design unconditional: any persisted plan-step field change, title included, flagged the step's `drift_review.needs_review` and made `plan_drift_review` due — the prior contract explicitly said not to classify a change as material or cosmetic in Go. The same "any field" shape applied to the changelog backlog Gate reads (`plan_change_backlog` / `plan_change_dependencies`), which counted every unstamped entry regardless of what it changed.

A live audit of the changelog across running workflows found description-only edits on regular and message-sequence steps were the single largest share of the unreviewed backlog — ahead of every structural change kind (output, dependency, validation, routing). Since Plan Drift is also an exclusive prerequisite that defers Architecture (and, until PLAT-358's sibling Gate fix, wrongly deferred Technical and Goal Work too), this noise directly slowed down the rest of Pulse.

## Authorized change

Add `planDriftMaterialFieldNames`: the step fields whose change can affect a dependent step, an eval, a report query, a DB contract, or downstream learnings/KB (`context_dependencies`, `context_output`, `items`, `messages`, `validation_schema`, `success_criteria`, `routes` and routing/branch fields, `predefined_routes*`, `*.sub_agent_step`, etc.). Title and description are deliberately excluded — a wording rewrite that hides a real behavior change still surfaces as an output/data change on the step's next run, which Pulse's own step-output and step-concern review already catches.

- `planStepFieldChangesRequireDriftReview` gates the per-step `needs_review` flag on materiality instead of "any field non-empty".
- `planChangeFieldsAreDriftMaterial` gates the changelog backlog scan the same way: an entry whose every named field is cosmetic never enters the backlog. An entry with NO recorded field names (an untyped `update_step_config` call, or a step add/delete, which changes the plan's shape without a field-level diff) stays conservative and is still counted — there is nothing proving it is safe to skip.
- `plan-drift-review.md` guidance updated to describe the materiality trigger instead of "any field, title included; nothing is classified as cosmetic".

## Acceptance and evidence

- A title-only or description-only edit does not flag a step's `drift_review.needs_review`, and the prior review's evidence is untouched.
- A field that can affect a dependent (e.g. `context_dependencies`) still flags the step; a mix of only-cosmetic fields does not.
- A changelog entry whose only named fields are cosmetic (description/title, `workflow.json.schedules`, `workflow.json.updated_at`, model/tier settings) does not appear in `CollectPlanChangeBacklog`'s output at all.
- A changelog entry with no recorded field names (untyped `update_step_config`, step add/delete) still counts.
- `step_based_workflow` and `cmd/server` guidance/Pulse suites pass, including new tests: `TestClearDriftReviewAfterPlanUpdateSkipsTitleAndDescriptionOnly`, `TestClearDriftReviewAfterPlanUpdateFlagsOnMaterialField`, `TestClearDriftReviewAfterPlanUpdateSkipsMultipleNonMaterialFields`, `TestCollectPlanChangeBacklogExcludesCosmeticOnlyEntries`, `TestCollectPlanChangeBacklogKeepsEntriesWithNoNamedFields`.

Pushed to main (`a26420812`, 2026-09-26). Not yet run against a live Pulse pass; needs a local backend restart, then RTS deploy, to confirm the drop in `plan_drift_review` due-ness across the running workflows.

## Register notes

[PLAT-359](plat-359.md) fixes the trigger the
user reported as "even small change trigger a plan drift". Any persisted
plan-step field change, title included, flagged `plan_drift_review` due, by
design; a live audit found description-only edits were the single largest
share of the unreviewed changelog backlog. `planDriftMaterialFieldNames` now
gates both the per-step flag and the changelog backlog scan on whether a
changed field can actually affect a dependent (outputs, dependencies,
validation, routing); wording, review notes, schedule timing and model/tier
settings no longer count. An entry with no recorded field names at all
(untyped `update_step_config`, a step add/delete) stays conservative and is
still counted. Pushed to main; deployment and live acceptance pending.
