# Pulse Fixer engineering practices

Use this reference for every Pulse Fixer mutation pass. It defines how to turn
review findings into durable repairs. The lifecycle and proof rules remain in
`references/fix-verification.md`; this reference governs diagnosis and repair
quality.

## Capability contract

The Fixer is a full Workflow Builder writer. It receives the canonical Workshop
tool profile and the same workflow read/write paths, including plan and route
creation/deletion, step/config/measurement/report/store mutation, schedule
management, skills, model configuration, execution/debugging, secrets, and
managed database writes. Call `get_api_spec` when the exact arguments are
unclear; do not report a repair as platform-blocked without first checking the
actual tool surface.

Capability is not product approval. Continue to respect the current Pulse run,
finding lifecycle, explicit user decisions, external-side-effect boundaries,
and the strategy/goal approval rules below. Full Builder capability exists so a
technically valid repair is never blocked by a stale second allow-list—not so the
Fixer can silently broaden the requested behavior.

## Core method

For each actionable finding:

1. **Reproduce or re-read the cited failure with targeted evidence.** Treat
   reviewer prose as a lead, not proof. Resolve the exact run, target, inputs,
   tool result, and consumer. Use `query_step`/`get_step_prompts` for a named
   plan step, projected managed-DB queries for rows, and path/field-scoped shell
   searches with an explicit output bound. Never recursively print an entire
   plan step, README, database JSON column, or conversation just to locate one
   contract field; a large result is injected into every later model turn.
2. **Separate symptom from root cause.** Group failures only when they share the
   same cause, compatible target changes, and one verification condition. Keep
   every finding link on the bundle.
3. **Map the contract boundary.** Identify the producer, authoritative contract,
   validators, durable stores, downstream consumers, evals, reports, learnings,
   and knowledge notes affected by the target.
4. **Choose the smallest complete repair.** Change every surface required for
   consistency, but do not broaden into strategy, policy, or unrelated cleanup.
   The smallest complete repair includes moving the text it touches to its
   owner (PLAT-556), instead of appending a sentence to the step description:
   - a technique, procedure, selector or phrasing rule goes to a skill reference
     (`learnings/_global/references/<topic>.md`, correcting an existing topic in
     place) named in the step description's `## Guides`;
   - a business rule or decision goes to the knowledge layer (a local
     `knowledgebase/` note, <!-- product:knowledgebase -->or Brain for shared facts<!-- /product -->) or to the description's
     `## Rules` when it is that step's own constraint;
   - the evidence and history of the repair (dates, run ids, what failed) go to
     the change `reason`, never into the description or a guide.
   If the passage you change sits in dated or duplicated text, move or delete
   that passage in the same repair. The edit-time size and layout nudges apply.
5. **Preserve meaning and safety.** Never weaken a check, invent missing data,
   lower a threshold, change a destination, or reinterpret an operator decision
   merely to obtain a pass.
6. **Use proportional immediate checks.** When a small relevant check is
   practical, exercise the real consumer path: prefer the actual parser,
   validator, query, scheduler transition, or tool boundary over a look-alike
   check. Do not launch an extensive verification effort just to close a fix.
7. **Record the honest lifecycle result.** Use `fixed_verified` only for passed
   post-change proof. Otherwise use `changed_unverified` for the successfully
   applied repair; it closes immediately without a future verification obligation.

## Backlog close-out contract

Pulse finds an issue and closes it in the same pass. A Fixer pass works through
the complete active backlog that existed when it began, in priority order,
until every issue is closed: fixed, not a problem, the user's decision, or
platform-owned. There is no queue for later passes.

1. **Freeze a starting manifest.** Call
   `get_pulse_state(view="backlog", detail="compact")` exactly once with no
   module filter and retain every exact visible `issue_id`. The backend resolves
   the internal fingerprint and current attempt; never copy those internals into
   a Pulse write. Use `query_workflow_db` to inspect status, owning module, step,
   age, recurrence and attempts before choosing an order.
2. **Order by impact.** First issues that blind the goal (goal metrics not
   recorded) or stop the workflow's main output, then any with
   `times_deferred`, then reproduced failures, then the rest. Request
   `detail="full"` only for the issue you are about to work on.
3. **Bundle semantically.** Group items only by shared root cause, compatible
   targets, and one verification condition. Never bundle different public-action
   risks, user decisions, or routes that need separate context. A repair may
   carry many finding links, but no finding may disappear inside a bundle.
4. **Close each bundle before the next.** Investigate, apply the repair, run
   the immediate check, and record the disposition. If the check fails, record
   `failed` and try another fix in this pass. When only the user can choose,
   create the decision with the exact proposed change and record
   `awaiting_user`. When it is not a problem, close it with the check that
   showed it.
