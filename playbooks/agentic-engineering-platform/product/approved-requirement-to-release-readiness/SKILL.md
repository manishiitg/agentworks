---
name: approved-requirement-to-release-readiness
description: Join approved product requirements to exact-build QA, support content and measurement evidence for a Product release decision.
---

# Approved Requirement to Release Readiness

## Outcome

Produce an exact-requirement release readiness decision with named blockers or verified go evidence.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One accepted requirement, product, build, flag revision and rollout owner; no inferred exposure or adoption. Read current approved requirement, build and flag, independent QA gate, help content and versioned usage rule.

## Plan and AgentWorks tools

Create reviewed steps for product-requirements-coordinator (`product-requirements-brief/v1`) → product-release-coordinator (`product-release-readiness/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No flag, deployment, help publication or announcement from installation. A go decision is not customer exposure or feature adoption.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/requirements.json), [last artifact](examples/release.json) and [rejected artifact](examples/invalid-release.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

A passing QA gate is one input. Product go needs current help, measurement and rollout authority; a no-go names blockers. No flag, deployment, help publication or announcement from installation. A go decision is not customer exposure or feature adoption. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
