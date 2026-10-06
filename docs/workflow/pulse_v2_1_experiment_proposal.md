# Pulse v2.1: Reliability-First Experiment Proposal

> **Status (2026-08-03): historical experiment decision.** The experiment shaped
> the current hybrid design, but its proposed module topology and staging are no
> longer the current operating contract. See
> [`pulse_consolidation.md`](pulse_consolidation.md) for current architecture
> and [`pulse_v2_proof_carrying_architecture.md`](pulse_v2_proof_carrying_architecture.md)
> for the retained measurements and later decisions.
>
> **Date:** 2026-07-29
>
> **Primary objective:** Reliability
>
> **Secondary objective:** Cost efficiency
>
> **Scope:** Workflow Pulse Gate, evidence normalization, deterministic signals,
> semantic review, automatic repair boundaries, notifications, and Goal Advisor
> experiments
>
> **Background discussion:** [Pulse v2 architecture and review exchange](pulse_v2_proof_carrying_architecture.md)

## Executive decision

Pulse will not be rewritten wholesale and Gate will not be globally removed.

The approved direction is a **hybrid reliability architecture**:

- deterministic runtime facts are normalized in code;
- a Sentinel signal layer identifies indisputable conditions;
- Gate remains available for ambiguity, semantic drift, assurance expiry, and
  sampled review of apparently healthy runs;
- existing reviewer skills remain the core semantic review capability;
- operational truth stops expanding inside `builder/improve.html`;
- consequential changes continue to use the existing Pulse human-decision flow;
- Goal Advisor experiments move into a dedicated Lab lifecycle, which may be
  built now;
- broader Sentinel, Autopilot, and event-ledger work proceeds only when
  production measurement justifies it.

The next commitment is an experiment, not a control-plane replacement.

## Product goal

Pulse exists so a small team can operate 100+ autonomous workflows by exception.

It should reliably answer:

1. What did the workflow claim happened?
2. What current evidence supports or contradicts that claim?
3. What materially changed?
4. What requires deterministic handling, semantic review, or a user decision?
5. What was changed, and has the change been verified?

Reliability takes precedence over token reduction. Cost optimization is pursued
only where it does not materially reduce assurance.

## Settled decisions

### 1. Reliability first

When reliability and cost conflict, choose the more reliable path.

Cost remains important because excessive Pulse cost prevents the system from
scaling, but cost savings do not justify removing semantic review without
evidence.

### 2. Gate remains hybrid

Sentinel handles deterministic facts and may eventually own narrowly proven
routes.

Gate remains responsible for:

- ambiguous or conflicting evidence;
- semantic correctness;
- healthy-but-wrong detection;
- external or environmental drift that lacks a deterministic verifier;
- expired assurance;
- risk-weighted sampled audits;
- unfamiliar signal types.

There is no plan to globally delete Gate.

### 3. Notifications are workflow-specific

Users choose notification behavior during Pulse setup.

Run outcome and Pulse maintenance are configured separately:

- **Run outcome:** every run, transitions only, or digest.
- **Pulse activity:** normally transitions/decisions only.

Recommended defaults:

- externally side-effecting workflows: run outcome after every run;
- high-frequency read-only workflows: transitions or digest;
- Pulse maintenance: transitions only;
- dashboard: always updated.

The existing `run_summary` and `pulse_summary` preference model remains the
configuration surface.

### 4. Proof is boundary evidence, not reasoning disclosure

Proof means evidence that an important workflow effect or user-visible claim is
true. It does not mean exposing or validating hidden chain-of-thought.

Examples:

| Claim | Evidence |
|---|---|
| A post was published | External post ID, configured account ID, timestamp |
| A message was sent | Provider receipt, destination/recipient, message ID |
| A database was updated | Read-back from the authoritative table |
| A report reflects this run | Current run/group identity on its source rows |
| An evaluation passed | Criterion-level evidence against the current output |
| A backup completed | Commit/hash at the configured destination |

### 5. Missing proof means unverified, not automatically failed

Use these states:

- `success_verified`
- `success_unverified`
- `failed`

`success_unverified` means the action may have happened, but Pulse cannot prove
it.

For a material or high-risk claim, `success_unverified`:

