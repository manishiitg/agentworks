---
name: offer-to-seller-readiness
description: Join an owner-accepted priced SaaS offer to current, approved seller guidance without making unsupported claims or sending outreach.
---

# Offer to Seller Readiness

## Outcome

Produce an accepted offer decision and an unpublished seller-ready brief with exact claim and price evidence.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One offer, plan, price revision and buyer persona with protected customer contracts and a named pricing and enablement owner. Read current billing price and entitlement revisions, unit-cost evidence, approved product claims and dated buyer objections.

## Plan and AgentWorks tools

Create reviewed steps for pricing-packaging-analyst (`gtm-offer-decision/v1`) → sales-enablement-coordinator (`gtm-enablement-brief/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No billing price, entitlement, contract, public page, seller asset or prospect contact changes from this proposal.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/pricing.json), [last artifact](examples/enablement.json) and [rejected artifact](examples/invalid-enablement.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

Pricing owner accepts the exact offer; Enablement removes stale prices and unsupported claims before a separate content review. No billing price, entitlement, contract, public page, seller asset or prospect contact changes from this proposal. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
