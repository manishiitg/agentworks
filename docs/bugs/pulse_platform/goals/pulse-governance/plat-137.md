[← goals / pulse-governance](index.md)

# PLAT-137 — Strategy Auditor and Goal Advisor split one strategic reasoning lifecycle and constrain it to one globally active experiment

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | P1 — Pulse pays for two overlapping strategic reviews, yet |

> **2026-09-10 update:** [PLAT-306](plat-306.md) supersedes mandatory Markdown
> checkpoint/report maintenance with optional SQLite review notes on the existing
> result tool. Typed lifecycle records remain authoritative; historical design
> discussion below is retained. No reporting-only turn is required.

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — merged lifecycle plus direct standalone execution and typed completion enforcement shipped; live post-restart/compaction verification pending |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — Pulse pays for two overlapping strategic reviews, yet
  neither consistently performs the intended strategic-intelligence job:
  discovering hidden mechanisms that make the current strategy misleading and
  discovering materially better approaches outside the current plan.

> **2026-08-21 focus-history extension:** PLAT-163 now applies the common
> lightweight-scan + one-deep-focus persistence contract to
> `strategic_review`. This does not merge Strategy into Technical Review or
> change this ticket's strategic sequence and experiment ownership.
- **Owner:** Pulse module registry and Gate, Review+Fix dispatch, strategic
  guidance, typed Pulse persistence, advisor experiment lifecycle, and Pulse UI
  module projection.

## Problem

Pulse currently exposes two independent modules:

- `strategy_auditor` diagnoses weaknesses inside the selected strategy; and
- `goal_advisor` performs a less-frequent blank-sheet opportunity review.

Their actual contracts overlap substantially. Both reconstruct the causal
chain, inspect concentration and saturation, challenge assumptions, diagnose a
strategy ceiling, compare retained outcomes, and produce strategic
recommendations. The split causes both agents to reread the same evidence,
creates ambiguous ownership and duplicate findings, requires two receipts and
cadences, and spends additional model cost without reliably producing either
deep systemic diagnosis or genuine outside-the-plan discovery.

The current guidance also mixes reasoning with lifecycle administration. Some
instructions tell a reviewer to persist findings, verification and receipts;
the current Review+Fix dispatcher instead says children return evidence and the
main Pulse continuation reconstructs typed SQLite state. That conflict can
duplicate writes or lose detail when an automatic completion notification is
summarized or truncated.

Long review sequences have no explicit compaction-safe working memory. Exact
evidence, duplicate mappings, proposed repairs and verification boundaries can
fall out of conversational context before the final turn.

Finally, Goal Advisor makes one workflow-global strategy experiment the center
of the lifecycle and permits only one active experiment. Bounded experiments
are useful for uncertain changes, but they are only one possible strategic
outcome. A global count of one blocks independent experiments even when they
affect different channels, cohorts, metrics and resources.

## Intended strategic role

Replace both modules with one canonical `strategic_review` module. It owns two
complementary phases of one reasoning process:

1. **Hidden-mechanism audit:** find ways the current strategy can look rational
   and productive while moving away from the real goal — feedback loops,
   selection or survivorship bias, proxy optimization, observation
   contamination, concentration, path dependence, local optima, diminishing
   returns, missing causal stages, and unsupported assumptions.
2. **Opportunity discovery:** when warranted, start from the goal and approved
   constraints, inspect independent external evidence, and discover materially
   different strategies the current plan has not considered.

This is domain-independent. The social-feed reinforcement loop is one example,
not a special-case rule: a workflow's actions may alter the future evidence it
observes, just as a job workflow can mistake one platform for the whole market,
a security workflow can mistake its configured scan surface for the complete
attack surface, or a sales workflow can train future selection on its own
shortlist.

## Strategic Review sequence

The background executor uses one retained session and one run-scoped Markdown
checkpoint. Sequence depth is conditional.

### Message 1 — verify prior strategic work and audit hidden mechanisms

- Read the complete relevant strategic backlog, previous decisions,
  interventions, experiments and verification boundaries from typed SQLite
  state.
- Verify matured prior findings and experiments before new discovery.
- Reconstruct the goal-to-action-to-observation-to-outcome causal system.
- Search for hidden mechanisms and competing explanations.
- Update one canonical finding list in the checkpoint; later evidence updates a
  matching root rather than appending a differently worded duplicate.

### Message 2 — independent opportunity discovery (conditional)

Run only for a due headroom/opportunity checkpoint, a supported strategy
ceiling, persistent trustworthy goal weakness, a material external change, a
matured experiment, or an explicit user request.

