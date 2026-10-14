<!-- STATUS: design spec, NOT loaded at runtime. -->

# Pulse scheduled review lifecycle — historical design spec

**This document is not delivered to any agent.** It was a `builder-reference`
reference doc until 2026-08-05; it is now a design spec kept for humans and for
the contract tests.

Why it moved: no production prompt ever loaded it. Pulse orchestration lives in
the per-module prompts in `cmd/server/scheduler.go`, in the focused reference
docs that prompts *do* load (`pulse-gate`, `pulse-review-fixer`,
`pulse-finalizer`, `strategy-auditor`, `pulse-fixer-practices`), and in the
`get_workflow_command_guidance` kinds. Commit `00ca7a46` ("Compact Pulse review
orchestration", 2026-07-21) moved orchestration into the prompts and created the
focused docs; this file was touched only lightly in that commit and was left
behind rather than retired.

It stayed in the runtime bundle as the only reference doc large enough to exceed
a single tool result on its own (69,576 bytes, ~25,769 tokens against a 25,000
token cap), reachable whenever an agent browsed the skill index — so it was a
standing hazard for content nothing needed.

**Because nothing loads it, it cannot be trusted to match behavior.** Treat
`scheduler.go` and the loaded reference docs as authoritative; when they and
this file disagree, this file is the stale one. The contract tests in
`cmd/server/guidance/render_all_test.go` and `cmd/server/scheduler_test.go` read
it from this path.

---

## Current scheduling invariant (PLAT-158)

Recurring Pulse is configured only by an enabled `pulse_review_only` schedule.
Ordinary workflow schedules never run Gate or Review+Fix inline; they retain
only the short backup, report-publication, and run-summary finalizer. The
workflow-level `post_run_monitor` and `post_run_monitor_mode` fields were
removed. A manual Pulse launch and a dedicated Pulse schedule both run the full
review lifecycle against retained workflow evidence.

The material below is retained for historical review-policy contract tests. Its
references to running after every workflow execution and its old dashboard
stage are not current scheduler behavior.

## Pulse — Dynamic Scheduled Steward

This is the comprehensive/manual Pulse playbook. Scheduled Pulse runs load the
focused stage references instead so no individual scheduler message or tool
result carries the entire contract: `pulse-archive`, `pulse-gate`,
`pulse-review-fixer`, `review-improve-log`, and `pulse-finalizer`.

Pulse reviews retained workflow evidence on its own schedule. It is not a fixed checklist. It is a small sequence with one mandatory intelligence turn:

1. **Gate / Worklist** — read the evidence and call `record_pulse_worklist`; Gate writes no HTML or workflow artifacts.
2. **Selected modules only** — the scheduler runs the modules Gate marked `due`.
3. **Dedicated Dashboard turn** — load the current `review-improve-log` contract, update `builder/improve.html` and `builder/card.health.html`, create only real user questions, read the HTML back, and record the dashboard result.
4. **One ordered finalizer turn** — backup, conditional publish, then notify. Each command records its own live/final status in `pulse_final_command_state`.

SQLite/runtime state is authoritative for scheduling, recovery, approvals, and
new behavior. `builder/improve.html` is the durable explanatory projection, never
the sole source for a machine action. Preserve its legacy recovery/Advisor state
until replaced, but add no new state semantics. Project user-relevant outcomes
without contradicting runtime state.

When updating `builder/improve.html`, keep it a small published executive journal. Runloop renders pending **Needs your decision** requests above the HTML. Schema 5 shows only the Bug/Goal verdicts, one status sentence, exactly three **Latest Pulse** cells (Outcome, Goal movement, Next), at most six material Activity transitions, and archive links. The Pulse popup owns reviewer coverage, assumptions, issue queues, backlog counts, evidence, checks, attempts, history, technical/cost tiles, filters, and complete reviewer detail; never duplicate those sections in HTML. A hidden `#pulse-agent-handoff` marker may project compact current state for agent orientation; it is not recovery authority, not a visible Agent log, and must not duplicate the report narrative.

## Timeout Recovery

The scheduler uses a sliding inactivity timeout: 10 minutes without observable progress for a normal Pulse step and 30 minutes without progress for Strategy Auditor or Goal Advisor. Tmux output, tool calls, tracked execution changes, and session activity reset that timer, so healthy long-running work is not canceled merely because its total duration exceeds 10 or 30 minutes. A timed-out reviewer is retained as incomplete evidence but does not cancel independent reviewers or the consolidated Fixer; the Fixer records a truthful terminal result for its module. A failed Fixer still prevents later maintenance writes. Dashboard and ordered finalization run in recovery when safe. If the finalizer itself times out, any final command that did not record an outcome is marked `timed_out`. Recovery turns read SQLite module results, SQLite-backed reviewer records, and current target state; they do not treat HTML as recovery truth and must not claim that timed-out or skipped work succeeded.

## Gate Contract

Gate decides what the next Pulse modules should do. Read runtime facts first and
HTML only for narrative context; HTML never overrides contradictory runtime state.
Gate must call:

- `get_pulse_state(view="module", workspace_path="<current workflow>")` before deciding.
- `record_pulse_worklist(workspace_path="<current workflow>", pulse_run_id="<pulse session id>", decisions=[...])` exactly once before stopping.

Gate uses a **progressive evidence scan**. Start with compact state and metadata:

- latest run metadata/summary and run status, including the compact final
  execution results for every step that actually ran
- `builder/improve.html` current dashboard, open items, recent timeline, and cadence
- `soul/soul.md`
- `planning/plan.json`, `planning/step_config.json`, and `planning/changelog/`
- existence/freshness of measurement rows (producer outputs, goal observations)
- existence/freshness of `reports/report_plan.json` and report HTML
- `db/README.md` and a compact DB schema summary
- a compact KB note index; `knowledgebase/context` remains read-only user context
- per-step learning metadata and whether global learnings changed
- the code-owned store freshness ledgers `learnings/_global/_freshness.json` and
  `knowledgebase/_freshness.json` (`last_confirmed_run`, `last_confirmed_at`,
  `confirm_count`) — how recently an actual run re-confirmed each store
- open and answered report human inputs in `db/db.sqlite`
- legacy Chief of Staff recommendation cards in `builder/improve.html` are history only
- compact cost/timing availability and change signals when present
- workflow version, compact resolved LLM/tier signature, and backup/publish/notification readiness metadata

### Step concerns are first-class run evidence

Execution agents use a plain Markdown handoff, not a separate findings schema:
`CONCERNS: <brief evidence-backed concern>`, immediately before their final
`STATUS:` line. Gate must inspect these markers for every step/item that actually
ran, even when the overall run and the step both completed successfully. A
successful status means the primary work completed; it does not resolve or erase
a reported concern.

Use the durable compact results for the current `run_folder`, rather than relying
only on resumed chat context:

- regular and todo-task steps: prefer
  `runs/<run_folder>/logs/<step>/execution/execution-final-summary.json`
  `execution_result`; for failed, incomplete, or legacy runs where that file is
  absent, use the latest applicable
  `runs/<run_folder>/logs/<step>/execution/execution-attempt-*.json`
  `execution_result`
- message-sequence steps: `runs/<run_folder>/execution/<step>/session.json`
  `entries[].summary`

A targeted search for the literal `CONCERNS:` marker is sufficient. Do not open
the corresponding `*-conversation.json`, prompt, tool-call, or other long logs
unless a selected reviewer later needs them. If a step retried, use its latest
successful/final attempt; do not revive concerns from an earlier attempt when a
later attempt explicitly resolved them.

For every current concern, preserve the step/item and evidence path, deduplicate
it against open `builder/improve.html` findings, and make one explicit Gate
decision:

- operational correctness, runtime, stale-input, unsupported-success, report,
  evaluation, learning, knowledgebase, DB, artifact, cost, or LLM/ops signal:
  mark `workflow_review` due
- strategy or outcome concern: independently mark `strategy_auditor` due when
  the current plan needs an inside-the-strategy audit; independently mark
  `goal_advisor` due when its blank-sheet opportunity cadence, current
  goal/headroom evidence, decision, or experiment checkpoint requires it
- user judgment is genuinely required: route it to a due module whose Fixer can
  use `create_human_input_request`; Gate itself does not create the question.
  Strategy Auditor and Goal Advisor must classify each recommendation as
  `decision_required`, `evidence_wait`, `fixer_handoff`, or `none`;
  `proposal_only` is valid only for `evidence_wait` with an exact `next_check`
- already resolved, superseded, or informational: record a compact reviewed/no
  action disposition with the evidence

Keep unresolved concerns in the durable worklist evidence until the selected
module records a verified resolution, blocker, or durable human-input request.
The Dashboard later projects them into the Gate timeline entry and compact
handoff. Never silently drop a concern merely because the run status is
successful. Conversely, the presence of `CONCERNS:` is evidence to classify,
not an automatic run failure or an automatic Bug verdict.

Do not load full report HTML, full KB/learnings, broad DB rows, every cost file, or long run logs merely to decide cadence. Open large evidence only when a compact signal makes that module plausibly due or one targeted fact is needed to justify a decision. The selected module performs the deep inspection later; Gate only selects the evidence-backed worklist. When Gate sees a plausible operational signal, mark Workflow Review due so its single read-only agent can investigate through the relevant ordered lens and the Pulse Fixer can repair and verify it.

Gate writes only the complete SQLite worklist. The dedicated Dashboard stage
later writes one compact **Pulse Gate / Worklist** timeline entry and refreshes
`#pulse-agent-handoff` from that durable state. Do not mutate
`builder/improve.html` during Gate.

Treat `soul/soul.md` as stable intent only. Objective, success criteria, explicit
user-approved constraints, and notification preferences are authoritative.
Architecture, providers, tools, models, channels, thresholds, tactics, step shape,
and assumptions written by an agent remain revisable. When one materially limits
the goal, keep at most three active items under **Assumptions challenged**, naming
where each came from, evidence for/against it, and how it will be validated or
retired. Do not create user questions for routine implementation choices.

The first screen may legitimately combine evidence measured by different routes or runs, but freshness must be explicit. The overall status reflects the latest run. Every carried-forward verdict, goal criterion, brief cell, and important signal/cost tile must visibly say `as of run <id/date>` or `last measured <id/date>`; never leave an older value looking current. If the latest run did not measure a signal, retain the last trustworthy value and label it `not measured this run · last measured ...`.

Update the stable header elements `#pulse-bug-verdict` and `#pulse-goal-verdict` in place. If either is missing from an otherwise current-format page, insert the standard two-element `.verdicts` block beside the workflow title without rewriting the timeline. Never create a duplicate verdict block.

- Bug verdict: did the workflow run correctly?
- Goal verdict: is the workflow moving toward `soul.md` success criteria?
- Maintenance Radar: which lanes are quiet, watching, or due?
- Module worklist: each module `due` or `skipped`, with a short plain-language reason and evidence.

Gate does not launch reviewers or call mutation tools, plan modification tools, backup, publish, or notify.

Gate must record exactly one decision for each module. A partial worklist is invalid because omitted modules would otherwise disappear silently.

## One Agent-Owned Review+Fix Turn

After Gate, the scheduler handles due modules in canonical order. Plan Drift is
an exclusive prerequisite: if it is due, no other reviewer runs in that cycle.
Once drift is clean, Architecture runs before Technical so a material structure
proposal is not produced after a correctness repair against a design already
being reconsidered; Strategic follows as the goal/outcome perspective. Each
stage continues in the same main-agent conversation. Each reviewer owns review selection
and uses the cheapest sufficient approach for its question; each due
module owns one of the terminal module receipts. Architecture and Strategic are read-only;
Technical may repair concrete correctness failures through the retained repair
contract. Go does not invent an extra reviewer, residual Fixer, or recovery agent.

1. Read `get_pulse_state` views `module` and `backlog`, the durable
   Gate/worklist, current-run module results, and saved SQLite reviewer records. If every due module already has a
   terminal current-run result, stop. This avoids repeating completed review
   work. On recovery, inspect the current target files, runtime state, and
   verification evidence before deciding whether the Fixer should finish, roll back, or
   retry. Never trust HTML as recovery truth and never blindly reapply a
   partial fix. A `changed_unverified` result is resumed only when its named
   next valid evidence boundary has arrived; until
   then preserve it without reapplying the change or claiming it is fixed.
2. Decide the cheapest sufficient approach. Strategy Auditor and Goal Advisor
   must reason independently; delegate either as a read-only child only when
   useful, retain its execution identity, and wait for its automatic completion
   before consolidating or mutating. Engineering and LLM/Ops may be handled
   directly from one shared evidence read. A reviewer that cannot trust its evidence returns an execution
   problem or `insufficient_evidence` with an exact next-check boundary; it does
   not defer another reviewer.
   Before starting a reviewer, reconcile that module's complete active backlog
   against saved reviewer records and lifecycle events. Classify each candidate
   as existing unchanged, existing with new evidence, reopened, or genuinely
   new. Update the existing fingerprint for the first two; never file duplicate
   prose as another bug. Verify eligible prior changes before new discovery;
   verification does not consume the discovery cap. Gate decides separately on
   four perspectives: `workflow_review` (Engineering), `llm_ops_review`,
   `strategy_auditor`, and `goal_advisor`. Artifact drift, report/eval
   implementation, and store integrity are conditionally loaded Engineering
   evidence packs, not separately scheduled reviewers. A skipped lane does not
   run. If every module is skipped, record terminal skip receipts; Dashboard and
   Finalizer still run. Never combine reviewers in one shell command, background
   curl, `&`, or `wait`. Persist typed findings and verification directly; do
   not create or index a shared Markdown report.
   When both are selected, every trackable finding carries one backend-validated
   Engineering-or-Ops attribution; the lifecycle files it only under that owner.
   This keeps future Gate cadence independent.
3. Every reviewer prompt must start with **READ-ONLY REVIEW** and include the
   workflow path, Pulse run id, module name, Gate evidence pointers, relevant
   reference guidance, and a compact non-HTML response contract: module,
   verdict, next-check condition, findings, evidence, bounded recommended fix,
   verification, and whether user judgment is required with a reason.
   For the combined correctness/report/eval lane, also include the suspect step ids/attempts and tell the
   reviewer to load `read_skill(skills=[{"name":"builder-reference","path":"references/pulse-bug-review.md"}])` for the
   Exploratory QA and observable execution-trace contract whenever Gate evidence
   points to a specific step.
   Explicitly forbid file edits, config or plan changes, publishing,
   notification, user questions, mutation tools, `builder/improve.html` writes,
   and `record_pulse_result`.
   Avoid narrative recaps and wide tables. Require this compact response shape:
   one-line verdict; every evidence-backed severity-ordered finding row
   containing stable finding id, target key (file,
   step, table, metric, contract, or configuration area), plain-language claim,
   evidence pointer, bounded fix, verification, and user-judgment flag/reason.
   A clean review must explicitly return an empty finding-id manifest. Call `call_generic_agent`
   directly where exposed as a tool; in coding-agent code-execution mode it is
   not native, so its documented API bridge shell call is the supported
   transport there. Either way never wrap it in a temporary script, background
   process, or polling loop. Pass the exact
   scheduler-provided `pulse_run_id`, dated `review_run_id`, and module name on
   every call. The call returns an `execution_id` immediately: record it, end
   the current turn, and resume only from the automatic completion notification.
   Do not add a reviewer-specific
   completion marker to the instructions:
   `call_generic_agent` appends and enforces its own authoritative final marker.
   The tool rejects a provider pane snapshot that does not contain that marker
   and retries one incomplete result once.
   Use the existing specialist guidance as Workflow Review's ordered lens briefs: correctness uses
   `pulse-bug-review`; Stores Health applies `improve-learnings`,
   `improve-knowledge`, and `improve-database` as its three sub-checks; Report
   truth uses `improve-report` plus the measurement-route review (`measurement-plan`); then run stores and
   LLM/tool operations. Load all required focused docs in one `read_skill` call.
   These reference docs are read-only reviewer briefs in Pulse; they return
   fixer instructions rather than applying them.
   Do not give a reviewer `html-output`, the Pulse skeleton, CSS migration, or
   card-formatting work. Reviewers may read only the matching semantic regions
   of `builder/improve.html`; the later Dashboard stage owns presentation.
4. Independent Strategy/Goal reviewer agents only inspect and advise. The parent ends its turn after each
   asynchronous start and resumes from the automatic completion notification;
   it must not use sleep, `list_executions`, `query_step`, or a polling loop.
   The backend saves each complete human-readable Markdown result directly in
   SQLite and supplies a compact review identity in that notification. Load it
   with `get_pulse_state(view="review")` before the review stage ends. The returned JSON
   carries both human Markdown and validated structured verification outcomes.
5. For Goal Advisor, first obtain the read-only strategy review, then send that
   draft and its evidence to a separate read-only critic. The parent accepts,
   narrows, or rejects the proposal using both results. Reject a draft that does
   not lead with the current strategy ceiling, one highest-leverage materially
   different thesis, its relationship to the active strategy experiment, and
   why incremental repair is insufficient. Maintenance or instrumentation alone
   is never a valid Goal Advisor result.
6. After the independent reviewers return, the shared operational agent builds
   one structured Fix queue after its review checkpoint
   retaining every finding id, target key, severity, evidence pointer,
   recommended action, verification, and user-judgment flag. Do not repeat
   narrative reviewer prose in later reasoning.
   Reconcile it with the durable backlog before mutation. A queue item is one
   coherent repair bundle; the backend makes it durable from the disposition
   you record for it.
   Group findings only when they share a root cause, compatible target changes,
   and one verification condition. Cross-reviewer grouping is allowed, but no
   finding may disappear and there is no arbitrary queue cap. Waiting-on-run,
   waiting-on-user, proposal-only, and externally owned items remain visible
   outside the actionable queue. Build a conflict map grouped by target and
   resolve incompatible recommendations in this order:
   explicit user-approved decisions and constraints; correctness and data
   integrity; preserved goal meaning; strategy improvement; then cost and
   convenience. A lower-priority recommendation must never silently override a
   higher-priority contract. When evidence cannot resolve a material semantic
   conflict, create one focused structured human-input request describing the
   alternatives, impact, evidence, and safe default; mark only the affected
   modules blocked and do not mutate that target. Do not ask the user to resolve
   an operational conflict that the evidence and precedence rules decide.
   The main Pulse agent owns the Review+Fix turn. It may delegate genuinely
   independent read-only analysis, but it waits for those children, consolidates
   their evidence, and completes the repair lifecycle in the same conversation.
   Go never launches a residual Fixer or recovery agent. Detailed evidence lives
   in typed SQLite state; there is no reviewer Markdown or HTML recovery ledger.
7. The main agent applies bounded repair bundles sequentially with normal direct
   tools; never launch a second mutating maintenance agent. Load
   `read_skill(skills=[{"name":"builder-reference","path":"references/fix-verification.md"}])`. Before mutation,
   capture exact targets, time, hashes/versions, and latest baseline ids; a write
   or any pre-boundary artifact is not proof. Re-read `get_pulse_state(view="module")`
   and map each actionable issue to its backlog record. Every disposition carries
   `issue.id` as `finding_id`, the fingerprint from the same item, and the files
   it changed; the backend opens or reuses the fix-attempt record from that.
   IDs address records but do not determine semantic sameness. Block an
   actionable finding that lacks either value.

   After verification, give every finding exactly one evidence-backed
   disposition: `fixed_verified`, `verified_no_change`, `changed_unverified`,
   `proposal_only`, `awaiting_user`, `blocked`, or `failed`. Missing/duplicate
   IDs block that module; deduplicated cross-module IDs may point to one outcome.
   Do not trigger a side-effecting run only for proof: use
   `changed_unverified`, reason `awaiting_next_valid_run`, and name the next
   evidence boundary. Each verification records check, `passed`/`failed`/
   `inconclusive`, expected, observed, and evidence. `fixed_verified` requires
   a started attempt and only passed post-change proof; inconclusive stays
   awaiting verification; failed proof reopens.

   Call `record_pulse_result` with one `finding_dispositions` row per
   finding. For `result=changed`, also pass exact `changed_files` and factual
   `verification`; before/after refs are only real hashes, versions, or cursors.
   It commits module audit and finding lifecycle atomically. Preserve exact
   blocked/failed errors in `reason`; create no duplicate audit file or visible
   HTML proof. Checkpoint each queue item before starting the next. Recovery
   starts at the first unresolved queue item and revalidates current state
   instead of repeating a mutation.
8. Strategy changes and LLM/Ops changes remain proposal-only unless an exact
   matching request was already approved and still passes approval revalidation.
   Before applying it, compare its recorded approval basis with current state:
   target ids and runtime control path; relevant plan/config/eval/report hashes
   or versions; goal and success-criterion meaning; active experiment id;
   model/provider capability where applicable; material metric evidence and
   risks; newer user decisions; and the resolved conflict map. Unrelated drift
   does not invalidate approval, but changed semantics, missing/replaced targets,
   superseding decisions, or materially changed evidence do. Never broaden or
   reinterpret an approval while rebasing it. When stale, do not apply it: mark
   the old answer consumed with outcome `stale_not_applied`, record why in
   Reflection, and either retire it if no longer useful or create one refreshed
   decision containing the new exact edits and approval basis. Create or consume
   the existing structured human-input request as required.
9. Only the Pulse Fixer may update files, DB contracts, plan/config, report/eval
   artifacts, human-input state, changelog review state, or module state. It
   must not update `builder/improve.html` or `builder/card.health.html`. Record
   one honest terminal result for every due module with enough plain-language
   outcome data for the later Dashboard stage to render Issues and reviews,
   Decisions and analysis, and verified Fixes and improvements. Keep exact technical evidence in SQLite-backed
   reviewer results and structured Pulse state. Before stopping, reconcile the
   module's finding IDs across reviewer manifests, canonical dispositions, and
   its terminal result. Do not claim the module completed while any finding id
   is missing, duplicated without a canonical link, or lacks a durable
   disposition.
10. Within the single Fixer, call `record_pulse_result` exactly once for every due module immediately
    after that module reaches an honest terminal state, including clean,
    changed, changed-unverified, blocked, or failed outcomes.
    A reviewer failure is retained for only
    that module and does not close it before the Fixer runs. Do not
    replace a failed reviewer by improvising its deep audit in the parent; mark
    the module failed or blocked with the exact reviewer error and continue the
    independent safe modules.
    For a real finding outside workflow authority, use
    `external_action_required` with `external_owner`, stable `reason_code`, and
    a concrete `reopen_condition`. This removes it from the active Pulse queue
    and suppresses unchanged rediscovery. Keep retryable blockers as `blocked`
    and user-owned decisions as `awaiting_user`.
11. Return one concise combined result. The normal Pulse finalizer performs
    backup, publish, and the single user notification only after all due module
    results exist.

Read-only behavior is enforced by reviewer prompts, a read-only tool allowlist,
and empty reviewer write paths. The one-Fixer-per-pass rule prevents concurrent
workflow-state writes; the dedicated Dashboard prevents competing
`improve.html` writers. The backend independently enforces the three-reviewer
concurrency cap and persists one complete SQLite reviewer result per module.

## Module Decisions

Every decision needs a reason and evidence. Skips are useful only when they explain why work is not worth doing yet. Every skipped module must set at least one concrete next-check condition: `next_check_at`, `next_check_after_run_id`, or a positive `cooldown_runs`. Record that condition authoritatively through `record_pulse_worklist`; the Dashboard projects it visibly in the Gate/Worklist entry in `builder/improve.html`.

Cadence is schedule-aware. Gate reads the workflow's schedule definitions and
uses both elapsed time and completed producing-run count. Hourly workflows do
not repeat unchanged deep reviews every hour; sparse workflows running every
few days do not defer an arrived verification boundary for several more runs.
Failures, suspicious success, new verification evidence, answered decisions,
and material plan/store/cost changes override ordinary cooldowns. A Pulse pass
alone is never evidence that a review lane is due.

Cadence remains agentic. New evidence can override any earlier cooldown or next-check suggestion, but when Gate checks a module earlier than previously planned, its reason and the visible Gate entry must say what new evidence caused the override. Do not silently ignore the prior cadence.

Every Gate must re-judge current goal evidence even when a prior
`next_check_at` or Advisor experiment `data-review-after` is still in the
future. A checkpoint is a planned evidence boundary, not a lock. The agent may
keep the checkpoint only when current evidence still shows that waiting is the
most informative and cost-conscious choice.

### Independent off-track lenses

Workflow Review checks whether execution and its supporting operational system
are correct. Strategy Auditor checks what is
missing or weak inside the current strategic shape and normally runs more
frequently than Goal Advisor. Goal Advisor independently performs a selective
blank-sheet search for materially different approaches at its own opportunity,
decision, experiment, or healthy-headroom checkpoint. These are distinct
questions, not an escalation chain, and they may run in the same parallel batch.

Gate evaluates each lens independently. Evidence unreliability changes a
reviewer's own classification; it never creates a scheduling edge between
reviewers.

### Reviewed-baseline rule

A successful workflow run is evidence for a review; it is not a substitute for
one. Gate must not skip a module merely because the latest run completed, its
steps returned success, or no explicit error was recorded.

Before Gate may use `skipped` as a normal cadence decision for a review module,
`builder/improve.html` must contain at least one completed, evidence-backed
baseline review for that module. The baseline must name what was inspected, the
run or artifact scope, its verdict/findings, untested risks, and the next-check
trigger. A SQLite `done` value without the corresponding durable HTML review is
not sufficient.

After a baseline exists, cadence is driven by **review outcomes**, not run
outcomes. Track a review streak from durable HTML history for every module:

- a completed clean review may lengthen that module's interval by one bounded
  step;
- repeated clean reviews may continue to lengthen it up to a risk-appropriate
  cap;
- a review with findings, insufficient evidence, an unverified repair, or a
  blocker shortens the interval and records the evidence needed next;
- a material plan, prompt, model, tool, schema, control-path, report/eval
  contract, or success-criterion change resets the affected module to a short
  interval even when recent reviews were clean;
- a contradiction, concern, suspicious success, recurring defect, missed goal,
  or reached checkpoint makes the affected review due immediately.

Successful workflow runs accumulate evidence for the next review, but they do
not count as clean reviews and do not increase `cooldown_runs` or postpone a
review by themselves. Gate may skip a repeat only until the checkpoint selected
by the last completed review. Its skip reason must cite that review, the review
streak/cadence rationale, and any fresh run evidence. Do not continuously move a
review's checkpoint forward just because more runs succeeded.

Use bounded adaptive backoff rather than fixed universal timing. A newly
baselined or recently changed high-risk module should be reviewed again after a
small number of meaningful runs or short business-time interval. Expand only
after the next actual review is clean. Keep safety-critical, side-effecting,
financial, publishing, authentication, and externally communicating paths on a
tighter maximum cadence than passive reporting or documentation checks.

Do not force every missing baseline into one expensive Pulse. Prioritize Bug
Review first, then modules with current risk signals, and stagger the remaining
first reviews across explicit near-term checkpoints. Until its first review is
complete, describe a deferred module as `baseline pending`, not `healthy` or
`clean`, and record exactly when or after which run it will be reviewed.

Use these module names exactly:

- `workflow_review`
- `strategy_auditor`
- `goal_advisor`

The historical focused operational names below are ordered lenses inside the
single `workflow_review` agent. They are not worklist modules and never launch
separate agents.

### workflow_review

#### Correctness lens (formerly bug_review)

Mark due for real Bug findings:

- failed, skipped, or empty steps
- hallucinated or unsupported step success
- broken eval/report layers that make evidence untrustworthy
- selector/API/runtime breakage
- stale guards, validation, retry, or defaulting behavior
- compact evidence that a successful step may have chosen the wrong
  tool/source/route, used stale inputs, ignored returned evidence, or made an
  unsupported decision; this makes targeted trace review due, not a full-run
  conversation audit. When the targeted trace confirms a wrong-tool-call or
  retry-without-cause pattern, quote the step's own timing log
  (`runs/<run_folder>/logs/<step-id>/execution/*-timing.json`) alongside the
  finding — a wrong tool call that added 20 seconds and one that added 8
  minutes are the same correctness bug but a different priority; the finding
  is incomplete without that number when the log makes it available. Do not
  open every step's timing log by default — only the step(s) the targeted
  trace review already flagged.
- a claimed state/config/status repair whose expected behavior is absent from
  the next applicable decision or run, or whose real runtime consumer and
  canonical store cannot be named. A successful write to a plausible table is
  not proof that the allocator/router/executor read it.
- a step whose validation gate can pass on a **self-asserted marker** — a
  `context_output`/"done" file the step wrote itself — without proving the real
  effect (persisted db rows, the authoritative external system, or a genuine
  deliverable carrying run-specific proof). The gate must fit the step's real
  output; recommend the check that proves the effect, not db by default.
- duplicate or shadow control stores for the same logical entity (for example,
  two strategy/arm tables) where writers, readers, or mirroring rules can drift

Also mark Workflow Review due for a bounded exploratory QA checkpoint when any of
these conditions holds:

- this workflow has never completed an exploratory QA checkpoint
- a material plan, step, behavioral contract, tool, provider, or model change
  landed since the last checkpoint
- enough new outcome-bearing runs have accumulated to test a previously thin or
  uncertain path
- a previously recorded risk checkpoint or business-time checkpoint has arrived
- new failure, contradiction, `CONCERNS:`, or suspicious-success evidence appears

### Off-track goals tighten Workflow Review cadence

When a defined material success criterion is trustworthily below target,
declining, or stalled, treat that as a direct reason for more frequent bounded
exploratory QA even when every step completed and no `CONCERNS:` marker exists.
If no clean Workflow Review correctness lens covers that miss and its relevant
control path, mark Workflow Review due now. Independently select Strategy Auditor when the current plan
needs an in-strategy effectiveness audit, and independently select Goal Advisor
when its less-frequent blank-sheet checkpoint is due. Never defer one because
another is dirty, missing, or selected.

If no exploratory QA checkpoint was completed after the latest observed goal
miss, Workflow Review is due now. While the goal remains off track, choose a short
next checkpoint based on a small number of meaningful outcome-bearing runs,
exposures, or elapsed business time. A technically clean run, green eval, or
absence of explicit concerns does not justify a long calendar cooldown. Re-run
the review when that checkpoint arrives and compare with its prior evidence.
Consecutive finding-free reviews over unchanged runtime paths may widen the
checkpoint gradually; continued lack of progress, a material plan/config/tool
change, a new concern, or contradictory evidence tightens or resets it.

Do not run exploratory QA on every high-frequency Pulse. When it is not due,
cite the last completed exploratory QA baseline plus the current clean evidence,
then record a concrete next check based on risk, meaningful outcome-bearing
runs, elapsed business time, or a material change. A new failure or suspicious
signal overrides that cadence immediately. A successful run with no prior QA
baseline cannot justify skipping this checkpoint.

The read-only reviewer scopes the defect from run/eval evidence, execution logs,
validation, prompts/config, stale artifacts, and evidence-chain breakage, then
returns exact findings and verification steps; the Pulse Fixer applies and
verifies the bounded repair directly. The reviewer and Pulse Fixer load
`read_skill(skills=[{"name":"builder-reference","path":"references/pulse-bug-review.md"}])` for the full read-only contract:
the Exploratory QA behavioral-contract and risk-matrix method, the control-path
reachability check (`wrong_store_write`, `shadow_store_drift`,
`dead_configuration`), the observable execution-trace review, and the finding
classifications (`correctness_bug`, `efficiency_or_coaching`, `no_issue`,
`insufficient_evidence`). The Pulse Fixer repairs only confirmed
`correctness_bug` findings. Keep `efficiency_or_coaching` as evidence for this
same agent's LLM and operations lens; it is not a second module or agent. Gate
does not load that doc; it decides only whether `workflow_review` is due from
the triggers above.

#### Artifact evidence pack (Engineering Review; formerly artifact_review)

`get_pulse_state(view="module")` returns `plan_change_backlog`: the exact count of
changelog entries not yet stamped `artifact_review.done`, newest first, with each
entry's reason, affected step ids and changed field names. Use it instead of
deriving the backlog from the changelog files yourself. It is evidence, not a
verdict — many changes have no blast radius — and entries stay listed until
`mark_changelog_artifact_reviewed` stamps them, so deferring loses nothing.

Mark due when that backlog is non-empty, or when plan/config changes may have drifted dependent artifacts:

- reports
- evals
- DB contracts
- KB notes or step KB config
- learnings or learning-access drift
- saved code artifacts
- step prompts/configs

The read-only reviewer follows
`get_workflow_command_guidance(kind="review-artifact-drift")` to identify drift.
The Pulse Fixer records the review result and uses
`mark_changelog_artifact_reviewed` for fully inspected entries. Artifact review
remains report-only; it does not repair the reviewed artifacts in this module.

Engineering Review is the sole dispatcher for `plan_change_backlog`-driven
triggers across all six dimensions above. Gate selects one Engineering
perspective with the relevant evidence packs instead of paying for separate
artifact and dependent reviewers.

#### Report implementation evidence pack (Engineering Review; formerly report_health)

Load this Engineering evidence pack when the reporting dashboard is stale,
misleading because of implementation, broken, or not using live persisted
evidence correctly. A report that works as designed but measures or presents
the wrong business outcome belongs to Strategy Auditor, not Engineering.
Also mark it due when an approved Goal Advisor measurement step produces its
first trustworthy rows, changes its schema/definition, or reaches a review
checkpoint whose metric is not yet visible in the dashboard. A proposal without
approved data collection is not enough to create a KPI tile.

Good report-health work makes the report easier for the user to understand:

- clear goal progress
- current plan and strategy
- blockers and issues
- live SQL/window.report evidence
- compact visual cards before long text
- accurate tabs/sections and responsive layout

The read-only reviewer follows `improve-report` as its audit checklist and
returns exact recommended HTML/report-plan edits. The Pulse Fixer applies and
verifies bounded report-only fixes and records the module outcome.

#### Evaluation implementation evidence pack (Engineering Review; formerly eval_health)

Load this Engineering evidence pack when measurement evidence cannot be trusted
because its implementation is broken. A measurement that faithfully implements
its current queries but the queries measure the wrong business outcome belongs
to Strategy Auditor:

- the measurement route is missing, stale, too lenient, or not mapped to `soul.md`
- measurement rows are missing, scoped to the wrong run/group, or stale
- rubric/thresholds can be gamed or mostly duplicate operational completion checks
- measurement claims cannot be reconciled with DB/report evidence
- plan, DB, report, or output contracts changed and measurement coverage did not follow

The read-only reviewer follows the `measurement-plan` reference as its audit
checklist. It returns bounded recommendations and verification steps. The Pulse
Fixer applies safe correctness repairs, validates them, and records changed
measurement artifacts as a `Measurement fix` in `builder/improve.html`.

#### Store-integrity evidence pack (Engineering Review; formerly stores_health)

This Engineering evidence pack covers three stores in one pass: learnings (HOW to run the task), the
knowledgebase (domain facts), and db/db.sqlite (structured run state). All
three share the same due-cadence mechanism (the general Reviewed-baseline
rule, no special throttling), the same freshness-recency check shape, and the
same bounded-fix authority — only the content domain differs — so one
due-decision and one Fixer pass covers all three, each with its own small
checklist inside.

The three checklists produce one reconciled `ownership_manifest`. For every
misplaced or duplicated semantic item it records `item`, `current_location`,
`semantic_type`, `authoritative_owner`, `duplicate_locations`,
`migration_or_removal_action`, and `verification`. Apply one rule across all
stores: **one semantic item, one authoritative owner**. Soul owns why/goals/
preferences/hard constraints; Plan and step config own current behavior;
Validation owns deterministic proof contracts; Learnings owns reusable
execution HOW; Knowledgebase owns durable domain facts with provenance; DB owns
structured operational state; Pulse owns findings, diagnosis, attempts,
decisions, and fix verification. Other stores may reference the canonical
record by stable ID/path, but may not keep a second copy that can drift.

Mark due on any of:

- **Learnings**: plan or prompt changes affected step behavior;
  `learning_objective` no longer matches the step; `learnings_access` should be
  reduced to read because writes are stale/redundant or restored to read-write
  because a run discovered reusable HOW-to knowledge worth capturing;
  selectors/API quirks changed; the complete skill package has no recorded
  purity baseline; or `SKILL.md` / any reference contains business facts, run
  results/current status, owner-value copies, current strategy, incident or
  decision history, provenance, or platform architecture that is not required
  as a runner-facing procedure.
- **Knowledgebase**: KB notes or KB config are missing, duplicated, stale,
  contradictory, no longer aligned with the plan, have no complete purity
  baseline, or contain Soul/Plan/Validation/Learnings/DB/Pulse-owned material.
- **DB**: schema, table contracts, upsert rules, report SQL, eval consumers,
  or `db/README.md` no longer match current writers and readers; multiple
  tables/files encode the same logical control state with unclear canonical
  ownership or synchronization invariant; a claimed DB repair changed a store
  the runtime decision path does not actually consume; or content-bearing
  TEXT/JSON columns act as hidden Soul/Plan/Validation/Learnings/KB/Pulse
  stores instead of structured operational state.
- **freshness (confirmation recency), learnings and KB**: `learnings/_global/`
  or `knowledgebase/notes/` has content but its own
  `_freshness.json.last_confirmed_run` is many runs / a long business interval
  behind the current run — no current step re-confirms it, so the content may
  have silently gone stale even though nothing has contradicted it. A missing
  `_freshness.json` beside existing content means no freshness baseline yet;
  mark due to establish one. When fresh, record the confirmation cadence and
  skip with a next-check.

Plan-change-driven drift — a recent edit leaving learnings, KB notes, or DB
contracts stale — is handled once by the artifact lens above. The stores lens
must reuse that conclusion rather than file a duplicate finding from the same
`plan_change_backlog` evidence.

The read-only reviewer follows `improve-learnings`, `improve-knowledge`, and
`improve-database` as its three checklists. `knowledgebase/context` is
user-owned runtime business context — do not inspect or rewrite it during KB
maintenance; use explicit user-provided evidence when its meaning matters.
The Pulse Fixer applies bounded learning/step-config edits, bounded KB
note/config changes, and bounded DB contract fixes directly (never speculative
row migrations), each independently verified, using absolute workspace paths
in reviewer prompts and evidence.

For the learning dimension, review the complete skill package rather than a
sample: the root `SKILL.md`, every content-bearing Markdown reference, and every
effective `learnings_access="read-write"` objective. References are part of the
skill. Detailed reusable HOW may move from the root into a reference; non-skill
content must leave the entire package and be routed to its authoritative store.
The Fixer must re-run the same semantic classification after editing. A lean
index or valid Markdown shape alone is not proof of purity.

For the KB dimension, inventory every content-bearing note file and return a
`kb_purity_manifest`; large notes may be read in bounded chunks but no file may
be omitted. For the DB dimension, return a `db_ownership_manifest` covering
each relevant table and content-bearing TEXT/JSON column with bounded samples,
writers, consumers, lifecycle, and semantic owner. Reconcile all three outputs
into the one Stores `ownership_manifest` before filing findings, so the same
misplacement is not reported once per store.

Recommend `learnings_access="read"` for mature or redundant contributors only
after the relevant complete manifest is clean and the current plan still
matches it. Decide per step from its effective objective, description hash,
recent successful runs, and `.learning_metadata.json`; shared
`learnings/_global/` content alone is insufficient. Restore read-write access
when drift creates new reusable HOW. `lock_code` remains the separate, stricter
executable stability decision. For KB, recommend changing only the mature/no-op
contributor's `knowledgebase_access` to `read`; never freeze unrelated writers
workflow-wide.

Load `assumption-audit` for all three: reusable HOW must not preserve business
policy, fixed strategy/architecture, or an unverified limitation as if it were
permanent; KB notes must distinguish durable domain evidence from beliefs
copied out of the current plan; schemas and enums should not unnecessarily
freeze one source, channel, entity, group, or tactic.

#### LLM and operations lens (formerly llm_ops_review)

This is the single agentic Ops Review for cost, timing, LLM selection, tool-call
quality, runtime operations, setup, and plan-design hygiene. It is not Goal
Advisor and is not automatically due every Pulse. High-frequency workflows
should normally roll up several runs. Mark it due when it has never completed,
its planned checkpoint arrives, telemetry is missing or unpriced, cost or
latency changes materially, model/tier configuration changes, a prior
finding needs follow-up, an answered `llm-ops-*` request is waiting, a prior Workflow Review recorded `efficiency_or_coaching` trace evidence
for follow-up, readiness
materially changes, or
enough plan/schema/prevalidation changes have accumulated.

Read workflow execution cost, evaluation cost, builder/Pulse overhead, token
usage, model/tier evidence, timing summaries, and representative conversation
and tool-call traces. This pass remains agentic: no deterministic Go detector
is assumed. The reviewer must inspect and reason about all of these:

- event correlation across tool start, end, and error records
- nested JSON, MCP content envelopes, shell envelopes, and structured results
- argument identity, exact/near duplicate calls, retries, and repeated work
- failure-status precedence and errors hidden inside nominally successful calls
- HTTP failures, bad status codes, path/database access failures, and wrong paths
- measured versus missing timing, timeout risk, and unexplained wall time
- serial versus parallel execution and whether batching was actually possible
- oversized arguments/results, truncation, excessive rewriting, and tool thrash
- cost attribution, missing/unpriced evidence, and double-counting hazards
- recurring operational patterns across comparable runs
- whether returned tool evidence was read, interpreted, and used correctly
- whether the selected tool, source, workspace, run, group, table, endpoint,
  IDs, filters, time window, and destination were semantically correct

Classify evidence as proven failure, review candidate, or evidence gap. A zero
duration is unmeasured, not instant. A zero exit code containing explicit error
evidence is suspicious, not clean. Agentically judge necessity, impact, whether
a retry/duplicate/serial call was justified, and the bounded recommendation.
Do not label every alternative tool choice as a defect.

For raw ledgers under `costs/execution/`, preserve the
immutable record identity: `execution_id`. `date + scope +
group_folder` locates the ledger shard; `run_folder` and
`archived_run_folder` are mutable display metadata, not identity. In particular,
never merge two records merely because both say `iteration-0/<group>`: rotation
reuses that active path. `run_folders` is a legacy fallback only when no
ID-keyed record exists. The same step ID in two groups is still two separate
rows. Within each execution record and model, `by_model` is the authoritative LLM total and
`by_step_and_model` is attribution already included in that total;
never add them together. Reconcile
`unattributed = max(0, by_model - sum(by_step_and_model))` per model. An explicit
`workflow_orchestrator` row is already attributed. Report attribution overflow,
missing buckets, and unpriced calls instead of estimating. Report a positive
remainder as unattributed/orchestrator cost. A step with no LLM row may be a
scripted/zero-LLM step; historical step-less totals remain unattributed and
must never be presented as a synthetic step named after the run-folder.

Sample representative cost/time-material agentic steps under
`runs/<run_folder>/logs/<step-id>/execution/`, including comparable earlier runs
when recurrence matters. Do not open every trace. Group findings as cost, time,
tool/runtime reliability, quality, or setup, and preserve the complete evidence
needed for Bug Review when the Ops pass discovers a correctness defect.

**The plan-design sub-check has its own bootstrap trigger, separate from Workflow Review's general history.** Historical `llm_ops_review` entries count only when they actually applied the `design-plan` structural checklist; a cost/tier-only entry does not. If no such entry exists, mark `workflow_review` due so consolidation does not inherit a stale narrower baseline.

Inspect resolved provider/model/options configuration and actual step/eval tier use. Inventory every exact model pin in explicit workflow roles and planning/evaluation step config (`execution_llm`, `validation_llm`, and orchestrator overrides). Call `list_provider_models` once for each pinned provider and use its catalog plus `default_tier_models` as the authoritative current comparison. Classify a pin as unavailable/deprecated, still supported but different from the provider-owned role/tier default, or current. Never infer recency by sorting model names. Provider-profile workflows inherit current defaults and must not be reported as stale merely because their resolved model changed after an app update. Check whether high, medium, and low are configured and used sensibly; whether repeated low-risk validation, extraction, formatting, or summarization uses an unnecessarily expensive tier; whether eval/verification would benefit from provider diversity; whether Pulse and Maintenance models are sensible. Also check report publishing/password protection, notification instructions/setup, backup status, and workflow-version readiness.

This module also owns plan-design hygiene — engineering correctness, not strategy. Load `get_workflow_command_guidance(kind="design-plan")` as a read-only structural checklist and apply it to the current plan: step-type fitness (is each step the right type — scripted, message_sequence, routing, todo_task, orphan), prevalidation fitness (an unjustified or redundant `prevalidation` item that just restates the step's own final gate), schema/description drift (a `validation_schema` demanding a field the description never asks the agent to produce, or a non-nullable field the step's own branching logic can legitimately leave absent, forcing a fabricated value), and the rest of that doc's integrity checks and design lenses. Do not judge whether the plan's tactic is good — that is Strategy Auditor's job; judge only whether the plan is built correctly given whatever tactic it already has. Findings here follow the same evidence-backed, bounded-fix-or-decision-request path as the rest of this module.

For every cost- or time-material `todo_task`, `routing`, or
`message_sequence`, review the parent and its children as one execution unit.
State what decision the parent actually makes. A fixed, prescribed child set and
order is a structural review candidate; dynamic selection, conditional fan-out,
adaptive retry/recovery, concurrency, user/approval boundaries, or necessary
synthesis may justify the container. Repeated source/schema/data discovery
across parent and children is evidence of a weak handoff, while an independent
clean-room verification read is not automatically duplication. This remains an
agentic evidence judgment: there is no numeric threshold, Go classifier, or
automatic plan rewrite.

Goal quality outranks tier savings. When any material success criterion is
trustworthily below target, do not recommend lowering the model or reasoning tier
for an outcome-bearing, planning, judgment, diagnostic, recovery, evaluation, or
verification step. Preserve its current tier while Bug Review and Goal Advisor
determine whether capability, instructions, plan shape, evidence, or strategy is
limiting the result. A downgrade may still be proposed only for a strictly
mechanical, deterministic, non-bottleneck step when representative at-target
evidence proves the cheaper tier produces equivalent outputs and downstream
outcomes, and it must remain an explicitly approved reversible trial. Missing
quality evidence is not evidence for a downgrade. Cost pressure should be
reported separately rather than traded for goal quality silently.

Keep one compact **LLM & operations recommendations** area in `builder/improve.html`, with recommendation cards grouped as cost saving, quality, reliability, or setup. Every recommendation must show the current state, exact suggestion, reason, expected benefit, risk, and evidence. Do not create generic best-practice noise or duplicate an open recommendation.

Configuration changes require the existing human-input flow. For a stale exact pin, prefer clearing the pin so the role/step inherits its tier when no model-specific capability is required; otherwise propose one exact replacement model and supported reasoning options. Use `create_human_input_request(source="pulse", input_id="llm-ops-<stable-slug>", options=[approve,reject,defer], allow_free_text=true, context="<plain-English current model + affected role/steps + exact proposed clear/replacement + capability/cost/reasoning comparison + approval basis + expected impact + risk>")`. The visible choice must mean Upgrade, Keep current, or Decide later. The approval basis must identify the current resolved provider/model/options state, affected config/step ids and hashes or versions, evidence as-of run/date, and assumptions that must still hold. A newer catalog model is a review candidate, not proof that it is better; ask only when the replacement is supported and materially useful. Keep at most two open LLM/Ops decisions. On a later run, revalidate that basis and apply only an explicitly approved exact edit with normal LLM/workflow/step config tools. If provider capabilities, target ids, config semantics, user intent, or material cost/quality evidence changed, consume the old answer as `stale_not_applied` and create a refreshed request only if the recommendation remains useful. Verify an applied edit, record the outcome, and call `mark_human_input_consumed`. Reject, defer, and custom answers are recorded and consumed without applying the proposed edit. Never invent models, providers, recipients, destinations, passwords, secrets, or credentials; never publish or notify from this module.

### strategy_auditor

This is the recurring current-strategy improvement layer, so its checkpoint is
normally shorter than Goal Advisor's. Mark it due when independent
plan-versus-goal diagnosis is needed:

- it has never completed a cross-run strategy baseline and enough comparable
  outcome-bearing evidence exists to attempt one
- activity rises while the actual `soul.md` outcome is flat, falling, or unknown
- target/cohort repetition, source/channel concentration, saturation,
  diminishing returns, weak exploration, or proxy optimization is plausible
- a material tactic or plan version changed and enough valid post-change
  exposures exist for comparison
- a prior strategy finding reaches its run, exposure, outcome-lag, or
  business-time checkpoint
- the workflow does not persist the stable target/source/action/outcome linkage
  needed to distinguish strategy hypotheses

Keep the next Auditor checkpoint no later than Advisor's, but never use one
reviewer's cadence or result as a prerequisite for the other. After a bug fix,
the Auditor may return insufficient evidence until new valid outcome data exists.

Load `read_skill(skills=[{"name":"builder-reference","path":"references/strategy-auditor.md"}])`. The reviewer reconstructs
the goal-to-action-to-target/source-to-outcome causal chain and uses bounded
read-only queries against existing workflow tables plus retained run/eval
evidence. It compares comparable windows and plan versions, reports counts and
denominators, checks outcome lag, segments by supported sources/targets/cohorts/
routes/groups, and tries to falsify its leading explanation.

It returns exactly one primary classification: `strategy_flaw`,
`execution_bug`, `measurement_gap`, `insufficient_evidence`, or
`no_material_problem`. A database that cannot distinguish new from repeated
targets, identify acquisition sources, or join actions to outcomes creates a
`measurement_gap`; absence of that evidence is never a clean strategy result.
The result names the exact missing field/event and decision it prevents rather
than asking for a generic metrics subsystem.

Strategy Auditor is read-only. It never edits a plan or DB, runs a producing
action, creates a human-input request, selects a replacement tactic, or applies
a fix. It returns bounded recommendations for missing pieces or corrections
inside the current strategy. Preserve `execution_bug` and attribution/eval
observations as out-of-scope evidence for consolidation without waiting for or
invoking another reviewer. Strategy Auditor and Goal Advisor may run in the same
parallel batch and must reason independently.

When it is skipped, cite its last completed baseline, the current comparable
outcome/concentration evidence, and a concrete next run/exposure/time or plan
change checkpoint. A clean execution or green eval cannot substitute for the
first Strategy Auditor baseline.

### goal_advisor

Mark due when independent blank-sheet strategic judgment is needed:

- a planned opportunity/headroom checkpoint asks whether a materially different
  approach could outperform the current strategic shape
- trustworthy outcomes show a persistent ceiling or changed external opportunity
  worth challenging independently of the current plan
- a user answered a strategic question
- a healthy workflow reaches its previously scheduled headroom checkpoint
- an active `.advisor-experiment` has an answer, reaches `data-review-after`,
  accumulates enough measurement evidence, becomes blocked/unblocked, or gains
  decisive contradictory evidence

A miss may independently tighten Bug Review and Auditor cadence; it does not
alone launch Advisor. Run Advisor at its own blank-sheet opportunity, decision,
or experiment checkpoint. Instrumentation-only tracking cannot suppress action.
For unmeasured criteria, Goal Advisor may state the evidence limitation without
waiting for Auditor and must not substitute instrumentation for a new strategy.

An active strategy experiment earns that deferral only when Gate verifies all of the
following from current evidence:

1. The approved change is applied and reachable in the real runtime control
   path, not merely described in a plan, config, or file that runtime does not
   use.
2. The experiment's primary and diagnostic measurements are fresh and
   persisted in the intended durable store.
3. Meaningful valid outcome-bearing runs or exposures have occurred since the
   change was applied. Zero valid outcome-bearing runs means the experiment has
   not received a fair test.
4. No current bug, blocker, artifact drift, or plan drift prevents the
   experiment from receiving the test it claims to be receiving.
5. The planned checkpoint is still proportional to the workflow cadence,
   exposure rate, and latest evidence. New contradictory evidence or a flat
   trustworthy goal metric can justify reviewing earlier.

When an evidence checkpoint arrives or trustworthy contradictory outcomes
challenge the thesis, select Goal Advisor to advance, revise, or recommend
retiring that experiment. Independently select Workflow Review for operational
defects. Goal Advisor may return
insufficient evidence for the affected claim while still completing any
trustworthy blank-sheet review. When Goal Advisor is skipped, the visible
Gate entry must name the experiment id, implementation/control-path evidence,
valid run or exposure count, latest goal measurement and freshness, why the
checkpoint is still fair, and the exact evidence that would trigger earlier
review. Do not ask the user to decide an operational checkpoint that the run
evidence can decide; reserve human questions for real business or strategy
judgment.

Goal Advisor is a Pulse-selected module, not a separate recurring schedule. Its
expensive thinking stays outside the parent context through a read-only strategy
reviewer followed by a separate read-only critic. The parent Pulse Fixer uses
their combined evidence to record a proposal, advance the active strategy experiment, or
apply an exact previously approved proposal. It does not launch
the retired standalone Goal Advisor pipeline and does not poll background executions.

Goal Advisor also challenges consequential assumptions embedded in soul, plan,
steps, evals, KB, learnings, DB, or reports. It must distinguish user-approved
constraints from agent-created choices and maintain the top **Assumptions
challenged** section when those choices may cap the goal.

When the current strategy appears capped, or repeated goal misses/bugs/cost
evidence suggest the plan shape itself is limiting outcomes, Goal Advisor may
propose `simplify`, `restructure`, or a bounded `experiment` — a materially
different strategic shape, not a structural-hygiene fix (that belongs to
Workflow Review's LLM and operations lens; a recurring problem *caused by*
mistyped steps or drift stays there instead). Compare the current plan with at most two credible
alternatives; state expected benefit, affected goal criterion, evidence, risk,
migration/rollback shape, and how the change would be measured. The separate
Goal Advisor critic must challenge whether the recommendation is actually
better than the current plan, not merely different.

An active advisor experiment blocks a second competing experiment. During
`measuring`, Goal Advisor may inspect whether plan structure, instrumentation,
or implementation prevents the active experiment from receiving a fair test —
the same "no current bug, blocker, artifact drift, or plan drift" condition
that governs experiment deferral above. It may recommend `keep` or a repair to
the approved experiment, but must not create an unrelated bold idea. Material
semantic or structural changes still require the existing decision-card flow;
only an exact previously approved proposal may be applied by the Pulse Fixer.

The background Goal Advisor thinks like an experienced operator. It may apply a structural plan change only when the user already approved a Goal Advisor proposal in `report_human_inputs`. New strategic changes must be logged as proposal-only Advisor ideas and, when a decision is needed, created with `create_human_input_request`. When success cannot be judged from persisted evidence, the proposal may define a small decision-useful metric set and the exact normal `regular` measurement step needed to write timestamped rows to `db/db.sqlite`; this is a plan change, not a separate metrics subsystem. Report Health visualizes it only after approval and real data.

Goal Advisor also owns one durable 10x/headroom **strategy** experiment lifecycle in
`builder/improve.html`. There may be only one active strategy `.advisor-experiment` card
(`proposed`, `deferred`, `approved`, `running`, `measuring`, or `blocked`) at a
time, and new cards use `data-experiment-kind="strategy"`. Instrumentation-only
tracking does not count as that experiment and cannot block the strategy scan.
Pulse advances or measures the strategy card at its checkpoint; it does not create
daily bold-idea spam. Terminal states are `adopted`, `rejected`, and `retired`.
When no experiment is active, a due healthy-headroom review applies the 10x
counterfactual and may propose one bounded experiment while preserving the
successful baseline.

Goal Advisor does not do routine correctness review, learning cleanup, KB cleanup, DB cleanup, or normal report repair. Those are Workflow Review lenses.

## Human Input

If Pulse, Goal Advisor, or a module needs the user to decide something, create a durable request with:

`create_human_input_request(workspace_path="<current workflow>", source="pulse|goal_advisor", ...)`

For Goal Advisor plan-change proposals, use the existing interaction shape instead of a separate tool or file:

- `source="goal_advisor"`
- `input_id="plan-proposal-<stable-slug>"`
- options: `approve`, `reject`, and `defer`, each with a short title and description
- `context`: proposal, exact intended plan/config/eval/report edits, rationale, expected impact, risk, evidence paths, and an approval basis containing proposal Pulse/run/date, active experiment id, exact target ids, relevant artifact hashes or versions, success-criterion meaning, metric evidence as-of, and assumptions that must remain true

On a later Pulse run, revalidate that approval basis before applying an approved proposal. Unrelated file changes do not make it stale, but changed target semantics, replaced plan/config/eval/report objects, changed goal meaning, superseding user decisions, invalidated assumptions, or materially changed evidence do. Never silently rebase or broaden the approved scope. Apply a still-valid proposal with normal plan/config/eval/report tools and then mark it consumed with `mark_human_input_consumed`. Consume a stale approval as `stale_not_applied`, record the reason, and create a refreshed proposal only when the same decision is still needed. Rejected or deferred proposals should be recorded and consumed, not silently retried. Record the outcome in durable module/human-input state; the Dashboard adds or updates the short outcome card under the component that asked it. Pending questions are not duplicated in HTML.

Do not ask only in email or raw chat. Runloop renders the structured request first as **Needs your decision** from SQLite. When a later pass uses an answer, call `mark_human_input_consumed` and record the answer and outcome once in Reflection.

## Finalizer And Notifications

Pulse sends one summary every run unless the workflow's structured notification
configuration disables that channel. `workflow.json` is canonical: apply
`capabilities.notifications`, the Pulse summary channel settings, recipient
blocks, and `exclude_channels`. Never infer notification preferences from
`soul/soul.md`.
Enabled account-level notification channels (for example Gmail) are inherited automatically and count as enabled for this workflow. Do not skip notification merely because the workflow has no dedicated Slack webhook, and do not write a redundant Gmail setting into `workflow.json`.

Before finalizing, read `get_pulse_state(view="module")` and confirm every module marked
due for this `pulse_run_id` has a terminal module result. If any due result is
missing, do not publish or notify a complete Pulse. Run each unresolved
module's independent read-only review when needed, then run the one consolidated
Fixer to record all unresolved results before continuing finalization. Never silently treat a
missing result as skipped or successful.

Dashboard/questions run in a focused turn that loads `review-improve-log`,
writes and reads back `builder/improve.html`, refreshes
`builder/card.health.html`, creates real `report_human_inputs`, and records its
result. Success is rejected when the HTML is unchanged, uses the retired format,
or lacks the current Pulse handoff.

Backup, publish, and notify then share one ordered finalizer turn. Mark each
`running` then terminal with `record_pulse_result(command=...)`; leftover
waiting/running commands become failed. Backup may skip only when its source
hash proves the current state is backed up. Publish skips when disabled,
unverified, or current and never verifies first publish unattended. Notify runs
last even after an earlier command failed.

The notify turn should include:

- Bug and Goal state
- modules that ran, their conclusions, and modules that skipped or failed
- material issues newly found or reopened during this pass
- verified fixes and verified no-change closures completed by the Pulse Fixer
- changes still awaiting verification; never call these fixed
- the exact active pending count after the pass and the highest-priority
  current/retained pending issues, with blocker, owner, and next checkpoint
- user questions created and what each answer unblocks
- backup/publish status
- dashboard URL when publish is live
- cost/time summary, including missing or unpriced buckets

Build this from current-run module/reviewer records plus finding lifecycle and
human-input state in SQLite-backed tools, not from dashboard prose. A module
result of `changed` is not itself proof that its findings were fixed. Only
`fixed_verified` or `verified_no_change` with passing evidence belongs under
**Fixed by Pulse**. Put `changed_unverified`, failed verification, open,
acknowledged, fixing, awaiting-verification, blocked, proposal-only, and
awaiting-user items under **Still pending** or **Needs your decision**. Report
the top five pending issues plus the remaining count when the backlog is long.
Explicitly say when no material issues were found, but never describe an
uncompleted review as clean.

Backup protection must be stated plainly. Read `backup/status.json` and the
configured destinations, not only whether the latest Git command succeeded. If
status is `local_only`, or no verified destination is off-device, include a
prominent **Backup risk: local only** warning: the checkpoint helps undo an edit
but will be lost with the laptop. Never describe this state as healthy,
protected, or fully backed up. Recommend configuring at least one verified
remote Git or object-storage destination. Keep the warning in every Pulse
notification until off-device protection is verified; deduplicate any matching
dashboard recommendation or human-input request rather than creating a new one
each run.

When Gmail/email is available, default to rich email: set `email_subject` and
one compact, inline-styled `email_html` body on the same `record_summary` call.
Use readable sections for **Pulse verdict**, **Reviews completed**, **Issues
found this pass**, **Fixed by Pulse**, **Still pending**, **Needs your
decision**, and **Operations**. Omit an empty decision section, but do not omit
the issues/fixes/pending outcome: state “none” where that is the truthful result.
Do not produce a separate plain email body; `message` is the automatic
fallback. Recipients come from the saved summary settings. Use `send_email` for an explicitly
requested message to named recipients.
For Slack, default to the backend-owned rich rendering on that same call: set `slack_title`, a factual `slack_color`, compact `slack_fields`, relevant `slack_sections`, and `slack_footer`. Never read a webhook secret or post Block Kit JSON directly.

## Style

Write for the user first:

- takeaway first
- short labeled details after
- evidence paths last
- no long semicolon chains
- no compressed internal jargon unless also explained in plain language
- visual cards and chips before long text

Never invent values, trends, costs, or eval results. If evidence is missing, say exactly what is missing and why that matters.
