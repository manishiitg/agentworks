---
name: order-exception-to-reviewed-update
description: Propose an exact-order Operations investigation and a case-linked, unsent Support update with verified source states.
---

# Order Exception to Reviewed Update

## Outcome

Turn one cross-system order exception into a sourced owner action and a case-linked customer update for review. A label, carrier acceptance, delivery, refund, drafted message, sent message and resolution are distinct observed states.

## When to use

Use for a paid order with a fulfillment or shipment exception when the business has an exact support case, current order/payment/fulfillment/carrier sources, contact policy and accountable owners. This route works with authorized commerce or ERP records; it is not Shopify-specific. Start with one manual case.

## Discovery and user direction

Inspect existing Operations and Support Crews, source access, order and case revisions, prior contact, owners and promises. Propose reuse or reviewed creation. Show the exact route before Builder applies it; selection leaves setup pending.

## Required inputs

Bind business, order, payment, fulfillment, shipment, customer and case IDs; promised time and time zone; source revisions; current contact; exception and approval policies; and recipient/channel if an update is appropriate.

## Plan and AgentWorks tools

Reuse Order Operations Coordinator and Support Reply Drafter. Operations writes `order-exception/v1` from current sources; add a blocking scripted validator step before Support consumes its exact path. Support re-reads the case, policy and source state, then writes `order-customer-update-review/v1` with an unsent draft or no-message decision. Validate the pair before showing it as a reviewed handoff. Any customer send, refund, reship or order write requires a separate exact approval, current-state recheck and provider receipt.

## Knowledge and persistence

Save business/order/case IDs, source revisions and times, stable exception/action key, prior contact, artifact IDs, run/validator IDs, owner decisions and later provider receipts. Re-read order, carrier and case state on every retry; skip superseded or duplicate work.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks exact identity, source and policy evidence, legal state transitions, case linkage and unsent message claims. Fictional [order exception](examples/order-exception.json) and [reviewed update](examples/order-customer-update-review.json) pass. The [false resolution](examples/invalid-order-customer-update-review.json) fails. The reporting dashboard separates pickup unverified, carrier accepted, delivered, no-message, draft pending review, provider-sent and resolved states using their own evidence.

## Guardrails

Do not equate a carrier label with acceptance or delivery, infer loss from a missing scan, promise a refund or arrival time without evidence, or reuse another customer's case. Installation does not contact customers, alter orders, refund, reship, or activate a trigger or schedule. A draft is not a sent or resolved update.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for source and contact rules and [setup](SETUP.json) for ten pending checks.

## Completion contract

Return Crew IDs, source map, validated order and update artifacts, manual run, owner decisions, actual provider and case state or unknown, activation choice and blockers. Keep recurrence off until reviewed.
