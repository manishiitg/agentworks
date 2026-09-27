# Offer to Seller Readiness: team and handoffs

## Exact job

Produce an accepted offer decision and an unpublished seller-ready brief with exact claim and price evidence. One offer, plan, price revision and buyer persona with protected customer contracts and a named pricing and enablement owner.

## Source and owner boundary

Read current billing price and entitlement revisions, unit-cost evidence, approved product claims and dated buyer objections. Pricing owner accepts the exact offer; Enablement removes stale prices and unsupported claims before a separate content review. No billing price, entitlement, contract, public page, seller asset or prospect contact changes from this proposal.

## Typed sequence

pricing-packaging-analyst (`gtm-offer-decision/v1`) → sales-enablement-coordinator (`gtm-enablement-brief/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-enablement.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Reconcile a real plan against billing and entitlement sources, reject one stale claim, then review the unpublished seller asset. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
