# A simpler Pulse: three roles that find and fix

Status: proposal for owner review (2026-10-06). Nothing here is built.
Builds on [workflow knowledge layers](workflow_knowledge_layers.md) (PLAT-555, PLAT-556).

## Why

Pulse has the disease it is meant to cure: every fix added a rule, a tool or a module, and nothing
consolidated.
- Guidance for the reviewers is 23,243 words across 8 core templates (22,337 before PLAT-556 added more).
- 21 Pulse tools and 33 `pulse_*` tables: findings, finding details/events/aliases, issues, decisions,
  fix attempts/runs/verifications, interventions and their sources/effects, impact assessments, goal work
  and observations, review notes/log/recovery, review focus state/history, module state/audit/result
  history, schedule state, prompt budget metrics, and more.
- Four reviewers plus a gate: Technical (fixes), Plan Drift (checks changes), Architecture (proposes),
  Strategic/Goal Work (acts on goals); an LLM gate turn decides each pass who runs.
- Only some roles may act. Architecture proposes, and its proposals go proposed -> approved -> applied
  -> adopted; PLAT-305 never observed one reach a workflow. Ownership questions recur (who owns
  descriptions, learnings, the DB) because finding and fixing are split.

## The design

### Three roles, each finds and fixes in its lane

| Role | Lane | Replaces |
|---|---|---|
| **QA** | failures: broken runs, wrong outputs, validation, scheduler and safety failures | Technical review and its lenses, the separate fixer stage |
| **Engineer** | structure and quality: step layout and prompts, skills, knowledge (local and Brain), DB design, scripts, models and cost | Architecture review and the judgment part of Plan Drift |
| **Product** | goals: finds work that moves the owner's goals and does it, runs experiments, follows up | Strategic review / Goal Work |

Plan Drift's mechanical checks (schema vs `db/README.md`, script queries, references, dependencies)
become automatic code checks run after every plan change; their failures go to QA (broken) or the
Engineer (drift), not to a fourth reviewer.

### Due rules in code, not a gate turn

- QA is due when a run failed, validation failed, or an automatic check failed.
- Engineer is due when a budget is breached (prompt size, dated text, duplication, missing layout,
  stale knowledge, DB measures), a plan change needs structural review, or a learning-access rule fires.
- Product has a protected rhythm set by the owner (default weekly) and runs after any goal-relevant event.
- Ordering when several are due: QA, then Engineer, then Product, one writer at a time per workflow.
There is no LLM gate turn. A role that finds nothing records "nothing to do" in one line.

### Find and fix, with a safety net instead of approval

Every role applies its own fixes. Each fix:
1. is recorded in the plan changelog with its evidence in `reason`;
2. passes the no-loss check when it moves text (`check_plan_no_loss`);
3. is verified by one real run of the affected step in test mode (decision below), and
4. is restored automatically (`restore_step_from_changelog`) when that run fails.

**Owner-only changes** (the role writes a decision and does not apply): business rules and limits
(rates, caps, floors), goals and strategy, spending money, external actions (messages, bids, posts,
submissions), deleting data. Everything else is done and reported.

### One record type

Replace findings, issues, decisions, fix attempts/runs/verifications, interventions and impact records
with one **work item**: `{id, workflow, role, kind (problem | improvement | owner_decision), target,
evidence, state (open | fixed | verified | reverted | needs_owner | closed), changes (changelog ids),
verification (run id, result), outcome (metric before/after)}`. One table, one write tool
(`record_work_item`), one read view in `get_pulse_state`.

### Short guidance per role

One guide per role, each under a fixed budget (for example 2,500 words), in the step layout: Goal,
Inputs, Output, Rules, Done when, Guides. Procedures move into skill references the guide names
(delivered by PLAT-556 decision 1). The same prompt budgets apply to Pulse's own guidance.

## Also: what else would make Pulse better

1. **A test mode for steps with external effects.** Verifying a fix needs a real run, but steps like
   Upwork `bid-pick-job` claim real jobs and `bid-submit` spends Connects. Add a test mode in which
   external writes and spending are stubbed or redirected (dry run) while reads stay real, so every
   fix can be verified without side effects. Without it the safety net only works for harmless steps.
2. **A weekly digest for the owner per workflow:** what Pulse changed, what improved (metric before/after),
   what was reverted, and the few owner decisions waiting. One message instead of a dashboard to visit.
3. **Measure Pulse itself:** cost and time per pass, fixes applied vs reverted, problems that come back
   after a fix. A role whose fixes keep being reverted, or that costs more than it saves, is visible.
4. **Platform problems go to the platform.** The same fix applied in several workflows (paths, shell
   rules, tool quirks — PLAT-049) becomes one platform ticket and one platform change, not a patch per
   workflow. The Engineer files it instead of patching.
5. **Learn from reverts.** A reverted fix records why, so the next attempt does not repeat it.

## Goal for QA: close to zero workflow bugs

A sample of Upwork's Pulse issues (PLAT-559) shows only about one in five is a bug in the plan as written:
~32% are changes not carried through to dependents, ~20% platform bugs, ~28% notes or strategy filed as issues.
So: (1) plan edits list and update their dependents in the same change; (2) platform problems become one
platform ticket and fix, never per-workflow patches; (3) the builder runs a changed step once in test mode
before the change counts as done. The measure is workflow-caused issues found by QA per week; the target is
close to zero, leaving QA with the outside world changing.

## Migration

1. Map existing records into work items (open findings/issues -> open; pending decisions -> needs_owner;
   verified fixes -> verified/closed); keep the old tables read-only for history, then archive.
2. Ship the automatic drift checks and code due rules alongside the gate; compare decisions for a few
   passes on local workflows; then remove the gate turn.
3. Merge Technical + fixer into QA, Architecture + Drift judgment into Engineer; rename Strategic to Product.
4. Rewrite guidance to the per-role budget.
5. Add test mode before turning on automatic verification for steps with external effects.
6. Retire the unused tools and tables.

## Decisions needed

1. The three roles and their lanes.
2. The owner-only list.
3. Replace the gate turn with code due rules.
4. One work-item record replacing the current record types.
5. Test mode for external-effect steps as a prerequisite for automatic fixes there.
