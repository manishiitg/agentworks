# Approved Requirement to Release Readiness: team and handoffs

## Exact job

Produce an exact-requirement release readiness decision with named blockers or verified go evidence. One accepted requirement, product, build, flag revision and rollout owner; no inferred exposure or adoption.

## Source and owner boundary

Read current approved requirement, build and flag, independent QA gate, help content and versioned usage rule. A passing QA gate is one input. Product go needs current help, measurement and rollout authority; a no-go names blockers. No flag, deployment, help publication or announcement from installation. A go decision is not customer exposure or feature adoption.

## Typed sequence

product-requirements-coordinator (`product-requirements-brief/v1`) → product-release-coordinator (`product-release-readiness/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-release.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Run a real authorized requirement/build pair, re-read current gate and help revision, and have the Product owner review go or no-go. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
