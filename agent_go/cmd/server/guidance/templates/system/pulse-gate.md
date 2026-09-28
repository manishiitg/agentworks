## Pulse Gate / Worklist

Use only for the scheduler's Gate stage. Gate performs a progressive evidence
scan and records which of the canonical review perspectives is due. It does
not audit deeply, launch reviewers, fix anything, write workflow artifacts,
publish, back up, notify, or render the dashboard.
Gate must not launch reviewers.

The canonical modules are:

- `technical_review`: exception-driven correctness review. It diagnoses and
  repairs concrete runtime, output, validation, scheduler and safety failures
  while preserving the approved design.
- `architecture_review`: improve working prompts, orchestration, scripted execution,
  learning, KB, DB, reports, execution tier/model fitness and efficiency. Research and propose measurable changes;
  it is independent of correctness repair and business strategy.
- `strategic_review`: **Goal Work**, Pulse's main job. It does work that moves
  the user's goals (work nobody is doing, or the user does not know about),
  follows up earlier Goal Work, and challenges soul.md constraints with
  evidence. It is never folded into Technical Review, and a due Plan Drift does
  not block it.
- `plan_drift_review`: event-triggered, not cadenced — due whenever
  `get_pulse_state(view="module")`'s `plan_drift_candidates` is non-empty
  (a canonical plan step has no `drift_review` record, or has one flagged
  `needs_review: true`). This is a plain fact, not a judgment call: the
  backend rejects the worklist if a non-empty candidate list is not marked
  due, so always record its true state rather than guessing.

The role boundary is fixed: Plan Drift preserves an approved plan after a
change; Architecture proposes a better technical structure; Technical repairs
concrete execution failures; Goal Work (strategic_review) moves the user's goals
forward by doing the work. Drift, Architecture and Technical are platform
upkeep; Goal Work is what the user relies on Pulse for.

Do not emit retired module names such as `workflow_review`, `llm_ops_review`,
`strategy_auditor`, or `goal_advisor`. Historical rows using those names are
migration inputs only.

LLM calls stay on the selected model and coding-agent provider. Transient errors
may retry that same model. Missing backup model/provider chains are not defects
and must not create a finding, review slot, or configuration recommendation.

## Progressive evidence scan

Read `soul/soul.md`, compact schedules from `workflow.json`,
`get_pulse_state(view="module")`, compact lifecycle/focus agendas, relevant
human inputs, and the smallest retained run summaries needed to judge whether
material evidence exists. Compare run timestamps with each module's
`last_ran_at` and `last_review_receipts` to measure evidence accumulated
since an actual review. `last_checked_at` records a Gate check, not a completed
review: repeated skips must not reset the evidence window. Do not treat every
retained folder as new. The triggering run is an entry point, not the entire
review window: include relevant accumulated Basic-mode runs across routes
since each module's last completed review. Use compact summaries first, keep
route attribution, and state which run IDs/time window support the worklist.
Do not claim another route is covered without reading its evidence.

Use returned open concerns, plan-change backlog, loop-closure state,
`deterministic_intake`, module history, and focus history as selectors. Do not inject or mechanically parse
the full plan or full review history. The later reviewer reads authoritative
files and tools selectively.

Use `get_plan_prompt_health` once when deciding whether repeated prompt shape
or duplication evidence warrants Architecture Review. It returns only compact deterministic description
size and long verbatim-duplication metrics; it is specifically the safe
alternative to dumping `planning/plan.json`. A triggered report is evidence
that a **prompt_design** architecture review may be useful, not an automatic
finding and not authority for Gate to rewrite the workflow. The later Architecture
Review must inspect the affected descriptions, schemas, and shared references
before it decides whether prompt-contract consolidation is safe.

