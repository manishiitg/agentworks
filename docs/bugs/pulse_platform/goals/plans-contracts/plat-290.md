[← goals / plans-contracts](index.md)

# PLAT-290 — Prompt-health tool reused a stale session plan after managed edits

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | plans-contracts |
| Summary | `get_plan_prompt_health` loaded the authored plan only when `approvedPlan` was |

| Coordination | Value |
|---|---|
| State | Fixed locally; registered-tool regression passes; deployment not verified |
| Date | 2026-09-05 |
| Report | Upwork PUL-07504731 (fingerprint `33f26e14056e14e3`), G26 in the remaining-report audit |

## Cause and repair

`get_plan_prompt_health` loaded the authored plan only when `approvedPlan` was
nil. Once a workshop controller held a plan, later managed writes or another
session's changes could leave its description counts and duplicate clusters
pinned to an older snapshot. The report's September 1 evidence explicitly
compared fresh canonical descriptions with two stale tool responses.

The registered tool now reads a fresh snapshot through the workspace API on
every invocation. Normal workflow mode uses the existing canonical plan reader,
including its normalization, mutex and validation. Evaluation mode retains its
evaluation-plan source but also reloads each time. The read-only report does not
overwrite `approvedPlan`, reload execution state, or silently fall back to stale
data when the current file cannot be read or parsed.

## Verification

`TestRegisteredPromptHealthReadsCurrentPlan` invokes the actual registered tool
with a stale execution cache and a workspace HTTP test endpoint. Before the fix
it failed with the cached single-step metrics instead of the current two-step
counts and duplicate cluster. After the fix it verifies successive snapshots,
duplicate removal, missing/invalid-file errors, one workspace read per call and
an untouched execution cache.

`TestCurrentPromptHealthEvaluationSnapshot` verifies the evaluation source,
fresh reads after step removal and no execution-cache initialization.
Existing prompt-health and workshop-registration tests also pass. This verifies
the consumer/transport path; it does not rerun the historical Upwork workflow or
claim a deployed server/session has adopted this source change.

## Follow-up: remove the session cache entirely

Per owner request, removed `approvedPlan`, its setter, the preload at chat
creation and `LoadPlanForWorkshop`. `ReadCurrentPlan(ctx, evaluation)` is the
single fresh workspace reader for builder lookups, config targeting, current
reviews, prompt health and run preflight. Config lookups no longer temporarily
swap the controller's evaluation mode or plan. KB consolidation reads current
plan/config declarations rather than whichever execution last populated a cache.

Full-workflow, workshop-step and evaluation execution bind their loaded plan to
the execution context. Progress, dependency and artifact helpers use that scoped
snapshot. A fresh tool read or another execution cannot replace it on a shared
controller. This addresses plan ownership, not all other mutable controller state.
Run revision creation also uses that loaded plan instead of rereading a possibly
edited current file at metadata-write time.

Historical prompt lookup and continuation recovery use content-verified retained
run revisions, including the revision's step configuration. Exact prompt artifact
IDs remain readable for legacy runs without revisions. Positional lookup cannot
guess from today's plan; recovery without a retained revision returns an explicit
error instead of silently executing a changed contract. No old revisions are
fabricated or workflow runs restarted.

Regression coverage includes successive reads/deleted steps, independently
scoped execution contexts, planning/evaluation lookup without mode mutation,
historical prompts after plan deletion, legacy exact-ID reads and rejected
revision tampering. Workflow package and focused race tests pass locally.

## Original report tracking

The exact internal report is resolved in SQLite with its prior concern and
detail records preserved in a `platform_tracking_resolved` event. Resolution is
local implementation/test completion, not deployment sign-off. No workflow
plan, historical run, schedule or business data is repaired or rewritten.
