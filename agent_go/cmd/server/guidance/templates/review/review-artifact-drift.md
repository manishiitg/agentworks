**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

Use this to run a manual artifact-drift audit after plan or configuration
changes — the on-demand equivalent of Pulse's scheduled pass, sharing the
exact same candidate collector, repair contract, and completion writer as
`plan_drift_review`, not a separate parallel implementation of it.{{if .Focus}} Focus especially on: {{.Focus}}.{{end}}

Run this full audit for scheduled Pulse or an explicit user request. Ordinary plan edits and targeted test runs use `builder-reference/references/plan-change-impact.md`: one combined compatibility check in the current agent after related edits. A stale `drift_review.needs_review` flag alone does not require this audit before testing. Merely reading this guide during an edit does not authorize launching a background audit.

## Execution model

- In Pulse, the Engineering review may include this checklist when artifact-drift
  evidence is selected.
- Perform the audit yourself in this turn. Never launch another reviewer or a
  nested maintenance agent; you may use your own subagents for parallel reading,
  and you stay responsible for the result.
- **Part 1 (below) has real repair authority, identical to `plan_drift_review`'s
  own scheduled turn** — read/apply/verify/persist exactly as
  `plan-drift-review.md` describes, because you ARE running that same
  contract, just dispatched manually instead of by Pulse Gate. **Part 2 (the
  remaining checklist) stays strictly read-only**, as before: it must not edit
  files, mutate the plan/config, mark changelog entries, or mark Pulse module
  state — everything it finds becomes a recorded finding for the parent to act
  on, never a same-turn edit.

## Part 1 — run plan_drift_review's real procedure now

First call `record_pulse_result(pulse_run_id="current", module=
"plan_drift_review", result="running", note_only=true, manual=true,
reason="manual /run-plan-drift invocation")`. This starts the manual
review and establishes its due claim through the same tool used to complete it.

If that initial `record_pulse_result` refuses because a real scheduled Pulse pass is
already mid-flight for `plan_drift_review` (a genuine, rare collision — it
means Gate is actively running this same module right now under a different
run), stop here: state that plainly and do not proceed into repair work whose
receipt could not be recorded anyway. Do not retry in a loop; the operator can
re-run this command once the scheduled pass finishes.

Call `get_pulse_state(view="module")`. Its `plan_drift_candidates` array is
the exact same durable candidate set the scheduled Pulse pass reads — every
step with no `drift_review` record or `needs_review == true`, plus (per
`plan_drift_candidates_note`) a possible workflow-level candidate with
`step_id == "__workflow_drift_review__"` when a step deletion since the last
audit still needs its dependent-artifact fallout traced.

If the array is non-empty, load
`read_skill(skills=[{"name":"builder-reference","path":"references/plan-drift-review.md"}])`
and follow its steps 1-7 (and its workflow-level deletion audit section, for
the `__workflow_drift_review__` candidate if present) exactly, for every
candidate — applying and verifying safe workflow-owned fixes directly,
routing only genuine human decisions or platform-owned boundaries, and
persisting via `record_plan_drift_review` and `record_pulse_result
(module="plan_drift_review", ...)`, precisely as that module's own scheduled
turn would. This is not optional evidence to defer past a stale record — it
is the same due work Pulse would otherwise run on its own schedule, done now
because the operator asked for it directly.

If the array is empty, state that plainly (nothing plan_drift_review-scoped
is currently due), close this manual claim with
`record_pulse_result(module="plan_drift_review", pulse_run_id="current",
result="done", reason="No plan drift candidates require review", ...)`,
and move on to Part 2 below. Do not leave an empty manual pass pending.

## Part 2 — the read-only checklist for affected dependencies

Keep this compatibility review bounded to changed surfaces and their consumers.
Do not repeat Part 1's prompt-quality or type-specific checks, audit unrelated
architecture, or create verification work for an applied fix. A title-only edit
with no changed meaning may need only a compact compatible result. Apply
`builder-reference/references/step-description.md` to affected step prompts;
Part 1 records `step_prompt_quality`, and Part 2 reuses that evidence unless
another materially changed prompt or dependency was not covered.

This checklist's job is what Part 1 does not cover: schedule
cron/timezone/queue drift, eval/success-criteria coverage, downstream-step
field consumption, dead step/schedule references not already resolved by
Part 1's deletion audit, and duplicate control stores. It checks whether step
config, the schedules that drive the workflow, learnings, saved code,
knowledge-base notes, database contracts, reports, evaluation, and recent run
evidence still match the current workflow, and flags missing or stale eval
coverage with an `Eval fix` owner label. Read only matching typed state
through `get_pulse_state(view="review")` with `module=technical_review`, using
the managed SQLite-backed tools and structured finding lifecycle; do not query
SQLite directly or inspect/create Pulse presentation artifacts.

For a suspected drift reproduced after a change or a material contract mismatch,
compare the current artifact/run evidence with up to three comparable retained
runs (same route/group and materially equivalent configuration). Read compact
receipts first; open raw traces only for the changed or suspicious step/attempt.
State an evidence limitation when fewer comparable runs remain.