- prevents a healthy Pulse verdict;
- triggers semantic review;
- may notify the user;
- never automatically retries an external side effect;
- remains open until proof, contradiction, user resolution, or expiry.

Use `failed` when evidence disproves the claim or a required fail-closed
contract explicitly defines absence of proof as failure.

### 6. Consequential changes require the existing user-decision flow

Pulse continues to use structured human-input requests for consequential
changes.

Automatic work is initially limited to bookkeeping and narrowly registered,
reversible repairs. Plan, strategy, destination, recipient, model, schedule,
database meaning, and business-semantic changes require exact approval.

### 7. Build Lab now

Lab is approved as a separate, proposal-first strategy experiment lifecycle.

It does not wait for Sentinel measurement, but it must not become a second
operational Fixer.

### 8. Defer a global event-ledger migration

Do not build or migrate to a universal Pulse event ledger during Stage A.

Use existing SQLite state for the measurement experiment. Lab may have a
dedicated experiment table and history.

A unified event ledger remains an option after Stage A if state fragmentation
continues to justify its implementation and migration cost.

## Assets to preserve

The current reviewer skills are product assets:

- `design-plan`
- `improve-evaluation`
- `improve-report`
- `improve-database`
- `improve-learnings`
- `improve-knowledge`
- `pulse-bug-review`
- cost/timing and LLM operations guidance

The architecture distinguishes:

- **Control-plane prose:** scheduling, module enumeration, concurrency,
  authorization, transaction state, recovery, and finalization. Move exact
  invariants into code where practical.
- **Reviewer expertise:** how to investigate, compare evidence, diagnose, and
  recommend. Preserve and evaluate this semantic content.

The objective is not fewer prompts. It is prompts doing judgment instead of
acting as an unreliable transaction coordinator.

## Risk classification

Pulse should infer an initial risk class from workflow capabilities and ask the
user only when uncertain.

### High risk

- sends external messages;
- publishes content;
- trades, pays, purchases, or moves money;
- changes external or production data;
- uses authentication or credentials;
- affects recipients, accounts, or destinations;
- generates business decisions people act on without review.

Any workflow that communicates externally or mutates an external system is
high-risk by default.

### Medium risk

- produces reports, recommendations, or evaluations;
- updates internal stores;
- prepares externally used content that a person reviews first;
- influences later workflows without directly causing an external effect.

### Low risk

- read-only research;
- drafts;
- local transformations;
- tests and simulations without external effects.

Risk class controls:

- assurance expiry;
- sampling frequency;
- required boundary coverage;
- notification recommendation;
- permitted automatic actions;
- verification strength.

## Boundary-first assurance

Do not author claims for every step.

Begin with critical assurance boundaries:

- workflow success criteria;
- external effects and their account/destination;
- current-run and current-group binding;
- durable writes and the runtime consumers expected to read them;
- user-visible report and evaluation claims;
- safety-critical stop, retry, and fail-closed behavior.

Claims may be inherited from:

- step type;
- tool type;
- validation schema;
- success criteria;
- reviewer-discovered assurance gaps.

Claims that encode business meaning require user or builder approval.

### Coverage state

Coverage must be explicit:

```text
required critical boundaries: 7
proved: 5
legitimately not exercised: 1
unknown/not instrumented: 1
```

`unknown`, `not_instrumented`, `stale`, or `contradicted` critical boundaries
prevent routine semantic-review skipping.

A workflow with no claim coverage is `coverage_unknown`, not healthy.

### Receipt adapters

Do not require the entire tool ecosystem to adopt new contracts immediately.

Introduce receipt extraction around a small set of important boundaries:

- publishing;
- messaging;
- external writes;
- database writes/read-back;
- report/eval run binding;
- backup and publish completion.

Adapters may normalize existing tool arguments and results. Native typed
receipts can be introduced later when the tool is owned and the benefit is
proven.

## Hybrid Gate and Sentinel responsibilities

### Sentinel

Sentinel's **routing policy** is deterministic and initially runs only in
shadow mode. Shadow mode does not require hiding deterministic facts from Gate.

Existing code-owned facts may remain live Gate inputs. This includes
`open_concerns`, `plan_change_backlog`, module review history, and the validated
loop-closure findings. For loop closure:

- Gate receives the findings and coverage state as read-only evidence;
- no finding mandates a module or overrides the three-module cap;
- no finding authorizes mutation;
- an empty result is clean only when coverage is verified;
- the server snapshots the same facts with Gate's worklist for measurement.

This measures whether a deterministic **routing decision** could replace Gate.
It does not claim to be a counterfactual test of how Gate would behave if the
underlying facts had been hidden. New fact families without equivalent
operational validation remain shadow-only.

Candidate deterministic signals:

- run or step failed;
- required output missing;
- evaluation score missing or zero;
- current-run/group mismatch;
- unreviewed plan change;
- recurring open concern;
- expired module/reviewer baseline;
- material cost/timing change;
- backup/publish failure;
- required boundary proof missing, stale, or contradicted.

Sentinel does not diagnose root cause or declare semantic health.

### Gate

Gate remains the only authoritative scheduler during Stage A.

Gate consumes normalized facts and continues selecting the existing reviewer
skills.

During Stage A, shadow signals:

- record what they would have selected;
- never launch or suppress a reviewer;
- never mutate module state;
- never compete with Gate.

Live fact feeds may influence Gate's reasoning, just as existing concerns and
plan-change backlog already do. What remains shadow-only is the deterministic
decision that maps those facts to reviewer selection or suppression.

### Route-level ownership after measurement

If a deterministic route is later approved:

- ownership transfers only for that signal family and risk class;
- exactly one scheduler is authoritative;
- Gate remains the fallback for unknown signal types, but not an ambiguous
  second owner of an already transferred route;
- a critical miss revokes deterministic ownership of that route until it is
  redesigned and re-evaluated.

## Routine Doctor skip eligibility

Do not use a synthetic numeric confidence score in Stage A.

Use a deterministic predicate:

```text
routine_doctor_skip_eligible =
  no_open_critical_or_high_signal
  AND no_watched_change_detected
  AND all_required_critical_boundaries_instrumented
  AND all_required_receipts_current
  AND no_contradictory_evidence
  AND semantic_assurance_not_expired
  AND not_selected_for_control_sample
```

Every clause returns:

- `pass`
- `fail`
- `unknown`
- evidence/reason

`unknown` makes the run ineligible for skipping Doctor review. It does not by
itself mark the workflow failed.

### Watched change dimensions

Initial dimensions:

- workflow, plan, prompt, validation, eval, report, and schedule versions;
- resolved model/provider and relevant runtime/tool versions;
- account, destination, permission, and credential state;
- source schema and material input-distribution indicators;
- external contract/page-shape fingerprints where available;
- output, timing, cost, and receipt shape.

Risk-specific windows and tolerances are measured in Stage A rather than guessed
in advance.

### Assurance categories

Use categorical state:

- `eligible_for_routine_skip`
- `semantic_review_due`
- `coverage_unknown`
- `evidence_contradicted`

Each category carries its exact determining clauses.

## Automatic action policy

### Class A: automatic bookkeeping

Initially automatic:

- update projections;
- deduplicate signals;
- schedule a recheck;
- record freshness from verified runtime evidence;
- render status;
- preserve or advance deterministic cursors.

### Class B: narrow registered repair

Eligible only after its action type is individually implemented and proven:

- correct current-run/group binding;
- restore a required field without changing meaning;
- repair a known report query against an unchanged contract;
- roll back a previous automatic action.

Requirements:

- expected target hash/version;
- bounded parameters or patch;
- rollback;
- verification policy;
- no business-semantic change;
- no recipient, destination, account, model, or schedule change.

### Class C: consequential operational change

User approval required:

- prompts that change external behavior;
- model/provider;
- schedule;
- destination/recipient/account;
- database semantics or migration;
- business thresholds;
- external side-effect behavior.

### Class D: strategy or goal change

Lab experiment plus user approval required:

- strategy replacement;
- goal or success-criterion change;
- material plan restructuring;
- externally visible experiment.

## Verification policy

Every action type declares:

- minimum verification method;
- permitted stronger methods;
- whether it may be applied before verification;
- maximum unverified lifetime;
- rollback requirement;
- escalation when verification is unavailable.

