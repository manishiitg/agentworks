# Release to Observed Adoption: team and handoffs

## Exact job

Produce a verified release handoff and a bounded first adoption observation with stated coverage and the next measurement question. One product, feature, exact release and flag revision, eligible population, observation window and Product owner.

## Source and owner boundary

Re-read deployment and flag records, the approved release gate, versioned product events and identity coverage for eligible accounts. A go decision is not deployment; deployment is not exposure; exposure is not use. Count only eligible, exposed and observed users under one frozen event rule. No flag change, customer announcement, adoption claim, roadmap decision or schedule follows from selection or a release plan.

## Typed sequence

product-release-coordinator (`product-release-readiness/v1`) → product-adoption-analyst (`feature-adoption-observation/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-adoption.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Use one actual released feature with exact build and flag, compare exposure and usage sources, and have Product review the first observation. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
