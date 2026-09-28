---
name: finance-operations-review
description: Build a chat-led multi-Crew SaaS finance review with source-linked billing exceptions, finance impact, and validated handoffs.
---

# Finance Operations Review

## Outcome

Coordinate Billing Operations Coordinator and Finance Analyst to review current billing exceptions and explain their finance impact. Add close or payables specialists only when a distinct owner or access boundary needs them.

## When to use

Use for a small B2B subscription business that needs a repeated, owned finance review across Crews. For one Crew's weekly brief, use that Crew's schedule.

## Discovery and user direction

Inspect existing Crews, selected skills, setup evidence, source access, current Automation, and policies. Propose a concrete team, handoff, first result, cost, and blockers. Selection is a proposal; save decisions in `SETUP.json` after verification.

## Required inputs

Resolve entity, period, currency, timezone, billing and finance source of truth, named owner, invoice/refund/contact policies, metric definition, and approval boundaries. Authorized exports support the first route; live connectors are optional.

## Plan and AgentWorks tools

Use two distinct ready Crews: billing-operations-coordinator produces `billing-exception-queue/v1`; finance-analyst receives only the validated queue and authorized source references and produces `finance-impact-readout/v1`. Reuse suitable Crews. After review, create missing ones with `create_crew` and stable idempotency keys; never pass their local skill names as global skill selections. Put the relevant contract fields and output path in each Crew step instruction so the Crew need not access the Workflow's installed Playbook files. Add blocking validator steps before consumers. Optional close/payables routes require their own artifact checks before activation.

## Knowledge and persistence

Save source/account scope, metric and policy decisions, Crew IDs, artifact paths, validator results, run IDs, and stable case/action IDs. Re-read current provider state before repeating a case; deduplicate events and avoid a second customer contact for the same case.

## Validation and reporting

Run the bundled validator against both real artifacts and the invalid fixture. A malformed billing queue blocks Finance Analyst. Review source truth separately: a valid JSON reference is not proof that a transaction occurred. The dashboard shows open/closed cases, aging, billed versus collected measures, source freshness, run cost, approvals, and blockers. Test one manual route before recurrence.

## Guardrails

Never infer a bank deposit from a pending payout or recognized revenue from cash received. No template or Playbook selection sends a message, retries a charge, issues a refund, pays a bill, posts a journal, or enables a schedule. Each write needs current object verification, narrow access, exact-action approval, and a recorded result. Keep customer and payroll data within approved Crew scopes.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md): goal and route decisions.
- [Team and handoff contract](references/team-and-handoffs.md): Crew binding, artifacts, and validation.
- [Artifact validator](scripts/validate_finance_artifact.py): blocking shape and reference checks.
- [Example billing queue](examples/billing-exception-queue.json), [finance readout](examples/finance-impact-readout.json), and [invalid queue](examples/invalid-billing-exception-queue.json): fictional fixtures.
- [Setup progress](SETUP.json): checks and evidence.

## Completion contract

Return applied version, owner choices, Crew IDs and setup state, source map, Workflow step IDs, validated handoffs, first manual run and dashboard links, metric limits, action decisions, activation state, and unresolved work.
