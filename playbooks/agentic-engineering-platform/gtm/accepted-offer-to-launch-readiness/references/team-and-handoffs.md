# Accepted Offer to Launch Readiness: team and handoffs

## Exact job

Produce a priced offer decision, sourced positioning brief and owner-reviewed launch readiness register with explicit blockers. One offer, plan, price revision, buyer segment, intended launch window and accountable pricing and GTM owners.

## Source and owner boundary

Read current billing price, entitlement and contract exceptions, approved claims, buyer evidence, channel policy and asset revisions. The exact offer must be accepted before positioning; every launch claim must cite an approved current source; assets remain unlaunched until separate review. No price edit, campaign publication, spend, seller asset distribution, lead contact or recurrence from this Playbook.

## Typed sequence

pricing-packaging-analyst (`gtm-offer-decision/v1`) → gtm-strategy-analyst (`gtm-strategy-brief/v1`) → launch-coordinator (`gtm-launch-readiness/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-launch.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Run one actual offer and launch window, verify price and claims across systems, and ask the GTM owner to approve or block the plan. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
