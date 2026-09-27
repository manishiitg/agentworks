# Launch Signal to Pipeline Review: team and handoffs

## Exact job

Produce an observed launch register and a sourced funnel review with coverage, stage counts, gaps and owner action. One approved offer, launch, campaign, time window and attribution model; no inferred qualified lead or revenue lift.

## Source and owner boundary

Read provider publication evidence, campaign and form events, current CRM lead and opportunity records, identity rule and lookback window. Stage counts must reconcile in order and unmatched events retain partial attribution; contact and sales acceptance remain separate. No campaign spend, CRM stage edit, lead contact or revenue claim follows from a form event or this Playbook installation.

## Typed sequence

launch-coordinator (`launch-signal-register/v1`) → revenue-operations-analyst (`gtm-funnel-reconciliation/v1`). Every artifact carries artifact type and ID, tenant and subject IDs, source revision and refs, observed time, producer Crew ID, and exact upstream artifact ID and revision. The [validator](../scripts/validate_handoff.py) checks these joins and route-specific decision claims. The [rejected example](../examples/invalid-revenue.json) must fail. A valid fictional fixture proves the contract shape only; setup requires a real authorized manual case and owner review.

## First case and repeat

Run one authorized campaign with a duplicate event and unmatched lead; check source IDs and owner corrections manually. A later run must re-read source revisions and prior decisions, retain stable subject and action IDs, and avoid duplicate writes or notifications. Any provider action uses a separate owner approval and receipt.
