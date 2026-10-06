[← goals / pulse / reviews](index.md)

# PLAT-138 — Engineering Review buries prior-fix verification inside one oversized, unbounded contract

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse/reviews |
| Summary | The first live standalone Operations Review exposed a second persistence gap. |

> **2026-09-10 update:** [PLAT-306](plat-306.md) supersedes mandatory Markdown
> checkpoint/report maintenance with optional SQLite review notes on the existing
> result tool. Typed lifecycle records remain authoritative; historical design
> discussion below is retained. No reporting-only turn is required.

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — sequence memory and bounded-progress correction shipped; live post-restart verification pending |
| Last synchronized | `2026-08-21` |

> **Current boundary:** PLAT-199 supersedes the fresh independent Fixer
> mechanism introduced by PLAT-155. The compaction-safe sequence and bounded
> objective remain; one retained Technical Maintenance executor now reviews,
> persists its receipt, crosses a backend-enforced read-only barrier, and only
> then receives repair authority in a later message.
>
> **2026-08-21 identity consolidation:** PLAT-163 now names this retained
> reviewer `technical_review`. Engineering and Operations remain selectable
> lenses in this same sequence; they no longer create separate worklist rows or
> receipts. The bounded sequence and durable review/repair phase boundary stay
> intact.

## Decision-escalation correction — 2026-08-19

The first live standalone Operations Review exposed a second persistence gap.
It correctly classified `planning.step-execution-pipeline.fixed-container` as
`decision_required`, but that route was only a finding label: the review
guidance prohibited creating approval requests, so the operator was never
actually asked. The finding therefore waited on a human decision that did not
exist in the typed human-input queue.

The corrected contract is:

1. Engineering, Operations, and Strategic Review may escalate only a genuine
   approve/reject/defer boundary whose answer is not already implied by the
   goal or approved policy.
2. The reviewer first creates a typed request with its own durable source
   (`engineering_review`, `ops_review`, or `strategic_review`) and a stable
   reviewer-specific ID.
3. `record_pulse_finding(recommended_route="decision_required")` requires that
   exact pending `human_input_id`. The backend validates source/module
   ownership and atomically records the finding's `awaiting_user` lifecycle
   link. A bare decision-required label is rejected.
4. Before an ordinary scheduled run, answered decisions are still applied by
   the existing agentic decision-drain turn. Unanswered decisions are attached
   as context to the first normal schedule message without adding another LLM
   turn: the agent must not infer an answer or apply the proposed change, but
   it may continue unrelated safe work under the currently approved config.

Go transports and validates the typed state; it does not choose the option or
invent a patch. The Pulse popup remains the human decision surface. The live
Ops finding from review run
`workshop-background-task-1787151081044925000` must be revisited after server
restart so the reviewer creates and links its missing decision request rather
than inserting a synthetic database row.

- **Priority:** P1 — Pulse already instructs Engineering Review to verify its
  retained backlog before new discovery, but verification debt remains high and
  findings can accumulate faster than repairs are proven. The behavior exists
  in prose without a sequence and persistence boundary that reliably preserves
  it through long reviews and context compaction.
- **Owner:** Pulse Review+Fix dispatch and guidance, background
  message-sequence construction, run-scoped review checkpoints, typed Pulse
  persistence, recovery reconciliation, and focused contract tests.

## Existing behavior that must be preserved

The current Review+Fix contract already requires the executor to:

- load the complete active SQLite backlog;
- freeze the starting issue manifest;
- verify matured `changed_unverified` and next-run-gated fixes;
- repair retained active roots before broad discovery;
- compare up to three comparable runs where needed;
- preserve every starting issue in the durable manifest; and
- close only with passed post-change proof.

Stores Health is not a separate Pulse module. When selected by Gate evidence,
it is a distinct message in the same Engineering executor session. Operations
Review may follow in that session when `llm_ops_review` is due, and a later turn
applies and verifies safe technical repairs with the accumulated context.

The defect is not the absence of verification guidance. It is that prior
verification, new discovery, store/operations analysis, deduplication, repair,
verification and persistence are interleaved inside a very large contract with
no durable current-sequence working state and conflicting child-versus-parent
persistence instructions.

## Intended sequence

Build only the messages required by the Gate worklist. The same retained
background executor owns the complete sequence.

### Discovery mode

1. **Previous verification + Engineering Review**
   - Load the complete relevant backlog and prior attempts from typed SQLite
     state.
   - Verify every matured prior Engineering boundary before new discovery.
   - Inspect selected execution, plan/artifact, report and evaluation evidence.
   - Create or update the checkpoint's canonical finding list.
2. **Stores Health (conditional)**
   - Verify prior learnings/knowledgebase/workflow-DB repairs.
   - Add evidence to matching roots before creating a genuinely distinct
     store-integrity finding.
