# Performance Regression to Owned Change: team and handoffs

## Exact job

Produce a sourced regression diagnosis and an owned blocker ledger with reproduction, uncertainty and next verification. One service or page, endpoint or journey, baseline and regressed builds, environment, traffic condition and Engineering owner.

## Source and owner boundary

Use comparable trace or test runs, performance budget revision, deployment timeline, issue/PR/CI state and change policy. A metric change needs comparable conditions and coverage; correlation with a deploy is a hypothesis. An accepted change plan is not a deployed fix. No production change, rollback, issue write, customer claim or performance recovery claim from installation or a proposed fix.

## Typed sequence

performance-investigator (`performance-regression-finding/v1`) → engineering-delivery-coordinator (`engineering-performance-action/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-delivery.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Run one actual regression with baseline and current traces, verify the exact build and ask the delivery owner to review the next change. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