Policy may strengthen the requirement based on risk. Doctor may request a
stronger method but cannot weaken the action-policy minimum.

### Verification ladder

1. Static/schema/contract assertion.
2. Deterministic fixture or subgraph replay.
3. Read-only postcondition query against the real system.
4. Sandbox or synthetic-account execution.
5. Shadow decision with side effects suppressed.
6. Limited canary.
7. Next legitimate production evidence boundary.

Full workflow replay is not assumed.

### Applied but unverified

An action may become `applied_unverified` only when its registered policy
permits next-boundary verification.

It records:

- action and finding IDs;
- target/version;
- application time;
- expected evidence boundary;
- expiry;
- rollback state;
- user-visible impact;
- escalation owner.

Expired items become `verification_overdue`, stay visible in Pulse, and block
overlapping actions on the same target.

## Lab MVP

Lab is a strategy experiment system, not a maintenance reviewer.

### Lifecycle

```text
draft
→ proposed
→ awaiting_user
→ approved
→ running
→ awaiting_evidence
→ adopted | rejected | retired | blocked
```

### Experiment record

Each experiment stores:

- workflow;
- goal criterion;
- current strategy and baseline;
- proposed alternative;
- hypothesis;
- expected effect;
- measurement method;
- evidence checkpoint;
- risks;
- approval;
- relevant plan/config versions;
- result and disposition.

### MVP rules

- one active strategy experiment per workflow;
- Goal Advisor and critic remain read-only;
- the existing Pulse human-input system obtains approval;
- existing builder tools apply the exact approved change;
- no automatic strategy changes;
- approval expires when the proposal target/version materially changes;
- browser workflows may use future legitimate runs as evidence;
- Lab writes a dedicated SQLite experiment table/history;
- Lab does not require the global Pulse event-ledger migration.

## Stage A: measurement experiment

Stage A is the only approved Sentinel implementation stage.

### Immediate prerequisite: canonical registry

Create one canonical Go registry for current Pulse module metadata.

Generate or validate:

- module order and IDs;
- current versus legacy aliases;
- reviewer artifact path validation;
- tool schemas/enums;
- result validation;
- scheduler labels;
- UI metadata;
- guidance/documentation invariants where feasible.

Parity tests must fail when a current module/tool appears in one required
registry surface and not another.

This work is justified independently by demonstrated production defects and
does not wait for the experiment.

### No-new-HTML-truth rule

Effective immediately:

- do not add new authoritative state or recovery semantics to
  `builder/improve.html`;
- use SQLite/runtime state as authoritative for new behavior;
- treat HTML as an explanatory view;
- keep the current hidden recovery ledger until its durable replacement exists;
- do not expand its responsibilities.

### Minimal EvidenceBundle

Normalize facts already available today:

```text
EvidenceBundle
  workflow/run identity
  workflow/plan/config versions
  step terminal states
  run metadata
  validation outcomes
  evaluation verdicts
  concern recurrence
  plan-change backlog
  cost/timing summaries
  report/eval current-run binding
  backup/publish state
  human-input state
  reviewer/module history
  available boundary receipts
```

The bundle is read-only input to Gate and shadow Sentinel.

### Gate instrumentation

For every pass, record:

- EvidenceBundle identity/version;
- Gate decisions and reasons;
- deterministic signals present;
- modules/reviewers selected;
- reviewer findings and severity;
- actions and verification outcomes;
- Gate, reviewer, Fixer, finalizer, and recovery cost/timing.

### Shadow routing signals

Begin with a small set:

- run/step failed;
- missing required output;
- missing current eval verdict;
- unreviewed plan change;
- recurring concern;
- backup/publish failure;
- current-run mismatch;
- material cost/timing change.

Do not begin with every proposed signal family.

## Shadow-clean control sample

The experiment must review some runs that Sentinel would classify as requiring
no Doctor review. Otherwise it cannot measure false negatives.

### Pilot population

Start with three workflows:

1. one high-risk browser/publishing workflow;
2. one report/data/evaluation workflow;
3. one mostly deterministic workflow.

### Initial sampling

- review every run after a material change;
- additionally review one apparently shadow-clean run per pilot workflow per
  day;