Load `read_skill(skills=[{"name":"builder-reference","path":"references/assumption-audit.md"}])`. While tracing changed surfaces, identify dependent artifacts that preserved an old architecture, tactic, schema, metric, or execution assumption after the plan evolved. Keep consequential unresolved restrictions under Pulse's Assumptions challenged.

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
     `diff_patch_workspace_file`/`update_workspace_file` is a different kind
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
   - **Deferral to plan_drift_review (Part 1 above)**: never re-derive
     `report_query_compatibility`, `validation_schema_db_rules`,
     `validation_schema_file_rules`, `scripted_code_db_queries`,
     `scripted_best_practices`, `message_sequence_best_practices`,
     `orchestrator_best_practices`,
     `routing_best_practices`, `branch_best_practices`,
     `db_readme_contract`, or `orphaned_tables` by hand. Part 1 already ran
     the real `plan_drift_review` procedure earlier in this same turn for
     every due candidate, applying and verifying fixes directly — read the
     step's now-current `agent_configs.drift_review` record in
     `step_config.json` as the authoritative, just-updated result. Only
     inspect one of these surfaces directly yourself when the step was not a
     Part 1 candidate at all (already clean and current) and its evidence
     still looks insufficient for the entry under review.
   - the affected schedules in `workflow.json` — for each affected schedule, its cron,
     timezone, and the `messages` queue. The queue is what the scheduler
     actually sends, so it is a first-class contract with the plan, not
     configuration noise: read every message and resolve each to the plan step
     it drives.
   - `<script-dir>/main.py`, script metadata, per-step learning metadata, and relevant `learnings/_global/` guidance — plan_drift_review's
     `scripted_code_db_queries` check only covers whether a scripted step's SQL
     still resolves against the live schema; content staleness, stale locks,
     and access-mode appropriateness stay this checklist's job (or the
     judgment pass inside plan_drift_review's own reviewer turn — do not
     duplicate a finding both places for the same step)
   - relevant `knowledgebase/notes/` content and KB access/contribution settings; treat `knowledgebase/context/` as read-only user-owned context
   - `db/README.md`, named DB tables/assets/contracts, and their writers/consumers — deferral above covers the DDL-vs-live-schema contract check;
     this checklist still owns writer/consumer disagreement across steps
   - `db/reports/index.html`, its internal views, SQL, and data contracts — deferral above covers whether each `window.report.query(...)` call still
     resolves; this checklist still owns data-contract/semantic disagreement.
     Flag a remaining `reports/report_plan.json` as an incomplete version migration
   - producer measurements and matching goal/success-criteria coverage — deferral above covers whether a
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
   - stale code locks or stale learning access after a material change without review evidence
   - learnings or KB preserve obsolete behavior or agent-inferred policy
   - DB writers, report consumers, or eval consumers disagree on schema or semantics
   - a change updated a plausible but non-canonical store, or duplicate control
     stores disagree so the allocator/router/executor cannot observe the repair
   - report/eval checks use stale artifacts, fields, thresholds, or run identity
   - a change breaks explicitly required evaluation coverage, or an eval still
     references a retired criterion/producer; do not invent an eval requirement
     merely because an evaluation file is empty
   - new steps lack required dependent wiring (a deleted step's own dangling
     references are Part 1's workflow-level deletion audit's job now — report
     one here only if Part 1 found no due `__workflow_drift_review__`
     candidate at all yet a dangling reference still exists, which would
     itself be evidence the workflow-level flag was missed)
   - a schedule-local procedure that duplicates durable plan behavior, lacks
     `direct_messages_reason`, or claims canonical step-level learning,
     validation/retry, repair, or Pulse attribution that it does not receive.
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

## Reviewer result

This section covers Part 2's own findings only — Part 1 already persisted its
own `plan_drift_review` result (`record_plan_drift_review` per candidate,
`record_pulse_result(module="plan_drift_review", ...)` once) earlier in this
same turn; do not fold Part 1's checks or findings into the package below.

Return one compact review package containing:

- `module=technical_review`, `verdict`, and `next_check`
- cursor before and proposed cursor after
- changelog files and zero-based entry indexes fully inspected
- affected steps inspected
- findings ordered by severity; each includes no invented identifier, a plain-
  language root-cause summary, exact evidence, recommended owner, and
  `user_judgment_required` with reason. Do not create separate recommendation,
  verification, impact, or disposition records
- clean checks
- exact proposed marks grouped as `clean`, `findings`, or `cursor-backfill`
- for every proposed non-backfill mark, `surface_reviews` covering
  `downstream_steps`, `validation`, `evaluation`, `reporting`, `database`, and
  `learnings_and_knowledge`, each with one disposition (`updated`,
  `already_compatible`, `not_applicable`, `blocked`, or `broken`) and concrete
  evidence; `blocked` and `broken` also require durable Pulse `issue_ids`
- any blocked entry that prevented further cursor advancement

Before returning, persist each material Part 2 finding through
`record_pulse_finding` under `technical_review`, reusing its canonical issue.
Create and link a typed `create_human_input_request` when the recommendation
needs a human decision; do not block the review with a chat question.
This persistence does not authorize Part 2 implementation edits, changelog
marks, or a separate Technical Review terminal receipt. The slash-command
parent only presents your result; do not leave findings for it to persist.
A later Pulse Fixer/workshop action can apply bounded approved repairs and
call `mark_changelog_artifact_reviewed` for only the exact inspected entries
with all required surface reviews. Proposed marks are not completed marks.
That action closes applied fixes without waiting for later execution and
reopens them only when new evidence reproduces the defect.
Do not edit or delete changelog JSON directly or create a second cursor/state file.