- Begin from the goal and explicit constraints rather than treating the current
  plan as authoritative.
- Use current external evidence, adjacent-domain analogies and first-principles
  reasoning when available.
- Generate materially different approaches before comparing them with the
  current strategy.
- Include the counterfactual: “What would we consider if the current plan did
  not exist?”

### Message 3 — critic and strategic conclusion

- Falsify the leading audit explanation and challenge each alternative.
- Select one primary conclusion: `keep`, `improve`, `propose_alternative`,
  `experiment`, or `evidence_wait`.
- For action, define expected benefit, named goal criterion, evidence, risk,
  decision boundary, checkpoint, guardrails and rollback.
- Route technical prerequisites to Engineering Review; do not repair workflow
  implementation from this sequence.

### Message 4 — persist and close

- Read the final checkpoint; perform no new investigation or workflow mutation.
- Persist canonical findings, verification, strategic interventions and any
  real approve/reject/defer request through typed Pulse tools.
- Complete exactly one `strategic_review` receipt and mark the checkpoint
  persisted.

An audit-only pass skips Message 2. There is no separate consolidation turn:
each later message reads the checkpoint and continuously updates matching root
causes.

## Compaction-safe checkpoint

The scheduler injects one exact path such as:

```text
runs/<run>/pulse/<pulse-run-id>/strategic-review-checkpoint.md
```

The checkpoint contains only compact working state: scope, comparable evidence
window, prior-verification matrix, canonical findings, competing explanations,
candidate opportunities, decision/experiment boundary, remaining work, and the
next sequence message. It must not contain copied logs, full tool output, HTML,
or a user-facing report.

The checkpoint is not lifecycle authority. Typed SQLite findings,
verifications, decisions, interventions and receipts remain authoritative. The
checkpoint follows ordinary run retention and may be pruned after its final
typed state is persisted.

## Persistence ownership

Use one writer per normal strategic review:

- Messages 1–3 reason and update only the run-scoped checkpoint.
- Message 4 writes the module's typed Pulse lifecycle records.
- The main Pulse continuation reads and validates the terminal receipt. It may
  merge a genuine cross-module duplicate or conflict, but must not recreate the
  review from completion-notification prose.
- If the sequence dies before Message 4, the main continuation records only a
  truthful incomplete review; it does not invent findings from partial output.
- Go validates and stores typed tool calls; it does not choose the strategic
  conclusion.
- A strategic conclusion that genuinely requires operator approval first
  creates a typed `strategic_review` approve/reject/defer request, then links
  that exact request as the finding's `human_input_id`. A decision-required
  finding without a pending request is rejected, so the advisor cannot leave
  an invisible human dependency in SQLite.

This removes the current contradictory reviewer-versus-parent normal write
contract while keeping recovery truthful.

## Experiment model

An experiment is optional. Strategic Review may instead keep the current
strategy, record an observation, recommend an approved permanent change, or
wait for named evidence.

Remove the workflow-global one-active-experiment limit. Permit concurrent
`running` or `measuring` experiments only when their interference domains do
not conflict. Each experiment identifies:

- affected goal criterion;
- workflow steps/control surface;
- channel/source and target cohort;
- primary metric and observation stream;
- shared budget or scarce resource; and
- plausible contamination of another experiment's inputs or outcomes.

Two experiments conflict when they control the same causal surface, compete
for the same scarce resource, contaminate one another's observations, or make
outcome attribution unreliable. Proposed or approved-but-not-started items do
not consume active experimental capacity. Conflicting experiments are queued
or deliberately designed with separated treatment groups; independent ones may
run together.

## Historical-state migration

- Migrate historical `strategy_auditor` and `goal_advisor` findings, reviews,
  decisions and interventions to `strategic_review` while preserving stable
  issue/request IDs, evidence, receipts and lifecycle history. The retired
  perspective is not kept as a live routing identity.
- Migrate the existing active advisor experiment into the new strategic
  intervention portfolio.
- Gate, backend tool enums, UI projections and prompts emit only
  `strategic_review` after migration. Do not retain indefinite dual-write or
  two-module compatibility behavior.
- Existing stable issue IDs and evidence history remain intact.

## Engineering boundary

Engineering Review, conditional Stores Health, Operations Review, technical
repair and verification remain in their existing ordered executor sequence.
This ticket does not merge strategic reasoning into Engineering or authorize
Strategic Review to mutate workflow implementation.

## Acceptance

1. The canonical Pulse registry, Gate, scheduler, typed tool validation and UI
   expose one `strategic_review` module and no live Strategy Auditor/Goal
   Advisor dual dispatch.
