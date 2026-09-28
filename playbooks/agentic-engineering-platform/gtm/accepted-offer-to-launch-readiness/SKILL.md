---
name: accepted-offer-to-launch-readiness
description: Carry an accepted priced SaaS offer through positioning into a reviewed launch plan with current claims, owners and channels.
---

# Accepted Offer to Launch Readiness

## Outcome

Produce a priced offer decision, sourced positioning brief and owner-reviewed launch readiness register with explicit blockers.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One offer, plan, price revision, buyer segment, intended launch window and accountable pricing and GTM owners. Read current billing price, entitlement and contract exceptions, approved claims, buyer evidence, channel policy and asset revisions.

## Plan and AgentWorks tools

Create reviewed steps for pricing-packaging-analyst (`gtm-offer-decision/v1`) → gtm-strategy-analyst (`gtm-strategy-brief/v1`) → launch-coordinator (`gtm-launch-readiness/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No price edit, campaign publication, spend, seller asset distribution, lead contact or recurrence from this Playbook.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/pricing.json), [last artifact](examples/launch.json) and [rejected artifact](examples/invalid-launch.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

The exact offer must be accepted before positioning; every launch claim must cite an approved current source; assets remain unlaunched until separate review. No price edit, campaign publication, spend, seller asset distribution, lead contact or recurrence from this Playbook. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