3. **Operations Review (conditional)**
   - Verify prior model, cost, latency, retry and reliability repairs.
   - Update matching roots before creating a genuinely distinct operational
     finding.
4. **Fix and verify**
   - Read the already-deduplicated checkpoint.
   - Repair retained active/reopened issues first, then safe new findings.
   - Capture exact changed targets, checks, expected results and observations.
5. **Persist and close**
   - Perform no new investigation or mutation.
   - Persist final findings, evidence updates, dispositions, verification and
     exactly one terminal receipt per owned due module through typed tools.

There is no default standalone consolidation message. Messages 2 and 3 read the
existing canonical roots and continuously merge evidence. Message 4 performs a
short conflict/safety check before mutation.

### Backlog-drain mode

1. Verify matured previous fixes.
2. Repair retained active/reopened issues and verify them.
3. Persist and close.

Do not perform broad strategic discovery in backlog-drain mode. A Strategic
Review child may run only when retained strategic findings have matured
verification or disposition work; it skips opportunity discovery. `observe`
launches no executor. `strategy` is owned by PLAT-137's Strategic Review
sequence.

## Compaction-safe checkpoint

Inject one exact run-scoped path, for example:

```text
runs/<run>/pulse/<pulse-run-id>/engineering-ops-checkpoint.md
```

Every sequence message reads it first and updates it last. It contains compact
working state only:

- Gate scope and complete starting issue manifest;
- a previous-issue verification matrix;
- canonical semantic root causes and owning modules;
- exact evidence references and remaining proof gaps;
- duplicate/evidence mapping;
- repair plan, affected targets and rollback boundary;
- changes made and verification observations; and
- remaining work plus the next message's exact task.

It must not contain copied logs, full tool outputs, HTML, or a long-form review
report. It is retained with the run and is not lifecycle authority; typed
SQLite state remains authoritative after final persistence and checkpoint
pruning.

### Reuse across Pulse runs

A new Pulse run must not blindly restart an investigation that the immediately
preceding review already performed. Its first Engineering turn reads sources in
this order:

1. authoritative typed SQLite backlog, attempts, verification and receipts;
2. the current run's checkpoint, when it already exists; and
3. only when needed, the newest relevant earlier Engineering checkpoint.

The third read is allowed when the prior review was interrupted or lacks its
terminal receipt, an active finding references reasoning/evidence held there,
or a suspected recurrence needs repair context that SQLite does not retain. It
is not a default history scan. Earlier checkpoints are investigation pointers,
not lifecycle state: every carried claim is revalidated against current
artifacts and runtime evidence, and only compact still-valid reasoning and
evidence references enter the current checkpoint. Stale verdicts, copied raw
logs and unverified conclusions do not carry forward.

## Continuous deduplication

Treat observations as one root when they share:

```text
affected target/component
+ behavioral root cause
+ required repair or decision boundary
```

Different wording, another symptom, another reviewer lens, or a second evidence
path updates the root instead of creating a new issue. A distinct issue requires
a different repair, owner, or verification boundary.

## Previous-issue verification

For every issue selected for this pass, record in the checkpoint:

```text
issue | previous change | required boundary | current evidence | verdict
```

Allowed outcomes include `fixed_verified`, `changed_unverified`,
`awaiting_next_valid_run`, `reopened`, `still_active`, `blocked`, and
`inconclusive`. Closure requires proof that the change is on the real runtime
path, a valid post-change execution exercised it, the expected behavior
occurred, and no material adjacent regression appeared. File existence, a
green static test, or absence of the old error alone is not producing-run proof.

Every selected issue receives a terminal disposition or an exact still-unmet
evidence/decision/external boundary. Unselected starting issues remain in the
ordered durable queue. No item disappears when context compacts.

## Persistence ownership

- Intermediate messages update only the run-scoped checkpoint and workflow
  artifacts legitimately owned by their phase.
- The final sequence message owns normal typed findings, verification,
  dispositions and module receipts because it has the full review/fix context.
- The main Pulse continuation validates those receipts and handles only genuine
  cross-sequence merges/conflicts. It does not reconstruct records from an
  automatic-notification summary.
- If the sequence dies before persistence, the main continuation records a
  truthful incomplete result; it does not infer clean, fixed, or verified work
  from partial prose.
- Go validates and stores typed calls; it does not select reviewers, semantic
  duplicates, repairs or verdicts.

## Out of scope

- Merging Strategy Auditor and Goal Advisor is PLAT-137.
- Changing domain workflow plans or their success criteria.
- Turning Stores Health back into a separate scheduled module or agent.
- Adding a Go-selected residual Fixer or recovery reviewer.

## Acceptance