- continue sequentially until the evidence threshold, experiment budget, or a
  verified critical miss stops the experiment;
- target approximately 60 clean sampled reviews as an initial planning number,
  not an automatic promotion threshold.

If zero misses occur in `n` independent samples, the rough 95% upper bound on
the true miss rate is approximately `3/n`. Sixty zero-miss samples therefore
support only a roughly 5% upper bound, not proof of zero risk.

### Stratification

Track samples by:

- workflow type;
- risk class;
- recent change versus observational stability;
- first baseline versus previously reviewed state;
- external side-effect versus read-only behavior.

### Reviewer

Use the existing read-only reviewer skills as Doctor reviewers.

Human-adjudicate a smaller subset to calibrate whether agent findings are real.

### Miss definition

A shadow-policy miss is:

- a verified actionable correctness, evidence-integrity, safety, or material
  goal-measurement finding;
- supported by evidence that existed when the shadow decision was made;
- not represented or routed by a shadow signal.

Style preferences, unsupported suspicions, facts that occurred after the
decision, and findings below the predeclared materiality threshold do not count.

## Critical miss policy

Examples:

- wrong recipient/account/destination;
- unsafe external action;
- false success on a materially failed workflow;
- stale evidence causing an incorrect material conclusion;
- financial/authentication failure;
- corruption or loss of production data.

One verified critical miss:

- stops deterministic ownership promotion for that route;
- keeps or returns it to hybrid Gate/Doctor handling;
- requires signal/coverage redesign;
- resets evidence collection for that route.

It does not cancel unrelated deterministic routes.

## Experiment metrics

### Selection and assurance

- percentage of Gate selections explainable by deterministic facts;
- Gate-only actionable-finding rate and severity;
- deterministic-route actionable-finding rate;
- shadow-clean sampled miss rate;
- critical miss count;
- changed versus observationally stable finding yield;
- coverage state by risk class;
- false positives and false negatives after adjudication.

### Reviewer value

- actionable findings per reviewer;
- actionable findings per token/cost;
- reviewer-skill yield independent of trigger;
- duplicate or unsupported finding rate;
- time from signal to verified resolution.

### Pulse operating cost

```text
current_pulse_cost =
  gate
  + reviewers
  + fixer
  + finalizer
  + recovery/retry
```

Report per pass, workflow/day, and organization/day where data permits.

### Stage A incremental cost

```text
stage_a_cost =
  engineering
  + telemetry/storage
  + shadow evaluation
  + control-sample Doctor reviews
  + human adjudication
```

### Assurance value

Also record:

- production defects found;
- incidents prevented or shortened;
- Pulse-caused failures;
- operator investigation time;
- time to detection;
- time to verified resolution.

The Stage B decision compares reliability, assurance value, cost, and migration
risk—not token savings alone.

## Stage A stopping and decision rules

Before running the experiment, declare:

- observation/run budget;
- Doctor review budget;
- minimum samples per important risk stratum;
- maximum acceptable critical/high miss rate;
- materiality definitions;
- adjudication procedure;
- stop/extend rule;
- inconclusive outcome.

Possible outcomes:

### Outcome 1: narrow Sentinel ownership

Approved only for signal families and risk classes that meet the predeclared
reliability evidence.

### Outcome 2: hybrid fact layer

Sentinel normalizes facts and prioritizes work, while Gate remains the semantic
scheduler.

This is the expected default unless narrow replacement is strongly supported.

### Outcome 3: retain Gate

Keep Gate scheduling. Ship only the improvements justified independently:

- canonical registry;
- normalized facts;
- structured state;
- reporting/projection discipline;
- measurement and reviewer evaluations.

### Inconclusive

If the budget expires without adequate assurance, do not promote Sentinel.
Extend the experiment or choose Outcome 2/3.

## Stage C narrow-route pilot

Stage C exists only after an explicit Stage B decision.

### Ownership

- select one workflow, signal family, and risk class;
- exactly one system owns new decisions for that route;
- legacy Gate/module state owns earlier occurrences;
- late events route by original run/correlation identity;
- do not dual-write authoritative decisions.

### Drain before cutover

