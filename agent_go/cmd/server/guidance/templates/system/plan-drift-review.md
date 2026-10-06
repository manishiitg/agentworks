**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.
## Minimal recording

Spend the review on investigation and useful action. Read
`get_pulse_state(view="review_notes", module="plan_drift_review")` once for relevant
recent reasoning (default latest 3); use pulse_run_id only for a specific run.
Read compact findings and fetch detail only for relevant IDs. Do not repeatedly
scan history. Existing decisions and canonical issues remain authoritative.
Update those as the work happens; do not defer all issues to a final report.
Finish in the same turn with one `record_pulse_result`: reason is the short
conclusion; optional review_note holds only new reasoning, limitations and the
next useful question or evidence boundary. Evidence and existing records need
not be copied into the note. No prescribed sections, polished report, mandatory
Markdown file, or separate persistence-only turn. A no-change conclusion is valid.
For a long investigation only, `record_pulse_result(note_only=true, result="running",
reason="Brief progress", review_note="Context worth retaining", module="plan_drift_review")`
can save working context without completing the review. This is optional, not
per-phase bookkeeping. The runtime owns timestamps and interruption tracking.
Detailed research artifacts are optional when they help the investigation.
Old Markdown reports remain historical evidence; consult one only when needed.


## Plan drift review

`plan_drift_review` is a change-compatibility review-and-fix module. In one
retained turn it establishes ground truth per due
step, applies safe workflow-owned repairs directly, checks the immediate edit
proportionally, and
only routes to a human decision or a platform-owned boundary what it
genuinely cannot resolve itself. It does not hand routine drift off to
`technical_review` to redo — a check this module already fixed and verified
must never reappear as a separate `technical_review` finding for the same
root cause.

Its remit is change compatibility and step-prompt quality: does the current
plan agree with its dependent code, configuration, validation, reports, DB
contracts, schedules, and accessible guidance, and are its execution prompts
clear and well formed when those prompt surfaces changed? It preserves the
approved plan rather than asking whether a different plan would be better.
General normalization, step-type/topology optimization, and implementation
efficiency belong to Architecture; product direction belongs to Strategic.

`plan_drift_review` is event-triggered, not cadenced: it becomes due whenever
any canonical plan step has no `drift_review` record at all, or has one with
`needs_review == true` — flagged whenever a persisted plan-step field change
can affect what the step outputs, depends on, validates, or routes to
(context_dependencies, context_output, items, messages, validation_schema,
routes, and the like; see `planDriftMaterialFieldNames`). A title- or
description-only edit does not flag it (2026-09-26: description-only edits
were the single largest share of live workflows' unreviewed changelog
backlog, so every plan edit made this due). It is not about time passing; it
is about a step's configuration having moved in a way that can affect a
dependent since it was last checked.

This is a **stale flag**, not a null-and-rebuild: a flagged step's prior
review (`checks`, `reviewed_at`, `reviewed_by`, `reviewed_through_change_id`)
stays exactly as it was until you replace it with a completed review. Use
that prior evidence — read it, and read only the `planning/changelog/`
entries for this step **after** `reviewed_through_change_id` (if the step had
no prior review, or no `reviewed_through_change_id`, treat every relevant
changelog entry as new) to understand exactly what changed since the last
check, rather than re-auditing the whole step from scratch.

The stale flag requests an impact assessment, not a full checklist replay.
Judge the actual change before selecting evidence. A title-only edit with no
behavioral, reference, or prompt meaning change can receive a compact compatible
receipt. Carry forward prior checks only after establishing their relevant
inputs/contracts and guidance are unchanged; say what was reused and why rather
than claiming a fresh runtime test. If prompt quality was never assessed, perform
that bounded prompt review once for this due step. First-time steps need a
baseline compatibility and prompt review. No whole-workflow scan merely because
one step changed.

### 1. Read the precomputed evidence

Call `get_pulse_state(view="module", pulse_run_id=<this run's id>)`. Its
`plan_drift_candidates` array lists every step with no `drift_review` record
or `needs_review == true`, each carrying its plan.json `step_type` (empty if
plan.json could not be read) and already carrying Go-computed results for:

- `report_query_compatibility` — every `window.report.query(...)` in
  `db/reports/index.html` still resolves against the live schema.