1. Discovery, backlog-drain, observe and strategy Gate modes construct only the
   required sequence messages.
2. Stores Health and Operations remain conditional messages in the same
   Engineering executor session, not independent module agents.
3. A forced context-compaction test proves the starting manifest, canonical
   findings, repair evidence and verification matrix survive to final
   persistence through the run-scoped checkpoint.
4. Every matured previous issue selected by Gate is verified before broad new
   discovery. Every issue selected for this pass receives a disposition or
   exact unmet boundary; unselected issues remain durable in the ordered queue
   without synthetic no-op dispositions.
5. Evidence from later messages updates a matching semantic root and does not
   create a duplicate finding.
6. Backlog-drain mode produces no broad discovery findings; a Strategic Review
   child is allowed only to verify or disposition retained strategic work and
   must omit opportunity discovery.
7. The Fix-and-Verify turn works retained active/reopened issues before safe new
   findings and captures exact changed-target plus observed verification proof.
8. Exactly one final sequence message writes normal lifecycle records and
   exactly one terminal receipt per owned due module.
9. The main continuation reads the typed receipts rather than recreating child
   findings from auto-notification prose.
10. A pre-persistence crash records an incomplete review and leaves prior
    issues open; it never claims a clean or verified result.
11. A recovery/recurrence test proves the next Pulse run can use the newest
    relevant prior checkpoint without treating it as authority, while a normal
    unrelated run does not scan old checkpoints.

## Implementation status — 2026-08-18

- Review+Fix dispatch now builds an explicit retained Engineering sequence:
  prior verification plus Engineering Review, conditional Stores Health,
  conditional Operations Review, Fix-and-Verify, then final typed persistence.
- Discovery and backlog-drain construct different worklists; backlog drain
  prioritizes retained roots and suppresses broad discovery. A due Strategic
  child in that mode may only verify or disposition retained strategic work.
- Every message uses one run-scoped compact checkpoint, continuously updates
  matching semantic roots and leaves the final child turn as the sole normal
  lifecycle writer. The parent validates typed receipts rather than rebuilding
  findings from notification prose.
- The first Engineering turn now starts from SQLite and may consult only the
  newest relevant prior checkpoint for interrupted-review recovery, missing
  repair reasoning, or recurrence analysis. It must revalidate anything it
  carries forward; normal reviews do not scan checkpoint history.
- Scheduler/guidance contract tests and the repository Go build pass. A live
  Pulse run after server restart, including forced context compaction and a
  pre-persistence interruption exercise, remains required before closure.

## Live correction — 2026-08-19

The first live Social Media backlog-drain pass exposed a second defect in the
original acceptance contract. The starting manifest contained 198 active
findings. The Engineering/Ops child was told both to process the “complete
canonical repair queue sequentially” and not to stop because the backlog was
large. The workflow itself finished in about 3h08m; Pulse was still working
roughly 53 minutes later, after Gate had taken about 4m24s. Its checkpoint was
still advancing, so this was not a deadlock—the contract had assigned an
unbounded amount of legitimate work to one retained child.

That is not a model-latency problem and an arbitrary numeric cap would only hide
it. The contract now gives one Pulse pass one agent-chosen coherent repair
objective. The executor still inventories the complete SQLite backlog and
ranks it from compact lifecycle evidence. It then chooses the highest-value
root that can reach honest proof, repairs every issue genuinely sharing that
root/target/proof boundary, persists those selected dispositions, and leaves
the exact ordered remainder in the run-scoped checkpoint and SQLite. A nonempty
backlog no longer makes a truthful pass incomplete.

This preserves agentic prioritization and causal bundling while making Pulse a
repeatable progress loop instead of a single attempt to zero every unrelated
root in one context window.

## Batch-sizing refinement — 2026-08-21

The one-objective rule made the fixer safe but unnecessarily slow for a backlog
containing several small, independently provable repairs. A fixer now selects a
bounded **repair batch** rather than one hard-capped objective: it starts with
the highest-value coherent bundle, then may add a separate bundle only when it
is low-risk, needs no broad rediscovery, has its own clear proof boundary, and
fits the same context window with targeted evidence. It does not batch public
action risks, user decisions, route-specific contexts, or unresolved design
work. This remains agentic sizing, not a fixed issue count, and each bundle
still receives its own disposition and proof before the next begins.

The same live child also returned 47 KB and 36 KB shell results while locating
single schema/plan fields. The Fixer practices now direct agents to
`query_step`/`get_step_prompts`, projected managed-DB reads, and field/path-
scoped bounded searches rather than recursively printing an entire plan step,
README, JSON column, or conversation into every later model turn. The agent
still chooses what evidence it needs; the harness no longer implies that broad
dumping is the normal discovery route.