2. Audit-only mode runs Messages 1, 3 and 4; a full opportunity pass runs all
   four messages.
3. The first message verifies every matured prior strategic issue or preserves
   its exact unmet evidence boundary before new discovery.
4. All messages use one run-scoped checkpoint, and a forced context-compaction
   test retains the exact canonical findings and verification evidence through
   final persistence.
5. Matching observations across messages update one semantic root instead of
   producing duplicate findings.
6. The final sequence message records exactly one terminal review receipt; the
   main continuation does not rewrite findings from auto-notification prose.
7. A pre-persistence child failure produces `review_incomplete`, never a clean
   or inferred strategic result.
8. Opportunity discovery demonstrates at least one independent evidence source
   or explicitly records why none is available; it does not merely restate the
   existing plan.
9. Experiment is optional, and tests prove two non-conflicting experiments may
   run while two conflicting experiments cannot.
10. A versioned migration preserves historical findings, decisions,
    interventions and evidence while removing live dual-module behavior.

## Implementation status — 2026-08-18

- The canonical registry, scheduler, Gate/tool validation, typed persistence,
  human-input sources and UI now expose one `strategic_review` module. Legacy
  module IDs are accepted only by migration/normalization boundaries and are
  never emitted as live modules.
- The scheduler dispatches the four-phase retained Strategic Review sequence
  with conditional opportunity discovery and final-turn persistence through a
  run-scoped checkpoint.
- Existing module-state, audit, review-log, finding and human-input rows are
  normalized into the canonical module while retaining their evidence and
  stable issue identity.
- Strategy experiments now declare interference domains. Independent active
  experiments may coexist; an overlapping active experiment is rejected.
- Focused Go tests, the frontend TypeScript build and the repository Go build
  pass. A live Pulse run after server restart, including a forced context
  compaction, remains required before closing the ticket.

## Standalone slash-command lifecycle recurrence — 2026-08-19

The retained scheduled Strategic Review sequence was canonicalized, but the
manual `/strategy-auditor` wrapper still used the former two-layer pattern. The
slash command first launched `strategy-auditor review` in the background, then
its guidance told that already-isolated agent to launch another `Standalone
Strategy Audit` and return the child's completion. It also still emitted the
retired `strategy_auditor` module label and had no backend-enforced terminal
receipt, so a failed or absent nested dispatch could be presented as a clean
completion from prose alone—the same failure class reproduced by Ops Review.

The manual command now performs the audit directly in its first background
agent, persists findings and matured verifications under the canonical
`strategic_review` module, and calls `complete_pulse_review` exactly once for
that module. Its dispatch carries
`required_pulse_review_modules=["strategic_review"]`, and the generic background
completion boundary validates the exact child-session SQLite receipt before
reporting success. Missing and failed receipts produce a failed/incomplete
background execution rather than a false strategy result.

Focused guidance, frontend dispatch, and canonical strategic-receipt tests
pass. Live `/strategy-auditor` verification after restart remains pending.

### Decision record — standalone Strategic Review execution boundary

| Question | Decision and reasoning |
|---|---|
| What evidence forced the decision? | The manual Strategy slash path still had the same two-agent topology as the failed Ops reproduction: an already-isolated background reviewer was instructed to launch another reviewer, while no typed terminal receipt was required. It also emitted the retired `strategy_auditor` identity after PLAT-137 had merged the lifecycle into `strategic_review`. |
| Keep a separate nested Strategy Auditor for independence? | Rejected. The slash-created background agent already has an isolated conversation. A second identical-role agent adds cost and another failure boundary without adding a critic phase or independent evidence source. Independence needed inside the strategic reasoning belongs in the retained review sequence, not another lifecycle owner. |
| Continue accepting the legacy `strategy_auditor` module? | Rejected for new writes. Legacy IDs remain migration inputs only; emitting them again would recreate split cadence, history, and ownership after the explicit merge into `strategic_review`. |
| Infer completion from a strategy recommendation? | Rejected. `no_material_problem` is a valid result, and an agent can produce plausible recommendations before finishing verification or persistence. Only the terminal receipt proves completion. |
| Selected design | `/strategy-auditor` remains the human-facing command alias, but it dispatches one direct reviewer that persists under `strategic_review`. The slash call declares `required_pulse_review_modules=["strategic_review"]`, and the backend accepts completion only from that exact child-session receipt. |
| What would justify revisiting it? | A product decision to expose a genuinely separate strategic role with a distinct question, evidence boundary, cadence, and receipt—not merely a renamed duplicate. Historical migration compatibility alone is not sufficient. |
