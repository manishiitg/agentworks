---
name: opportunity-to-reviewed-requirements
description: Move a bounded customer problem through accepted priority into reviewable product requirements without silently opening delivery work.
---

# Opportunity to Reviewed Requirements

## Outcome

Produce a sourced opportunity, an owner-accepted priority decision, and testable requirements with open questions and no automatic issue write.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One product, customer segment, opportunity and strategy horizon; preserve participant privacy and the current roadmap revision. Use authorized research, current opportunity and roadmap records, an approved strategy goal, and current design or behavior evidence.

## Plan and AgentWorks tools

Create reviewed steps for product-discovery-researcher (`product-opportunity-brief/v1`) → roadmap-prioritization-analyst (`product-priority-decision/v1`) → product-requirements-coordinator (`product-requirements-brief/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No participant contact, roadmap edit, issue write, delivery date or customer promise follows from installation or a draft.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/discovery.json), [last artifact](examples/requirements.json) and [rejected artifact](examples/invalid-requirements.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

Discovery is evidence, Product priority needs explicit owner acceptance, and requirements stay a draft until Product, Design and Engineering review. No participant contact, roadmap edit, issue write, delivery date or customer promise follows from installation or a draft. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