5. **Reconcile before completion.** Re-read
   `get_pulse_state(view="backlog", detail="compact")` after lifecycle
   mutations and compare it with the starting manifest. Submit the per-module
   disposition sets through `record_pulse_result` and record the terminal
   module receipts.

If the context runs out before the backlog is empty, stop truthfully: the pass
ends partial, the scheduler records it, and the next fix run continues from the
same backlog. Never describe an unclosed issue as deliberately left for later.
Findings first created while the pass is running are closed in the same pass
when context allows.

## Evidence hierarchy

Resolve conflicts in this order:

1. Explicit user decisions, safety constraints, and `soul.md` goal meaning.
2. Current platform and tool contracts proven by the running implementation.
3. Current plan and step configuration at the canonical saved path.
4. Current durable store schema and real consumer behavior.
5. Current validation and evaluation contracts.
6. Examples, learnings, knowledge notes, and old run artifacts.

Lower-ranked evidence may reveal drift but does not override higher-ranked
evidence. An old successful artifact proves only the old boundary.

## Repair-bundle contract

Before editing, state one compact bundle:

- root cause;
- linked visible issue IDs;
- canonical target and producer;
- affected consumers;
- intended files;
- baseline evidence;
- one post-change verification condition.

Do not file or repair one issue per field, stack frame, retry, table row, or
downstream symptom when they come from one cause. Do not merge findings that
need different policy decisions or different proof.

## Schema and artifact contract repair

Apply this playbook when JSON, database rows, tool envelopes, or other produced
artifacts fail shape, type, required-field, enum, or consistency checks.

### Diagnose

1. Group every prevalidation error from one producing step into one root
   finding. Artifact paths, fields, run identity, and contract version are
   evidence under that step finding, never separate bugs.
2. Read one real failing artifact. Do not infer its shape from error strings.
3. Read the producer instructions or code and the exact validation schema.
4. Search every downstream consumer, eval, report query, learning example, and
   schema manifest for the affected file and fields.
5. Determine which case applies:
   - **producer stale:** consumers and validator agree; repair the producer;
   - **validator stale:** producer and consumers agree; repair the validator;
   - **contract split:** producer, validator, and consumers disagree; establish
     one canonical shape from higher-ranked evidence and update all dependents;
   - **missing semantics:** the required value cannot be derived; keep the step
     failed or explicitly unavailable rather than fabricating a default.

### Repair

- Prefer a deterministic serializer or shared output builder when the step can
  compute the fields mechanically.
- For agentic producers, make the output contract concise and unambiguous, give
  one canonical example, and require a mechanical self-check before first write.
- Update producer instructions/code, validation, consumer assumptions, evals,
  report queries, and active learning/schema references together when they are
  in the blast radius.
- Preserve unknown, unavailable, skipped, and empty as distinct semantic states.
- Do not add placeholder zeros, empty arrays, or success values merely because
  the schema requires a field.
- Do not remove required checks until real consumers prove the field obsolete.
- Avoid compatibility aliases unless a live consumer still requires them; if
  needed, name the owner and removal condition.

### Verify

1. Validate a representative post-change artifact with the real prevalidator.
2. Exercise at least one real downstream consumer or parser against it.
3. Check a negative fixture so the validator still rejects the original defect.
4. Confirm every linked file- and field-level check is covered by the step-level
   root repair.
5. If an agentic or externally producing step has not run after the mutation,
   record `changed_unverified` with the exact next run/artifact boundary.

Prevalidation is a guard, not the durable repair. A repair turn may correct one
artifact, but recurring drift requires aligning the saved producer and every
consumer contract.

## Database repair

- Read the declared schema and real table shape before querying or mutating.
- Distinguish schema defects, invalid rows, stale derived data, and inaccessible
  storage; they require different repairs.
- Preserve keys, provenance, and idempotency. Use a transaction for related
  mutations and verify through the canonical query tool or consumer.
- Never perform a speculative migration. When row meaning is ambiguous, retain
  the evidence and request the missing decision.

## Applying an Architecture config recommendation

Use this whenever a finding asks you to change `execution_tier` or
`execution_llm`. These are tier/model decisions `architecture_review`
owns; apply them only through their approved decision contract. (Moving a step between
scripted and agentic is a step-type change — `change_step_type(step_id,
target_type, reason)` — not a config field; the same evidence bar applies and
the `reason` lands in the plan changelog.)

1. **Never apply one without an owning Architecture finding.** These fields are not
   yours to tune opportunistically. If no finding recommends the change, the
   change is not justified — file the finding first, or leave it alone.
2. **Carry the evidence into the config, not just the fix.** Each field requires
   a paired reason (`execution_tier_reason`, `execution_llm_reason`) and the
   tool rejects the change without it.
   Write it so a reviewer six weeks from now can judge the decision without
   re-deriving it: cite the finding id, the current state, and the measured
   evidence. `planning/step_config.json` is what the next reviewer actually
   reads; a rationale that stays only in the finding is lost to them.
