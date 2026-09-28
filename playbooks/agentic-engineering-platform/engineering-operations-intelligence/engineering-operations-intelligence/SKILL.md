---
name: engineering-operations-intelligence
description: Propose governed team metric observation and a separate owned improvement review.
---

# Engineering Operations Intelligence

## Outcome

Reconcile one team delivery, quality or reliability measure under a versioned rule, then prepare an owner-reviewed improvement question. The broader data model and recurring review guidance remain available; the first route proves one bounded metric and handoff.

## When to use

Use when an engineering team wants a reproducible metric linked to an accountable next step. An incident, performance regression or cloud cost exception uses its focused Playbook. Start manually; add recurrence only after a real source-backed case succeeds.

## Discovery and user direction

Builder inspects existing Engineering Operations Analyst and Engineering Delivery Coordinator Crews, source authorization, team/service identity, metric definitions and owner policy. Propose reuse or reviewed creation, validator steps, a manual route and a later observation before configuration. Selection starts no issue write or message.

## Required inputs

Bind tenant, team, service/repository, metric family and policy revision, distinct item/event identity, source coverage minimum, baseline and current windows, numerator and denominator rules, predeclared target, current issue source and decision owner. Missing coverage or changed definitions stay visible.

## Plan and AgentWorks tools

Workflow steps: Analyst reads authorized engineering records and emits `engineering-metric-observation/v1` with exact scope, source revisions, count arithmetic, baseline/comparable/not-evaluable state and no causal claim. Run the [validator](scripts/validate_handoff.py) before Delivery. Delivery Coordinator re-reads current issue and ownership state, prepares `engineering-improvement-review/v1` bound to the exact observation, and asks the owner to accept, defer or reject a bounded next action. Validate the pair. Issue writes, notifications and deployment are separate reviewed routes with receipts.

## Knowledge and persistence

Persist identity joins, source and policy revisions, windows, case key, owner decisions and later observations. Use the [operations data model](../references/operations-data-model.md). Changed populations, mappings or metric rules start a new baseline; do not silently rewrite a past result.

## Validation and reporting

The validator checks scope, complete versus partial coverage, count/rate arithmetic, equal comparable windows, target state, current issue read, duplicate case key and owner/action claims. Fictional [comparable](examples/engineering-metric-observation.json), [baseline](examples/engineering-metric-baseline.json), [not evaluable](examples/engineering-metric-not-evaluable.json), [pending review](examples/engineering-improvement-review.json) and [invalid claim](examples/invalid-engineering-improvement-review.json) teach the contract. The dashboard shows source health, population, metric, owner question, action state and later independently observed result.

## Guardrails

Never rank individuals or infer productivity from one metric. Do not treat missing records as zero, compare changed scopes, claim causality from timing, create work or count improvement from a closed ticket. An owner decision is not an issue write or measured outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for activation.
- [Data foundation](references/data-foundation.md), [metrics and findings](references/metrics-and-findings.md), and [recurring review](references/recurring-review.md) for the broader method.
- [Team and handoffs](references/team-and-handoffs.md) and [setup](SETUP.json) for the proposed Crew route.

## Completion contract

Return Crew IDs, source and metric rules, validated artifacts, limits, owner decision or pending state, real manual case, later observation rule, blockers and activation choice.