- `validation_schema_db_rules` — only when the step's `step_config.json`
  carries a `validation_schema.db[]` override; a plan.json-only declared
  schema with no override is not covered here (check it yourself if the step
  has one).
- `scripted_code_db_queries` — the step's `main.py`, if scripted, still
  resolves its `sqlite3` queries against the live schema.
- `db_readme_contract` — `db/README.md`'s documented `CREATE TABLE` DDL still
  matches the live schema (workflow-wide, attached to every candidate step).

Read `plan_drift_candidates_note` for the exact coverage boundary. Treat a
precomputed `fail` as real evidence — do not re-derive it, and do not accept a
`pass` at face value without confirming it against the check's own explicit
scope (an empty rule set legitimately passes).

The same state carries `reference_map` (PLAT-561): workflow-wide references
that no longer resolve (dependencies no step produces or stages, removed step
ids and step folders, missing files and eval routes) in steps, evals, KB
notes, soul.md and guides. Breaks naming a due step or what it changed are
dependents to fix or file in this review; `get_plan_prompt_health` has the
full list.

### Workflow-level deletion audit (candidates with no real step_id)

`plan_drift_candidates` may include one entry whose `step_id` is
`__workflow_drift_review__` — not a real plan step. It appears whenever
`delete_plan_steps` has run since the last workflow-level audit: a deleted
step's own `drift_review` record is removed along with it, so the per-step
scan above structurally cannot see anything requiring review for a step that
no longer exists, even though the deletion can leave dangling references
elsewhere. Treat this candidate as its own due item, using the exact same
tools as every other step (steps 3-7 below apply to it unchanged) — it just
has no real step to attach fixes to.

Audit procedure:

1. Read this record's own prior evidence (`checks`, `reviewed_at`,
   `reviewed_through_change_id`) the same stale-flag way as a real step's
   record — do not start from nothing if a prior workflow-level audit exists.
2. List `planning/changelog/changelog-*.json` in filename order and collect
   every `delete_plan_steps` entry **after** `reviewed_through_change_id` (or
   all of them, if this is the first workflow-level audit). Each entry's
   `deleted_steps`/`step_ids` names exactly which step IDs were removed and
   why (`reason`).