3. **Cite the decision when there was one.** If the recommendation was labelled
   `user_judgment_required` or applied after approval, include the
   `human_input_id` in the reason. A reason pointing at a recorded human call is
   the strongest form available.
4. **Preserve explicit settings and the trial contract.** Runtime never changes
   tiers from run counts. Pinning `execution_llm` overrides tier entirely; a tier
   change cannot trial a different model until that pin is cleared or changed.
   Respect user pins and apply only the approved scope, checkpoint and rollback
   settings. Record the reason so the next reviewer can assess the trial.
5. **If the evidence does not settle it, do not change it.** Raise the question
   with `create_human_input_request` and park the finding `awaiting_user`
   against that pending decision. Never invent a reason to satisfy the field —
   a fabricated justification is harder to challenge later than a missing one,
   and uncertainty is a legitimate terminal state.

## Learning and skill purity repair

Apply this playbook whenever a finding concerns `learnings/_global/`, a
`learning_objective`, or `learnings_access`.

1. **Inventory the complete skill package.** Read the root `SKILL.md` and every
   content-bearing Markdown file under `references/`. References are part of
   the skill, not an archive for content removed from the index.
2. **Classify before editing.** Keep only reusable execution HOW: procedures,
   selector strategy, auth/API/CLI/tool quirks, parsing/retry/recovery rules,
   stable failure signatures, and concise operational constraints needed to
   perform the task. Route everything else by meaning:
   - business/domain facts → knowledgebase;
   - run results/current values/status → DB or run evidence;
   - owner goals/preferences/thresholds → `soul.md`;
   - strategy/cadence/routing/current step behavior → plan/config;
   - incidents, provenance, action/run IDs, decisions, and fix history → durable
     Pulse review/finding/decision evidence;
   - platform architecture/schema history → authoritative documentation or DB
     contract, retaining only the short runner-facing instruction in the skill.
3. **Do not launder content through references.** Move detailed reusable HOW
   from `SKILL.md` to a focused reference. Remove non-skill content from the
   whole package; putting it in `references/` is not a repair.
4. **Preserve evidence before removal.** Cite the exact source block in the
   durable Pulse result. If an authoritative destination already contains the
   material, remove the duplicate. If a bounded authorized KB/config repair is
   safe, apply it through the canonical tool. Never invent a fact, rewrite
   user-owned context, mutate run data speculatively, or discard the only copy
   of an unresolved user decision.
5. **Repair contribution configuration.** For every effective
   `learnings_access="read-write"` step, require a concrete objective that asks
   only for reusable HOW. Clear a misplaced/legacy objective and set access to
   `read` when the step should consume but not contribute. Preserve
   `read-write` only when the objective and actual skill coverage agree.
5b. **Judge whether each objective is still yielding, and refine it.** An
   objective is an instruction, and instructions are expected to improve as
   evidence accumulates — treat them as tunable, not as set-once configuration.
   The evidence is already recorded per step in
   `learnings/<step-id>/.learning_metadata.json`: each reflection turn appends a
   `detection_history` entry with `has_new_learning`, plus
   `last_detection_reasoning` and `last_detection_confidence`. Read it and act:
   - **Repeated `has_new_learning: false`** — the objective is not yielding.
     Either sharpen it to name what this step uniquely observes, or drop the
     step to `learnings_access="read"`. A reflection turn that never produces
     anything is pure cost.
   - **Yields, but the content is misrouted** (facts, run results, or incident
     narrative reaching the skill) — the objective is asking for the wrong
     thing. Rewrite it to ask only for reusable HOW.
   - **Yields good technique** — leave it alone.

   Refining an objective needs **no** paired reason field and is not gated:
   these are meant to be iterated. Record the why in `review_notes`. Apply the
   same judgment to `knowledgebase_contribution`, whose yield signal is the KB
   self-review outcome rather than `detection_history`.
6. **Keep the root as an index.** `SKILL.md` contains valid frontmatter, a short
   scope, core cross-cutting instructions, and links to focused references.
   Detailed HOW belongs in references without duplication.

Verification is an immediate semantic re-read, not merely a successful patch:

- re-inventory and re-read every content-bearing Markdown file in the skill;
- confirm every remaining block is reusable execution HOW;
- confirm no incident/provenance, decision ledger, run result/current status,
  business fact, strategy, owner-value duplicate, or architecture history was
  retained or hidden in a reference;
- validate all index links and confirm references are reachable without
  duplicate homes;
- re-read every changed step config and confirm effective access/objective
  pairing;
- name separately routed work that could not be safely applied. Content cleanup
  may be `fixed_verified` when this complete post-change re-read passes; routed
  KB/DB/plan changes still follow their own proof boundary.

## Cross-store ownership repair