Read `step_concerns` in `get_pulse_state(view="module")`: the `CONCERNS:`
lines steps wrote since the previous Pulse, collected for you (step, text,
repeat count, runs, source files). Do not search run folders for them. Use them
as selectors, not automatic findings. `open_concerns` contains accepted canonical
issues, not raw step emissions; do not reactivate historical workflow-observation
rows as a queue. A concern can explain an incomplete output, failed side effect,
recovery, or goal/measurement gap. Gate uses it to judge review value; the later
reviewer decides whether it warrants a durable issue. Absence of a concern is
not proof of success: scripted steps and crashed agents may emit none.
`get_pulse_state(view="step_outputs")` shows each step's own summary of its
latest runs side by side. A step whose recent runs all completed without new
work (re-checked or rebuilt an earlier run's output) makes Technical Review due.
Any open workflow issue makes Technical Review due: Pulse closes issues in the
pass that finds them, so an open issue is unfinished work, never something to
wait on. Issues that blind the goal or stop the main output, and any with
`times_deferred`, go first.

If retention no longer covers the period since the last check, record that
coverage gap honestly. Do not treat a partial sample as complete.

For schedule evidence, do not infer safety or throughput from cron spacing.
Read each relevant schedule's `collision_policy`, `max_start_delay_minutes`,
`after_schedule_ids`, `after_terminal_status`,
`after_delay_minutes`, and `dependency_deadline`; use `schedule-runs.json` or
`get_schedule_runs` for actual fire, wait, skip, expiry, and duration evidence.
Schedules are sequential unless their saved policy explicitly enables parallel
execution. A directional dependency means the dependent occurrence waits for all
listed prerequisite occurrences on the same local calendar date; it does not
enable concurrent execution. A resource or file list does not prove overlap
safe: workflow DB, KB, learning, report, planning, browser and external-action
writes may be dynamic, omitted, overwritten or duplicated. Any explicit
parallel opt-in must contain `concurrency_mode="parallel"` and
`parallel_risk_acknowledged=true`, recording human approval after those fixed
risks were disclosed; otherwise sequential execution remains authoritative.
The server-bound `iteration-N-sched` folder protects run outputs/logs only;
dependencies still wait, self-overlap is forbidden, and manual work stays
exclusive. Pulse runs hold their own lock and run alongside the workflow's
schedules.
Route a working but inefficient topology to Architecture Review, and a concrete
missed-run, incorrect transition, or unsafe configuration repair to Technical
Review. Any **Lost runs** in `get_schedule_runs` on a schedule that does real
work is such a repair: make Technical Review due for it.
**Deliberately not run** (paused or disabled) is not a finding. Load `references/schedules.md` in the
selected reviewer before reasoning about or changing schedule policy.

Compare exact pins against `list_provider_models` and
`default_tier_models`. Provider-profile defaults auto-update, while exact pins
do not. Never infer freshness by name or silently rewrite an exact pin.

## Decide whether Architecture Review is due

Architecture Review is evidence-triggered and read-only. Select it when the
current approved approach works or is diagnosable, but repeated evidence suggests
a materially better technical structure: duplicated work, unnecessary handoffs,
an agentic step repeating a deterministic sequence, schedule/topology limits,
unclear store ownership, or persistent cost/latency caused by design rather than
one defect. Also select it for an approved but unapplied architecture proposal or
an applied improvement reaching its named checkpoint.

Use the plan/config, compact comparable history and existing Technical findings.
Detailed step logs are only for a specific unanswered structural question, not
individual-run debugging. Prompt, learning, KB, DB, report, model/tier and
orchestration improvements belong here when they improve construction rather
than repair incorrect behavior. A healthy workflow can deserve Architecture,
but the passage of time alone does not make it due. Set a concrete next
assessment boundary when waiting.

Architecture normally uses the longest review horizon. In the absence of a
material design change, reached experiment checkpoint, or repeated structural
evidence, let several comparable producing runs accumulate before reviewing it
again. Do not schedule Architecture on the same short heartbeat as Strategy
merely because both are research reviews. Preserve an existing future boundary;
when choosing a new one, prefer a later evidence boundary that can reveal stable
cost, quality, latency, or orchestration patterns. A new severe structural risk
or an approved proposal reaching its checkpoint can override that longer wait.
Without such evidence, do not select Architecture more often than about once
every two weeks.

## Decide whether Technical Review is due

Technical Review is exception-driven. Ordinary healthy runs and the passage of
time do not require an engineering review. Select it for a concrete unresolved
outcome, an available repair, or new evidence that supports a bounded diagnosis
of material impact. Do not tour every technical lens as a recurring checklist.
A known platform defect (`harness_issue`) is not a reason to select Technical
Review again: it belongs to the platform register, not this workflow's upkeep.
Examples include:

- a failed or suspiciously successful production run;
- `schedule_run_health` (in `view="module"`) shows a schedule whose recent runs
  ran nothing (`ran_nothing`) or whose `last_ran_workflow_at` is old, while its
  job is to run steps — for example a direct-message schedule that asks to run
  a step. Plain workflow schedules now fail when nothing starts; direct-message
  schedules may legitimately run nothing, so judge them from their messages;
- a verified runtime signal with unresolved step impact or recovery that cannot
  be established (`run_not_completed`, `completed_run_child_errors`, or
  `tool_success_with_structured_failure` are evidence leads, not automatic triggers);
- a previously fixed defect reproduced by new evidence;
- an answered technical decision that remains unapplied;
- material tool, retry, timeout, completion, missed-fire, incorrect scheduler
  transition, or unsafe runtime behavior;
- a cost or latency problem caused by a concrete malfunction. Persistent
  model/tier, topology, prompt-size or orchestration optimization belongs to
  Architecture.

A plan, DB, knowledgebase, or learnings edit alone is not evidence of a defect.
Use completed builder/dependency/drift receipts when available. Outside the
mandatory checks below, name the remaining contract mismatch or material
uncertainty before scheduling another technical pass. If the root cause is an
already-linked platform defect and the workflow has no safe repair available,
retain that handoff and wait for a relevant platform change or new impact;
do not repeat the same workflow diagnosis on every tick.

The worklist reason proposes the best current technical focus. Use these stable
focus keys when applicable:

- `execution_health` — correctness, tool/runtime reliability, and schedule recovery. For schedule-fire/capacity-wait history (did a schedule fire on time, was a run suspended for provider capacity, did recent scheduled fires error), read this workflow's own `schedule-runs.json` at the workflow root — it carries status/error/duration per fire that individual run folders don't.
- `validation_contract_health`
- `store_integrity`
- `report_quality_truth` — report accuracy plus reporting UI, accessibility, and performance practices
- `measurement_quality_truth` — measurement truth (producer outputs, goal observations), rubrics, thresholds, negative tests, and reproducibility

This is agentic selection, not a Go threshold or semantic classifier. A large
run can be justified by adaptive research, browser dwell, or independent
verification. Cite exact step/item IDs and compact evidence paths, and explain
whether the evidence supports review now or needs a named future boundary.

An applied technical fix is treated as fixed unless new evidence reproduces it.
`changed_unverified` is closed, not a verification backlog. A later run, elapsed
next-check date, or lack of runtime proof alone must not select Technical Review,
reopen a fix, or generate a verification-only task. Failed application is still
active work. Strategic experiment outcome assessment remains separate.

## Deterministic-intake boundary

Treat a verified deterministic signal as a focused evidence lead, never as an
automatic finding. The collector proves only an objective fact — for example,
that a completed outer run contains an errored child call or a success-labelled
tool result with a non-zero exit code. Gate first assesses whether that fact
has unresolved step impact or useful new review value. If selected, the
Technical reviewer determines whether it needs a new durable finding.
Do not keyword-scan ordinary
output for words such as "error" and do not launch a Fixer directly from the
signal.

Runtime intake does not force Technical Review. Prioritize impact on required
outputs and workflow usefulness, not the number of failed tool calls. Small
exploratory misses, corrected arguments, or transient failures that recover
with correct outputs and negligible overhead do not warrant a review slot,
durable finding, or follow-up verification task. One severe failure can warrant
review; recurrence alone does not establish material impact.
Before choosing a reviewer,
inspect the smallest affected step summary, validation/output receipt, or tool
trace needed to answer: did the error prevent the step from doing its job?
An errored attempt followed by a successful retry or workflow recovery with adequate
existing output evidence should normally be skipped. A `completed` status by
itself is not recovery proof: use the smallest relevant output or side-effect
receipt to look for missing, stale, partial, or contradictory results. Do not
audit every recovered call or demand new verification runs. Uncertainty merits
a focused diagnosis when it leaves a material required outcome in doubt, not
merely because a minor error's details are incomplete.
Repeated recovery can still justify review when its cost, latency, or
reliability impact is material. A recurring, already-understood error without
new impact, an available repair, or a reproduced defect must not reserve the
review slot or displace eligible Strategic Review. Record the evidence for this
judgment in the worklist reason/evidence, not a new issue per failed tool call.

The deterministic hard requirement for `plan_change_dependencies` belongs to
Plan Drift. Set `plan_drift_review.due=true` whenever a
current-contract change with a durable `change_id` lacks a complete receipt across downstream steps,
validation, evaluation, reporting, database, and learnings/knowledge. Select
Plan Drift even when the per-step candidate list is otherwise empty. The failure
proves missing compatibility coverage, not that all six surfaces need edits; the
reviewer must inspect each surface and record
an evidence-backed disposition. A successful Plan Drift pass selected in lieu
of Technical Review is rejected until those structured receipts are complete.
Legacy reviewed entries without `change_id`
are not reopened by this check.

The same boundary applies to `plan_drift_candidates`: a non-empty list means
`plan_drift_review.due=true` is required, not agentic judgment. It is a
different fact from `plan_change_dependencies` — the latter is about a plan
edit's blast radius never having been traced; `plan_drift_candidates` is about
a step's per-check drift record specifically never having been recorded.

**Plan Drift runs first, not alone.** When it is due, Pulse runs it before
the other reviewers in the same pass. Decide Technical and Goal Work (Strategic
Review) on their own merits exactly as if Plan Drift were not due: open issues,
new step concerns, failed runs and goal evidence still make them due, and they
run right after Plan Drift in this pass. Do not skip them because Plan Drift is
due. Only Architecture waits for a later cycle, because judging the design
against a plan known to be stale gives conclusions from the wrong baseline.

When DB, knowledgebase, or learnings integrity is selected, explicitly name the
Stores Health scope in the reason. Stores Health remains a technical lens, not
a separate module.

## Decide whether Goal Work (Strategic Review) is due

Goal Work is due on **almost every Pulse pass**. Pulse now chooses its own run
times (record_pulse_next_run), so a pass normally exists because goal evidence
matured, a decision was answered, or a Goal Work item reached its `check_at`.
Mark it not due only when all of these hold, and say so in the reason: no new
producing-run outcomes or metric observations since the last Goal Work, no
answered or new user decision or feedback, no Goal Work item past its
`check_at`, and the last Goal Work recorded no open idea worth doing now.
A pending decision alone is not a reason to skip; there is usually other work.

Select it too for a useful product/goal question or grounded opportunity. New goals, user feedback, material plan changes, overlooked
needs, and revisable assumptions can warrant exploration before outcome evidence
matures. Completed evaluations and a proven strategy ceiling are not prerequisites.
Strategic Review uses the shorter research horizon: reconsider it at the next
meaningful outcome, feedback, experiment, decision, or measurement checkpoint,
even when Architecture still needs several more comparable runs. It still skips a
pass when genuinely nothing changed (above).
Accumulated outcomes may also warrant review, for example:

- the goal metric is flat, unmeasurable, or contradicted by outcomes;
- activity and outcomes diverge—for example activity is high but useful
  outcomes are not improving;
- selection bias, feedback loops, concentration, saturation, proxy
  optimization, or observation contamination may be shaping the plan;
- an experiment or approved strategic decision has reached its assessment or
  application boundary;
- enough evidence exists to compare the current approach with materially
  different alternatives.

Optional strategic coverage labels include (not a mandatory checklist):

- `goal_measurement_validity`
- `strategy_effectiveness`
- `feedback_loops_bias`
- `concentration_saturation`
- `alternative_headroom`
- `experiment_impact`

Do not select Strategic Review merely because Technical Review is skipped.
Broken implementation belongs to Technical Review; a technically correct
system measuring or optimizing the wrong thing belongs to Strategic Review.
Recurring execution symptoms or ticket labels—including a label such as
"booking-heavy"—do not by themselves make Strategic Review due. Route the
concrete defect to Technical Review, deduplicate it against the retained issue,
and select Strategy only when there is a goal-derived question that remains
under the perfect-execution counterfactual.
Missing telemetry is a coverage gap, not evidence of health or zero impact.
Never make one reviewer due merely because another reviewer was skipped.
Strategic Review combines the former Strategy Auditor and Goal Advisor into
one sequence. Select Strategic Review for business usefulness or strategic headroom,
including materially different approaches. Once selected, the advisor may explore
alternatives immediately; Gate does not prescribe its reasoning or require a separate
opportunity permission. Reuse focus labels where they fit, or record a descriptive
lowercase snake_case strategic key (maximum 64 characters). Existing categories
must not exclude a useful question. No quota of proposals or category coverage.

## Focus priority and rotation

### Evidence accumulation between reviews

For Architecture, Technical and Strategic Review, first inspect `last_ran_at`, the latest
`last_review_receipts` result/reason, module review history, and the recorded `next_check_at`,
`next_check_after_run_id`, or `cooldown_runs`. Compare with the latest relevant
focus/route review when available. A recent review with unchanged evidence is
normally a reason to wait, not repeat it.

Current-pass `last_result` can be empty after a skip; use the retained receipt,
not that empty field, for the last review conclusion. A failed, blocked, or
timed-out receipt is not a completed assessment: honor pending review recovery.
If `last_review_receipts_error` is non-empty or history is missing, state that
uncertainty instead of inventing a previous clean review.

Count distinct completed, comparable workflow runs since that actual review:
same relevant route/group and materially comparable configuration. Chat turns,
tool calls, retries, unrelated routes, and repeated views of one run are not new
data points. Use execution identity/provenance rather than iteration-folder
names, which can rotate. If retention/provenance is incomplete, state the known
sample and coverage gap; never invent a complete count. Read only compact run
summaries needed to establish this sample, not every trace.

Let the evidence question determine how many additional relevant runs or which
outcome boundary is needed; do not impose a universal run-count threshold.
Several ordinary successful runs may be needed for trend assessment, whereas
one run can settle a specific verification. Explain the last review outcome,
new relevant sample, what remains unknown, and why the sample is or is not
sufficient in `reason`/`evidence`. For a skip, persist the concrete next boundary
using the existing scheduling fields. Do not keep moving an unmet boundary
forward merely because another Gate check occurred, or blindly restart a
cooldown on every pass. A boundary becoming due prompts assessment, not an
automatic full review without useful evidence.

All three non-drift reviews may be skipped in `observe` mode when no mandatory
plan check is due and none has useful new evidence. Waiting for more data
is a valid result. Do not manufacture an architecture, technical or strategic review to fill
the slot. New critical regressions, security/data-loss risks, and materially
failed required outcomes override waiting for more samples; select a focused
Technical Review promptly. Ordinary recovered errors do not override it.

For each selected module, use the compact focus agenda and reason within the
highest applicable lifecycle class:

1. new critical regression, security issue, data loss, or widespread failure;
2. a reproduced technical defect or matured strategic experiment evidence;
3. answered but unapplied decision;
4. materially changed or never-reviewed focus;
5. overdue focus;
6. oldest remaining focus.

This is not rigid round-robin and not a numeric relevance score. A lightweight
safety scan may preempt normal rotation. Map evidence to stable
route/group/sub-workflow scopes first, then let the reviewer choose the smallest
sufficient focus set. A small route often needs one focus; materially distinct
large routes may justify several. Every additional focus must have distinct
evidence, risk, decision, or repair value. Record what won, what it inspected,
and what it deferred. Repeated selection of the same unchanged focus while
eligible never-reviewed or overdue work exists requires an explicit urgent or
verification reason.

It is valid to skip Architecture, Technical and Strategic Review. Every skip
must record why no useful review is currently possible and include evidence
plus `next_check_at`, a positive `cooldown_runs`, or
`next_check_after_run_id`. Skipping is a durable decision, not an assertion
that the module is healthy forever.

## Choose the pass shape

Choose one mode and give a concrete `mode_reason`:

- `backlog_drain`: retained active issues, reproduced failures, or answered
  decisions already provide the useful work. Do not add broad discovery.
- `discovery`: materially new architecture, technical or strategic evidence may reveal a
  root cause not explained by retained work.
- `strategy`: a product/goal question warrants Strategic Review. Do not use it
  as a disguised correctness pass.
- `observe`: neither review has actionable or mature evidence. Wait for the
  named next boundary.

An old backlog must not hide a new production failure. Conversely, unchanged
backlog must not force another expensive discovery pass. A cooldown or focus
rotation cannot suppress a new materially harmful miss or a critical regression.

Select each module independently. Multiple modules may be due: the scheduler
runs Drift Check, QA, Architecture, and Strategy sequentially, with separate
receipts and recovery. A failed technical review does not cancel the later
research reviews; they must state any evidence limitations. Do not skip an
eligible research review solely because another module is due.

Use `next_check_at` for Architecture and Strategy's next useful assessment date.
Their saved dates survive repeated skips and become due when reached. If a
critical condition or genuinely missing evidence requires postponement, supply
`defer_reason` and a future `next_check_at`; explain exactly what is needed.
Do not perpetually defer unchanged platform handoffs. No universal cadence or
new cron is imposed. A reached date permits a bounded evidence assessment,
including an honest no-change/evidence-wait outcome; it does not require novel ideas.
When both modules need a new boundary and the domain provides no stronger one,
place Architecture later than Strategy: Strategy waits for the next meaningful
goal/outcome evidence cycle, while Architecture waits across several comparable
producing runs for a stable structural pattern. Never copy one module's date or
cooldown mechanically onto the other.

Call `record_pulse_worklist` exactly once with one decision for every module, covering all four modules:
`technical_review`, `architecture_review`, `strategic_review`, `plan_drift_review`.
The pass mode summarizes the run; backlog_drain is not a veto on a separately
due Architecture or Strategy investigation. Reuse an already-recorded worklist
on recovery rather than recording it again.

### Worklist payload boundary (required)

`record_pulse_worklist.decisions[]` is deliberately a **small scheduling
receipt**, not a review plan. Each decision may contain only `module`, `due`,
`reason`, `evidence`, `next_check_at`, `next_check_after_run_id`, and
`cooldown_runs`, and optional `defer_reason`. Do **not** put `focuses`, `route_scope`, `issue_ids`,
`deferred_focuses`, `decision`, or any review-plan field in this call. Explain
the selected scope in `reason` and `evidence`; the later reviewer records one
terminal review result after inspecting the evidence. An unknown field rejects the whole worklist and prevents all review
and repair work from starting.

Workflow steps and measurement/collector steps own metric observations. Gate
must not duplicate them through an impact ledger. The later retained module
tasks own selected architecture, technical and strategic work, canonical issue
updates, genuine decisions and one terminal review result. Stop after recording
the worklist.

## Goal progress context
When missing or inadequate measurements prevent a material strategic decision,
consider Strategic Review for a concrete measurement proposal, not just a setup
reminder. An existing unchanged finding or pending measurement decision is not by
itself a reason to repeat the review on every tick; use new evidence or its due
checkpoint. Missing measurements must not prevent other useful strategic thinking.

Read the canonical Objective in soul/soul.md, including Primary goals and Secondary goals when configured. Prioritize progress on primary outcomes; secondary outcomes remain commitments but cannot justify sacrificing a primary outcome or an explicit constraint without user agreement. Goal priorities are separate from primary/supporting metric roles. Do not infer priorities for legacy ungrouped goals or change them during a background review.

Use get_goal_metrics to read the primary and supporting metric definitions and comparable observations. Inspect outcome movement before selecting improvements; distinguish activity, quality boundaries and actual outcomes. Unknown/stale data does not prove no growth or success. Never change targets or metric definitions in a scheduled review. When measurement is not set up, surface /setup-goals as the setup path without filing repeated platform defects. Reviewers interpret measurements; producing runs/collectors own record_goal_observations.

For Architecture tier/model review, use material quality, retry, cost or latency
changes and due trial checkpoints as evidence. A count of successful runs alone
is not a review trigger or permission to change tier. Let the normal Architecture
cadence cover stable opportunities; reuse pending decisions and wait for the named
checkpoint rather than scheduling a tier review after every execution.
