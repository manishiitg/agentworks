---
name: renewal-risk-to-owned-decision
description: Propose a sourced customer-health to exact-contract renewal decision without activating contact or changing terms.
---

# Renewal Risk to Owned Decision

## Outcome

Give the renewal owner one current, reviewable account and contract decision before the notice deadline. Separate observed health and billing facts from churn hypotheses. An owner decision is not a customer message, renewal, cancellation or amendment.

## When to use

Use for a contracted SaaS account when health evidence and renewal authority differ. Reuse one Crew with both skills only if owner and access are compatible. Use a simple chat review when no cross-owner handoff is needed.

## Discovery and user direction

Builder inspects existing Customer Health Coordinator and Renewal Coordinator capabilities, current account and contract sources, renewal policy, owner and contact route. Propose Crew reuse or reviewed creation, source boundaries and a manual first case. Selection leaves setup pending and schedules off.

## Required inputs

Bind tenant, account, legal entity, executed contract and revision, subscription, renewal date, calendar-day notice period/timezone, billing source, health window, current owner, decision policy and prior case key. A complex or missing notice rule stays `needs_terms` until a reviewed manual calculation. Missing health coverage stays unknown, not a churn claim.

## Plan and AgentWorks tools

Workflow steps: Health reads authorized usage, first-value, support and relationship records and emits `renewal-health-brief/v1`. A blocking validator checks account, source coverage and observed versus hypothetical signals. Renewal re-reads the exact agreement, subscription, billing state and notice receipts; it emits `renewal-decision-register/v1` linked to the validated brief. Validate the pair, then request the renewal owner's decision. Any outreach, credit, cancellation, CRM update or amendment requires a separate reviewed route, fresh read, idempotency key and provider receipt.

## Knowledge and persistence

Save stable account, contract revision and case keys; source observation times, health IDs, notice calculation, owner decisions, blockers and later receipts. On repeat, re-read terms, prior notice, invoice and health state. A revised agreement starts a new review without erasing the prior one.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks exact account and artifact joins, dated sources, notice arithmetic, contract and billing evidence, decision chronology and false action claims. Fictional [health](examples/renewal-health-brief.json) and [decision](examples/renewal-decision-register.json) pass; [unknown health](examples/renewal-health-unknown.json) and [pending terms](examples/renewal-terms-pending.json) remain honest; [false decision](examples/invalid-renewal-decision.json) fails. The reporting dashboard shows notice timing, source coverage, owner, pending decisions and action receipts separately.

## Guardrails

Do not treat a low-use account or an open invoice as proven churn intent. Do not infer that a contract renewed because a date passed. No installation or read-only result sends notice, changes auto-renewal, grants a discount, moves CRM, issues a credit or enables recurrence. The owner and authorized executor control later actions.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choices.
- [Team and handoffs](references/team-and-handoffs.md) for contract, notice and action boundaries.
- [Setup](SETUP.json) for ten pending customer checks; the examples are fictional.

## Completion contract

Return Crew IDs, source and policy map, validated artifacts, owner decision or blocker, real manual case, next check and activation choice. Keep external actions separate.
