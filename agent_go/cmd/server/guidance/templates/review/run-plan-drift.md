Review whether the current plan and its dependent artifacts agree.{{if .Focus}} Focus: {{.Focus}}.{{end}}

Do this in the current authorized conversation. There is no separate reviewer, due claim, Gate or completion handshake. Ordinary edits use `builder-reference/references/plan-change-impact.md`: one combined compatibility check in the current agent. Run a full audit when the requesting person or reviewing Crew asks for it when the evidence warrants it. A stale `drift_review.needs_review` flag alone does not require this audit before testing. Merely reading this guide during an edit does not authorize launching a background audit.

Read the goal, current plan, step configuration and relevant plan-change history. Identify changed steps, downstream consumers and references left by deleted steps. Use existing Builder tools for authorized repairs and verify actual results. A reviewing Crew asks Builder for repairs through the shared entry points; do not create a separate repair agent. Preserve route ownership, existing access and the unreadable-input guard.

Record per-step evidence with `record_plan_drift_review`: pass for a demonstrated match, fixed after a verified repair, fail for an unresolved mismatch. Record the mismatch and follow up with Builder in the normal conversation. Use the live tool schema for change identifiers and workflow deletion checks. There is no worklist claim or reviewer-result tool.

Resolve the placeholder `<script-dir>` from `workflow.json.code_layout_version`: `code/<step-id>` for version 1, or `learnings/<step-id>` for a legacy workflow. Do not infer the version from folders or migrate code implicitly.

## Dependent artifacts

Keep this compatibility review bounded to changed surfaces and their consumers.
Do not repeat checks already completed in this turn, audit unrelated
architecture, or create verification work for an applied fix. A title-only edit
with no changed meaning may need only a compact compatible result. Apply
`builder-reference/references/step-description.md` to affected step prompts;
Record `step_prompt_quality` once and reuse that evidence unless
another materially changed prompt or dependency was not covered.

This checklist's job is dependent compatibility surfaces: schedule
cron/timezone/queue drift, eval/success-criteria coverage, downstream-step
field consumption, dead step/schedule references not already resolved by
the step-deletion audit, and duplicate control stores. It checks whether step
config, the schedules that drive the workflow, skills, saved code,
knowledge-base notes, database contracts, reports, evaluation, and recent run
evidence still match the current workflow, and flags missing or stale eval
coverage with an `Eval fix` owner label. Read the affected artifacts and database
contracts directly, or ask Builder for the relevant evidence. Saved review
receipts are history, not a required packet or a new reviewer dispatch.

For a suspected drift reproduced after a change or a material contract mismatch,
compare the current artifact/run evidence with up to three comparable retained
runs (same route/group and materially equivalent configuration). Read compact
receipts first; open raw traces only for the changed or suspicious step/attempt.
State an evidence limitation when fewer comparable runs remain.

Load `read_skill(skills=[{"name":"builder-reference","path":"references/assumption-audit.md"}])`. While tracing changed surfaces, identify dependent artifacts that preserved an old architecture, tactic, schema, metric, or execution assumption after the plan evolved. Raise consequential unresolved restrictions through the existing human-input request tools.

## Audit checklist

1. List `planning/changelog/changelog-*.json` in filename order and select entries where `artifact_review.done` is not true.
   - Changelog markers are the only artifact-review cursor. If a marker is absent,
     inspect the exact entry; do not rely on a separate presentation cursor.
   - If no cursor exists and more than 100 entries are unreviewed, inspect only the latest 100 and report that the older entries remain unreviewed.
   - Never advance the proposed cursor past an entry that was not fully inspected or safely cursor-backfilled.
   - **The changelog only records plan mutations** (`update_step` and
     the other typed plan-mod tools) — `planning/` is never a granted write
     path for any other tool, so `plan.json` itself is always fully and
     truthfully captured here. A direct edit to a step's own code
     (`<script-dir>/main.py`) or a shared doc (`db/README.md`) made via
     `files action=patch`/`update_workspace_file` is a different kind
     of change and does not produce a changelog entry, even when it happens
     in the same turn as a plan-tool call — the entry that turn produces
     describes only the plan-tool's own field change. When a changelog entry
     does flag a step as affected, step 3 below already directs inspecting
     that step's `main.py` directly, so the code change is still caught. The
     residual gap is narrower: an isolated code/doc edit with **no**
     accompanying plan-tool call in the same turn produces no changelog
     entry at all, so nothing routes that step into this checklist's
     cursor-selection step in the first place — this is a scope boundary of
     the changelog cursor, not evidence the checklist itself missed
     something once a step is in scope.
