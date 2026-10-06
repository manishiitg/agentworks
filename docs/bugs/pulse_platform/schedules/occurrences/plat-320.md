[← schedules / occurrences](index.md)

# PLAT-320 — Reserve `iteration-0` for Builder runs and give every workflow-producing schedule occurrence an immutable `-sched` run identity

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | schedules |
| Area | occurrences |
| Summary | P0 schedule evidence isolation and identity integrity. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented on main; live acceptance and comprehensive legacy evidence mismatch coverage remain open` |
| Last synchronized | `2026-09-14` |

- **Priority:** P0 schedule evidence isolation and identity integrity. This is
  not a prerequisite that automatically authorizes parallel schedules.
- **Owner:** scheduler occurrence identity, workflow run allocation, execution
  evidence, evaluation pairing, retention, Pulse intake and run-selection UI.
- **Origin:** Social Media throughput investigation. One 00:30 run lasted 517
  minutes and held the workflow-wide lease across the 03:00, 07:00 and 08:30
  occurrences. The follow-up design exposed that allowing those schedules to run
  concurrently would make each one rotate or reuse the same `iteration-0` tree.
- **Related:** [PLAT-047](../history/plat-047.md), [PLAT-089](../history/plat-089.md),
  [PLAT-031](../../ops/cost/plat-031.md), [PLAT-070](../history/plat-070.md),
  [PLAT-071](../stopping/plat-071.md), [PLAT-084](../../goals/pulse/findings/plat-084.md),
  [PLAT-101](../../coding-agents/claude/plat-101.md),
  [PLAT-136](../../ops/cost/plat-136.md),
  [PLAT-145](plat-145.md), [PLAT-165](plat-165.md),
  [PLAT-176](../history/plat-176.md), [PLAT-182](../history/plat-182.md),
  [PLAT-194](../history/plat-194.md), [PLAT-241](../general/plat-241.md),
  [PLAT-242](../history/plat-242.md), [PLAT-254](../../app/navigation/plat-254.md),
  [PLAT-296](../../sandbox/access/plat-296.md),
  [PLAT-304](../../sandbox/general/plat-304.md),
  [PLAT-309](../../integrations/integrations/plat-309.md), and [PLAT-321](plat-321.md).

## Decision

`iteration-0` is the mutable Builder/manual-workflow slot. A normal Builder run
may continue to rotate the previous slot to plain `iteration-N` and create a new
empty `iteration-0`.

Every workflow-producing execution started from a saved schedule receives an
immutable physical folder before its first workflow tool call:

- `iteration-N-sched` — cron/calendar occurrences and **Run now** invocations of
  a saved schedule;
- `iteration-N-hook` — authenticated webhook invocations, preserving the
  deployed PLAT-309 model;
- `iteration-0` — Builder/manual workflow execution only; and
- `iteration-N` — retained history produced by rotating the Builder slot.

Variable groups remain children of the owning physical run, for example
`runs/iteration-421-sched/linkedin`. The suffix is a readable projection, not
the authority. Every producing execution also has a typed `run_kind` and an
immutable execution/occurrence identity.

A Pulse-only or platform-maintenance schedule that reviews an existing run does
not manufacture a new workflow run folder. It keeps its own schedule/session
lifecycle identity and binds its evidence target to the exact existing run it
reviews. This exception prevents a review occurrence from appearing as new
workflow production.

Folder isolation alone does **not** authorize two schedules to execute
concurrently. PLAT-321 now owns the separate runtime policy: sequential by
default, with an explicit human-approved parallel opt-in after a fixed risk
disclosure. It does not rely on resource claims. Schedule dependencies,
terminal policies and collision queues still coordinate the default sequential
lane; `after_schedule_ids` always forces waiting.

An agent-authored declaration of files, resources or side effects is not a safe
concurrency boundary: a run can discover another write target at runtime, omit
a shared file, overwrite workflow databases, knowledge bases, learnings,
reports or planning state, contend for shared browser/CDP state, or duplicate
an external action.

## Implementation status (2026-09-14)

Implemented, verified, and pushed to `main` in `70660472b` and `53e5a0cc0`:

- pre-agent, restart-idempotent `iteration-N-sched` allocation and durable
  schedule-run binding;
- trusted routing for full runs, partial groups, direct `execute_step`, paired
  evaluation and exact-folder scheduler reconciliation;
- Builder rotation/pruning protection for `-sched`/`-hook`, shared numeric
  discovery, run-index v2, Pulse intake, backend/frontend path recognition and
  schedule history folder/concurrency snapshots;
- schedule/tool/system/Builder/Run/Pulse guidance for the new identity;
- PLAT-321's default-sequential, explicit acknowledged parallel lane, including
  dependency precedence and same-schedule exclusion; and
- uniform `workflow.json::run_retention_count` enforcement for plain Builder,
  `-sched`, and `-hook` families, with independent per-family counts, paired
  workflow/evaluation deletion, durable expired-artifact markers, and run-index
  cleanup.

Still required before this ticket closes:

- live Social Media occurrence → folder → logs → costs → evaluation → Pulse
  acceptance evidence;
- identity stamping and mismatch quarantine coverage for every legacy evidence
  document family.

## Confirmed pre-implementation behavior and failure boundaries

The pre-migration audit covered all 318 `plat-*.md` ticket files and the then-current
working tree. Seventy-seven tickets directly mentioned run folders, logs,
schedule history, retention or current/latest-run resolution. The following
code behavior made a local folder-name substitution unsafe:

1. Normal schedules started with `runFolder := "iteration-0"`; only webhooks called
   an immutable allocator. The scheduler records that value in schedule history
   and then guesses the physical run by comparing pre/post folder listings and
   timestamps.
2. `run_full_workflow` rotated the previous `iteration-0` for full runs and
   reused it for partial-group runs. The only trusted custom-folder exception was
   a webhook with an `iteration-N-hook` folder.
3. `execute_step` independently constructed `iteration-0/<group>`. A schedule
   agent could therefore bypass a change made only to `run_full_workflow` and
   still mix evidence in the Builder slot.
4. Evaluation normalized every non-webhook target back to
   `evaluation/runs/iteration-0[/group]`. A scheduled workflow and its
   evaluation would lose their physical pairing unless evaluation accepts the
   exact `-sched` target.
5. Attempt, conversation, prompt, timing, validation and final-summary
   documents did not all carry the immutable scheduler/execution identity.
   Several readers selected evidence by folder name, timestamp or local
   attempt/loop counters. This is the PLAT-089/176/182/241 class of defect.
6. Run provenance, retention, debug-tool validation and frontend path filters
   knew plain iterations and selected `-hook` variants, but not a typed
   scheduled-run family.
7. System/AgentWorks instructions said that all new executions landed in
   `iteration-0` and that `iteration-0` always contains the latest run.
   `run_full_workflow` repeats the same claim. Pulse intake understands only one
   active iteration and plain retained iterations. These contracts would direct
   Builder, Run, Pulse Gate and Pulse Architecture to the wrong evidence after
   the storage change.
8. Manual Pulse used the scheduler path but intentionally reviewed an existing
   retained folder. Treating every scheduler session as a producing `-sched`
   run would have created empty phantom runs and corrupted Pulse coverage/recency.

## Required identity contract

The scheduler must allocate and durably bind the folder before creating or
continuing the schedule chat. The same binding must flow through schedule
history, scheduler state, active-execution tracking, execution options,
orchestrator context, run metadata, logs, cost/evaluation records, Pulse intake
and APIs.

At minimum, a new scheduled run exposes:

- `run_kind: "schedule"`;
- the durable scheduler `run_id`/schedule-run entry ID;
- `schedule_id` and `scheduled_for` when the trigger is a cron occurrence;
- `trigger_source` distinguishing cron from Run now;
- the exact top-level `run_folder` and group folder;
- the producing workflow `execution_id`;
- the executable `plan_revision`; and
- created, started, completed and terminal-status timestamps.

One canonical identity should link the scheduler occurrence to producing
workflow executions. If a group execution retains a separate child
`execution_id`, its parent schedule `run_id` must also be present; generating an
unrelated timestamp identity inside `markRunMetadataStarted` is not sufficient.

The folder allocator must be collision-safe across processes and all folder
families. If numbered names are retained, allocation/rotation must share one
coordination boundary and consider plain, `-sched` and `-hook` numbers. An
exclusive directory creation plus an immutable `.schedule-run-id` marker is the
minimum filesystem arbiter. Capacity suspension and restart must recover and
reuse the already-bound folder rather than allocate another one.

Public request JSON must not be able to choose an arbitrary run folder. Only a
trusted server-owned schedule/webhook binding can bypass Builder-slot rotation.

## Log and evidence requirements

Folder isolation is necessary but not enough. Every newly written execution
result, conversation, prompt, timing record, pre-validation, final validation,
final summary, progress/completion receipt and evaluation report must include
the immutable execution identity, parent schedule run identity, `run_kind`,
exact `run_folder`, group and step ID where applicable.

Readers must:

- reject or visibly quarantine a document whose embedded identity contradicts
  the owning `run_metadata.json`;
- order attempts by durable timestamps/dispatch identity, not by the maximum
  local attempt or loop counter;
- query the exact schedule-bound folder instead of inferring “new work” from a
  folder-name delta;
- preserve all dispatches without overwriting earlier evidence; and
- keep legacy documents readable as `unknown_legacy` rather than inventing an
  identity from timestamps or folder names.

Immutable scheduled folders remove rotation/repointing for new schedule
evidence. They do not automatically fix Builder reruns inside `iteration-0` or
the embedded stale path described by PLAT-241; the Builder compatibility path
must retain correct cleanup/repointing or adopt the same lower-level identity
checks.

## Provenance, retention and UI contract

Upgrade `runs/run_index.json` compatibly so it distinguishes the mutable
Builder slot, plain Builder archives and immutable scheduled/webhook runs. The
authoritative latest run must be selected by typed metadata and timestamps, not
by assuming `iteration-0` or taking the highest parsed number.

Retention must define separate treatment for Builder archives, scheduled runs
and webhook runs, remove paired `runs/` and `evaluation/runs/` evidence
together, and leave durable history rows that visibly report expired artifacts.
Existing historical folders are not renamed or rewritten.

Backend run-folder APIs and frontend selectors must accept `-sched`, retain
group suffixes, expose `run_kind`, and sort by metadata timestamps. Schedule
history, Execution Logs, Costs, Evaluation and Pulse must resolve the same exact
identity. Numeric parsing remains a display aid only.

## Agent and Pulse awareness

The compatibility release must update all generated/runtime guidance that says
“all executions use iteration-0” or “iteration-0 is always latest,” including:

- the AgentWorks system/workflow instructions;
- `run_full_workflow`, `execute_step` and `debug_step` descriptions/validation;
- scheduled Run-mode instructions;
- schedule authoring guidance;
- Pulse Gate and deterministic intake;
- Pulse Architecture and Technical Review; and
- Pulse Fixer evidence-selection guidance.

Agents should consume a typed current/latest run projection from the backend or
run index. They should not independently reconstruct recency from suffixes.

## Staged rollout

1. Introduce one shared typed run-reference parser and metadata/index schema v2;
   make all readers dual-read legacy and new records.
2. Introduce the shared allocator and durable schedule-folder binding without
   changing normal execution behavior.
3. Route scheduled `run_full_workflow`, partial-group execution, direct
   `execute_step`, evaluation, stop and capacity resume through that binding.
4. Stamp and validate immutable identity across every log/evidence writer and
   reader.
5. Update retention, APIs, UI and all generated prompts/guidance.
6. Switch saved schedules to `-sched`, preserve sequential admission by default,
   and verify live schedule, Run now, capacity-resume, failure, stop and
   evaluation cases.
7. Keep workflow-producing schedules sequential by default. Do not add a
   resource-claim system. PLAT-321 may admit only the separately acknowledged
   parallel schedule lane after this identity binding is active.

## Acceptance criteria

- Two consecutive occurrences of one schedule create different immutable
  `iteration-N-sched` folders; neither occurrence writes under `iteration-0`.
- Two different schedules targeting the same workflow receive different
  immutable folders even if they become due simultaneously.
- Pulse-only and platform-maintenance occurrences do not allocate empty
  workflow run folders; their history names the exact evidence run reviewed.
- A schedule using `run_full_workflow`, a partial group, or direct
  `execute_step` writes all outputs and logs beneath its bound folder.
- Evaluation writes beneath the paired `evaluation/runs/iteration-N-sched`
  folder and scores the matching workflow evidence.
- Schedule history records the final folder at creation and never needs a later
  `iteration-0 -> iteration-N` repoint.
- Failure reconciliation reads the exact bound run metadata; it does not depend
  on successful pre/post folder listings.
- Capacity wait/resume and server restart reuse the same physical folder and
  identity.
- Stop/cancellation cannot terminate or relabel an unrelated run.
- Every required log/evidence document carries matching run identities, and an
  injected mismatched document is rejected or visibly quarantined.
- Costs and evaluation scores remain keyed by immutable execution identity and
  cannot be inherited by another run.
- Retention removes paired scheduled artifacts without corrupting history;
  legacy `iteration-0`, plain `iteration-N`, and `-hook` evidence remains
  readable.
- Builder/manual execution still owns and rotates `iteration-0` without moving
  or deleting an immutable `-sched` or `-hook` run.
- AgentWorks, schedule Run mode, Pulse Gate, Pulse Architecture, Technical
  Review and Fixer all select run evidence through the typed identity contract.
- Sequential schedule admission remains the default; no acceptance result from
  this ticket alone is treated as authorization for parallel shared-state
  execution. Only PLAT-321's explicit acknowledged policy may change admission.
- Builder, Run and Pulse guidance state that resource/file claims cannot make
  schedule overlap safe; any later parallel opt-in requires explicit human
  approval after the fixed shared-state risk disclosure.

## Verification matrix

Required focused coverage includes scheduler allocation/restart, orchestrator
full/partial/direct-step binding, evaluation pairing, capacity resume, stop and
timeout ownership, log mismatch rejection, cost/evaluation attribution,
run-index/Pulse intake, retention, backend folder listing, frontend folder
selection and rendered prompt/guidance assertions. A live Social Media run must
demonstrate exact occurrence → folder → logs → cost → evaluation → Pulse
linkage before the ticket can close.
