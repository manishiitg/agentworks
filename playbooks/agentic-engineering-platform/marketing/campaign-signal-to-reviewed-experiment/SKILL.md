---
name: campaign-signal-to-reviewed-experiment
description: Propose a validated campaign measurement handoff to a bounded, owner-reviewed growth experiment.
---

# Campaign Signal to Reviewed Experiment

## Outcome

Explain one campaign change with comparable spend, click and qualified-event evidence, then propose one testable experiment. A performance correlation, approved plan, launched variant and observed result are separate states.

## When to use

Use when a B2B team wants to move from a campaign signal to a reviewed next test. One Crew may hold both capabilities for the same owner and access; separate Crews when measurement and change authority differ. Competitive context is optional.

## Discovery and user direction

Inspect existing Marketing Crews, account/campaign/offer IDs, current and baseline sources, conversion definition, attribution and lag, eligible audience, owner and experiment destination. Ask only for missing choices. Show the concrete proposal before Builder applies it.

## Required inputs

Bind tenant, campaign platform account, campaign, offer, market, currency, periods, time zone, click and qualified-event definitions, source revisions, baseline, coverage, owner, primary metric, guardrail and sample/stop policy. Missing data remains explicit.

## Plan and AgentWorks tools

Reuse Campaign Performance Analyst and Growth Experiment Planner; add Competitor Intelligence Analyst only for a bounded, dated source question. Performance writes campaign-performance-brief/v1. Run the bundled validator before Experiment consumes it. Optional Competitor writes competitor-context/v1 and is validated separately. Experiment writes growth-experiment-plan/v1; validate its join and decision rule. Insert explicit validator steps in the Workflow. Publishing a variant, changing spend or sending a message requires a separate exact approval and provider receipt.

## Knowledge and persistence

Save campaign/account/offer, period, metric definition, denominator, source, competitor product, plan, experiment, approval, provider and run IDs. On later periods, preserve the comparison policy, account for attribution lag and avoid duplicate alerts or launches.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity, comparable periods, source citations, arithmetic, qualified-event coverage and plan joins. Fictional [campaign](examples/campaign-performance-brief.json), [competitor](examples/competitor-context.json) and [experiment](examples/growth-experiment-plan.json) examples pass; the [invalid plan](examples/invalid-growth-experiment-plan.json) fails. The reporting dashboard separates measurement gaps, proposals, approved plans, provider-confirmed launches, guardrail state and observed outcomes.

## Guardrails

Do not claim causality or incremental lift from an observational comparison. Do not treat ad-platform leads as qualified CRM events, use competitor pages as customer-demand proof, change targeting or budget, activate flags, publish pages, or announce a winner through installation.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for measurement and approval; [setup](SETUP.json) has ten checks.

## Completion contract

Return Crew IDs, source and metric map, validated artifacts, owner decisions, manual run, activation choice, change receipts or none and blockers. Keep recurrence and experiment launch off until reviewed.