Apply this playbook to every Stores Health finding, including KB and DB purity:

1. **Reconcile before editing.** Merge the learning `purity_manifest`,
   `kb_purity_manifest`, and `db_ownership_manifest` into one
   `ownership_manifest`. Each item names its current location, semantic type,
   authoritative owner, duplicate locations, action, and verification. One
   semantic item has one authoritative owner: Soul=why/goals/preferences/hard
   constraints; Plan/step config=current behavior; Validation=deterministic
   proof; Learnings=reusable execution HOW; KB=durable domain facts with
   provenance; DB=structured operational state; Pulse=findings, diagnosis,
   attempts, decisions, and fix verification.
2. **Protect the only copy.** Preserve exact source evidence in Pulse before
   removing anything. Never rewrite user-owned `knowledgebase/context`, invent
   provenance, or move ambiguous row semantics. If the destination write is
   not authorized or meaning is unclear, keep the source and record the exact
   blocker or user decision instead of creating data loss.
3. **Move meaning, not formatting.** Put reusable HOW in a skill, durable facts
   with provenance in KB notes, and structured state in normalized DB records.
   Soul/Plan/Validation changes use their canonical workflow tools. Pulse keeps
   review history. Replace legitimate cross-store consumers with stable
   IDs/paths or queries, not copied prose/JSON snapshots.
4. **Remove duplicates only after destination proof.** Re-read the destination,
   prove provenance/keys and downstream consumers, then remove the old copy.
   Re-run all three manifests and confirm no contradictory owner remains. A
   patch success or smaller file is not verification.
5. **Reduce learning writes after cleanup.** Keep `learnings_access="read-write"`
   only when the step has a current reusable-HOW objective and run evidence shows
   its contributions remain useful. Change mature, redundant, or no-op contributors
   to `"read"`. Treat `lock_code` as the separate, stricter executable proof.
   KB write ownership is not a lock: make a mature/no-op contributor read-only
   by changing that step's `knowledgebase_access` and contribution contract.

Immediate semantic moves may be `fixed_verified` only when source removal,
destination exactness, references, and the current consumer are all re-checked.
Behavioral/configuration changes without immediate runtime proof close as
`changed_unverified`. Do not add a producing-run boundary solely to verify them;
assume the applied fix holds unless the defect is reproduced.

## Tool, path, and permission repair

- Interpret nested bridge envelopes and semantic error markers, not only process
  exit codes.
- Confirm the actual tool surface and target type before declaring a capability
  missing.
- Resolve canonical absolute workspace and run paths; do not make a stale
  relative-path workaround permanent.
- Repair the harness when the same failure crosses workflows. Do not copy a
  platform workaround into every workflow.

## Scheduler and lifecycle repair

- Trace live execution state, child completion, durable run metadata, and UI
  projection separately.
- Define the state transition invariant before editing. A UI badge change is not
  a scheduler repair.
- Load `references/schedules.md` and inspect actual run history before changing
  schedule policy. Cron spacing is not a concurrency guarantee: preserve
  sequential behavior unless the saved schedule explicitly opts into parallel execution.
- Model cooperating schedules as directional `after_schedule_ids` edges. The
  dependent waits for all prerequisites on the same local calendar date; never
  introduce a cycle, a cadence mismatch, or treat the edge as permission to
  overlap runs.
- Never treat a resource/file list as proof that parallel schedules are safe.
  Workflow DB, KB, learning, report, planning, browser and external-action
  writes can be dynamic or omitted. Sequential remains the default; set
  `concurrency_mode="parallel"` with `parallel_risk_acknowledged=true` only after
  the fixed overwrite/duplicate-action risks were disclosed and the human
  explicitly approved it. The `iteration-N-sched` folder isolates output/logs,
  not those shared resources. Dependencies, self-overlap, manual work and Pulse
  remain serialized.
- Reconcile collision queue/discard behavior, `max_start_delay_minutes`,
  terminal-status release, delay, and dependency
  deadline as one transition policy. Preserve unrelated schedule fields when
  using typed update tools.
- Test ordering, retries, cancellation, recovery, and durable terminal state.

## Evaluation and report repair

- Missing evidence is unavailable, never a perfect score or zero by default.
- Preserve raw precision and provenance until presentation formatting.
- Verify that report and evaluation queries resolve the intended run, group,
  time window, and canonical store.
- Presentation-only changes cannot claim direct goal impact.

## Stop conditions

Stop and disposition honestly when:

- the change alters goal meaning, policy, money, recipients, credentials, or
  external side effects without exact approval;
- evidence cannot establish the canonical semantics;
- the root cause is an externally owned platform or vendor defect;
- the proposed repair would merely silence detection.

Never describe these states as verified fixes. If the repair was successfully
applied and only stronger future proof is missing, that is not a stop condition
or pending work: close it as `changed_unverified` and continue.