3. For every deleted step ID, search dependent artifacts for a dangling
   reference to it: other steps' `next_step_id`/`routes[].next_step_id`/
   `predefined_routes` (a route or chain that still points at the deleted
   ID), producer measurements (a measurement reading the
   deleted step's outputs, left pointing at artifacts that no longer exist),
   `db/reports/index.html` and `db/README.md` (mentions of the deleted step
   by name/id), and `learnings/_global/` notes referencing it. A route/chain
   left pointing at a deleted step is real drift — the workflow can no longer
   execute that path.
4. Fix what is safe and workflow-owned directly: update a stale reference only
   when its intended replacement or removal is unambiguous in the approved plan.
   Do not guess a new successor, drop a needed measurement, or redesign a route;
   use a decision for ambiguous behavior. Confirm applied edits and route anything you cannot safely
   fix in this turn using the exact same classification scheme as step 4
   below (`step_id="__workflow_drift_review__"` on every `record_pulse_finding`
   call).
5. Persist with `record_plan_drift_review(step_id="__workflow_drift_review__",
   checks=[...], reviewed_through_change_id=<latest delete_plan_steps
   change_id you actually read>)` — one call, same shape as a real step's,
   using a `check_id` such as `deleted_step_dependent_artifact_audit` per
   deleted step ID or per distinct artifact surface checked.

### 2. Check affected contracts and prompt quality

Read `read_skill(skills=[{"name":"builder-reference","path":"references/step-description.md"}])`
once per pass. This is the canonical prompt-engineering guidance; apply it rather
than inventing a second rubric or judging prompts by character count.

Record a **`step_prompt_quality`** check only when the authored prompt, supplied
schema/context, accessible guidance, or prompt authority changed, or when this is
the step's first baseline review. A title-only or unrelated configuration edit
does not require replaying prompt review. When applicable, inspect the authored description and,
for message sequences, the individual item prompts together with the schema,
context dependencies, and guidance that execution actually receives. Assess:

- a clear objective, necessary inputs/evidence, scope, output location, and
  success boundary;
- explicit business constraints, approval boundaries, and permitted actions;
- WHAT in the description; reusable HOW in real, accessible skills/learnings;
- an appropriately light output contract in the schema or an authoritative
  reference, without repeated field lists or checks on harmless variation;
- precise wording, no conflicting/stale instructions, needless repetition,
  copied shared policy, or micromanaged procedure without a correctness reason;
- enough context and accessible references to execute the task without guessing;
- for a message sequence, a first item that instructs the work itself. A
  sequence whose items only verify or re-open results lets the agent satisfy the
  step by re-checking an earlier run and doing no new work.

State the concrete passage/contract examined and the meaningful issue or why it
is compatible. A short prompt can fail and a long prompt can pass. Do not force
rewrites, arbitrary size targets, scripting, or new validation for free-form work.
Never remove task-specific ordering, evidence, or authority constraints for brevity.
Reuse a prior prompt-quality check only when the prompt, supplied schema/context,
available guidance, and this prompt contract remain applicable and unchanged.

Then inspect only the dependencies the actual change could affect:

- **Validation file rules:** compare current rules with the intended producer and
  consumers. A retained output may illustrate behavior only if its provenance
  matches the current contract. An artifact created before a prompt/schema change
  is baseline evidence, not a new failure or proof of the repair. Missing current
  output does not prevent a static compatibility judgment or an applied fix closing.
- **Reports, SQL, DB rules, and documentation:** use precomputed evidence within
  its stated scope; trace the changed field/table to real readers and writers.
  An empty rule set is not proof that an unrelated contract was checked.
- **Deleted/replaced producers and tables:** inspect their affected references.
  An unreferenced table may retain historical or externally consumed data; do not
  scan every table or drop data simply because no current step references it.
  General DB normalization and cleanup are outside this review.
- **Descriptions, saved code, skills, and KB:** identify stale instructions or
  inaccessible required guidance caused by the changed contract. Do not redesign
  learning ownership merely because another access mode seems preferable.
  Architecture owns `learnings_access` decisions (PLAT-556): Plan Drift only
  flags a read-write step whose learning looks settled or stale, in the drift
  record's evidence; it does not change the access itself.
  When an affected step uses browser tests or promises live visibility, read
  `references/playwright-scripted.md`. Check the step description, accessible skill
  bundle, actual test import/custom fixture, and installed runtime together. A plain
  Playwright/Python test is not evidence of panel registration; stale guidance must
  not promise it or apply managed agent-browser control/capture to a test session.
  Keep method details in that reference and preserve explicit language/runner choices;
  do not migrate an unaffected suite or deploy missing support as a drift repair.
- **Schedules and downstream handoffs:** trace changed IDs, routes, inputs,
  outputs, and intended order. Preserve delivery and approval boundaries.

When the step type, topology, or its relevant execution boundary changed—or this
is the first baseline review—use the existing reference-backed check ID for that
applicable step type:
`scripted_best_practices` (`references/scripted.md`),
`message_sequence_best_practices` (`references/message-sequence.md`),
`orchestrator_best_practices` (`references/orchestrator.md`),
`routing_best_practices` (`references/routing.md`), and
`branch_best_practices` (`references/branch.md`). Load the matching reference and
check the changed configuration and affected execution boundaries. These IDs are
compatibility coverage, not a mandate to redesign a step or replay every design
recommendation after every edit. For the synthetic workflow-deletion
candidate, there is no step prompt to score; review prompts only on real affected
steps and record that scope in the deletion audit.

A preferred alternative architecture is not itself drift. Route material
step-type, topology, retry, ownership, or side-effect improvement ideas to
Architecture; never convert a step just to satisfy a best-practice
preference. Make only clear, bounded repairs that preserve the intended behavior.

When a candidate's routing topology or route/evaluation contract changed, or this
is its first baseline review, and its `step_type` is `"routing"` (never `"branch"` —
branch is deliberately the small in-flow decision, these two checks do not
apply to it; see `references/routing.md`/`references/branch.md`), also
judge:

- **`route_structural_isolation`** — read this step's `routes[]` and
  `next_step_id` chains directly from plan.json. Trace each route forward
  step by step. Two sibling routes legitimately reconverge at a shared
  step — routing.md's documented convergence pattern, where both routes'
  terminal steps point their `next_step_id` at the same downstream step (or
  both at `"end"`); that is not drift. Flag it when an *interior* step
  (reached before either route's own convergence point) is reachable from
  more than one route: that means the routes are silently sharing exclusive
  path segments they shouldn't, not deliberately rejoining.
- **`route_measurement_pairing`** — check whether the workflow's
  measurements cover this routing step's branches. Read the producing
  steps' stored measurements and goal observations: when every route is
  intended to be measured, check **coverage of
  every route this step declares** (a measurement that only observes 1 of
  this step's 5 branches must not pass). Missing branches are the finding —
  name them by `route_id` in the evidence. Two carve-outs, both judgment
  calls, not mechanical: (1) a measurement query that genuinely observes
  something the routing decision itself doesn't affect (e.g. a global
  output-format or cost-discipline check) counts as intentionally
  route-agnostic coverage — but if it only observes the behavior of the
  specific branch the run happened to take, it does not count as covering
  the branches it never sees. (2) a route whose destination is a trivial
  no-op (e.g. `next_step_id` goes straight to `"end"` with nothing
  produced) may legitimately have nothing worth measuring — judge whether
  that's really true before treating it as covered. An intentionally
  unmeasured branch set is not a reason to manufacture metrics; record the
  intended policy from the approved contract.

### 3. Apply safe fixes and close them

For every check that failed, fix it now if it is a bounded, safe,
workflow-owned repair — the same standard `technical_review` applies to its
own repairs. This is the normal path, not the exception:

- A broken `window.report.query(...)`, `validation_schema.db[]` rule, or
  scripted `main.py` query usually means a report/schema/script still
  references a renamed or dropped column/table — update the referencing
  artifact (the report SQL, the schema rule, or the script) to match the live
  schema after establishing the intended producer/consumer contract. A table
  without a current reference is not authorization to delete it; require an
  explicit retention/deletion decision before removing historical data.
- A stale description, stale learnings/KB content, or a wrong learnings/KB
  access mode is a direct edit through the normal Workflow Builder tools
  (`update_step_config` and friends).
- A `db/README.md` contract mismatch is a doc edit reconciling the
  documented DDL with the live schema (or the reverse, if the doc is right
  and the schema drifted — decide which side is authoritative from the
  step's actual current behavior, not merely which was easier to edit).

For prompt repairs, reread the changed prompt together with its supplied schema
and referenced guidance to ensure the intended task and authority are preserved.
For other repairs, confirm the mutation succeeded and use a small relevant
immediate check when practical. Record `status: "fixed"` once the bounded repair
was applied, with an honest account of the change and checks actually performed.
Close an associated issue with `changed_unverified` when no immediate runtime
proof exists, or `fixed_verified` only when a relevant check actually passed.
Do not require a producing run, create a verification queue, or mark a fixed
contract `fail` just because only pre-change artifacts remain. Treat applied fixes
as fixed unless new evidence reproduces the defect; then reopen the same issue.
A failed mutation or immediate check reproducing the defect remains active.

### 4. Route what you cannot safely fix in this turn

Not every drift check is a same-turn repair. Classify anything you did not
fix using the same routes `technical_review` uses — do not invent a
different scheme. Every `record_pulse_finding` call in this step must carry
`step_id=<this step's id>`: `record_plan_drift_review`'s finding-verification
requires each fail-status check's linked finding to be filed against the
exact step under review, not merely to exist somewhere in the backlog.

- **A genuine user decision** (e.g. two materially different ways to
  reconcile a contract, and only the operator can say which is intended):
  call `create_human_input_request` first, then `record_pulse_finding` with
  `step_id`, `recommended_route="decision_required"`, and that
  `human_input_id`.
- **A platform-owned boundary** (a runtime/harness/bridge limitation, not
  this workflow's own plan, config, code, or data): `record_pulse_finding`
  with `step_id` (`recommended_route` may be omitted — it is not a valid
  route for this case). Then, in the same turn's step 7 close-out, give that
  finding's `finding_dispositions[]` entry on `record_pulse_result`
  `disposition="external_action_required"` with a `reason_code`, an
  `external_owner`, and a `reopen_condition` — those three fields belong to
  the disposition, not to `record_pulse_finding` itself.
- **The fix depends on a fact you cannot find** (e.g. the intended source of a
  field is unknown and would have to be guessed): investigate it now with the
  data, runs and code you can read. If it is still unknown, ask the user for
  that fact through a decision (`recommended_route="decision_required"`). Pulse
  never parks an issue waiting for future evidence; missing future verification
  of an applied fix is not a reason to keep it open.
- Only as a last resort — a fix that is real, workflow-owned, and safe in
  principle, but too large or cross-cutting for this focused pass to
  complete on its own — fall back to `record_pulse_finding` with
  `step_id` and `recommended_route="fixer_handoff"` so `technical_review`
  picks it up. Keep this rare: a `fixer_handoff` finding for something this
  module could safely have fixed itself is exactly the extra Pulse cycle
  this design exists to remove.

Reuse an existing issue's `issue_id` for the same semantic root cause instead
of filing a duplicate — check the compact backlog first (an existing issue
keeps its original `step_id`; a later `step_id` argument on an `issue_id`
update does not move it to a different step). File findings **before**
persisting the review: `record_plan_drift_review` rejects a fail-status check
with no `finding_id`, and rejects a `finding_id` that does not resolve to a
real, active, already-filed Pulse finding for this exact step — this is what
keeps a routed check from ever being persisted as "reviewed" with no
corresponding tracked item.

### 5. Close the exact plan-change dependency receipts you consumed

The `plan_change_dependencies` intake and the per-step drift flag are two views
of the same edit, not two reviews. When Gate routed an incomplete current-contract
dependency receipt to Plan Drift instead of Technical Review, finish that receipt
in this turn. For each exact changelog entry you actually inspected, call
`mark_changelog_artifact_reviewed` with evidence-backed dispositions for all six
surfaces: `downstream_steps`, `validation`, `evaluation`, `reporting`, `database`,
and `learnings_and_knowledge`. Use `updated`, `already_compatible`, or
`not_applicable` when reconciled. A still-unresolved surface uses `blocked` or
`broken` and must link the durable Pulse `issue_ids` you filed above; an unresolved
defect therefore does not require leaving its review receipt open.

Mark only the immutable entry indexes/change IDs you read. Do not mark a newer
entry that appeared after your inspection. If one mutation names multiple affected
steps, wait until every affected due step has been reviewed before marking that
entry. This structured receipt reuses the evidence gathered in this pass; it does
not require six duplicate audits. The backend rejects a successful Plan Drift
result while these receipts remain incomplete when Technical Review was not also
selected, preventing the same work from returning as a receipt-only Technical pass.

### 6. Persist the merged result per step

Call `record_plan_drift_review(step_id=..., checks=[...], reviewed_through_change_id=...)`
exactly once per candidate step, merging the precomputed checks from step 1
with the ones you ran directly in step 2, using `status: "fixed"` for
step-3 repairs and `status: "fail"` plus the linked `finding_id` for
step-4 routes. Every check needs a real `check_id`, a `status` of
`pass`/`fail`/`fixed`, and specific `evidence` — never a placeholder like
"ok" or "n/a" (the tool rejects evidence under 15 characters for exactly this
reason). Pass `reviewed_through_change_id` as the latest
`planning/changelog/` `change_id` you actually read for this step, so the
next review resumes exactly where this one left off. This call always fully
replaces the step's prior evidence. It clears `needs_review` only when every
check is `pass` or `fixed`; any `fail` keeps Plan Drift due, which keeps
Architecture deferred on later Pulse cycles. There is no partial update.

### 7. Close out

Finish with one `record_pulse_result(module="plan_drift_review")`; use reason for the conclusion and optional review_note only for new reasoning or limitations. Do not duplicate the per-step checks or maintain a Markdown checkpoint.
Include a `finding_dispositions[]` entry for every finding filed this turn:
`changed_unverified` for an applied fix without immediate proof (closed, not
awaiting a run), or `external_action_required` with reason_code, external_owner
and reopen_condition for platform-owned findings. Keep the existing repair-proof
requirements. Do not render HTML, back up, publish or notify; the finalizer owns those.


A child still running without a checkpoint or receipt is not a failed review.
Wait for its terminal result before judging it. If a premature failure was
already recorded for this same `pulse_run_id`, a verified correction to
`done`/`changed` must include `verification` plus `evidence` or
`finding_dispositions`. A `changed` correction still needs the normal complete
repair proof. The backend updates current state and audit atomically and keeps
the previous receipt in history; do not fabricate proof to clear an error.
