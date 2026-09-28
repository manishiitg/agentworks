# Verified Knowledge to Case Reply: team and handoffs

## Exact job

Produce a verified live knowledge reference and an exact-case reply draft with source links and a contact decision. One product, help article and exact open case, locale, recipient, support owner and contact policy.

## Source and owner boundary

Read the live help revision and publication receipt, current case thread and account state, approved product facts and suppression history. An approved article draft is not live knowledge. Recheck the published revision, case applicability and permission before a separate reply approval. No article publication, case closure, customer send or deflection claim from selecting this Playbook or drafting a reply.

## Typed sequence

support-knowledge-curator (`support-knowledge-publication/v1`) → support-reply-drafter (`support-reply-draft/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-reply.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Use one real live article and open case, verify exact revision and recipient policy, then have Support review the unsent response. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
