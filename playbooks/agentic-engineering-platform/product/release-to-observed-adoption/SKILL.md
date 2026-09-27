---
name: release-to-observed-adoption
description: Connect a verified feature release and exposure rule to a source-bound adoption observation without assuming usage or impact.
---

# Release to Observed Adoption

## Outcome

Produce a verified release handoff and a bounded first adoption observation with stated coverage and the next measurement question.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One product, feature, exact release and flag revision, eligible population, observation window and Product owner. Re-read deployment and flag records, the approved release gate, versioned product events and identity coverage for eligible accounts.

## Plan and AgentWorks tools

Create reviewed steps for product-release-coordinator (`product-release-readiness/v1`) → product-adoption-analyst (`feature-adoption-observation/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No flag change, customer announcement, adoption claim, roadmap decision or schedule follows from selection or a release plan.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/release.json), [last artifact](examples/adoption.json) and [rejected artifact](examples/invalid-adoption.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

A go decision is not deployment; deployment is not exposure; exposure is not use. Count only eligible, exposed and observed users under one frozen event rule. No flag change, customer announcement, adoption claim, roadmap decision or schedule follows from selection or a release plan. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