- refuse cutover with an in-flight `fixing` row;
- leave `changed_unverified` work under its legacy owner until terminal;
- snapshot relevant module state, audit, concerns, reviewer artifacts, and
  recovery metadata;
- block overlapping legacy and signal actions.

### History

- preserve module audit and reviewer artifacts immutably;
- expose history through read-through adapters;
- seed signal projections with explicit legacy references;
- preserve concern fingerprints and recurrence counts.

### Rollback

- disable the route at a recorded cursor;
- return future decisions to Gate;
- never delete signal history;
- keep already-applied actions under their verification policy until terminal.

## Current operational-state decision

During Stage A:

- existing `pulse_module_state` remains authoritative;
- existing `pulse_module_audit` remains the fixer history;
- `pulse_review_log` remains reviewer history;
- `run_concerns` remains durable concern state;
- `pulse_final_command_state` remains finalizer state;
- structured finding evidence and compact review receipts remain forensic evidence;
- current HTML recovery state is preserved but not expanded.

No live-state migration occurs during measurement.

## Implementation sequence

### Track A: reliability measurement

1. Canonical module/tool registry and invariant tests.
2. No-new-HTML-truth rule.
3. Gate/reviewer/fixer/finalizer instrumentation.
4. Minimal EvidenceBundle.
5. Small shadow signal set.
6. Three-workflow control sample.
7. Stage B decision.

### Track B: Lab MVP

1. Experiment schema and SQLite history.
2. One-active-experiment invariant.
3. Goal Advisor proposal and critic output mapped into the experiment record.
4. Existing human-input approval.
5. Exact approved-change application through current builder tools.
6. Measurement checkpoint and adopted/rejected/retired disposition.
7. Pulse UI representation.

Track B does not wait for Stage A, but the two tracks must not share mutation
ownership or introduce a second Fixer.

## Acceptance criteria

### Canonical registry

- every current module is accepted by reviewer persistence;
- scheduler/tool/UI registries match;
- legacy aliases are explicit and read-only where appropriate;
- parity tests reproduce the defect class found during the July refactor.

### Stage A

- shadow routing decisions never launch or suppress live work;
- validated live fact feeds may inform Gate but never mandate routing;
- every comparison is tied to the exact EvidenceBundle and Gate decision;
- sampled clean runs receive independent Doctor review;
- misses are adjudicated;
- cost and finding yield are reportable;
- no operational truth is added to HTML.

### Lab MVP

- no strategy change occurs without exact approval;
- one active experiment per workflow;
- baseline and measurement are explicit;
- approval expires on material target drift;
- experiment history survives restarts;
- Goal Advisor remains read-only;
- Lab cannot overwrite operational Fixer state.

### Stage C, if approved

- one authoritative route owner;
- in-flight legacy work drains before cutover;
- history and concern recurrence remain legible;
- rollback preserves actions and evidence;
- one critical miss revokes route ownership.

## Explicitly deferred

- global Gate removal;
- universal claims for every step;
- typed receipts for every tool;
- universal workflow replay;
- broad automatic plan/config changes;
- automatic strategy adoption;
- global Pulse event-ledger migration;
- deterministic HTML/published-page rendering rewrite;
- retirement of legacy module orchestration.

These remain hypotheses or later improvements, not approved work.

## Open implementation inputs

The architecture is settled enough to begin the two approved tracks, but Stage A
planning still needs operational numbers:

- current number of Pulse-enabled workflows;
- runs per workflow/day;
- current Gate/reviewer/Fixer/finalizer cost;
- Stage A Doctor-review budget;
- experiment duration/run budget;
- acceptable high-severity miss bound;
- materiality/adjudication owner;
- initial three pilot workflows.

These values configure the experiment. They do not reopen the architectural
decisions above.

## Final recommendation

Proceed with:

1. the canonical registry;
2. Stage A instrumentation and shadow measurement;
3. the independent, proposal-only Lab MVP.

Do not proceed yet with:

- Gate replacement;
- broad proof/receipt rollout;
- general Autopilot;
- unified event-ledger migration;
- live-state cutover.

The final operating principle is:

> Reliability comes first. Deterministic code should own facts and exact
> invariants; Gate and the existing reviewer skills should continue to own
> semantic judgment until production evidence proves that a narrower route can
> be safely automated.
