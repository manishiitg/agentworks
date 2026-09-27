# Opportunity to Reviewed Requirements: team and handoffs

## Exact job

Produce a sourced opportunity, an owner-accepted priority decision, and testable requirements with open questions and no automatic issue write. One product, customer segment, opportunity and strategy horizon; preserve participant privacy and the current roadmap revision.

## Source and owner boundary

Use authorized research, current opportunity and roadmap records, an approved strategy goal, and current design or behavior evidence. Discovery is evidence, Product priority needs explicit owner acceptance, and requirements stay a draft until Product, Design and Engineering review. No participant contact, roadmap edit, issue write, delivery date or customer promise follows from installation or a draft.

## Typed sequence

product-discovery-researcher (`product-opportunity-brief/v1`) → roadmap-prioritization-analyst (`product-priority-decision/v1`) → product-requirements-coordinator (`product-requirements-brief/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-requirements.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Use one real authorized opportunity with a counterexample, current roadmap and an owner decision; retain an accepted or blocked outcome. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