3. For each affected step, inspect only relevant current artifacts:
   - `planning/plan.json` and `planning/step_config.json`
   - Reuse checks already verified in this turn. Read current step drift receipts
     for their evidence, but inspect a changed surface when that evidence is missing
     or no longer applies. Do not defer the work to another reviewer.
   - the affected schedules in `workflow.json` — for each affected schedule, its cron,
     timezone, and the `messages` queue. The queue is what the scheduler
     actually sends, so it is a first-class contract with the plan, not
     configuration noise: read every message and resolve each to the plan step
     it drives.
   - `<script-dir>/main.py`, script metadata and relevant workflow-local skills.
     Check content staleness, locks, access settings and database query compatibility.
     Record one finding per root cause rather than duplicating the same step issue.
   - relevant `knowledgebase/notes/` content and KB access/contribution settings; treat `knowledgebase/context/` as read-only user-owned context
   - `db/README.md`, named DB tables/assets/contracts, and their writers/consumers — the existing verified check covers the DDL-vs-live-schema contract check;
     this checklist still owns writer/consumer disagreement across steps
   - `db/reports/index.html`, its internal views, SQL, and data contracts — the existing verified check covers whether each `window.report.query(...)` call still
     resolves; this checklist still owns data-contract/semantic disagreement.
     Flag a remaining `reports/report_plan.json` as an incomplete version migration
   - producer measurements and matching goal/success-criteria coverage — the existing verified check covers whether a
     measurement query still resolves; coverage gaps (an orphaned or missing
     measurement) stay this checklist's job
   - one representative recent run for changed runtime behavior when compatible
     evidence exists; pre-change artifacts are baseline only, not a reason to
     leave an applied fix awaiting a future run
   - for any changed status, strategy, feature flag, guard, routing rule, or
     other control value, trace the exact changed record to the current runtime
     reader. Inspect a resulting decision/output only when matching current-contract
     evidence exists; do not require a new run to establish static compatibility. If similarly named tables/files
     carry the same logical IDs, compare them and identify the canonical owner
     plus the required mirror rule. A clean changelog/file diff is not enough
     when the runtime reads a different store.
4. Record a finding only when evidence shows drift, including:
   - code, paths, fields, selectors, tool/API usage, or validation still implement an old contract
   - stale code locks or stale skill selections after a material change without review evidence
   - skills or KB preserve obsolete behavior or agent-inferred policy
   - DB writers, report consumers, or eval consumers disagree on schema or semantics
   - a change updated a plausible but non-canonical store, or duplicate control
     stores disagree so the allocator/router/executor cannot observe the repair
   - report/eval checks use stale artifacts, fields, thresholds, or run identity
   - a change breaks explicitly required evaluation coverage, or an eval still
     references a retired criterion/producer; do not invent an eval requirement
     merely because an evaluation file is empty
   - new steps lack required dependent wiring (a deleted step's own dangling
     references are the per-step check's workflow-level deletion audit's job now — report
     one here only if the deletion check found no due `__workflow_drift_review__`
     candidate at all yet a dangling reference still exists, which would
     itself be evidence the workflow-level flag was missed)
   - a schedule-local procedure that duplicates durable plan behavior, lacks
     `direct_messages_reason`, or claims canonical step-level learning,
     validation/retry, repair, or run attribution that it does not receive.
     Direct schedule conversation is a supported execution model, so do not
     report it merely because it is long. Compare reuse, inputs/outputs,
     side effects, approval and failure boundaries; report the missing route
     only when those facts show the work belongs in the canonical plan.
   - a schedule message that drives no plan step, or a plan step no schedule message reaches and no other step invokes — dead work, or a queue that was
     never updated after the step was added
   - execution order or grouping that exists only in the message queue while
     `plan.json` leaves order/groups unset. The plan is then not runnable on its
     own, and the queue silently owns the sequence
   - a message queue that only restates steps the plan already orders, which is
     duplication that will drift the next time either side changes
   - schedule cron/timezone that contradicts what `soul.md` or the plan claims
     about cadence or the window the work is valid for
   - a schedule message that should invoke canonical work but bypasses
     `run_full_workflow`/`execute_step`; use `validate_plan_change` when a repair
     changes plan references or dependency wiring
5. Include clean checks briefly. Do not manufacture drift merely because an artifact exists.

## Result

Summarize what was checked, changed and verified, and what remains unresolved, with exact evidence. Record useful work and its evidence in the existing plan changelog or requesting conversation. Measurements and their history belong in the database. A successful run or a delivered message alone does not prove the goal improved. Do not mark an uninspected change reviewed or require a fresh audit before every normal step run.
